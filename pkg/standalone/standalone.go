package standalone

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/kmhalvin/github-action-runners-mux/api"
	"github.com/kmhalvin/github-action-runners-mux/config"
)

// ListenerProcess represents a managed GitHub Actions Runner Listener
type ListenerProcess struct {
	Config *config.RunnerConfig
	Cmd    *exec.Cmd
	PGID   int
	Mutex  sync.Mutex
	Active bool
}

type Manager struct {
	listeners    map[api.RunnerName]*ListenerProcess
	mutex        sync.RWMutex
	globalPaused bool
}

func NewManager() *Manager {
	return &Manager{
		listeners: make(map[api.RunnerName]*ListenerProcess),
	}
}

// StartAll initializes the environment and starts all listeners concurrently.
func (m *Manager) StartAll(cfg *config.Config) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(cfg.Runners))

	for i := range cfg.Runners {
		rCfg := &cfg.Runners[i]
		if rCfg.Mode != "standalone" {
			continue
		}
                c := rCfg
                wg.Go(func() {
			if err := InitializeEnvironment(c); err != nil {
				errCh <- fmt.Errorf("failed to initialize %s: %v", c.Name, err)
				return
			}
			if err := m.startRunner(c); err != nil {
				errCh <- fmt.Errorf("failed to start %s: %v", c.Name, err)
			}
                })
	}

	wg.Wait()
	close(errCh)

	if len(errCh) > 0 {
		return <-errCh // Return the first error encountered
	}

	return nil
}

func (m *Manager) startRunner(cfg *config.RunnerConfig) error {
	log.Printf("[%s] Starting Listener via Go command...", cfg.Name)
	// We no longer need run.sh wrappers. We execute the listener natively.
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
		return err
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		// Fallback to PID if Getpgid fails, though it shouldn't if Setpgid was set.
		pgid = cmd.Process.Pid
	}

	rp := &ListenerProcess{
		Config: cfg,
		Cmd:    cmd,
		PGID:   pgid,
		Active: true,
	}

	m.mutex.Lock()
	m.listeners[cfg.Name] = rp
	
	// If the system is globally paused at max capacity, instantly freeze this new listener
	if m.globalPaused {
		log.Printf("[%s] System is at max capacity. Instantly freezing new listener (PGID: %d)", cfg.Name, pgid)
		if err := syscall.Kill(-pgid, syscall.SIGSTOP); err != nil {
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
		rp.Active = false
		m.mutex.Unlock()
		if err != nil {
			log.Printf("[%s] Listener exited with error: %v", cfg.Name, err)
		} else {
			log.Printf("[%s] Listener exited cleanly", cfg.Name)
		}
	}()

	return nil
}

func (m *Manager) streamLogs(name api.RunnerName, r io.Reader, level string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		// Prefix each log line with the runner name
		fmt.Printf("[%s][%s] %s\n", name, level, strings.TrimSpace(line))
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[%s] Error reading %s stream: %v", name, level, err)
	}
}

func (m *Manager) LockOthers(activeRunners []api.RunnerName) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	m.globalPaused = true

	activeMap := make(map[api.RunnerName]bool)
	for _, name := range activeRunners {
		activeMap[name] = true
	}

	for name, rp := range m.listeners {
		if rp.Active && !activeMap[name] {
			log.Printf("[Mutex] Sending SIGSTOP to %s (PGID: %d)", name, rp.PGID)
			if err := syscall.Kill(-rp.PGID, syscall.SIGSTOP); err != nil {
				log.Printf("[Mutex] Failed to freeze %s: %v", name, err)
			}
		}
	}
}

func (m *Manager) UnlockOthers() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	m.globalPaused = false

	for name, rp := range m.listeners {
		if rp.Active {
			log.Printf("[Mutex] Sending SIGCONT to %s (PGID: %d)", name, rp.PGID)
			if err := syscall.Kill(-rp.PGID, syscall.SIGCONT); err != nil {
				log.Printf("[Mutex] Failed to unfreeze %s: %v", name, err)
			}
		}
	}
}

// GetListeners returns all tracked listener processes.
func (m *Manager) GetListeners() []*ListenerProcess {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	var listeners []*ListenerProcess
	for _, rp := range m.listeners {
		listeners = append(listeners, rp)
	}
	return listeners
}
