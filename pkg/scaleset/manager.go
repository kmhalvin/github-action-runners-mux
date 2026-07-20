package scaleset

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/actions/scaleset"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

type ScaleSetManager struct {
	*mux.BaseManager
	orch      *orchestrator.Orchestrator
	db        *sql.DB
	queries   *sqlc.Queries
	cancels   map[string]context.CancelFunc
}

func NewScaleSetManager(orch *orchestrator.Orchestrator, db *sql.DB, queries *sqlc.Queries) *ScaleSetManager {
	m := &ScaleSetManager{
		orch:    orch,
		db:      db,
		queries: queries,
		cancels: make(map[string]context.CancelFunc),
	}
	m.BaseManager = mux.NewBaseManager(m)
	return m
}

// Launch implements mux.ManagerHooks
func (m *ScaleSetManager) Launch(ctx context.Context, cfg *sqlc.Runner, token string) error {
	// Get global max workers for fallback
	maxWorkers := 5
	val, err := m.queries.GetSetting(ctx, "max_workers")
	if err == nil {
		if mw, err := strconv.Atoi(val); err == nil {
			maxWorkers = mw
		}
	}

	runCtx, cancel := context.WithCancel(context.Background())
	
	m.BaseManager.Mu.Lock()
	m.cancels[cfg.Name] = cancel
	m.BaseManager.Mu.Unlock()

	go func() {
		err := m.runListener(runCtx, cfg, maxWorkers)
		if err != nil {
			m.BaseManager.Transition(cfg.Name, mux.StateFailed)
			m.BaseManager.SetError(cfg.Name, err.Error())
			log.Printf("[%s] ScaleSet Listener exited with error: %v", cfg.Name, err)
		} else {
			m.BaseManager.Transition(cfg.Name, mux.StateOffline)
			m.BaseManager.SetError(cfg.Name, "")
			log.Printf("[%s] ScaleSet Listener exited cleanly", cfg.Name)
		}
		
		m.BaseManager.Mu.Lock()
		delete(m.cancels, cfg.Name)
		m.BaseManager.Mu.Unlock()
	}()

	// Wait for the listener to come online, fail, or timeout.
	// This catches fast failures synchronously.
	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.BaseManager.Mu.RLock()
			proc, exists := m.BaseManager.Processes[cfg.Name]
			m.BaseManager.Mu.RUnlock()
			if exists {
				if proc.State == mux.StateOnline {
					return nil // success
				}
				if proc.State == mux.StateFailed {
					return fmt.Errorf("scaleset listener failed: %s", proc.Error)
				}
			}
		case <-timeout:
			log.Printf("[%s] ScaleSet listener still registering after 60s — continuing in background", cfg.Name)
			return nil
		}
	}
}

// Halt implements mux.ManagerHooks
func (m *ScaleSetManager) Halt(name string, force bool) error {
	m.BaseManager.Mu.Lock()
	cancel, exists := m.cancels[name]
	m.BaseManager.Mu.Unlock()
	
	if exists && cancel != nil {
		cancel()
	}
	return nil
}

// Cleanup implements mux.ManagerHooks
func (m *ScaleSetManager) Cleanup(cfg sqlc.Runner, token string) error {
	ctx := context.Background()

	client, err := scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{
		GitHubConfigURL:     cfg.URL,
		PersonalAccessToken: cfg.PAT,
	})
	if err != nil {
		return fmt.Errorf("failed to create scaleset client: %w", err)
	}

	runnerGroup := cfg.RunnerGroup
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

	scaleSet, err := client.GetRunnerScaleSet(ctx, runnerGroupID, cfg.Name)
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

	log.Printf("[%s] Deleting scale set '%s' (ID: %d) from GitHub...", cfg.Name, cfg.Name, scaleSet.ID)
	if err := client.DeleteRunnerScaleSet(ctx, scaleSet.ID); err != nil {
		return fmt.Errorf("failed to delete runner scale set: %w", err)
	}

	log.Printf("[%s] Successfully deleted scale set from GitHub", cfg.Name)
	return nil
}

// Mode implements mux.ManagerHooks
func (m *ScaleSetManager) Mode() string {
	return "scaleset"
}

// MarkIdle overrides BaseManager.MarkIdle to return to Online state unconditionally.
func (m *ScaleSetManager) MarkIdle(name string) {
	m.BaseManager.MarkIdle(name, mux.StateOnline)
}
