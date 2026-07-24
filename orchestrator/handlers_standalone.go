package orchestrator

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/kmhalvin/github-action-runners-mux/api"
)

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

	json.NewEncoder(w).Encode(api.AllocateResponse{
		WorkerIP:    ww.IPAddress,
		ContainerID: ww.ContainerID,
	})
}

// HandleAbortWorker handles requests to abort an allocated container before the job starts.
func (o *Orchestrator) HandleAbortWorker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload api.AbortRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	o.AbortWorker(payload.ContainerID)
	w.WriteHeader(http.StatusOK)
}
