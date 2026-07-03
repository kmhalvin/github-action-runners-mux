package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/api"
	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"
	"github.com/kmhalvin/github-action-runners-mux/pkg/scaleset"
	"github.com/kmhalvin/github-action-runners-mux/pkg/standalone"
)

const DrainTimeout = 30 * time.Minute

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// 1. Load Configuration
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("Fatal: %v", err)
	}

	// 2. Reconcile Configuration (Deregister stale runners) - Note: currently only impacts standalone
	config.SyncRunners(cfg)

	// 3. Initialize Standalone Manager
	stdManager := standalone.NewManager()

	// 4. Initialize Orchestrator
	maxWorkers := cfg.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 5 // Default
	}
	warmWorkers := min(max(cfg.WarmWorkers, 0), maxWorkers)

	orch, err := orchestrator.NewOrchestrator(stdManager, maxWorkers, warmWorkers, cfg)
	if err != nil {
		log.Fatalf("Fatal: failed to initialize Orchestrator: %v", err)
	}

	// 5. Initialize ScaleSet Manager
	ssManager := scaleset.NewScaleSetManager(orch)

	// Start Unix socket server for Standalone shim allocations
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

	// 6. Start Runners Based on Mode
	var wg sync.WaitGroup
	if err := stdManager.StartAll(cfg); err != nil {
		log.Fatalf("Fatal: %v", err)
	}

	for i := range cfg.Runners {
		rCfg := &cfg.Runners[i]
		if rCfg.Mode == "scaleset" {
			c := rCfg
			wg.Go(func() {
				ssManager.StartRunner(ctx, c, maxWorkers)
			})
		}
	}

	// 7. Graceful Shutdown & Lifecycle Management
	log.Printf("All listeners started. Waiting for interrupt signal to shutdown...")
	<-ctx.Done()
	log.Printf("Interrupt signal received, initiating graceful shutdown...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), DrainTimeout)
	defer shutdownCancel()

	doneCh := make(chan struct{})
	go func() {
		drainAndCleanup(stdManager)
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		log.Printf("Graceful shutdown completed.")
	case <-shutdownCtx.Done():
		log.Printf("Hard drain timeout reached (30m). Escalating to SIGKILL.")
		forceKillAll(stdManager)
	}
}

func drainAndCleanup(stdManager *standalone.Manager) {
	log.Println("Shutting down gracefully stopping all Standalone Listeners...")
	for _, rp := range stdManager.GetListeners() {
		if rp.Active {
			log.Printf("[%s] Sending SIGINT (Graceful Shutdown) and SIGCONT...", rp.Config.Name)
			syscall.Kill(-rp.PGID, syscall.SIGCONT)
			syscall.Kill(rp.Cmd.Process.Pid, syscall.SIGINT)
		}
	}

	for _, rp := range stdManager.GetListeners() {
		if rp.Active {
			_ = rp.Cmd.Wait()
		}
	}
}

func forceKillAll(stdManager *standalone.Manager) {
	listeners := stdManager.GetListeners()
	for _, rp := range listeners {
		if rp.Active {
			log.Printf("[%s] Force killing...", rp.Config.Name)
			syscall.Kill(rp.Cmd.Process.Pid, syscall.SIGKILL)
			syscall.Kill(-rp.PGID, syscall.SIGCONT)
		}
	}
}
