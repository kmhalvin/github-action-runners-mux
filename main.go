package main

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/api"
	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/db"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"
	"github.com/kmhalvin/github-action-runners-mux/pkg/dashboard"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
	"github.com/kmhalvin/github-action-runners-mux/pkg/scaleset"
	"github.com/kmhalvin/github-action-runners-mux/pkg/standalone"

	_ "github.com/mattn/go-sqlite3"
)

const DrainTimeout = 30 * time.Minute
const DBPath = "/etc/github-mux/github-mux.db"
const LegacyYAMLPath = "config.yaml"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// 1. Initialize Database
	sqliteDB, err := sql.Open("sqlite3", DBPath)
	if err != nil {
		log.Fatalf("Fatal: failed to open database: %v", err)
	}
	defer sqliteDB.Close()

	if err := db.RunMigrations(sqliteDB); err != nil {
		log.Fatalf("Fatal: failed to run database migrations: %v", err)
	}

	queries := sqlc.New(sqliteDB)

	// 2. Import legacy config.yaml if present and DB is empty
	if err := db.ImportFromYAML(context.Background(), sqliteDB, queries, LegacyYAMLPath); err != nil {
		log.Printf("Warning: failed to import legacy YAML config: %v", err)
	}

	// Reconcile Configuration (Deregister stale standalone runners)
	dbRunners, err := queries.ListRunners(context.Background())
	if err != nil {
		log.Fatalf("Fatal: failed to list runners from DB: %v", err)
	}
	config.SyncRunners(dbRunners)

	// 3. Get Settings from DB
	maxWorkersStr, _ := queries.GetSetting(context.Background(), "max_workers")
	warmWorkersStr, _ := queries.GetSetting(context.Background(), "warm_workers")

	maxWorkers := 5 // Default
	if mw, err := strconv.Atoi(maxWorkersStr); err == nil && mw > 0 {
		maxWorkers = mw
	}

	warmWorkers := 0
	if ww, err := strconv.Atoi(warmWorkersStr); err == nil && ww >= 0 {
		warmWorkers = ww
	}
	warmWorkers = min(warmWorkers, maxWorkers)

	// 4. Initialize Standalone Manager
	stdManager := standalone.NewManager()

	// 5. Initialize Orchestrator
	orch, err := orchestrator.NewOrchestrator(stdManager, maxWorkers, warmWorkers, sqliteDB, queries)
	if err != nil {
		log.Fatalf("Fatal: failed to initialize Orchestrator: %v", err)
	}

	// 6. Initialize ScaleSet Manager
	ssManager := scaleset.NewScaleSetManager(orch, sqliteDB, queries)

	// 7. Initialize Multiplexer
	multiplexer := mux.NewMultiplexer(sqliteDB, queries, stdManager, ssManager)

	// 8. Start Unix socket server for Standalone shim allocations
	go func() {
		os.Remove(api.SockPath)
		listener, err := net.Listen("unix", api.SockPath)
		if err != nil {
			log.Fatalf("Fatal: failed to listen on unix socket: %v", err)
		}
		os.Chmod(api.SockPath, 0777)

		muxServer := http.NewServeMux()
		muxServer.HandleFunc("/api/v1/worker/allocate", orch.HandleAllocate)

		log.Printf("[Orchestrator] Listening on unix socket %s for Standalone Shim allocations...", api.SockPath)
		if err := http.Serve(listener, muxServer); err != nil {
			log.Fatalf("Fatal: orchestrator server failed: %v", err)
		}
	}()

	// 9. Start Dashboard Server
	sseHub := dashboard.NewSSEHub()
	dashboardAPI := dashboard.NewAPI(sqliteDB, queries, multiplexer, orch, sseHub)
	go dashboard.ServeDashboard(dashboardAPI, ":8080")

	// 10. Start all runners from DB
	for _, r := range dbRunners {
		cfg := config.RunnerConfig{
			Name:         r.Name,
			Mode:         r.Mode,
			URL:          r.Url,
			Token:        r.Token.String,
			Dir:          r.Dir.String,
			PAT:          r.Pat.String,
			ScaleSetName: r.ScaleSetName.String,
			MaxRunners:   int(r.MaxRunners.Int64),
		}
		
		if r.Labels.Valid {
			cfg.Labels = strings.Split(r.Labels.String, ",")
		}
		if r.RunnerGroup.Valid {
			cfg.Group = r.RunnerGroup.String
		}

		if err := multiplexer.AddRunner(context.Background(), cfg); err != nil {
			log.Printf("Warning: failed to start runner %s: %v", r.Name, err)
		}
	}

	// 11. Graceful Shutdown & Lifecycle Management
	log.Printf("System fully booted. Waiting for interrupt signal to shutdown...")
	<-ctx.Done()
	log.Printf("Interrupt signal received, initiating graceful shutdown...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), DrainTimeout)
	defer shutdownCancel()

	doneCh := make(chan struct{})
	go func() {
		// Stop all runners gracefully
		var wg sync.WaitGroup
		for _, r := range multiplexer.GetRunnerStatuses() {
			wg.Add(1)
			go func(name, mode string) {
				defer wg.Done()
				log.Printf("Shutting down %s runner %s...", mode, name)
				if err := multiplexer.RemoveRunner(context.Background(), name, false, mode); err != nil {
					log.Printf("Error shutting down runner %s: %v", name, err)
				}
			}(r.Name, r.Mode)
		}
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		log.Printf("Graceful shutdown completed.")
	case <-shutdownCtx.Done():
		log.Printf("Hard drain timeout reached (30m). Escalating to SIGKILL.")
		// Force kill
		for _, r := range multiplexer.GetRunnerStatuses() {
			_ = multiplexer.RemoveRunner(context.Background(), r.Name, true, r.Mode)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
