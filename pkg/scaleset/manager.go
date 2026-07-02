package scaleset

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
)

type ScaleSetManager struct {
	orch *orchestrator.Orchestrator
}

func NewScaleSetManager(orch *orchestrator.Orchestrator) *ScaleSetManager {
	return &ScaleSetManager{
		orch: orch,
	}
}

func (m *ScaleSetManager) StartRunner(ctx context.Context, cfg *config.RunnerConfig, maxWorkers int) {
	log.Printf("[%s] Starting ScaleSet listener...", cfg.Name)

	client, err := scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{
		GitHubConfigURL:     cfg.URL,
		PersonalAccessToken: cfg.PAT,
	})
	if err != nil {
		log.Printf("[%s] Failed to create scaleset client: %v", cfg.Name, err)
		return
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
			log.Printf("[%s] Failed to get runner group ID: %v", cfg.Name, err)
			return
		}
		runnerGroupID = rg.ID
	}

	scaleSet, err := client.GetRunnerScaleSet(ctx, runnerGroupID, cfg.ScaleSetName)
	if err != nil {
		log.Printf("[%s] Failed to get runner scale set: %v", cfg.Name, err)
		return
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
			log.Printf("[%s] Failed to create runner scale set: %v", cfg.Name, err)
			return
		}
	}

	client.SetSystemInfo(scaleset.SystemInfo{
		ScaleSetID: scaleSet.ID,
	})

	sessionClient, err := client.MessageSessionClient(ctx, scaleSet.ID, "github-mux")
	if err != nil {
		log.Printf("[%s] Failed to create message session client: %v", cfg.Name, err)
		return
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := sessionClient.Close(closeCtx); err != nil {
			log.Printf("[%s] Failed to close message session: %v", cfg.Name, err)
		}
	}()

	log.Printf("[%s] Initializing listener", cfg.Name)
	listenerMaxRunners := maxWorkers // Default to global max workers
	if cfg.MaxRunners > 0 {
		listenerMaxRunners = cfg.MaxRunners // Override for this scale set
	}
	lsnr, err := listener.New(sessionClient, listener.Config{
		ScaleSetID: scaleSet.ID,
		MaxRunners: listenerMaxRunners,
	})
	if err != nil {
		log.Printf("[%s] Failed to create listener: %v", cfg.Name, err)
		return
	}

	scaler := &Scaler{
		orch:           m.orch,
		runnerName:     cfg.Name,
		scaleSetID:     scaleSet.ID,
		scalesetClient: client,
		maxRunners:     listenerMaxRunners,
	}

	if err := lsnr.Run(ctx, scaler); err != nil {
		log.Printf("[%s] Listener run failed: %v", cfg.Name, err)
	}
}

