package mux

import (
	"context"
	"fmt"
	"sync"

	"github.com/kmhalvin/github-action-runners-mux/config"
)

// ManagerHooks defines mode-specific behavior that concrete managers implement.
type ManagerHooks interface {
	// Launch starts the runner process. Called after state is set to Registering.
	// The hook is responsible for transitioning to Online on success.
	// If it returns an error, BaseManager sets the state to Failed.
	Launch(ctx context.Context, cfg *config.RunnerConfig) error
	// Halt stops the runner process. Called after state is set to Draining.
	Halt(name string, force bool) error
	// Cleanup deregisters the runner from GitHub and removes local resources.
	Cleanup(cfg config.RunnerConfig) error
	// Mode returns the runner mode string ("standalone" or "scaleset").
	Mode() string
}

// ProcessState holds the shared runtime state for a managed runner.
type ProcessState struct {
	Config        *config.RunnerConfig
	State         RunnerState
	Error         string
	ActiveWorkers int
}

// BaseManager provides shared lifecycle management for all runner modes.
type BaseManager struct {
	Hooks     ManagerHooks
	Processes map[string]*ProcessState
	Mu        sync.RWMutex
}

func NewBaseManager(hooks ManagerHooks) *BaseManager {
	return &BaseManager{
		Hooks:     hooks,
		Processes: make(map[string]*ProcessState),
	}
}

// Transition validates and performs a state transition for the named runner.
// Must be called WITHOUT holding Mu.
func (b *BaseManager) Transition(name string, to RunnerState) error {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.transitionLocked(name, to)
}

// transitionLocked validates and performs a state transition. Must be called
// WITH Mu already held.
func (b *BaseManager) transitionLocked(name string, to RunnerState) error {
	proc, exists := b.Processes[name]
	if !exists {
		return ErrRunnerNotFound
	}
	from := proc.State
	if err := ValidateTransition(from, to); err != nil {
		return err
	}
	proc.State = to
	return nil
}

// SetError sets the error message for a runner. Must be called WITHOUT holding Mu.
func (b *BaseManager) SetError(name string, errMsg string) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if proc, exists := b.Processes[name]; exists {
		proc.Error = errMsg
	}
}

// Start validates state, creates the process entry, and delegates to hooks.Launch.
func (b *BaseManager) Start(ctx context.Context, cfg config.RunnerConfig) error {
	b.Mu.Lock()
	if proc, exists := b.Processes[cfg.Name]; exists {
		if proc.State != StateOffline && proc.State != StateFailed && proc.State != StateRegistering {
			b.Mu.Unlock()
			return fmt.Errorf("%w: %s is in state %s", ErrRunnerAlreadyRunning, cfg.Name, proc.State)
		}
	}
	proc := &ProcessState{
		Config: &cfg,
		State:  StateRegistering,
	}
	b.Processes[cfg.Name] = proc
	b.Mu.Unlock()

	if err := b.Hooks.Launch(ctx, &cfg); err != nil {
		b.Mu.Lock()
		proc.State = StateFailed
		proc.Error = err.Error()
		b.Mu.Unlock()
		return err
	}
	return nil
}

// Stop validates state, sets Draining, and delegates to hooks.Halt.
func (b *BaseManager) Stop(name string, force bool) error {
	b.Mu.Lock()
	proc, exists := b.Processes[name]
	if !exists {
		b.Mu.Unlock()
		return fmt.Errorf("%w: %s", ErrRunnerNotFound, name)
	}
	if proc.State == StateOffline || proc.State == StateFailed {
		b.Mu.Unlock()
		return nil
	}
	proc.State = StateDraining
	b.Mu.Unlock()

	return b.Hooks.Halt(name, force)
}

// Deregister delegates to hooks.Cleanup.
func (b *BaseManager) Deregister(cfg config.RunnerConfig) error {
	return b.Hooks.Cleanup(cfg)
}

// GetStatus returns the status of a single runner.
func (b *BaseManager) GetStatus(name string) (RunnerStatus, error) {
	b.Mu.RLock()
	defer b.Mu.RUnlock()
	proc, exists := b.Processes[name]
	if !exists {
		return RunnerStatus{}, fmt.Errorf("%w: %s", ErrRunnerNotFound, name)
	}
	return RunnerStatus{
		Name:          name,
		Mode:          b.Hooks.Mode(),
		State:         proc.State,
		Error:         proc.Error,
		ActiveWorkers: proc.ActiveWorkers,
	}, nil
}

// ListRunners returns the status of all managed runners.
func (b *BaseManager) ListRunners() []RunnerStatus {
	b.Mu.RLock()
	defer b.Mu.RUnlock()
	var statuses []RunnerStatus
	for name, proc := range b.Processes {
		statuses = append(statuses, RunnerStatus{
			Name:          name,
			Mode:          b.Hooks.Mode(),
			State:         proc.State,
			Error:         proc.Error,
			ActiveWorkers: proc.ActiveWorkers,
		})
	}
	return statuses
}

// MarkBusy increments ActiveWorkers and transitions to Busy if Online.
func (b *BaseManager) MarkBusy(name string) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	proc, exists := b.Processes[name]
	if !exists {
		return
	}
	proc.ActiveWorkers++
	if proc.State == StateOnline || proc.State == StatePaused {
		proc.State = StateBusy
	}
}

// MarkIdle decrements ActiveWorkers and transitions back from Busy.
// The idleState parameter lets callers specify what to return to (Online or Paused).
func (b *BaseManager) MarkIdle(name string, idleState RunnerState) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	proc, exists := b.Processes[name]
	if !exists {
		return
	}
	if proc.ActiveWorkers > 0 {
		proc.ActiveWorkers--
	}
	if proc.ActiveWorkers == 0 && proc.State == StateBusy {
		proc.State = idleState
	}
}
