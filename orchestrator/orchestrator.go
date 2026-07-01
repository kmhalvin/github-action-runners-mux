package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/kmhalvin/github-action-runners-mux/api"
	"github.com/kmhalvin/github-action-runners-mux/multiplexer"
)

const (
	labelManaged = "github-mux.managed"
	labelRunner  = "github-mux.runner"

	namePrefixWarm   = "github-mux-warm-"
	namePrefixActive = "github-mux-active-"

	eventReplayMargin = 60 // seconds
)

type WarmWorker struct {
	ContainerID string
	IPAddress   string
}

type ActiveWorker struct {
	ContainerID string
	IPAddress   string
	RunnerName  api.RunnerName
}

type Orchestrator struct {
	mux               *multiplexer.Multiplexer
	dockerCli         *client.Client
	mutex             sync.Mutex
	cond              *sync.Cond
	warmPool          map[string]*WarmWorker
	activeWorkers     map[string]*ActiveWorker
	activeListeners   map[api.RunnerName]int
	maxWorkers        int
	warmWorkersConfig int
	bootingCount      int
	isPaused          bool
}

func NewOrchestrator(mux *multiplexer.Multiplexer, maxWorkers int, warmWorkers int) (*Orchestrator, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	o := &Orchestrator{
		mux:               mux,
		dockerCli:         cli,
		warmPool:          make(map[string]*WarmWorker),
		activeWorkers:     make(map[string]*ActiveWorker),
		activeListeners:   make(map[api.RunnerName]int),
		maxWorkers:        maxWorkers,
		warmWorkersConfig: warmWorkers,
		isPaused:          false,
	}
	o.cond = sync.NewCond(&o.mutex)

	since := fmt.Sprintf("%d", time.Now().Unix()-eventReplayMargin)

	if err := o.recoverState(); err != nil {
		log.Printf("[Orchestrator] Warning: state recovery failed (fresh start): %v", err)
	}

	go o.watchEvents(since)
	go o.maintainPool()

	return o, nil
}

func shortID() string {
	return uuid.NewString()[:8]
}

func (o *Orchestrator) recoverState() error {
	f := filters.NewArgs()
	f.Add("label", labelManaged+"=true")

	containers, err := o.dockerCli.ContainerList(context.Background(), container.ListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	for _, c := range containers {
		if c.State != "running" {
			log.Printf("[Orchestrator] Cleaning up exited container %s (%s)", c.ID[:12], c.State)
			_ = o.dockerCli.ContainerRemove(context.Background(), c.ID, container.RemoveOptions{Force: true})
			continue
		}

		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		switch {
		case strings.HasPrefix(name, namePrefixActive):
			runnerName := parseRunnerFromActiveName(name)
			ip := firstIP(c)

			o.activeWorkers[c.ID] = &ActiveWorker{
				ContainerID: c.ID,
				IPAddress:   ip,
				RunnerName:  runnerName,
			}
			o.activeListeners[runnerName]++
			log.Printf("[Orchestrator] Recovered active worker %s (runner=%s)", name, runnerName)

		case strings.HasPrefix(name, namePrefixWarm):
			ip := firstIP(c)

			o.warmPool[c.ID] = &WarmWorker{
				ContainerID: c.ID,
				IPAddress:   ip,
			}
			log.Printf("[Orchestrator] Recovered warm worker %s", name)

		default:
			log.Printf("[Orchestrator] Skipping unrecognized managed container %s", name)
		}
	}

	total := len(o.warmPool) + len(o.activeWorkers)
	log.Printf("[Orchestrator] State recovery complete: %d warm, %d active, %d total", len(o.warmPool), len(o.activeWorkers), total)
	return nil
}

func (o *Orchestrator) watchEvents(since string) {
	f := filters.NewArgs()
	f.Add("type", "container")
	f.Add("label", labelManaged+"=true")
	f.Add("event", "die")

	ctx := context.Background()

	for {
		eventCh, errCh := o.dockerCli.Events(ctx, events.ListOptions{
			Since:   since,
			Filters: f,
		})

		for msg := range eventCh {
			o.handleContainerDeath(msg.Actor.ID)
		}

		if err := <-errCh; err != nil {
			log.Printf("[Orchestrator] Event stream error: %v, reconnecting...", err)
		}
		since = fmt.Sprintf("%d", time.Now().Unix())
		time.Sleep(1 * time.Second)
	}
}

func (o *Orchestrator) handleContainerDeath(containerID string) {
	o.mutex.Lock()
	
	changed := false
	if ww, ok := o.warmPool[containerID]; ok {
		delete(o.warmPool, containerID)
		log.Printf("[Orchestrator] Warm worker %s died", ww.ContainerID[:12])
	} else if aw, ok := o.activeWorkers[containerID]; ok {
		delete(o.activeWorkers, containerID)
		o.activeListeners[aw.RunnerName]--
		log.Printf("[Orchestrator] Active worker for [%s] died (%s)", aw.RunnerName, containerID[:12])
		changed = true
	} else {
		o.mutex.Unlock()
		return
	}

	o.logCapacityLocked()
	o.cond.Broadcast()
	o.mutex.Unlock()

	if changed {
		o.evaluateCapacity()
	}
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
		log.Printf("[Orchestrator] MAX CAPACITY REACHED. Freezing idle listeners...")
		o.isPaused = true

		var active []api.RunnerName
		for rName, count := range o.activeListeners {
			if count > 0 {
				active = append(active, rName)
			}
		}

		o.mux.LockOthers(active)
	} else if totalAssigned < o.maxWorkers && o.isPaused {
		log.Printf("[Orchestrator] CAPACITY FREED. Unfreezing listeners...")
		o.isPaused = false
		o.mux.UnlockOthers()
	}
}

func (o *Orchestrator) maintainPool() {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	for {
		for len(o.warmPool)+o.bootingCount >= o.warmWorkersConfig {
			o.cond.Wait()
		}

		total := len(o.warmPool) + len(o.activeWorkers) + o.bootingCount
		if total >= o.maxWorkers {
			o.cond.Wait()
			continue
		}

		o.bootingCount++
		go func() {
			ww, err := o.startContainer()

			o.mutex.Lock()
			o.bootingCount--
			if err == nil {
				o.warmPool[ww.ContainerID] = ww
			}
			o.logCapacityLocked()
			o.mutex.Unlock()

			if err != nil {
				log.Printf("[Orchestrator] Failed to spawn warm container: %v", err)
				o.cond.Broadcast()
				return
			}

			if alive := o.checkContainerAlive(ww.ContainerID); !alive {
				log.Printf("[Orchestrator] Warm container %s died before entering pool, cleaning up", ww.ContainerID[:12])
				o.handleContainerDeath(ww.ContainerID)
			} else {
				log.Printf("[Orchestrator] Warm container ready: %s", ww.ContainerID[:12])
			}
			o.cond.Broadcast()
		}()
	}
}

func (o *Orchestrator) startContainer() (*WarmWorker, error) {
	ctx := context.Background()

	workerEnv := []string{}
	startDocker := os.Getenv("WORKER_START_DOCKER_SERVICE") == "true"
	if startDocker {
		workerEnv = append(workerEnv, "START_DOCKER_SERVICE=true")
	}

	workerImage := os.Getenv("WORKER_IMAGE")
	if workerImage == "" {
		workerImage = "github-mux-worker:latest"
	}

	containerName := namePrefixWarm + shortID()

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
exec worker-launcher
			`},
			Labels: map[string]string{
				labelManaged: "true",
				labelRunner:  "",
			},
		},
		&container.HostConfig{
			NetworkMode: "github-mux_default",
			Privileged:  startDocker,
			AutoRemove:  true,
		},
		&network.NetworkingConfig{},
		nil,
		containerName,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	if err := o.dockerCli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	inspect, err := o.dockerCli.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	ip := ""
	for _, netObj := range inspect.NetworkSettings.Networks {
		ip = netObj.IPAddress
		break
	}

	log.Printf("[Orchestrator] Spawned warm worker %s at %s", containerName, ip)

	return &WarmWorker{
		ContainerID: resp.ID,
		IPAddress:   ip,
	}, nil
}

func (o *Orchestrator) allocateContainer(ctx context.Context, runnerName api.RunnerName) (*WarmWorker, error) {
	o.mutex.Lock()

	for {
		for id, ww := range o.warmPool {
			delete(o.warmPool, id)

			newName := fmt.Sprintf("%s%s-%s", namePrefixActive, runnerName, shortID())
			if err := o.dockerCli.ContainerRename(ctx, ww.ContainerID, newName); err != nil {
				log.Printf("[Orchestrator] Warning: failed to rename container %s: %v", ww.ContainerID[:12], err)
			}

			o.activeWorkers[ww.ContainerID] = &ActiveWorker{
				ContainerID: ww.ContainerID,
				IPAddress:   ww.IPAddress,
				RunnerName:  runnerName,
			}
			o.activeListeners[runnerName]++
			o.logCapacityLocked()
			o.cond.Broadcast()

			o.mutex.Unlock()
			return ww, nil
		}

		total := len(o.warmPool) + len(o.activeWorkers) + o.bootingCount
		if total < o.maxWorkers {
			o.bootingCount++
			o.mutex.Unlock()

			ww, err := o.startContainer()

			o.mutex.Lock()
			o.bootingCount--

			if err != nil {
				o.cond.Broadcast()
				o.mutex.Unlock()
				return nil, fmt.Errorf("failed to create worker container: %w", err)
			}

			newName := fmt.Sprintf("%s%s-%s", namePrefixActive, runnerName, shortID())
			if renameErr := o.dockerCli.ContainerRename(ctx, ww.ContainerID, newName); renameErr != nil {
				log.Printf("[Orchestrator] Warning: failed to rename container %s: %v", ww.ContainerID[:12], renameErr)
			}

			o.activeWorkers[ww.ContainerID] = &ActiveWorker{
				ContainerID: ww.ContainerID,
				IPAddress:   ww.IPAddress,
				RunnerName:  runnerName,
			}
			o.activeListeners[runnerName]++
			o.logCapacityLocked()
			o.cond.Broadcast()
			o.mutex.Unlock()

			// Safety check: if the container died before the mutex was locked,
			// the watchEvents goroutine would have missed it. 
			// We check if it's alive now, and if not, manually trigger death handling.
			if !o.checkContainerAlive(ww.ContainerID) {
				log.Printf("[Orchestrator] Active container %s died before entering pool, cleaning up", ww.ContainerID[:12])
				o.handleContainerDeath(ww.ContainerID)
				return nil, fmt.Errorf("container died immediately after allocation")
			}

			return ww, nil
		}

		select {
		case <-ctx.Done():
			o.mutex.Unlock()
			return nil, ctx.Err()
		default:
		}
		o.cond.Wait()
	}
}

func (o *Orchestrator) HandleAllocate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload api.AllocateRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	ww, err := o.allocateContainer(r.Context(), payload.RunnerName)
	if err != nil {
		log.Printf("[Orchestrator] Allocation failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	o.evaluateCapacity()

	json.NewEncoder(w).Encode(api.AllocateResponse{
		WorkerIP: ww.IPAddress,
	})
}

func (o *Orchestrator) logCapacityLocked() {
	total := len(o.warmPool) + len(o.activeWorkers) + o.bootingCount
	log.Printf("[Orchestrator] Capacity: %d warm, %d active, %d booting, %d/%d total",
		len(o.warmPool), len(o.activeWorkers), o.bootingCount, total, o.maxWorkers)
}

func (o *Orchestrator) checkContainerAlive(containerID string) bool {
	inspect, err := o.dockerCli.ContainerInspect(context.Background(), containerID)
	if err != nil {
		return false
	}
	return inspect.State != nil && inspect.State.Running
}

func parseRunnerFromActiveName(name string) api.RunnerName {
	s := strings.TrimPrefix(name, namePrefixActive)
	lastDash := strings.LastIndex(s, "-")
	if lastDash > 0 {
		return api.RunnerName(s[:lastDash])
	}
	return api.RunnerName(s)
}

func firstIP(c types.Container) string {
	for _, netObj := range c.NetworkSettings.Networks {
		return netObj.IPAddress
	}
	return ""
}
