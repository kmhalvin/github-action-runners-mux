package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/manager"
	"github.com/kmhalvin/github-action-runners-mux/monitor"
	"github.com/kmhalvin/github-action-runners-mux/reaper"
)

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

	// 4. Initialize IPC Monitor (Mutex)
	ipcMon, err := monitor.NewIPCMonitor(mgr.LockOthers, mgr.UnlockOthers)
	if err != nil {
		log.Fatalf("Fatal: failed to start IPC Monitor: %v", err)
	}
	go ipcMon.Start()

	// 5. Start Runners
	if err := mgr.StartAll(cfg); err != nil {
		log.Fatalf("Fatal: %v", err)
	}

	// 6. Graceful Shutdown & Lifecycle Management
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	log.Printf("Received signal %v, initiating graceful shutdown...", sig)

	activePGID := ipcMon.GetActivePGID()

	// Drain logic
	// Create a context for the hard timeout
	ctx, cancel := context.WithTimeout(context.Background(), DrainTimeout)
	defer cancel()

	doneCh := make(chan struct{})

	go func() {
		drainAndCleanup(mgr, activePGID)
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

func drainAndCleanup(mgr *manager.Manager, activePGID int) {
	runners := mgr.GetRunners()

	// Send SIGTERM to idle runners first
	for _, rp := range runners {
		if rp.PGID != activePGID && rp.Active {
			log.Printf("[%s] Idle runner. Sending SIGTERM and SIGCONT...", rp.Config.Name)
			syscall.Kill(-rp.PGID, syscall.SIGTERM)
			syscall.Kill(-rp.PGID, syscall.SIGCONT) // Wake it up so it processes SIGTERM
		}
	}

	// Now wait for the active worker to finish, if any
	if activePGID != 0 {
		var activeRP *manager.RunnerProcess
		for _, rp := range runners {
			if rp.PGID == activePGID {
				activeRP = rp
				break
			}
		}

		if activeRP != nil && activeRP.Active {
			log.Printf("[%s] Active runner detected. Waiting for job to finish before terminating...", activeRP.Config.Name)
			_ = activeRP.Cmd.Wait()
			log.Printf("[%s] Active runner exited.", activeRP.Config.Name)
		}
	}

	// Wait for all remaining runners to exit and run cleanup
	for _, rp := range runners {
		if rp.Active && rp.PGID != activePGID {
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
