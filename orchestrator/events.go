package orchestrator

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"


	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/kmhalvin/github-action-runners-mux/api"
)

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

		o.pauser.LockOthers(active)
	} else if totalAssigned < o.maxWorkers && o.isPaused {
		log.Printf("[Orchestrator] CAPACITY FREED. Unfreezing listeners...")
		o.isPaused = false
		o.pauser.UnlockOthers()
	}
}

