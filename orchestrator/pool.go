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

func (o *Orchestrator) checkContainerAlive(containerID string) bool {
	inspect, err := o.dockerCli.ContainerInspect(context.Background(), containerID)
	if err != nil {
		return false
	}
	return inspect.State != nil && inspect.State.Running
}

