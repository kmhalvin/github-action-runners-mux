package mux

import (
	"fmt"
	"slices"
)

type RunnerState string

const (
	StateRegistering RunnerState = "Registering"
	StateOnline      RunnerState = "Online"
	StateBusy        RunnerState = "Busy"
	StatePaused      RunnerState = "Paused"
	StateDraining    RunnerState = "Draining"
	StateOffline     RunnerState = "Offline"
	StateFailed      RunnerState = "Failed"
)

// validTransitions defines the allowed state transitions. Any transition
// not in this map is rejected by ValidateTransition.
var validTransitions = map[RunnerState][]RunnerState{
	StateRegistering: {StateOnline, StateFailed, StateOffline},
	StateOnline:      {StateBusy, StatePaused, StateDraining, StateFailed, StateOffline},
	StateBusy:        {StateOnline, StateDraining, StateFailed},
	StatePaused:      {StateOnline, StateDraining},
	StateDraining:    {StateOffline},
	StateOffline:     {StateRegistering},
	StateFailed:      {StateRegistering, StateOffline},
}

// ValidateTransition checks if transitioning from `from` to `to` is allowed.
func ValidateTransition(from, to RunnerState) error {
	allowed, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidTransition, from)
	}
	if slices.Contains(allowed, to) {
		return nil
	}
	return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
}
