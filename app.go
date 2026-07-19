package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
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

type App struct {
	db           *sql.DB
	queries      *sqlc.Queries
	orchestrator *orchestrator.Orchestrator
	mux          *mux.Multiplexer
	stdManager   *standalone.Manager
	ssManager    *scaleset.ScaleSetManager
	authCfg      *config.AuthConfig
	
	httpServer   *http.Server
	unixListener net.Listener
}

func NewApp(dbPath string, authCfgPath string) (*App, error) {
	sqliteDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.RunMigrations(sqliteDB); err != nil {
		sqliteDB.Close()
		return nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	queries := sqlc.New(sqliteDB)

	maxWorkersStr, _ := queries.GetSetting(context.Background(), "max_workers")
	warmWorkersStr, _ := queries.GetSetting(context.Background(), "warm_workers")

	maxWorkers := DefaultMaxWorkers
	if mw, err := strconv.Atoi(maxWorkersStr); err == nil && mw > 0 {
		maxWorkers = mw
	}

	warmWorkers := 0
	if ww, err := strconv.Atoi(warmWorkersStr); err == nil && ww >= 0 {
		warmWorkers = ww
	}
	warmWorkers = min(warmWorkers, maxWorkers)

	stdManager := standalone.NewManager()

	dbRunners, err := queries.ListRunners(context.Background())
	if err != nil {
		sqliteDB.Close()
		return nil, fmt.Errorf("failed to list runners from DB: %w", err)
	}
	stdManager.SyncStaleRunners(dbRunners)

	orch, err := orchestrator.NewOrchestrator(stdManager, maxWorkers, warmWorkers, sqliteDB, queries)
	if err != nil {
		sqliteDB.Close()
		return nil, fmt.Errorf("failed to initialize Orchestrator: %w", err)
	}

	ssManager := scaleset.NewScaleSetManager(orch, sqliteDB, queries)

	multiplexer := mux.NewMultiplexer(sqliteDB, queries, stdManager, ssManager)
	orch.SetStatusReporter(multiplexer)

	authCfg, err := config.LoadAuthConfig(authCfgPath)
	if err != nil {
		log.Printf("Warning: failed to load auth config: %v", err)
	}

	return &App{
		db:           sqliteDB,
		queries:      queries,
		orchestrator: orch,
		mux:          multiplexer,
		stdManager:   stdManager,
		ssManager:    ssManager,
		authCfg:      authCfg,
	}, nil
}

func (a *App) Start(ctx context.Context) error {
	os.Remove(api.SockPath)
	listener, err := net.Listen("unix", api.SockPath)
	if err != nil {
		return fmt.Errorf("failed to listen on unix socket: %w", err)
	}
	os.Chmod(api.SockPath, 0660)
	a.unixListener = listener

	go func() {
		muxServer := http.NewServeMux()
		muxServer.HandleFunc("/api/v1/worker/allocate", a.orchestrator.HandleAllocate)
		log.Printf("[Orchestrator] Listening on unix socket %s for Standalone Shim allocations...", api.SockPath)
		if err := http.Serve(listener, muxServer); err != nil && err != http.ErrServerClosed {
			log.Printf("Fatal: orchestrator server failed: %v", err)
		}
	}()

	dashboardAPI := dashboard.NewAPI(a.db, a.queries, a.mux, a.orchestrator, a.authCfg)
	router := http.NewServeMux()
	
	// Mount API routes
	dashboardAPI.MountRoutes(router)
	
	// Mount static files
	dashboard.MountStaticFiles(router)

	// Wrap router with CORS if needed or let it be for app.go
	a.httpServer = &http.Server{
		Addr:    ":8080",
		Handler: router,
	}

	go func() {
		log.Printf("Starting dashboard server on :8080")
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Dashboard server failed: %v", err)
		}
	}()

	dbRunners, _ := a.queries.ListRunners(ctx)
	startupDelay := 5 * time.Second
	if d := os.Getenv("RUNNER_STARTUP_DELAY"); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil {
			startupDelay = parsed
		}
	}

	standaloneStarted := 0
	for _, r := range dbRunners {
		if r.Mode == "standalone" && standaloneStarted > 0 {
			log.Printf("Staggering standalone runner startup: waiting %v before starting %s...", startupDelay, r.Name)
			time.Sleep(startupDelay)
		}

		cfg := config.RunnerConfigFromDB(r)

		if err := a.mux.AddRunner(context.Background(), cfg); err != nil {
			log.Printf("Warning: failed to start runner %s: %v", r.Name, err)
		}

		if r.Mode == "standalone" {
			standaloneStarted++
		}
	}

	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	log.Printf("Shutting down HTTP and Unix servers...")
	if a.httpServer != nil {
		a.httpServer.Shutdown(ctx)
	}
	if a.unixListener != nil {
		a.unixListener.Close()
	}

	var wg sync.WaitGroup
	for _, r := range a.mux.GetRunnerStatuses() {
		wg.Add(1)
		go func(name, mode string) {
			defer wg.Done()
			log.Printf("Shutting down %s runner %s...", mode, name)
			if err := a.mux.RemoveRunner(ctx, name, false, mode); err != nil {
				log.Printf("Error shutting down runner %s: %v", name, err)
			}
		}(r.Name, r.Mode)
	}
	
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		log.Printf("Runners drained gracefully.")
	case <-ctx.Done():
		log.Printf("Context cancelled during shutdown, forcing runner shutdown...")
		for _, r := range a.mux.GetRunnerStatuses() {
			_ = a.mux.RemoveRunner(context.Background(), r.Name, true, r.Mode)
		}
	}
	
	if a.db != nil {
		a.db.Close()
	}

	return nil
}
