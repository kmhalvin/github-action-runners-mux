package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"

	"github.com/kmhalvin/github-action-runners-mux/pkg/api"
)

type WorkerLauncher struct {
	exitCode int
	finished bool
	started  bool
	mutex    sync.Mutex
	cond     *sync.Cond
}

func NewWorkerLauncher() *WorkerLauncher {
	wl := &WorkerLauncher{}
	wl.cond = sync.NewCond(&wl.mutex)
	return wl
}

func (wl *WorkerLauncher) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	wl.mutex.Lock()
	if wl.started {
		wl.mutex.Unlock()
		http.Error(w, "Worker already started", http.StatusConflict)
		return
	}
	wl.started = true
	wl.mutex.Unlock()

	var req api.StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)

	// Launch worker asynchronously
	go wl.runWorker(req.JITConfig)
}

func (wl *WorkerLauncher) runWorker(jitConfig string) {
	// The JIT config token contains an embedded .runner settings file with Ephemeral=true,
	// so the listener will accept exactly one job and exit cleanly.
	// We use run.sh which wraps Runner.Listener with proper signal routing.
	cmd := exec.Command("/home/runner/run.sh")
	cmd.Env = append(os.Environ(), "ACTIONS_RUNNER_INPUT_JITCONFIG="+jitConfig)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("Spawning run.sh with JIT config...")
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start listener with JIT config: %v", err)
		wl.finish(1)
		return
	}

	err := cmd.Wait()

	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			wl.finish(exitError.ExitCode())
		} else {
			wl.finish(1)
		}
	} else {
		wl.finish(0)
	}
}

func (wl *WorkerLauncher) finish(code int) {
	wl.mutex.Lock()
	wl.exitCode = code
	wl.finished = true
	wl.cond.Broadcast()
	wl.mutex.Unlock()
	log.Printf("Worker finished with exit code: %d", code)
}

func main() {
	wl := NewWorkerLauncher()

	http.HandleFunc("/start", wl.handleStart)

	server := &http.Server{Addr: ":9001"}
	go func() {
		log.Println("Worker Launcher HTTP server listening on :9001")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Wait for the worker to finish
	wl.mutex.Lock()
	for !wl.finished {
		wl.cond.Wait()
	}
	exitCode := wl.exitCode
	wl.mutex.Unlock()

	log.Printf("Exiting container with code: %d", exitCode)
	os.Exit(exitCode)
}
