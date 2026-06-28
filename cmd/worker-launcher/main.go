package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"

	"github.com/kmhalvin/github-action-runners-mux/api"
)

type WorkerLauncher struct {
	exitCode    int
	finished    bool
	started     bool
	mutex       sync.Mutex
	cond        *sync.Cond
}

func NewWorkerLauncher() *WorkerLauncher {
	ws := &WorkerLauncher{}
	ws.cond = sync.NewCond(&ws.mutex)
	return ws
}

func (ws *WorkerLauncher) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ws.mutex.Lock()
	if ws.started {
		ws.mutex.Unlock()
		http.Error(w, "Worker already started", http.StatusConflict)
		return
	}
	ws.started = true
	ws.mutex.Unlock()

	var req api.StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)

	// Launch worker asynchronously
	go ws.runWorker(req.JITConfig)
}

func (ws *WorkerLauncher) runWorker(jitConfig string) {
	// The JIT config token contains an embedded .runner settings file with Ephemeral=true,
	// so the listener will accept exactly one job and exit cleanly.
	// We use run.sh which wraps Runner.Listener with proper signal routing.
	cmd := exec.Command("/actions-runner/run.sh")
	cmd.Env = append(os.Environ(), "ACTIONS_RUNNER_INPUT_JITCONFIG="+jitConfig)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("Spawning run.sh with JIT config...")
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start listener with JIT config: %v", err)
		ws.finish(1)
		return
	}

	err := cmd.Wait()

	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			ws.finish(exitError.ExitCode())
		} else {
			ws.finish(1)
		}
	} else {
		ws.finish(0)
	}
}

func (ws *WorkerLauncher) finish(code int) {
	ws.mutex.Lock()
	ws.exitCode = code
	ws.finished = true
	ws.cond.Broadcast()
	ws.mutex.Unlock()
	log.Printf("Worker finished with exit code: %d", code)
}

func main() {
	shim := NewWorkerLauncher()

	http.HandleFunc("/start", shim.handleStart)

	server := &http.Server{Addr: ":9001"}
	go func() {
		log.Println("Worker Launcher HTTP server listening on :9001")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Wait for the worker to finish
	shim.mutex.Lock()
	for !shim.finished {
		shim.cond.Wait()
	}
	exitCode := shim.exitCode
	shim.mutex.Unlock()
	
	log.Printf("Exiting container with code: %d", exitCode)
	os.Exit(exitCode)
}
