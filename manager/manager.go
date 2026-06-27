package manager

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/kmhalvin/github-action-runners-mux/config"
)

// RunnerProcess represents an actively running Runner.Listener instance.
type RunnerProcess struct {
	Config *config.RunnerConfig
	Cmd    *exec.Cmd
	PGID   int
	Mutex  sync.Mutex
	Active bool
}

type Manager struct {
	runners map[string]*RunnerProcess
	mutex   sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		runners: make(map[string]*RunnerProcess),
	}
}

// StartAll initializes the environment and starts all listeners concurrently.
func (m *Manager) StartAll(cfg *config.Config) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(cfg.Runners))

	for i := range cfg.Runners {
		rCfg := &cfg.Runners[i]
		wg.Add(1)
		go func(c *config.RunnerConfig) {
			defer wg.Done()
			if err := config.InitializeEnvironment(c); err != nil {
				errCh <- fmt.Errorf("failed to initialize %s: %v", c.Name, err)
				return
			}
			if err := m.startRunner(c); err != nil {
				errCh <- fmt.Errorf("failed to start %s: %v", c.Name, err)
			}
		}(rCfg)
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

	// myoung34 root override MUST be injected because we bypass run.sh
	cmd.Env = append(os.Environ(), "RUNNER_ALLOW_RUNASROOT=1")

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

	rp := &RunnerProcess{
		Config: cfg,
		Cmd:    cmd,
		PGID:   pgid,
		Active: true,
	}

	m.mutex.Lock()
	m.runners[cfg.Name] = rp
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

func (m *Manager) streamLogs(name string, r io.Reader, level string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		// Prefix each log line with the runner name
		fmt.Printf("[%s][%s] %s\n", name, level, strings.TrimSpace(line))
	}
}

// LockOthers sends SIGSTOP to all runners except the ones in activeRunners.
func (m *Manager) LockOthers(activeRunners []string) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	activeMap := make(map[string]bool)
	for _, name := range activeRunners {
		activeMap[name] = true
	}

	for name, rp := range m.runners {
		if rp.Active && !activeMap[name] {
			log.Printf("[Mutex] Sending SIGSTOP to %s (PGID: %d)", name, rp.PGID)
			if err := syscall.Kill(-rp.PGID, syscall.SIGSTOP); err != nil {
				log.Printf("[Mutex] Failed to freeze %s: %v", name, err)
			}
		}
	}
}

// UnlockOthers sends SIGCONT to all frozen runners.
func (m *Manager) UnlockOthers() {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for name, rp := range m.runners {
		if rp.Active {
			log.Printf("[Mutex] Sending SIGCONT to %s (PGID: %d)", name, rp.PGID)
			if err := syscall.Kill(-rp.PGID, syscall.SIGCONT); err != nil {
				log.Printf("[Mutex] Failed to unfreeze %s: %v", name, err)
			}
		}
	}
}

// GetRunners returns all tracked runner processes.
func (m *Manager) GetRunners() []*RunnerProcess {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	var runners []*RunnerProcess
	for _, r := range m.runners {
		runners = append(runners, r)
	}
	return runners
}
