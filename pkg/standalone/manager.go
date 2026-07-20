package standalone

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

type StandaloneManager struct {
	*mux.BaseManager
	listenerData map[string]*ListenerData
	globalPaused bool
}

// ListenerData holds standalone-specific process data (not shared with BaseManager).
type ListenerData struct {
	Cmd         *exec.Cmd
	PGID        int
	retryCancel chan struct{}
}

func NewManager() *StandaloneManager {
	m := &StandaloneManager{
		listenerData: make(map[string]*ListenerData),
	}
	m.BaseManager = mux.NewBaseManager(m)
	return m
}

// Launch implements mux.ManagerHooks
func (m *StandaloneManager) Launch(ctx context.Context, cfg *sqlc.Runner, token string) error {
	// Initialize environment (registration, etc.)
	if err := InitializeEnvironment(cfg, token); err != nil {
		return err
	}

	m.BaseManager.Mu.Lock()
	m.listenerData[cfg.Name] = &ListenerData{}
	m.BaseManager.Mu.Unlock()

	return m.startRunner(cfg)
}

// Halt implements mux.ManagerHooks
func (m *StandaloneManager) Halt(name string, force bool) error {
	m.BaseManager.Mu.Lock()
	ld, exists := m.listenerData[name]
	m.BaseManager.Mu.Unlock()

	if !exists {
		return mux.ErrRunnerNotFound
	}

	// If the listener is in retry backoff (no live process), cancel the
	// backoff and mark as draining. The retry goroutine will see the cancel
	// and transition to Offline.
	if ld.Cmd == nil && ld.retryCancel != nil {
		close(ld.retryCancel)
		return nil
	}

	if ld.Cmd == nil {
		return nil
	}

	pgid := ld.PGID
	cmd := ld.Cmd

	var err error
	if force {
		err = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		// SIGCONT to the process group to unfreeze the whole tree if paused.
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
		// SIGINT to the listener PID ONLY — NOT the process group.
		err = syscall.Kill(cmd.Process.Pid, syscall.SIGINT)

		if err == nil {
			// Wait for graceful exit
			done := make(chan struct{})
			go func() {
				cmd.Wait()
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(30 * time.Minute):
				// Force kill after timeout
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		}
	}

	return err
}

// Cleanup implements mux.ManagerHooks
func (m *StandaloneManager) Cleanup(cfg sqlc.Runner, token string) error {
	credsFile := filepath.Join(cfg.Dir, ".credentials")
	if _, err := os.Stat(credsFile); os.IsNotExist(err) {
		log.Printf("[%s] No .credentials found — runner was never registered, skipping deregistration", cfg.Name)
		// Still clean up the directory if it exists
		if cfg.Dir != "" {
			cleanDir := filepath.Clean(cfg.Dir)
			if err := os.RemoveAll(cleanDir); err != nil {
				log.Printf("[%s] Warning: failed to remove directory %s: %v", cfg.Name, cleanDir, err)
			}
		}
		return nil
	}

	if token == "" {
		log.Printf("[%s] cannot deregister: no token available", cfg.Name)
	} else {
		log.Printf("[%s] Deregistering runner from GitHub via config.sh remove...", cfg.Name)
		args := []string{"remove"}
		if token != "" {
			args = append(args, "--token", token)
		}
		cmd := exec.Command("./config.sh", args...)
		cmd.Dir = cfg.Dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			log.Printf("[%s] Warning: failed to deregister from GitHub: %v. Proceeding to delete directory anyway.", cfg.Name, err)
		} else {
			log.Printf("[%s] Successfully deregistered from GitHub", cfg.Name)
		}
	}

	// Remove the runner directory
	if cfg.Dir != "" {
		cleanDir := filepath.Clean(cfg.Dir)
		if err := os.RemoveAll(cleanDir); err != nil {
			log.Printf("[%s] Warning: failed to remove directory %s: %v", cfg.Name, cleanDir, err)
		} else {
			log.Printf("[%s] Successfully removed runner directory", cfg.Name)
		}
	}

	return nil
}

// Mode implements mux.ManagerHooks
func (m *StandaloneManager) Mode() string {
	return "standalone"
}

// MarkIdle overrides BaseManager.MarkIdle to provide standalone-specific behavior.
func (m *StandaloneManager) MarkIdle(name string) {
	m.BaseManager.Mu.RLock()
	idleState := mux.StateOnline
	if m.globalPaused {
		idleState = mux.StatePaused
	}
	m.BaseManager.Mu.RUnlock()
	m.BaseManager.MarkIdle(name, idleState)
}
