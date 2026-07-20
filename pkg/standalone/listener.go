package standalone

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

const (
	// maxListenerRetries is the number of times a listener that exits shortly
	// after starting will be automatically restarted.
	maxListenerRetries = 3
	// fastFailThreshold is the uptime below which a failure is considered
	// transient (rate limit, network blip) and eligible for retry.
	fastFailThreshold = 30 * time.Second
)

// launchListener starts a new Runner.Listener process, wires up log streaming,
// and updates listenerData with the new Cmd/PGID and state to Online. Returns the started command.
func (m *StandaloneManager) launchListener(cfg *sqlc.Runner) (*exec.Cmd, error) {
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

	m.BaseManager.Mu.Lock()
	if ld, exists := m.listenerData[cfg.Name]; exists {
		ld.Cmd = cmd
		ld.PGID = pgid
	}

	m.BaseManager.SetError(cfg.Name, "")

	// If the system is globally paused at max capacity, instantly freeze this new listener
	if m.globalPaused {
		log.Printf("[%s] System is at max capacity. Instantly freezing new listener (PGID: %d)", cfg.Name, pgid)
		if err := syscall.Kill(-pgid, syscall.SIGSTOP); err == nil {
			m.BaseManager.Mu.Unlock()
			m.BaseManager.Transition(cfg.Name, mux.StatePaused)
		} else {
			log.Printf("[%s] Failed to freeze new listener: %v", cfg.Name, err)
			m.BaseManager.Mu.Unlock()
			m.BaseManager.Transition(cfg.Name, mux.StateOnline)
		}
	} else {
		m.BaseManager.Mu.Unlock()
		m.BaseManager.Transition(cfg.Name, mux.StateOnline)
	}

	log.Printf("[%s] Started listener (PID: %d, PGID: %d)", cfg.Name, cmd.Process.Pid, pgid)

	go m.streamLogs(cfg.Name, stdout, "INFO")
	go m.streamLogs(cfg.Name, stderr, "ERROR")

	return cmd, nil
}

func (m *StandaloneManager) startRunner(cfg *sqlc.Runner) error {
	log.Printf("[%s] Starting Listener via Go command...", cfg.Name)

	cmd, err := m.launchListener(cfg)
	if err != nil {
		m.BaseManager.Transition(cfg.Name, mux.StateFailed)
		m.BaseManager.SetError(cfg.Name, err.Error())
		return err
	}

	// Wait for the process to exit in a separate goroutine with auto-retry.
	go func() {
		currentCmd := cmd
		for attempt := 0; ; attempt++ {
			startTime := time.Now()
			err := currentCmd.Wait()
			uptime := time.Since(startTime)

			m.BaseManager.Mu.Lock()
			proc, procExists := m.BaseManager.Processes[cfg.Name]
			wasDraining := false
			if procExists {
				wasDraining = proc.State == mux.StateDraining
			}
			ld := m.listenerData[cfg.Name]
			m.BaseManager.Mu.Unlock()

			if wasDraining || err == nil {
				// Intentional stop (Stop() was called) or clean exit — no retry.
				m.BaseManager.Transition(cfg.Name, mux.StateOffline)
				m.BaseManager.Mu.Lock()
				if ld != nil {
					ld.Cmd = nil
					ld.retryCancel = nil
				}
				m.BaseManager.Mu.Unlock()
				if err != nil {
					m.BaseManager.SetError(cfg.Name, err.Error())
					log.Printf("[%s] Listener exited with error: %v", cfg.Name, err)
				} else {
					m.BaseManager.SetError(cfg.Name, "")
					log.Printf("[%s] Listener exited cleanly", cfg.Name)
				}
				return
			}

			// Unexpected error exit.
			if attempt < maxListenerRetries && uptime < fastFailThreshold {
				// Transient failure — retry with exponential backoff.
				m.BaseManager.Transition(cfg.Name, mux.StateRegistering)
				m.BaseManager.SetError(cfg.Name, "")
				cancelCh := make(chan struct{})
				m.BaseManager.Mu.Lock()
				if ld != nil {
					ld.Cmd = nil
					ld.retryCancel = cancelCh
				}
				m.BaseManager.Mu.Unlock()

				backoff := time.Duration((attempt+1)*10) * time.Second
				log.Printf("[%s] Listener exited after %v (attempt %d/%d), retrying in %v...",
					cfg.Name, uptime, attempt+1, maxListenerRetries, backoff)

				select {
				case <-time.After(backoff):
				case <-cancelCh:
					// Stop() was called during backoff — give up.
					m.BaseManager.Transition(cfg.Name, mux.StateOffline)
					m.BaseManager.Mu.Lock()
					if ld != nil {
						ld.retryCancel = nil
					}
					m.BaseManager.Mu.Unlock()
					log.Printf("[%s] Retry cancelled by Stop()", cfg.Name)
					return
				}

				// Restart the listener process.
				newCmd, launchErr := m.launchListener(cfg)
				if launchErr != nil {
					m.BaseManager.Transition(cfg.Name, mux.StateFailed)
					m.BaseManager.SetError(cfg.Name, fmt.Sprintf("retry %d failed to launch: %v", attempt+1, launchErr))
					m.BaseManager.Mu.Lock()
					if ld != nil {
						ld.retryCancel = nil
					}
					m.BaseManager.Mu.Unlock()
					log.Printf("[%s] Retry %d failed to launch listener: %v", cfg.Name, attempt+1, launchErr)
					return
				}
				currentCmd = newCmd
				continue
			}

			// Max retries exceeded or long-running failure — don't retry.
			m.BaseManager.Transition(cfg.Name, mux.StateOffline)
			m.BaseManager.SetError(cfg.Name, err.Error())
			m.BaseManager.Mu.Lock()
			if ld != nil {
				ld.Cmd = nil
				ld.retryCancel = nil
			}
			m.BaseManager.Mu.Unlock()
			log.Printf("[%s] Listener exited with error: %v", cfg.Name, err)
			return
		}
	}()

	return nil
}

func (m *StandaloneManager) streamLogs(name string, r io.Reader, level string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[%s][%s] %s\n", name, level, strings.TrimSpace(line))
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[%s] Error reading %s stream: %v", name, level, err)
	}
}
