package scaleset

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/actions/scaleset"
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
	if rp, exists := m.processes[cfg.Name]; exists && rp.State != mux.StateOffline && rp.State != mux.StateFailed {
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
		if err != nil {
			rp.State = mux.StateFailed
			rp.Error = err.Error()
			log.Printf("[%s] ScaleSet Listener exited with error: %v", cfg.Name, err)
		} else {
			rp.State = mux.StateOffline
			rp.Error = ""
			log.Printf("[%s] ScaleSet Listener exited cleanly", cfg.Name)
		}
		m.mutex.Unlock()
	}()

	// Wait for the listener to come online, fail, or timeout.
	// This catches fast failures (bad PAT, bad URL, scale set creation
	// errors) synchronously so the HTTP handler can return an error and
	// the frontend can redirect to the detail page for editing/retry.
	// If the listener is still registering after 60s, return nil and let
	// it continue in the background (same as the old async behavior).
	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.mutex.RLock()
			state := rp.State
			errMsg := rp.Error
			m.mutex.RUnlock()
			if state == mux.StateOnline {
				return nil // success — listener is running
			}
			if state == mux.StateFailed {
				return fmt.Errorf("scaleset listener failed: %s", errMsg)
			}
			// Still Registering — keep waiting
		case <-timeout:
			log.Printf("[%s] ScaleSet listener still registering after 60s — continuing in background", cfg.Name)
			return nil
		}
	}
}

func (m *ScaleSetManager) Stop(name string, force bool) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	rp, exists := m.processes[name]
	if !exists {
		return fmt.Errorf("scaleset runner %s not found", name)
	}

	if rp.State == mux.StateOffline || rp.State == mux.StateFailed {
		return nil
	}

	rp.State = mux.StateDraining
	if rp.Cancel != nil {
		rp.Cancel()
	}

	return nil
}

// Deregister removes the runner scale set from GitHub using the PAT.
// It creates a scaleset client, looks up the scale set by name, and deletes it.
// If the scale set is not found on GitHub, it returns nil (nothing to delete).
func (m *ScaleSetManager) Deregister(cfg config.RunnerConfig) error {
	ctx := context.Background()

	client, err := scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{
		GitHubConfigURL:     cfg.URL,
		PersonalAccessToken: cfg.PAT,
	})
	if err != nil {
		return fmt.Errorf("failed to create scaleset client: %w", err)
	}

	runnerGroup := cfg.Group
	if runnerGroup == "" {
		runnerGroup = scaleset.DefaultRunnerGroup
	}

	var runnerGroupID int
	if runnerGroup == scaleset.DefaultRunnerGroup {
		runnerGroupID = 1
	} else {
		rg, err := client.GetRunnerGroupByName(ctx, runnerGroup)
		if err != nil {
			return fmt.Errorf("failed to get runner group ID: %w", err)
		}
		runnerGroupID = rg.ID
	}

	scaleSet, err := client.GetRunnerScaleSet(ctx, runnerGroupID, cfg.ScaleSetName)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			log.Printf("[%s] Scale set not found on GitHub — nothing to deregister", cfg.Name)
			return nil
		}
		return fmt.Errorf("failed to get runner scale set: %w", err)
	}
	if scaleSet == nil {
		log.Printf("[%s] Scale set not found on GitHub — nothing to deregister", cfg.Name)
		return nil
	}

	log.Printf("[%s] Deleting scale set '%s' (ID: %d) from GitHub...", cfg.Name, cfg.ScaleSetName, scaleSet.ID)
	if err := client.DeleteRunnerScaleSet(ctx, scaleSet.ID); err != nil {
		return fmt.Errorf("failed to delete runner scale set: %w", err)
	}

	log.Printf("[%s] Successfully deleted scale set from GitHub", cfg.Name)
	return nil
}
