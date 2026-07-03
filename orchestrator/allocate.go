package orchestrator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/api"
)

func (o *Orchestrator) GetActiveCount(runnerName api.RunnerName) int {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return o.activeListeners[runnerName]
}

func (o *Orchestrator) allocateStandalone(ctx context.Context, runnerName api.RunnerName) (*WarmWorker, error) {
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

		if err := ctx.Err(); err != nil {
			o.mutex.Unlock()
			return nil, err
		}
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

	ww, err := o.allocateStandalone(r.Context(), payload.RunnerName)
	if err != nil {
		log.Printf("[Orchestrator] Allocation failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	o.evaluateCapacity()

	json.NewEncoder(w).Encode(api.AllocateResponse{
		WorkerIP:    ww.IPAddress,
		ConfigFiles: o.readRunnerConfigFiles(payload.RunnerName, payload.RunnerDir),
	})
}

// runnerConfigFileNames are the config files Runner.Worker needs.
// Verified against the actions/runner source (ConfigurationStore.cs):
//   - .runner       — required by GetSettings(); missing → ArgumentNullException
//   - .credentials  — required for OAuth authentication during job execution
//
// .credentials_rsaparams and .agent are NOT needed (the latter doesn't even
// exist in the runner source).
var runnerConfigFileNames = []string{
	".runner",
	".credentials",
}

// readRunnerConfigFiles reads the specific runner's config files and returns
// them as a map of filename → base64-encoded content. These are injected into
// the worker container via the TCP header so the worker never needs to mount
// the shared volume (which would expose all runners' credentials).
//
// The dir parameter is authoritative — it comes from the shim's own executable
// path, so it's guaranteed to be the directory where config.sh wrote the files.
// If dir is empty (e.g. older shim), we fall back to looking up the runner by
// name in the config.
func (o *Orchestrator) readRunnerConfigFiles(name api.RunnerName, dir string) map[string]string {
	// Prefer the directory from the shim (authoritative — it's where the shim lives)
	if dir == "" {
		// Fallback: look up by name in config
		if o.config == nil {
			log.Printf("[Orchestrator] Warning: no config and no dir provided for runner %s", name)
			return nil
		}
		for i := range o.config.Runners {
			if o.config.Runners[i].Name == name {
				dir = o.config.Runners[i].Dir
				break
			}
		}
		if dir == "" {
			log.Printf("[Orchestrator] Warning: runner %s not found in config and no dir provided", name)
			return nil
		}
		log.Printf("[Orchestrator] Using config-lookup dir for runner %s: %s", name, dir)
	}

	configFiles := make(map[string]string)
	for _, fname := range runnerConfigFileNames {
		data, err := os.ReadFile(filepath.Join(dir, fname))
		if err != nil {
			log.Printf("[Orchestrator] Warning: could not read %s for runner %s: %v", fname, name, err)
			continue
		}
		configFiles[fname] = base64.StdEncoding.EncodeToString(data)
	}

	if len(configFiles) == 0 {
		log.Printf("[Orchestrator] Warning: no config files found for runner %s in %s", name, dir)
		return nil
	}

	log.Printf("[Orchestrator] Read %d config files for runner %s from %s", len(configFiles), name, dir)
	return configFiles
}

// AllocateJIT acquires a container and pushes a JIT configuration to it via HTTP.
func (o *Orchestrator) AllocateJIT(ctx context.Context, runnerName api.RunnerName, jitConfig string) error {
	ww, err := o.allocateStandalone(ctx, runnerName)
	if err != nil {
		return fmt.Errorf("failed to allocate worker for JIT: %w", err)
	}

	reqPayload, _ := json.Marshal(api.StartRequest{JITConfig: jitConfig})

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(fmt.Sprintf("http://%s:9001/start", ww.IPAddress), "application/json", bytes.NewBuffer(reqPayload))
	if err != nil {
		log.Printf("[Orchestrator] Failed to send JIT config to container %s: %v", ww.ContainerID[:12], err)
		o.handleContainerDeath(ww.ContainerID)
		return fmt.Errorf("failed to send JIT config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[Orchestrator] Container %s rejected JIT config (status %d)", ww.ContainerID[:12], resp.StatusCode)
		o.handleContainerDeath(ww.ContainerID)
		return fmt.Errorf("container rejected JIT config")
	}

	log.Printf("[Orchestrator] Successfully pushed JIT payload to %s", ww.ContainerID[:12])
	return nil
}
