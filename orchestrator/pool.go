package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

func (o *Orchestrator) maintainPool() {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	for {
		target := o.warmWorkersConfig + o.pendingAllocations
		for len(o.warmPool)+o.bootingCount >= target {
			ch := o.broadcastCh
			o.mutex.Unlock()
			<-ch
			o.mutex.Lock()
			target = o.warmWorkersConfig + o.pendingAllocations
		}

		total := len(o.warmPool) + len(o.activeWorkers) + o.bootingCount
		if total >= o.maxWorkers {
			ch := o.broadcastCh
			o.mutex.Unlock()
			<-ch
			o.mutex.Lock()
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
				o.mutex.Lock()
				o.broadcast()
				o.mutex.Unlock()
				return
			}

			if alive := o.checkContainerAlive(ww.ContainerID); !alive {
				log.Printf("[Orchestrator] Warm container %s died before entering pool, cleaning up", ww.ContainerID[:12])
				o.handleContainerDeath(ww.ContainerID)
			} else {
				log.Printf("[Orchestrator] Warm container ready: %s", ww.ContainerID[:12])
			}
			o.mutex.Lock()
			o.broadcast()
			o.mutex.Unlock()
		}()
	}
}

func (o *Orchestrator) startContainer() (*WarmWorker, error) {
	ctx := context.Background()

	workerEnv := []string{
		// Pipe runner trace logs to stdout so they appear in `docker logs`.
		// The runner checks this env var in HostContext.cs and attaches a
		// StdoutTraceListener that mirrors all trace output (Info+ by default)
		// to stdout. Without this, logs only go to the _diag folder inside the
		// container (invisible after AutoRemove).
		"ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT=true",
	}
	startDocker := os.Getenv("WORKER_START_DOCKER_SERVICE") == "true"
	if startDocker {
		workerEnv = append(workerEnv, "START_DOCKER_SERVICE=true")
	}

	workerImage := os.Getenv("WORKER_IMAGE")
	if workerImage == "" {
		workerImage = "github-mux-worker:latest"
	}

	containerName := namePrefixWarm + shortID()

	// NOTE: Worker containers intentionally do NOT mount the runner-data volume.
	// Mounting it would expose .credentials of ALL registered runners to the CI
	// job (severe security vulnerability). Instead, the orchestrator injects
	// only the specific runner's config files via the TCP header at allocation
	// time. See orchestrator/allocate.go (readRunnerConfigFiles).
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

func (o *Orchestrator) checkContainerAlive(containerID string) bool {
	inspect, err := o.dockerCli.ContainerInspect(context.Background(), containerID)
	if err != nil {
		return false
	}
	return inspect.State != nil && inspect.State.Running
}

// KillWorker forcibly removes a container and lets the orchestrator's event loop reap it.
func (o *Orchestrator) KillWorker(containerID string) {
	log.Printf("[Orchestrator] Force killing worker container %s", containerID[:12])
	_ = o.dockerCli.ContainerRemove(context.Background(), containerID, container.RemoveOptions{Force: true})
}
