package mux

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
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
	Deregister(cfg config.RunnerConfig) error
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
	switch cfg.Mode {
	case "standalone", "":
		err = m.standalone.Start(ctx, cfg)
	case "scaleset":
		err = m.scaleset.Start(ctx, cfg)
	default:
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
	switch mode {
	case "standalone":
		return m.standalone.Stop(name, force)
	case "scaleset":
		return m.scaleset.Stop(name, force)
	}
	return fmt.Errorf("unknown runner mode: %s", mode)
}

// Deregister removes the runner from GitHub (config.sh remove for standalone,
// DeleteRunnerScaleSet for scaleset) without stopping the local process.
func (m *Multiplexer) Deregister(cfg config.RunnerConfig) error {
	switch cfg.Mode {
	case "standalone", "":
		return m.standalone.Deregister(cfg)
	case "scaleset":
		return m.scaleset.Deregister(cfg)
	}
	return fmt.Errorf("unknown runner mode: %s", cfg.Mode)
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
