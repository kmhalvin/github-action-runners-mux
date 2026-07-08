package scaleset

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
)

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
