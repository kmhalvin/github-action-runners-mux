package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

func (api *API) listRunners(w http.ResponseWriter, r *http.Request) {
	runners, err := api.queries.ListRunners(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	liveStatuses := api.mux.GetRunnerStatuses()
	statusMap := make(map[string]mux.RunnerStatus)
	for _, s := range liveStatuses {
		statusMap[s.Name] = s
	}

	// Combine DB state with Live state
	type Combined struct {
		sqlc.Runner
		State         mux.RunnerState `json:"state"`
		ActiveWorkers int             `json:"active_workers"`
		Error         string          `json:"error,omitempty"`
	}

	var results []Combined
	for _, dbR := range runners {
		dbR.Token = ""
		dbR.Pat = ""
		c := Combined{Runner: dbR, State: mux.StateOffline}
		if s, ok := statusMap[dbR.Name]; ok {
			c.State = s.State
			c.ActiveWorkers = s.ActiveWorkers
			c.Error = s.Error
		}
		results = append(results, c)
	}

	WriteJSON(w, http.StatusOK, results)
}

func (api *API) createRunner(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name         string   `json:"name"`
		Mode         string   `json:"mode"`
		URL          string   `json:"url"`
		Token        string   `json:"token,omitempty"`
		Dir          string   `json:"dir,omitempty"`
		PAT          string   `json:"pat,omitempty"`
		ScaleSetName string   `json:"scale_set_name,omitempty"`
		MaxRunners   int      `json:"max_runners,omitempty"`
		Labels       []string `json:"labels,omitempty"`
		RunnerGroup  string   `json:"runner_group,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	if strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.Mode) == "" || strings.TrimSpace(payload.URL) == "" {
		WriteError(w, http.StatusBadRequest, "name, mode, and url are required")
		return
	}

	// Persist to DB first
	labelsStr := strings.Join(payload.Labels, ",")

	dbRunner, err := api.queries.CreateRunner(r.Context(), sqlc.CreateRunnerParams{
		Name:         payload.Name,
		Mode:         payload.Mode,
		Url:          payload.URL,
		Token:        payload.Token,
		Dir:          payload.Dir,
		Pat:          payload.PAT,
		ScaleSetName: payload.ScaleSetName,
		MaxRunners:   int64(payload.MaxRunners),
		Labels:       labelsStr,
		RunnerGroup:  payload.RunnerGroup,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to save runner: %v", err))
		return
	}

	// Prepare config for manager
	cfg := config.RunnerConfig{
		Name:         payload.Name,
		Mode:         payload.Mode,
		URL:          payload.URL,
		Token:        payload.Token,
		Dir:          payload.Dir,
		PAT:          payload.PAT,
		ScaleSetName: payload.ScaleSetName,
		MaxRunners:   payload.MaxRunners,
		Labels:       payload.Labels,
		Group:        payload.RunnerGroup,
	}

	api.sse.Broadcast("runner:added", dbRunner)

	// Optimistically start. If it fails, mux keeps error state but we don't delete from DB automatically
	// to allow user to inspect the error, fix token, and recreate.
	err = api.mux.AddRunner(context.Background(), cfg)
	if err != nil {
		// Just log it. The UI will fetch status and see the error.
		log.Printf("[API] Failed to start runner %s: %v", payload.Name, err)
	}

	WriteJSON(w, http.StatusCreated, dbRunner)
}

func (api *API) deleteRunner(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	force := r.URL.Query().Get("force") == "true"

	dbRunner, err := api.queries.GetRunnerByName(r.Context(), name)
	if err != nil {
		WriteError(w, http.StatusNotFound, "runner not found")
		return
	}

	// Tell mux to stop it
	err = api.mux.RemoveRunner(context.Background(), name, force, dbRunner.Mode)
	if err != nil {
		log.Printf("[API] Warning: mux failed to stop runner: %v", err)
	}

	// Delete from DB
	err = api.queries.DeleteRunnerByName(r.Context(), name)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to delete from db")
		return
	}

	// In a real scenario we'd also call config.sh remove here if standalone,
	// but for simplicity we rely on config/sync.go to clean it up on next boot,
	// or we can invoke the cleanup manually. Let's do it manually for instant cleanup.
	if dbRunner.Mode == "standalone" && dbRunner.Dir != "" {
		go func(rName, rDir string) {
			// Small delay to let draining finish if not forced
			time.Sleep(2 * time.Second)
			_ = api.mux.RemoveRunner(context.Background(), rName, true, "standalone") // Ensure killed

			// For cleanup, we can just remove the directory, GitHub will eventually timeout the session
			// or we can run config.sh remove if token is still valid.
			// Let's rely on the simple directory removal for now since token is in DB.
			cleanDir := filepath.Clean(rDir)
			if strings.HasPrefix(cleanDir, "/opt/runners/") {
				os.RemoveAll(cleanDir)
			}
		}(name, dbRunner.Dir)
	}

	api.sse.Broadcast("runner:removed", map[string]string{"name": name})
	WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
