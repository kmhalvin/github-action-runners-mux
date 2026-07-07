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

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
)

type ScaleSetProcess struct {
	Config *config.RunnerConfig
	Cancel context.CancelFunc
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

func (m *ScaleSetManager) runListener(ctx context.Context, cfg *config.RunnerConfig, globalMaxWorkers int, rp *ScaleSetProcess) error {
	log.Printf("[%s] Starting ScaleSet listener...", cfg.Name)

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
		return fmt.Errorf("failed to get runner scale set: %w", err)
	}
	if scaleSet == nil {
		// If not found, create it
		labels := []scaleset.Label{{Name: cfg.ScaleSetName, Type: "custom"}}
		if len(cfg.Labels) > 0 {
			for _, lbl := range cfg.Labels {
				lbl = strings.TrimSpace(lbl)
				if lbl != "" {
					labels = append(labels, scaleset.Label{Name: lbl, Type: "custom"})
				}
			}
		}

		scaleSet, err = client.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
			Name:          cfg.ScaleSetName,
			RunnerGroupID: runnerGroupID,
			Labels:        labels,
		})
		if err != nil {
			return fmt.Errorf("failed to create runner scale set: %w", err)
		}
	}

	client.SetSystemInfo(scaleset.SystemInfo{
		ScaleSetID: scaleSet.ID,
	})

	sessionClient, err := client.MessageSessionClient(ctx, scaleSet.ID, "github-mux")
	if err != nil {
		return fmt.Errorf("failed to create message session client: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sessionClient.Close(closeCtx)
	}()

	listenerMaxRunners := globalMaxWorkers
	if cfg.MaxRunners > 0 {
		listenerMaxRunners = cfg.MaxRunners // Override for this scale set
	}
	lsnr, err := listener.New(sessionClient, listener.Config{
		ScaleSetID: scaleSet.ID,
		MaxRunners: listenerMaxRunners,
	})
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	scaler := &Scaler{
		orch:           m.orch,
		runnerName:     cfg.Name,
		scaleSetID:     scaleSet.ID,
		scalesetClient: client,
		maxRunners:     listenerMaxRunners,
	}

	m.mutex.Lock()
	rp.State = mux.StateOnline
	m.mutex.Unlock()

	return lsnr.Run(ctx, scaler)
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
