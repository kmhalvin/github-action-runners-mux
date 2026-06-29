package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

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
		// If not found, create it
		labels := []scaleset.Label{{Name: cfg.ScaleSetName, Type: "custom"}}
		if cfg.Labels != "" {
			labels = append(labels, scaleset.Label{Name: cfg.Labels, Type: "custom"})
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

	sessionClient, err := client.MessageSessionClient(ctx, scaleSet.ID, "multi-listener-runner")
	if err != nil {
		log.Printf("[%s] Failed to create message session client: %v", cfg.Name, err)
		return
	}
	defer sessionClient.Close(ctx)

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
		runnerName:     cfg.Name,
		scaleSetID:     scaleSet.ID,
		scalesetClient: client,
		maxRunners:     listenerMaxRunners,
		runnerCount:    0,
	}

	if err := lsnr.Run(ctx, scaler); err != nil {
		log.Printf("[%s] Listener run failed: %v", cfg.Name, err)
	}
}

type Scaler struct {
	mux            *Multiplexer
	runnerName     api.RunnerName
	scaleSetID     int
	scalesetClient *scaleset.Client
	maxRunners     int
	mutex          sync.Mutex
	runnerCount    int
}

func (s *Scaler) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	s.mutex.Lock()
	currentCount := s.runnerCount
	s.mutex.Unlock()

	targetRunnerCount := min(count, s.maxRunners)

	if targetRunnerCount > currentCount {
		scaleUp := targetRunnerCount - currentCount
		log.Printf("[%s] Scaling up runners by %d (Target: %d, Current: %d)", s.runnerName, scaleUp, targetRunnerCount, currentCount)

		for range scaleUp {
			go s.startWorker(context.Background())
		}

		s.mutex.Lock()
		s.runnerCount = targetRunnerCount
		s.mutex.Unlock()
	}

	return targetRunnerCount, nil
}

func (s *Scaler) startWorker(ctx context.Context) {
	name := fmt.Sprintf("runner-%s", uuid.NewString()[:8])

	jit, err := s.scalesetClient.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{
		Name: name,
	}, s.scaleSetID)
	if err != nil {
		log.Printf("[%s] Failed to generate JIT config: %v", s.runnerName, err)
		s.decrementCount()
		return
	}

	ww, err := s.mux.orch.AllocateContainer(ctx, s.runnerName)
	if err != nil {
		log.Printf("[%s] Failed to allocate warm container: %v", s.runnerName, err)
		s.decrementCount()
		return
	}

	reqBody, _ := json.Marshal(api.StartRequest{
		JITConfig: jit.EncodedJITConfig,
	})

	resp, err := http.Post(fmt.Sprintf("http://%s:9001/start", ww.IPAddress), "application/json", bytes.NewBuffer(reqBody))
	if err != nil || resp.StatusCode != http.StatusOK {
		log.Printf("[%s] Failed to send JIT config to worker %s: %v", s.runnerName, ww.IPAddress, err)
		s.decrementCount()
		return
	}

	log.Printf("[%s] Successfully dispatched JIT config to warm worker %s", s.runnerName, ww.IPAddress)
}

func (s *Scaler) decrementCount() {
	s.mutex.Lock()
	s.runnerCount--
	s.mutex.Unlock()
}

func (s *Scaler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	log.Printf("[%s] Job started: %s", s.runnerName, jobInfo.JobID)
	return nil
}

func (s *Scaler) HandleJobCompleted(ctx context.Context, jobInfo *scaleset.JobCompleted) error {
	log.Printf("[%s] Job completed: %s", s.runnerName, jobInfo.JobID)
	s.decrementCount()
	return nil
}
