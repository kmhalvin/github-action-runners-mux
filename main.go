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
	"github.com/kmhalvin/github-action-runners-mux/multiplexer"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"
)

const sockPath = "/tmp/multiplexer.sock"

const DrainTimeout = 30 * time.Minute

func main() {

	// 2. Load Configuration
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("Fatal: %v", err)
	}

	// 3. Reconcile Configuration (Deregister stale runners)
	config.SyncRunners(cfg)

	// 4. Initialize Multiplexer
	mux := multiplexer.NewMultiplexer()

	// 4. Initialize Orchestrator
	maxWorkers := cfg.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 5 // Default
	}
	warmWorkers := min(max(cfg.WarmWorkers, 0), maxWorkers)

	orch, err := orchestrator.NewOrchestrator(mux, maxWorkers, warmWorkers)
	if err != nil {
		log.Fatalf("Fatal: failed to initialize Orchestrator: %v", err)
	}

	go func() {
		os.Remove(sockPath)
		listener, err := net.Listen("unix", sockPath)
		if err != nil {
			log.Fatalf("Fatal: failed to listen on unix socket: %v", err)
		}
		// Ensure the Worker Shim processes can access the socket
		os.Chmod(sockPath, 0777)

		muxServer := http.NewServeMux()
		muxServer.HandleFunc("/api/v1/worker/allocate", orch.HandleAllocate)

		log.Printf("[Orchestrator] Listening on unix socket %s for Worker Shim allocations...", sockPath)
		if err := http.Serve(listener, muxServer); err != nil {
			log.Fatalf("Fatal: orchestrator server failed: %v", err)
		}
	}()

	// 5. Start Runners
	if err := mux.StartAll(cfg); err != nil {
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
		drainAndCleanup(mux)
		close(doneCh)
	}()

	select {
	case <-doneCh:
		log.Printf("Graceful shutdown completed.")
	case <-ctx.Done():
		log.Printf("Hard drain timeout reached (30m). Escalating to SIGKILL.")
		forceKillAll(mux)
	}
}

func drainAndCleanup(mux *multiplexer.Multiplexer) {
	log.Println("Shutting down Multiplexer and gracefully stopping all Listeners...")
	for _, rp := range mux.GetListeners() {
		if rp.Active {
			log.Printf("[%s] Sending SIGINT (Graceful Shutdown) and SIGCONT...", rp.Config.Name)
			syscall.Kill(-rp.PGID, syscall.SIGCONT)          // Wake it up so it processes SIGINT if it was frozen
			syscall.Kill(rp.Cmd.Process.Pid, syscall.SIGINT) // Send SIGINT directly to the Listener, NOT the PGID
		}
	}

	// Wait for all listeners to exit
	for _, rp := range mux.GetListeners() {
		if rp.Active {
			_ = rp.Cmd.Wait()
		}
	}
}

func forceKillAll(mux *multiplexer.Multiplexer) {
	listeners := mux.GetListeners()
	for _, rp := range listeners {
		if rp.Active {
			log.Printf("[%s] Force killing...", rp.Config.Name)
			syscall.Kill(rp.Cmd.Process.Pid, syscall.SIGKILL) // Kill the Listener
			syscall.Kill(-rp.PGID, syscall.SIGCONT)           // Wake up the PGID so children can die
		}
	}
}
