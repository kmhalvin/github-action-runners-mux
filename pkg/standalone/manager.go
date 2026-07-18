package standalone

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

type Manager struct {
	listeners    map[string]*ListenerProcess
	mutex        sync.RWMutex
	globalPaused bool
}

func NewManager() *Manager {
	return &Manager{
		listeners: make(map[string]*ListenerProcess),
	}
}

// Start dynamically initializes and starts a standalone listener.
func (m *Manager) Start(ctx context.Context, cfg config.RunnerConfig) error {
	m.mutex.Lock()
	if rp, exists := m.listeners[cfg.Name]; exists && rp.State != mux.StateOffline && rp.State != mux.StateFailed && rp.State != mux.StateRegistering {
		m.mutex.Unlock()
		return fmt.Errorf("runner %s is already running or not offline", cfg.Name)
	}

	rp := &ListenerProcess{
		Config: &cfg,
		State:  mux.StateRegistering,
	}
	m.listeners[cfg.Name] = rp
	m.mutex.Unlock()

	// Initialize environment (registration, etc.)
	if err := InitializeEnvironment(&cfg); err != nil {
		m.mutex.Lock()
		rp.State = mux.StateFailed
		rp.Error = err.Error()
		m.mutex.Unlock()
		return fmt.Errorf("failed to initialize environment: %w", err)
	}

	return m.startRunner(&cfg, rp)
}

// Stop sends SIGINT (graceful) or SIGKILL (force) to the listener process.
// If the listener is in retry backoff (no live process), the backoff is
// cancelled and the runner transitions to Offline.
func (m *Manager) Stop(name string, force bool) error {
	m.mutex.Lock()
	rp, exists := m.listeners[name]
	if !exists {
		m.mutex.Unlock()
		return fmt.Errorf("runner %s not found", name)
	}

	if rp.State == mux.StateOffline || rp.State == mux.StateFailed {
		m.mutex.Unlock()
		return nil
	}

	// If the listener is in retry backoff (no live process), cancel the
	// backoff and mark as draining. The retry goroutine will see the cancel
	// and transition to Offline.
	if rp.Cmd == nil && rp.retryCancel != nil {
		rp.State = mux.StateDraining
		close(rp.retryCancel)
		m.mutex.Unlock()
		return nil
	}

	pgid := rp.PGID
	cmd := rp.Cmd
	rp.State = mux.StateDraining
	m.mutex.Unlock()

	var err error
	if force {
		err = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		// SIGCONT to the process group to unfreeze the whole tree if paused.
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
		// SIGINT to the listener PID ONLY — NOT the process group.
		// Sending to the group kills the worker-shim proxy, severing the TCP
		// connection and immediately aborting any active CI jobs.
		// The listener will reject new jobs but wait for active jobs to finish.
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

// Deregister runs config.sh remove --token <token> to remove the runner from GitHub,
// then removes the runner directory. This is the complete cleanup for a standalone runner.
// The registration token expires after 1 hour; if expired, config.sh remove will
// fail — we log a warning but still proceed with directory removal.
func (m *Manager) Deregister(cfg config.RunnerConfig) error {
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

	if cfg.Token == "" {
		return fmt.Errorf("[%s] cannot deregister: no token available", cfg.Name)
	}

	log.Printf("[%s] Deregistering runner from GitHub via config.sh remove...", cfg.Name)
	cmd := exec.Command("./config.sh", "remove", "--token", cfg.Token)
	cmd.Dir = cfg.Dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		log.Printf("[%s] Warning: failed to deregister from GitHub: %v. Proceeding to delete directory anyway.", cfg.Name, err)
	} else {
		log.Printf("[%s] Successfully deregistered from GitHub", cfg.Name)
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


