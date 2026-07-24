package orchestrator

import (
	"context"
	"fmt"
	"log"

	"github.com/docker/docker/api/types/container"
)

func (o *Orchestrator) GetActiveCount(runnerName string) int {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return o.activeListeners[runnerName]
}

// AllocateWorker gets a container from the warm pool and assigns it to a runner.
func (o *Orchestrator) AllocateWorker(ctx context.Context, runnerName string) (*WarmWorker, error) {
	o.mutex.Lock()

	o.pendingAllocations++
	o.broadcast()

	for {
		var candidate *WarmWorker
		for id, ww := range o.warmPool {
			delete(o.warmPool, id)
			candidate = ww
			break
		}

		if candidate != nil {
			o.pendingAllocations--
			o.mutex.Unlock()

			newName := fmt.Sprintf("%s%s-%s", namePrefixActive, runnerName, shortID())
			if err := o.dockerCli.ContainerRename(ctx, candidate.ContainerID, newName); err != nil {
				log.Printf("[Orchestrator] Warning: failed to rename container %s: %v", candidate.ContainerID[:12], err)
			}

			// Safety check: if the container died before we picked it,
			// we must clean it up and retry allocation.
			if !o.checkContainerAlive(candidate.ContainerID) {
				log.Printf("[Orchestrator] Container %s died before allocation, cleaning up and retrying", candidate.ContainerID[:12])
				_ = o.dockerCli.ContainerRemove(context.Background(), candidate.ContainerID, container.RemoveOptions{Force: true})
				
				o.mutex.Lock()
				o.pendingAllocations++
				continue
			}

			// Now that we know it's alive, officially register it as active
			o.mutex.Lock()
			o.activeWorkers[candidate.ContainerID] = &ActiveWorker{
				ContainerID: candidate.ContainerID,
				IPAddress:   candidate.IPAddress,
				RunnerName:  runnerName,
			}
			o.activeListeners[runnerName]++
			o.logCapacityLocked()
			o.broadcast()
			o.mutex.Unlock()

			o.reporterMu.RLock()
			if o.reporter != nil {
				o.reporter.MarkBusy(runnerName)
			}
			o.reporterMu.RUnlock()

			o.evaluateCapacity()

			return candidate, nil
		}

		ch := o.broadcastCh
		o.mutex.Unlock()

		select {
		case <-ctx.Done():
			o.mutex.Lock()
			o.pendingAllocations--
			o.mutex.Unlock()
			return nil, ctx.Err()
		case <-ch:
			o.mutex.Lock()
		}
	}
}
