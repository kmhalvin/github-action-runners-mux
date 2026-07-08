package scaleset

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"sync"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

type ScaleSetProcess struct {
	Config        *config.RunnerConfig
	Cancel        context.CancelFunc
	State         mux.RunnerState
	Error         string
	ActiveWorkers int
}

type ScaleSetManager struct {
	orch      *orchestrator.Orchestrator
	db        *sql.DB
	queries   *sqlc.Queries
	processes map[string]*ScaleSetProcess
	mutex     sync.RWMutex
}

func NewScaleSetManager(orch *orchestrator.Orchestrator, db *sql.DB, queries *sqlc.Queries) *ScaleSetManager {
	return &ScaleSetManager{
		orch:      orch,
		db:        db,
		queries:   queries,
		processes: make(map[string]*ScaleSetProcess),
	}
}

func (m *ScaleSetManager) Start(ctx context.Context, cfg config.RunnerConfig) error {
	m.mutex.Lock()
	if rp, exists := m.processes[cfg.Name]; exists && rp.State != mux.StateOffline {
		m.mutex.Unlock()
		return fmt.Errorf("scaleset runner %s is already running", cfg.Name)
	}

	rp := &ScaleSetProcess{
		Config: &cfg,
		State:  mux.StateRegistering,
	}
	m.processes[cfg.Name] = rp
	m.mutex.Unlock()

	// Get global max workers for fallback
	maxWorkers := 5
	val, err := m.queries.GetSetting(ctx, "max_workers")
	if err == nil {
		if mw, err := strconv.Atoi(val); err == nil {
			maxWorkers = mw
		}
	}

	runCtx, cancel := context.WithCancel(context.Background())
	rp.Cancel = cancel

	go func() {
		err := m.runListener(runCtx, &cfg, maxWorkers, rp)
		m.mutex.Lock()
		rp.State = mux.StateOffline
		if err != nil {
			rp.Error = err.Error()
			log.Printf("[%s] ScaleSet Listener exited with error: %v", cfg.Name, err)
		} else {
			rp.Error = ""
			log.Printf("[%s] ScaleSet Listener exited cleanly", cfg.Name)
		}
		m.mutex.Unlock()
	}()

	return nil
}

func (m *ScaleSetManager) Stop(name string, force bool) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	rp, exists := m.processes[name]
	if !exists {
		return fmt.Errorf("scaleset runner %s not found", name)
	}

	if rp.State == mux.StateOffline {
		return nil
	}

	rp.State = mux.StateDraining
	if rp.Cancel != nil {
		rp.Cancel()
	}

	return nil
}
