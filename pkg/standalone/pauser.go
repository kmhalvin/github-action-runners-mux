package standalone

import (
	"log"
	"syscall"

	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

func (m *StandaloneManager) LockOthers(activeRunners []string) {
	m.BaseManager.Mu.Lock()
	defer m.BaseManager.Mu.Unlock()

	m.globalPaused = true

	activeMap := make(map[string]bool)
	for _, name := range activeRunners {
		activeMap[name] = true
	}

	for name, proc := range m.BaseManager.Processes {
		if proc.State == mux.StateOnline && !activeMap[name] {
			ld := m.listenerData[name]
			if ld != nil && ld.PGID != 0 {
				log.Printf("[Mutex] Sending SIGSTOP to %s (PGID: %d)", name, ld.PGID)
				if err := syscall.Kill(-ld.PGID, syscall.SIGSTOP); err != nil {
					log.Printf("[Mutex] Failed to freeze %s: %v", name, err)
				} else {
					proc.State = mux.StatePaused
				}
			}
		}
	}
}

func (m *StandaloneManager) UnlockOthers() {
	m.BaseManager.Mu.Lock()
	defer m.BaseManager.Mu.Unlock()

	m.globalPaused = false

	for name, proc := range m.BaseManager.Processes {
		if proc.State == mux.StatePaused {
			ld := m.listenerData[name]
			if ld != nil && ld.PGID != 0 {
				log.Printf("[Mutex] Sending SIGCONT to %s (PGID: %d)", name, ld.PGID)
				if err := syscall.Kill(-ld.PGID, syscall.SIGCONT); err != nil {
					log.Printf("[Mutex] Failed to unfreeze %s: %v", name, err)
				} else {
					proc.State = mux.StateOnline
				}
			}
		}
	}
}
