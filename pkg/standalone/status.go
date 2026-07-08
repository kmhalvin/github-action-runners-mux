package standalone

import (
	"fmt"

	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

func (m *Manager) GetStatus(name string) (mux.RunnerStatus, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	rp, exists := m.listeners[name]
	if !exists {
		return mux.RunnerStatus{}, fmt.Errorf("runner %s not found", name)
	}

	return mux.RunnerStatus{
		Name:          rp.Config.Name,
		Mode:          "standalone",
		State:         rp.State,
		Error:         rp.Error,
		ActiveWorkers: rp.ActiveWorkers,
	}, nil
}

func (m *Manager) ListRunners() []mux.RunnerStatus {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	var statuses []mux.RunnerStatus
	for name, rp := range m.listeners {
		statuses = append(statuses, mux.RunnerStatus{
			Name:          name,
			Mode:          "standalone",
			State:         rp.State,
			Error:         rp.Error,
			ActiveWorkers: rp.ActiveWorkers,
		})
	}
	return statuses
}

// MarkBusy sets a runner's state to Busy when a worker is allocated for it
func (m *Manager) MarkBusy(name string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if rp, exists := m.listeners[name]; exists {
		rp.ActiveWorkers++
		if rp.State == mux.StateOnline || rp.State == mux.StatePaused {
			rp.State = mux.StateBusy
		}
	}
}

// MarkIdle sets a runner's state back to Online or Paused after a job completes
func (m *Manager) MarkIdle(name string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if rp, exists := m.listeners[name]; exists {
		if rp.ActiveWorkers > 0 {
			rp.ActiveWorkers--
		}
		if rp.ActiveWorkers == 0 && rp.State == mux.StateBusy {
			if m.globalPaused {
				rp.State = mux.StatePaused
			} else {
				rp.State = mux.StateOnline
			}
		}
	}
}
