package standalone

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

// ListenerProcess represents a managed GitHub Actions Runner Listener
type ListenerProcess struct {
	Config *config.RunnerConfig
	Cmd    *exec.Cmd
	PGID   int
	Mutex  sync.Mutex
	State  mux.RunnerState
	Error  string
}

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

func (m *Manager) startRunner(cfg *config.RunnerConfig, rp *ListenerProcess) error {
	log.Printf("[%s] Starting Listener via Go command...", cfg.Name)
	
	cmd := exec.Command("./bin/Runner.Listener", "run", "--startuptype", "service")
	cmd.Dir = cfg.Dir

	// We create a new Process Group so the SIGSTOP/SIGCONT works cleanly on the whole listener tree
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		m.mutex.Lock()
		rp.State = mux.StateOffline
		rp.Error = err.Error()
		m.mutex.Unlock()
		return err
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}

	m.mutex.Lock()
	rp.Cmd = cmd
	rp.PGID = pgid
	rp.State = mux.StateOnline
	rp.Error = ""

	// If the system is globally paused at max capacity, instantly freeze this new listener
	if m.globalPaused {
		log.Printf("[%s] System is at max capacity. Instantly freezing new listener (PGID: %d)", cfg.Name, pgid)
		if err := syscall.Kill(-pgid, syscall.SIGSTOP); err == nil {
			rp.State = mux.StatePaused
		} else {
			log.Printf("[%s] Failed to freeze new listener: %v", cfg.Name, err)
		}
	}
	m.mutex.Unlock()

	log.Printf("[%s] Started listener (PID: %d, PGID: %d)", cfg.Name, cmd.Process.Pid, pgid)

	go m.streamLogs(cfg.Name, stdout, "INFO")
	go m.streamLogs(cfg.Name, stderr, "ERROR")

	// Wait for the process to exit in a separate goroutine
	go func() {
		err := cmd.Wait()
		m.mutex.Lock()
		rp.State = mux.StateOffline
		if err != nil {
			rp.Error = err.Error()
			log.Printf("[%s] Listener exited with error: %v", cfg.Name, err)
		} else {
			rp.Error = ""
			log.Printf("[%s] Listener exited cleanly", cfg.Name)
		}
		m.mutex.Unlock()
	}()

	return nil
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

func (m *Manager) GetStatus(name string) (mux.RunnerStatus, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	rp, exists := m.listeners[name]
	if !exists {
		return mux.RunnerStatus{}, fmt.Errorf("runner %s not found", name)
	}
	
	return mux.RunnerStatus{
		Name:  rp.Config.Name,
		Mode:  "standalone",
		State: rp.State,
		Error: rp.Error,
	}, nil
}

func (m *Manager) ListRunners() []mux.RunnerStatus {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	var statuses []mux.RunnerStatus
	for name, rp := range m.listeners {
		statuses = append(statuses, mux.RunnerStatus{
			Name:  name,
			Mode:  "standalone",
			State: rp.State,
			Error: rp.Error,
		})
	}
	return statuses
}

func (m *Manager) streamLogs(name string, r io.Reader, level string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[%s][%s] %s\n", name, level, strings.TrimSpace(line))
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[%s] Error reading %s stream: %v", name, level, err)
	}
}

func (m *Manager) LockOthers(activeRunners []string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.globalPaused = true

	activeMap := make(map[string]bool)
	for _, name := range activeRunners {
		activeMap[name] = true
	}

	for name, rp := range m.listeners {
		if rp.State == mux.StateOnline && !activeMap[name] {
			log.Printf("[Mutex] Sending SIGSTOP to %s (PGID: %d)", name, rp.PGID)
			if err := syscall.Kill(-rp.PGID, syscall.SIGSTOP); err != nil {
				log.Printf("[Mutex] Failed to freeze %s: %v", name, err)
			} else {
				rp.State = mux.StatePaused
			}
		}
	}
}

func (m *Manager) UnlockOthers() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.globalPaused = false

	for name, rp := range m.listeners {
		if rp.State == mux.StatePaused {
			log.Printf("[Mutex] Sending SIGCONT to %s (PGID: %d)", name, rp.PGID)
			if err := syscall.Kill(-rp.PGID, syscall.SIGCONT); err != nil {
				log.Printf("[Mutex] Failed to unfreeze %s: %v", name, err)
			} else {
				rp.State = mux.StateOnline
			}
		}
	}
}

// MarkBusy sets a runner's state to Busy when a worker is allocated for it
func (m *Manager) MarkBusy(name string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if rp, exists := m.listeners[name]; exists && rp.State == mux.StateOnline {
		rp.State = mux.StateBusy
	}
}

// MarkIdle sets a runner's state back to Online or Paused after a job completes
func (m *Manager) MarkIdle(name string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if rp, exists := m.listeners[name]; exists && rp.State == mux.StateBusy {
		if m.globalPaused {
			rp.State = mux.StatePaused
		} else {
			rp.State = mux.StateOnline
		}
	}
}
