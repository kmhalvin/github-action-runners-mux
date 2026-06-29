package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/pkg/api"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

type WarmWorker struct {
	ContainerID string
	IPAddress   string
}

type Orchestrator struct {
	dockerCli         *client.Client
	mutex             sync.Mutex
	activeListeners   map[api.RunnerName]int // runnerName -> active count
	maxWorkers        int
	warmWorkersConfig int
	isPaused          bool
	workerSem         chan struct{} // Counting semaphore
	warmPool          chan *WarmWorker
	bootingCount      int
	workerAssignments map[string]api.RunnerName // ContainerID -> api.RunnerName (empty means warm)
	deadWarmWorkers   map[string]bool           // ContainerID -> true if died while warm
}

func NewOrchestrator(maxWorkers int, warmWorkers int) (*Orchestrator, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	o := &Orchestrator{
		dockerCli:         cli,
		activeListeners:   make(map[api.RunnerName]int),
		maxWorkers:        maxWorkers,
		warmWorkersConfig: warmWorkers,
		isPaused:          false,
		workerSem:         make(chan struct{}, maxWorkers),
		warmPool:          make(chan *WarmWorker, maxWorkers),
		workerAssignments: make(map[string]api.RunnerName),
		deadWarmWorkers:   make(map[string]bool),
	}

	go o.maintainPool()

	return o, nil
}

func (o *Orchestrator) evaluateCapacity() {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	totalAssigned := 0
	for _, count := range o.activeListeners {
		totalAssigned += count
	}

	log.Printf("[Orchestrator] Capacity evaluation: %d/%d workers assigned to jobs. (Pool: %d warm, %d booting)", totalAssigned, o.maxWorkers, len(o.warmPool), o.bootingCount)

	if totalAssigned >= o.maxWorkers && !o.isPaused {
		log.Printf("[Orchestrator] MAX CAPACITY REACHED.")
		o.isPaused = true
	} else if totalAssigned < o.maxWorkers && o.isPaused {
		log.Printf("[Orchestrator] CAPACITY FREED.")
		o.isPaused = false
	}
}

// IsPaused returns true if the orchestrator is at max capacity
func (o *Orchestrator) IsPaused() bool {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return o.isPaused
}

func (o *Orchestrator) startContainer() (*WarmWorker, error) {
	containerName := fmt.Sprintf("worker-%d", time.Now().UnixNano())
	ctx := context.Background()

	workerEnv := []string{}
	startDocker := os.Getenv("WORKER_START_DOCKER_SERVICE") == "true"
	if startDocker {
		workerEnv = append(workerEnv, "START_DOCKER_SERVICE=true")
	}

	workerImage := os.Getenv("WORKER_IMAGE")
	if workerImage == "" {
		workerImage = "multi-listener-worker:latest"
	}

	resp, err := o.dockerCli.ContainerCreate(ctx,
		&container.Config{
			Image: workerImage,
			Env:   workerEnv,
			// The entrypoint is already configured in Dockerfile.runner to start worker-launcher.
			// We don't need to override it here anymore.
		},
		&container.HostConfig{
			// NetworkMode allows the multiplexer to talk to the worker's HTTP API
			NetworkMode: "github-action-runners-mux_default",
			// DinD requires privileged mode
			Privileged: startDocker,
			// Ensure Docker daemon cleans it up if the orchestrator gets hard killed
			AutoRemove: true,
		},
		&network.NetworkingConfig{},
		nil,
		containerName,
	)
	if err != nil {
		log.Printf("[Orchestrator] Failed to create container: %v", err)
		return nil, err
	}

	if err := o.dockerCli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		log.Printf("[Orchestrator] Failed to start container: %v", err)
		return nil, err
	}

	inspect, err := o.dockerCli.ContainerInspect(ctx, resp.ID)
	if err != nil {
		log.Printf("[Orchestrator] Failed to inspect container: %v", err)
		return nil, err
	}

	var ipAddress string
	for _, netObj := range inspect.NetworkSettings.Networks {
		ipAddress = netObj.IPAddress
		break
	}

	log.Printf("[Orchestrator] Spawned worker container %s at %s", containerName, ipAddress)

	return &WarmWorker{
		ContainerID: resp.ID,
		IPAddress:   ipAddress,
	}, nil
}

func (o *Orchestrator) maintainPool() {
	for {
		o.mutex.Lock()
		currentWarmAndBooting := len(o.warmPool) + o.bootingCount
		needsWarm := o.warmWorkersConfig - currentWarmAndBooting
		o.mutex.Unlock()

	ReplenishLoop:
		for range needsWarm {
			select {
			case o.workerSem <- struct{}{}:
				o.mutex.Lock()
				o.bootingCount++
				o.mutex.Unlock()

				go func() {
					ww, err := o.startContainer()

					o.mutex.Lock()
					o.bootingCount--
					if err == nil {
						o.workerAssignments[ww.ContainerID] = "" // Mark as warm
					}
					o.mutex.Unlock()

					if err == nil {
						o.warmPool <- ww
						go o.monitorWorker(ww.ContainerID)
					} else {
						<-o.workerSem
						o.evaluateCapacity()
					}
				}()
			default:
				// No capacity available to spawn warm workers
				break ReplenishLoop
			}
		}

		time.Sleep(500 * time.Millisecond)
	}
}

// AllocateContainer gives a WarmWorker to the requester, blocking until capacity is available.
func (o *Orchestrator) AllocateContainer(ctx context.Context, runnerName api.RunnerName) (*WarmWorker, error) {
	for {
		select {
		case ww := <-o.warmPool:
			o.mutex.Lock()
			isDead := o.deadWarmWorkers[ww.ContainerID]
			if isDead {
				delete(o.deadWarmWorkers, ww.ContainerID)
				o.mutex.Unlock()
				continue // Container died while warm, grab another
			}

			// Assign it
			o.workerAssignments[ww.ContainerID] = runnerName
			o.activeListeners[runnerName]++
			o.mutex.Unlock()

			o.evaluateCapacity()
			return ww, nil

		case <-ctx.Done():
			log.Printf("[Orchestrator] Allocation aborted by context.")
			return nil, ctx.Err()

		case o.workerSem <- struct{}{}:
			// Dynamically boot a container since warm pool is empty
			ww, err := o.startContainer()
			if err != nil {
				<-o.workerSem
				return nil, fmt.Errorf("failed to create worker container: %w", err)
			}

			o.mutex.Lock()
			o.workerAssignments[ww.ContainerID] = runnerName
			o.activeListeners[runnerName]++
			o.mutex.Unlock()

			go o.monitorWorker(ww.ContainerID)
			o.evaluateCapacity()

			return ww, nil
		}
	}
}

func (o *Orchestrator) monitorWorker(containerID string) {
	ctx := context.Background()

	statusCh, errCh := o.dockerCli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			log.Printf("[Orchestrator] Error waiting for worker container %s: %v", containerID, err)
		}
	case <-statusCh:
		log.Printf("[Orchestrator] Worker container %s finished execution. AutoRemove will clean it up.", containerID)
	}

	<-o.workerSem

	o.mutex.Lock()
	assignedRunner := o.workerAssignments[containerID]
	delete(o.workerAssignments, containerID)

	if assignedRunner == "" {
		// Died while warm
		o.deadWarmWorkers[containerID] = true
	} else {
		// Died while active
		o.activeListeners[assignedRunner]--
	}
	o.mutex.Unlock()

	o.evaluateCapacity()
}
