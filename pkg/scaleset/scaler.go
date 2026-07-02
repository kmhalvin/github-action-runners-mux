package scaleset

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/kmhalvin/github-action-runners-mux/api"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"

	"github.com/actions/scaleset"
	"github.com/google/uuid"
)

type Scaler struct {
	orch           *orchestrator.Orchestrator
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

	// Uses AllocateJIT directly on orchestrator
	err = s.orch.AllocateJIT(ctx, s.runnerName, jit.EncodedJITConfig)
	if err != nil {
		log.Printf("[%s] Failed to allocate JIT worker: %v", s.runnerName, err)
		return
	}
}

func (s *Scaler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	log.Printf("[%s] Job started: %s", s.runnerName, jobInfo.JobID)
	return nil
}

func (s *Scaler) HandleJobCompleted(ctx context.Context, jobInfo *scaleset.JobCompleted) error {
	log.Printf("[%s] Job completed: %s", s.runnerName, jobInfo.JobID)
	return nil
}
