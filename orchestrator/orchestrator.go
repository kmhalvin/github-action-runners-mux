package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/kmhalvin/github-action-runners-mux/manager"
)

type Orchestrator struct {
	mgr           *manager.Manager
	dockerCli     *client.Client
	mutex         sync.Mutex
	activeRunners map[string]int // runnerName -> active count
	maxWorkers    int
	isPaused      bool
	workerSem     chan struct{} // Counting semaphore
}

func NewOrchestrator(mgr *manager.Manager, maxWorkers int) (*Orchestrator, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	return &Orchestrator{
		mgr:           mgr,
		dockerCli:     cli,
		activeRunners: make(map[string]int),
		maxWorkers:    maxWorkers,
		isPaused:      false,
		workerSem:     make(chan struct{}, maxWorkers),
	}, nil
}

func (o *Orchestrator) evaluateCapacity() {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	currentActive := len(o.workerSem)
	log.Printf("[Orchestrator] Capacity evaluation: %d/%d workers active.", currentActive, o.maxWorkers)

	if currentActive >= o.maxWorkers && !o.isPaused {
		log.Printf("[Orchestrator] MAX CAPACITY REACHED. Freezing idle listeners...")
		o.isPaused = true

		var active []string
		for rName, count := range o.activeRunners {
			if count > 0 {
				active = append(active, rName)
			}
		}
		
		// We call the manager natively!
		o.mgr.LockOthers(active)
	} else if currentActive < o.maxWorkers && o.isPaused {
		log.Printf("[Orchestrator] CAPACITY FREED. Unfreezing listeners...")
		o.isPaused = false
		o.mgr.UnlockOthers()
	}
}

func (o *Orchestrator) HandleAllocate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		RunnerName string `json:"runner_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// Wait for capacity
	select {
	case <-r.Context().Done():
		log.Printf("[Orchestrator] Request aborted by client while waiting for capacity.")
		return
	case o.workerSem <- struct{}{}:
	}

	capacityAcquired := true
	
	// Mark runner as active immediately
	o.mutex.Lock()
	o.activeRunners[payload.RunnerName]++
	o.mutex.Unlock()

	defer func() {
		if capacityAcquired {
			<-o.workerSem
			o.mutex.Lock()
			o.activeRunners[payload.RunnerName]--
			o.mutex.Unlock()
			o.evaluateCapacity()
		}
	}()

	// Instantly evaluate capacity to freeze listeners if we hit max
	o.evaluateCapacity()

	containerName := fmt.Sprintf("worker-%d", time.Now().UnixNano())
	ctx := context.Background()

	workerEnv := []string{}
	startDocker := os.Getenv("WORKER_START_DOCKER_SERVICE") == "true"
	if startDocker {
		workerEnv = append(workerEnv, "START_DOCKER_SERVICE=true")
	}

	workerImage := os.Getenv("WORKER_IMAGE")
	if workerImage == "" {
		workerImage = "github-action-runners-mux:latest"
	}

	resp, err := o.dockerCli.ContainerCreate(ctx,
		&container.Config{
			Image:      workerImage,
			Env:        workerEnv,
			Entrypoint: []string{"/usr/bin/dumb-init", "--", "/bin/bash", "-c"},
			Cmd: []string{`
if [ "$START_DOCKER_SERVICE" = "true" ]; then
	echo "Starting Docker-in-Docker service..."
	sudo service docker start || service docker start
fi
exec worker-shim
			`},
		},
		&container.HostConfig{
			// NetworkMode allows the worker to talk back to this proxy easily
			NetworkMode: "github-action-runners-mux_default",
			// DinD requires privileged mode
			Privileged:  startDocker,
			// Ensure Docker daemon cleans it up if the proxy gets hard killed
			AutoRemove:  true,
		},
		&network.NetworkingConfig{},
		nil,
		containerName,
	)
	if err != nil {
		log.Printf("[Orchestrator] Failed to create container: %v", err)
		http.Error(w, "Failed to create worker container", http.StatusInternalServerError)
		return
	}

	if err := o.dockerCli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		log.Printf("[Orchestrator] Failed to start container: %v", err)
		http.Error(w, "Failed to start worker container", http.StatusInternalServerError)
		return
	}

	inspect, err := o.dockerCli.ContainerInspect(ctx, resp.ID)
	if err != nil {
		log.Printf("[Orchestrator] Failed to inspect container: %v", err)
		http.Error(w, "Failed to get worker IP", http.StatusInternalServerError)
		return
	}

	var ipAddress string
	for _, netObj := range inspect.NetworkSettings.Networks {
		ipAddress = netObj.IPAddress
		break
	}

	log.Printf("[Orchestrator] Spawned worker container %s at %s for runner %s", containerName, ipAddress, payload.RunnerName)

	// Transfer semaphore ownership to the monitorWorker goroutine
	capacityAcquired = false
	go o.monitorWorker(resp.ID, payload.RunnerName)

	json.NewEncoder(w).Encode(map[string]string{
		"worker_ip": ipAddress,
	})
}

func (o *Orchestrator) monitorWorker(containerID string, runnerName string) {
	ctx := context.Background()
	
	// Ensure we release capacity when the container dies
	defer func() {
		<-o.workerSem
		o.mutex.Lock()
		o.activeRunners[runnerName]--
		o.mutex.Unlock()
		o.evaluateCapacity()
	}()

	statusCh, errCh := o.dockerCli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			log.Printf("[Orchestrator] Error waiting for worker container %s: %v", containerID, err)
		}
	case <-statusCh:
		log.Printf("[Orchestrator] Worker container %s finished execution. AutoRemove will clean it up.", containerID)
	}
}
