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
	"time"

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
	// retryCancel, if non-nil, signals the retry goroutine to stop sleeping.
	// Set when the listener is running (so Stop can interrupt backoff waits).
	retryCancel chan struct{}
}

const (
	// maxListenerRetries is the number of times a listener that exits shortly
	// after starting will be automatically restarted.
	maxListenerRetries = 3
	// fastFailThreshold is the uptime below which a failure is considered
	// transient (rate limit, network blip) and eligible for retry.
	fastFailThreshold = 30 * time.Second
)

// launchListener starts a new Runner.Listener process, wires up log streaming,
// and updates rp with the new Cmd/PGID/state. Returns the started command.
func (m *Manager) launchListener(cfg *config.RunnerConfig, rp *ListenerProcess) (*exec.Cmd, error) {
	cmd := exec.Command("./bin/Runner.Listener", "run", "--startuptype", "service")
	cmd.Dir = cfg.Dir

	// We create a new Process Group so the SIGSTOP/SIGCONT works cleanly on the whole listener tree
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
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

	return cmd, nil
}

func (m *Manager) startRunner(cfg *config.RunnerConfig, rp *ListenerProcess) error {
	log.Printf("[%s] Starting Listener via Go command...", cfg.Name)

	cmd, err := m.launchListener(cfg, rp)
	if err != nil {
		m.mutex.Lock()
		rp.State = mux.StateOffline
		rp.Error = err.Error()
		m.mutex.Unlock()
		return err
	}

	// Wait for the process to exit in a separate goroutine with auto-retry.
	// If the listener exits shortly after starting (within fastFailThreshold),
	// it's likely a transient failure (GitHub rate limit, network blip).
	// We restart it up to maxListenerRetries times with exponential backoff.
	go func() {
		currentCmd := cmd
		for attempt := 0; ; attempt++ {
			startTime := time.Now()
			err := currentCmd.Wait()
			uptime := time.Since(startTime)

			m.mutex.Lock()
			wasDraining := rp.State == mux.StateDraining

			if wasDraining || err == nil {
				// Intentional stop (Stop() was called) or clean exit — no retry.
				rp.State = mux.StateOffline
				rp.Cmd = nil
				rp.retryCancel = nil
				if err != nil {
					rp.Error = err.Error()
					log.Printf("[%s] Listener exited with error: %v", cfg.Name, err)
				} else {
					rp.Error = ""
					log.Printf("[%s] Listener exited cleanly", cfg.Name)
				}
				m.mutex.Unlock()
				return
			}

			// Unexpected error exit.
			if attempt < maxListenerRetries && uptime < fastFailThreshold {
				// Transient failure — retry with exponential backoff.
				rp.State = mux.StateRegistering
				rp.Error = ""
				rp.Cmd = nil
				// Create a cancel channel so Stop() can interrupt the backoff.
				cancelCh := make(chan struct{})
				rp.retryCancel = cancelCh
				m.mutex.Unlock()

				backoff := time.Duration((attempt+1)*10) * time.Second
				log.Printf("[%s] Listener exited after %v (attempt %d/%d), retrying in %v...",
					cfg.Name, uptime, attempt+1, maxListenerRetries, backoff)

				select {
				case <-time.After(backoff):
				case <-cancelCh:
					// Stop() was called during backoff — give up.
					m.mutex.Lock()
					rp.State = mux.StateOffline
					rp.retryCancel = nil
					m.mutex.Unlock()
					log.Printf("[%s] Retry cancelled by Stop()", cfg.Name)
					return
				}

				// Restart the listener process.
				newCmd, launchErr := m.launchListener(cfg, rp)
				if launchErr != nil {
					m.mutex.Lock()
					rp.State = mux.StateFailed
					rp.Error = fmt.Sprintf("retry %d failed to launch: %v", attempt+1, launchErr)
					rp.retryCancel = nil
					m.mutex.Unlock()
					log.Printf("[%s] Retry %d failed to launch listener: %v", cfg.Name, attempt+1, launchErr)
					return
				}
				currentCmd = newCmd
				continue
			}

			// Max retries exceeded or long-running failure — don't retry.
			rp.State = mux.StateOffline
			rp.Error = err.Error()
			rp.Cmd = nil
			rp.retryCancel = nil
			m.mutex.Unlock()
			log.Printf("[%s] Listener exited with error: %v", cfg.Name, err)
			return
		}
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
