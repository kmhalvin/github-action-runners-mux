package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/kmhalvin/github-action-runners-mux/api"
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
			o.cond.Broadcast()

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

			o.reporterMu.RLock()
			if o.reporter != nil {
				o.reporter.MarkBusy(runnerName)
			}
			o.reporterMu.RUnlock()

			return ww, nil
		}

		select {
		case <-ctx.Done():
			o.mutex.Unlock()
			return nil, ctx.Err()
		default:
		}

		o.cond.Wait()

		if err := ctx.Err(); err != nil {
			o.mutex.Unlock()
			return nil, err
		}
	}
}

// HandleAllocate handles standalone container allocation requests via the proxy socket.
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

	ww, err := o.AllocateWorker(r.Context(), string(payload.RunnerName))
	if err != nil {
		log.Printf("[Orchestrator] Allocation failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	o.evaluateCapacity()

	json.NewEncoder(w).Encode(api.AllocateResponse{
		WorkerIP:    ww.IPAddress,
	})
}
