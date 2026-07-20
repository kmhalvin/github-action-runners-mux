package orchestrator

import (
	"context"
	"fmt"
	"log"
)

func (o *Orchestrator) GetActiveCount(runnerName string) int {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return o.activeListeners[runnerName]
}

// AllocateWorker gets a container from the warm pool and assigns it to a runner.
func (o *Orchestrator) AllocateWorker(ctx context.Context, runnerName string) (*WarmWorker, error) {
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
			o.broadcast()

			o.mutex.Unlock()
			o.reporterMu.RLock()
			if o.reporter != nil {
				o.reporter.MarkBusy(runnerName)
			}
			o.reporterMu.RUnlock()
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
				o.broadcast()
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
			o.broadcast()
			o.mutex.Unlock()

			// Safety check: if the container died before the mutex was locked,
			// the watchEvents goroutine would have missed it.
			// We check if it's alive now, and if not, manually trigger death handling.
			if !o.checkContainerAlive(ww.ContainerID) {
				log.Printf("[Orchestrator] Active container %s died before entering pool, cleaning up", ww.ContainerID[:12])
				o.handleContainerDeath(ww.ContainerID)
				return nil, fmt.Errorf("container died immediately after allocation")
			}

			o.reporterMu.RLock()
			if o.reporter != nil {
				o.reporter.MarkBusy(runnerName)
			}
			o.reporterMu.RUnlock()

			return ww, nil
		}

		ch := o.broadcastCh
		o.mutex.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		}

		o.mutex.Lock()
	}
}
