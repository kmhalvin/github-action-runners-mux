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

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

// ListenerProcess represents a managed GitHub Actions Runner Listener
type ListenerProcess struct {
	Config        *config.RunnerConfig
	Cmd           *exec.Cmd
	PGID          int
	Mutex         sync.Mutex
	State         mux.RunnerState
	Error         string
	ActiveWorkers int
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
