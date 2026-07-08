package main

import (
	"encoding/json"
	"net/http"

	"github.com/kmhalvin/github-action-runners-mux/api"
)

func (wl *WorkerLauncher) handleWait(w http.ResponseWriter, r *http.Request) {
	wl.mutex.Lock()
	for !wl.finished {
		wl.cond.Wait()
	}
	exitCode := wl.exitCode
	wl.mutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(api.WaitResponse{ExitCode: exitCode})

	// Signal that the host has fetched the response
	select {
	case wl.waitFetched <- struct{}{}:
	default:
	}
}

func (wl *WorkerLauncher) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req api.StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	startedHere := false
	wl.startOnce.Do(func() {
		startedHere = true
		w.WriteHeader(http.StatusOK)
		go wl.runJITWorker(req.JITConfig)
	})

	if !startedHere {
		http.Error(w, "Worker already started in another mode", http.StatusConflict)
	}
}
