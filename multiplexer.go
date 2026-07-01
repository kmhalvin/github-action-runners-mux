package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/pkg/api"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"github.com/google/uuid"
)

type Multiplexer struct {
	orch *Orchestrator
}

func NewMultiplexer(orch *Orchestrator) *Multiplexer {
	return &Multiplexer{
		orch: orch,
	}
}

// StartAll initializes the environment and starts all listeners concurrently.
func (m *Multiplexer) StartAll(ctx context.Context, cfg *Config, wg *sync.WaitGroup) error {
	for i := range cfg.Runners {
		rCfg := &cfg.Runners[i]
		wg.Add(1)
		go func(c *RunnerConfig) {
			defer wg.Done()
			m.startRunner(ctx, c, cfg.MaxWorkers)
		}(rCfg)
	}
	return nil
}

func (m *Multiplexer) startRunner(ctx context.Context, cfg *RunnerConfig, maxWorkers int) {
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
		for _, lbl := range cfg.Labels {
			lbl = strings.TrimSpace(lbl)
			if lbl != "" {
				labels = append(labels, scaleset.Label{Name: lbl, Type: "custom"})
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
	listenerMaxRunners := maxWorkers
	if cfg.MaxRunners > 0 {
		listenerMaxRunners = cfg.MaxRunners
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
		mux:            m,
		orch:           m.orch,
		runnerName:     api.RunnerName(cfg.Name),
		scaleSetID:     scaleSet.ID,
		scalesetClient: client,
		maxRunners:     listenerMaxRunners,
	}

	if err := lsnr.Run(ctx, scaler); err != nil {
		log.Printf("[%s] Listener run failed: %v", cfg.Name, err)
	}
}

type Scaler struct {
	mux            *Multiplexer
	orch           *Orchestrator
	runnerName     api.RunnerName
	scaleSetID     int
	scalesetClient *scaleset.Client
	maxRunners     int
	mutex          sync.Mutex
	pendingCount   int
}

func (s *Scaler) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	s.mutex.Lock()
	pending := s.pendingCount
	s.mutex.Unlock()

	currentCount := s.orch.GetActiveCount(s.runnerName) + pending

	targetRunnerCount := count
	if targetRunnerCount > s.maxRunners {
		targetRunnerCount = s.maxRunners
	}

	if targetRunnerCount > currentCount {
		scaleUp := targetRunnerCount - currentCount
		log.Printf("[%s] Scaling up runners by %d (Target: %d, Current: %d = %d active + %d pending)", s.runnerName, scaleUp, targetRunnerCount, currentCount, s.orch.GetActiveCount(s.runnerName), pending)

		s.mutex.Lock()
		s.pendingCount += scaleUp
		s.mutex.Unlock()

		for i := 0; i < scaleUp; i++ {
			go s.startWorker(context.Background())
		}
	}

	return targetRunnerCount, nil
}

func (s *Scaler) startWorker(ctx context.Context) {
	defer func() {
		s.mutex.Lock()
		s.pendingCount--
		s.mutex.Unlock()
	}()
	name := fmt.Sprintf("runner-%s", uuid.NewString()[:8])

	jit, err := s.scalesetClient.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{
		Name: name,
	}, s.scaleSetID)
	if err != nil {
		log.Printf("[%s] Failed to generate JIT config: %v", s.runnerName, err)
		return
	}

	ww, err := s.mux.orch.AllocateContainer(ctx, s.runnerName)
	if err != nil {
		log.Printf("[%s] Failed to allocate warm container: %v", s.runnerName, err)
		return
	}

	reqBody, _ := json.Marshal(api.StartRequest{
		JITConfig: jit.EncodedJITConfig,
	})

	resp, err := http.Post(fmt.Sprintf("http://%s:9001/start", ww.IPAddress), "application/json", bytes.NewBuffer(reqBody))
	if err != nil || resp.StatusCode != http.StatusOK {
		log.Printf("[%s] Failed to send JIT config to worker %s: %v", s.runnerName, ww.IPAddress, err)
		return
	}

	log.Printf("[%s] Successfully dispatched JIT config to warm worker %s", s.runnerName, ww.IPAddress)
}

func (s *Scaler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	log.Printf("[%s] Job started: %s", s.runnerName, jobInfo.JobID)
	return nil
}

func (s *Scaler) HandleJobCompleted(ctx context.Context, jobInfo *scaleset.JobCompleted) error {
	log.Printf("[%s] Job completed: %s", s.runnerName, jobInfo.JobID)
	return nil
}
