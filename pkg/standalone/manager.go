package standalone

import (
	"context"
	"fmt"
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
	if rp, exists := m.listeners[cfg.Name]; exists && rp.State != mux.StateOffline && rp.State != mux.StateRegistering {
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
		rp.State = mux.StateOffline
		rp.Error = err.Error()
		m.mutex.Unlock()
		return fmt.Errorf("failed to initialize environment: %w", err)
	}

	return m.startRunner(&cfg, rp)
}

// Stop sends SIGINT (graceful) or SIGKILL (force) to the listener process.
func (m *Manager) Stop(name string, force bool) error {
	m.mutex.Lock()
	rp, exists := m.listeners[name]
	if !exists {
		m.mutex.Unlock()
		return fmt.Errorf("runner %s not found", name)
	}

	if rp.State == mux.StateOffline {
		m.mutex.Unlock()
		return nil
	}

	pgid := rp.PGID
	rp.State = mux.StateDraining
	m.mutex.Unlock()

	var err error
	if force {
		err = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		// Ensure it's not paused before sending SIGINT, otherwise it won't process it
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
		err = syscall.Kill(-pgid, syscall.SIGINT)

		if err == nil {
			// Wait for graceful exit
			done := make(chan struct{})
			go func() {
				rp.Cmd.Wait()
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
