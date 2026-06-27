package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/manager"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"
	"github.com/kmhalvin/github-action-runners-mux/reaper"
)

const sockPath = "/tmp/multiplexer.sock"

const DrainTimeout = 30 * time.Minute

func main() {
	// 1. Start Zombie Reaper (PID 1 duties)
	reaper.StartZombieReaper()

	// 2. Load Configuration
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("Fatal: %v", err)
	}

	// 3. Initialize Manager
	mgr := manager.NewManager()

	// 4. Initialize Orchestrator (max 5 workers for now)
	orch, err := orchestrator.NewOrchestrator(mgr, 5)
	if err != nil {
		log.Fatalf("Fatal: failed to initialize Orchestrator: %v", err)
	}

	go func() {
		os.Remove(sockPath)
		listener, err := net.Listen("unix", sockPath)
		if err != nil {
			log.Fatalf("Fatal: failed to listen on unix socket: %v", err)
		}
		// Ensure the shim processes can access the socket
		os.Chmod(sockPath, 0777)

		mux := http.NewServeMux()
		mux.HandleFunc("/api/v1/worker/allocate", orch.HandleAllocate)
		
		log.Printf("[Proxy] Orchestrator listening on unix socket %s for Shim allocations...", sockPath)
		if err := http.Serve(listener, mux); err != nil {
			log.Fatalf("Fatal: orchestrator server failed: %v", err)
		}
	}()

	// 5. Start Runners
	if err := mgr.StartAll(cfg); err != nil {
		log.Fatalf("Fatal: %v", err)
	}

	// 6. Graceful Shutdown & Lifecycle Management
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	log.Printf("Received signal %v, initiating graceful shutdown...", sig)

	// Drain logic
	// Create a context for the hard timeout
	ctx, cancel := context.WithTimeout(context.Background(), DrainTimeout)
	defer cancel()

	doneCh := make(chan struct{})

	go func() {
		drainAndCleanup(mgr)
		close(doneCh)
	}()

	select {
	case <-doneCh:
		log.Printf("Graceful shutdown completed.")
	case <-ctx.Done():
		log.Printf("Hard drain timeout reached (30m). Escalating to SIGKILL.")
		forceKillAll(mgr)
	}
}

func drainAndCleanup(mgr *manager.Manager) {
	runners := mgr.GetRunners()

	// Send SIGINT to all runners. 
	// The actions/runner agent handles this natively:
	// - Idle listeners exit immediately.
	// - Active listeners wait for their remote worker to finish, then exit.
	for _, rp := range runners {
		if rp.Active {
			log.Printf("[%s] Sending SIGINT (Graceful Shutdown) and SIGCONT...", rp.Config.Name)
			syscall.Kill(-rp.PGID, syscall.SIGCONT) // Wake it up so it processes SIGINT if it was frozen
			syscall.Kill(-rp.PGID, syscall.SIGINT)
		}
	}

	// Wait for all runners to exit and run cleanup
	for _, rp := range runners {
		if rp.Active {
			_ = rp.Cmd.Wait()
		}
		// Aggressively clean up _work dir
		_ = config.CleanupWorkDir(rp.Config)
	}
}

func forceKillAll(mgr *manager.Manager) {
	runners := mgr.GetRunners()
	for _, rp := range runners {
		if rp.Active {
			log.Printf("[%s] Force killing...", rp.Config.Name)
			syscall.Kill(-rp.PGID, syscall.SIGKILL)
			syscall.Kill(-rp.PGID, syscall.SIGCONT)
		}
		_ = config.CleanupWorkDir(rp.Config)
	}
}
