package scaleset

import (
	"fmt"

	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

func (m *ScaleSetManager) GetStatus(name string) (mux.RunnerStatus, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	rp, exists := m.processes[name]
	if !exists {
		return mux.RunnerStatus{}, fmt.Errorf("scaleset runner %s not found", name)
	}

	return mux.RunnerStatus{
		Name:          rp.Config.Name,
		Mode:          "scaleset",
		State:         rp.State,
		Error:         rp.Error,
		ActiveWorkers: rp.ActiveWorkers,
	}, nil
}

func (m *ScaleSetManager) ListRunners() []mux.RunnerStatus {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	var statuses []mux.RunnerStatus
	for name, rp := range m.processes {
		statuses = append(statuses, mux.RunnerStatus{
			Name:          name,
			Mode:          "scaleset",
			State:         rp.State,
			Error:         rp.Error,
			ActiveWorkers: rp.ActiveWorkers,
		})
	}
	return statuses
}

func (m *ScaleSetManager) MarkBusy(name string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if rp, exists := m.processes[name]; exists {
		rp.ActiveWorkers++
		if rp.State == mux.StateOnline {
			rp.State = mux.StateBusy
		}
	}
}

func (m *ScaleSetManager) MarkIdle(name string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if rp, exists := m.processes[name]; exists {
		if rp.ActiveWorkers > 0 {
			rp.ActiveWorkers--
		}
		if rp.ActiveWorkers == 0 && rp.State == mux.StateBusy {
			rp.State = mux.StateOnline
		}
	}
}
