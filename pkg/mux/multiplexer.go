package mux

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
)

type RunnerState string

const (
	StateRegistering RunnerState = "Registering"
	StateOnline      RunnerState = "Online"
	StateBusy        RunnerState = "Busy"
	StatePaused      RunnerState = "Paused"
	StateDraining    RunnerState = "Draining"
	StateOffline     RunnerState = "Offline"
)

type RunnerStatus struct {
	Name          string      `json:"name"`
	Mode          string      `json:"mode"`
	State         RunnerState `json:"state"`
	ActiveWorkers int         `json:"active_workers"`
	JobsCompleted int         `json:"jobs_completed"`
	Error         string      `json:"error,omitempty"`
}

// Runner is the interface implemented by both Standalone and ScaleSet managers
type Runner interface {
	Start(ctx context.Context, cfg config.RunnerConfig) error
	Stop(name string, force bool) error
	GetStatus(name string) (RunnerStatus, error)
	ListRunners() []RunnerStatus
	MarkBusy(name string)
	MarkIdle(name string)
}

// Multiplexer coordinates runner managers (Standalone and ScaleSet)
type Multiplexer struct {
	db         *sql.DB
	queries    *sqlc.Queries
	standalone Runner
	scaleset   Runner
	
	mu sync.RWMutex
}

func NewMultiplexer(db *sql.DB, queries *sqlc.Queries, standalone Runner, scaleset Runner) *Multiplexer {
	return &Multiplexer{
		db:         db,
		queries:    queries,
		standalone: standalone,
		scaleset:   scaleset,
	}
}

// AddRunner dynamically adds and starts a runner
func (m *Multiplexer) AddRunner(ctx context.Context, cfg config.RunnerConfig) error {
	var err error
	if cfg.Mode == "standalone" || cfg.Mode == "" {
		err = m.standalone.Start(ctx, cfg)
	} else if cfg.Mode == "scaleset" {
		err = m.scaleset.Start(ctx, cfg)
	} else {
		return fmt.Errorf("unknown runner mode: %s", cfg.Mode)
	}

	if err != nil {
		// Log but don't crash, we'll let the UI handle the error state
		log.Printf("Failed to start runner %s: %v", cfg.Name, err)
		return err
	}
	return nil
}

// RemoveRunner gracefully or forcefully stops a runner
func (m *Multiplexer) RemoveRunner(ctx context.Context, name string, force bool, mode string) error {
	if mode == "standalone" {
		return m.standalone.Stop(name, force)
	} else if mode == "scaleset" {
		return m.scaleset.Stop(name, force)
	}
	return fmt.Errorf("unknown runner mode: %s", mode)
}

// GetRunnerStatuses returns the combined status of all runners
func (m *Multiplexer) GetRunnerStatuses() []RunnerStatus {
	var statuses []RunnerStatus
	
	if m.standalone != nil {
		statuses = append(statuses, m.standalone.ListRunners()...)
	}
	if m.scaleset != nil {
		statuses = append(statuses, m.scaleset.ListRunners()...)
	}
	
	return statuses
}

// MarkBusy marks a runner as busy when a job is allocated
func (m *Multiplexer) MarkBusy(name string) {
	if m.standalone != nil {
		m.standalone.MarkBusy(name)
	}
	if m.scaleset != nil {
		m.scaleset.MarkBusy(name)
	}
}

// MarkIdle marks a runner as idle when a job completes
func (m *Multiplexer) MarkIdle(name string) {
	if m.standalone != nil {
		m.standalone.MarkIdle(name)
	}
	if m.scaleset != nil {
		m.scaleset.MarkIdle(name)
	}
}
