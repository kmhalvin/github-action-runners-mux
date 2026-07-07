package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

type API struct {
	db      *sql.DB
	queries *sqlc.Queries
	mux     *mux.Multiplexer
	orch    *orchestrator.Orchestrator
	sse     *SSEHub
}

func NewAPI(db *sql.DB, queries *sqlc.Queries, mx *mux.Multiplexer, orch *orchestrator.Orchestrator, sse *SSEHub) *API {
	return &API{
		db:      db,
		queries: queries,
		mux:     mx,
		orch:    orch,
		sse:     sse,
	}
}

// WriteJSON is a helper to write JSON responses
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

// ErrorResponse is a standard error payload
type ErrorResponse struct {
	Error string `json:"error"`
}

func WriteError(w http.ResponseWriter, status int, err string) {
	WriteJSON(w, status, ErrorResponse{Error: err})
}

func (api *API) MountRoutes(router *http.ServeMux) {
	router.HandleFunc("GET /api/v1/runners", api.listRunners)
	router.HandleFunc("POST /api/v1/runners", api.createRunner)
	router.HandleFunc("DELETE /api/v1/runners/{name}", api.deleteRunner)
	
	router.HandleFunc("GET /api/v1/status", api.getStatus)
	
	router.HandleFunc("GET /api/v1/settings", api.getSettings)
	router.HandleFunc("PUT /api/v1/settings", api.updateSettings)
	
	router.HandleFunc("GET /api/v1/settings/domains", api.listDomains)
	router.HandleFunc("POST /api/v1/settings/domains", api.addDomain)
	router.HandleFunc("DELETE /api/v1/settings/domains/{id}", api.deleteDomain)
	
	router.HandleFunc("GET /api/v1/events", api.sse.Handler())
}

// --- Handlers ---

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
			os.RemoveAll(rDir)
		}(name, dbRunner.Dir)
	}

	api.sse.Broadcast("runner:removed", map[string]string{"name": name})
	WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (api *API) getStatus(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, api.orch.GetStatus())
}

func (api *API) getSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	maxStr, _ := api.queries.GetSetting(ctx, "max_workers")
	warmStr, _ := api.queries.GetSetting(ctx, "warm_workers")
	
	max, _ := strconv.Atoi(maxStr)
	warm, _ := strconv.Atoi(warmStr)
	
	WriteJSON(w, http.StatusOK, map[string]int{
		"max_workers": max,
		"warm_workers": warm,
	})
}

func (api *API) updateSettings(w http.ResponseWriter, r *http.Request) {
	var payload map[string]int
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	ctx := r.Context()
	maxWorkers := payload["max_workers"]
	warmWorkers := payload["warm_workers"]

	if maxWorkers > 0 {
		_ = api.queries.UpsertSetting(ctx, sqlc.UpsertSettingParams{
			Key:   "max_workers",
			Value: strconv.Itoa(maxWorkers),
		})
	}
	if warmWorkers >= 0 {
		_ = api.queries.UpsertSetting(ctx, sqlc.UpsertSettingParams{
			Key:   "warm_workers",
			Value: strconv.Itoa(warmWorkers),
		})
	}

	// Update orchestrator instantly
	api.orch.UpdateSettings(maxWorkers, warmWorkers)
	api.sse.Broadcast("capacity:changed", map[string]int{"max_workers": maxWorkers, "warm_workers": warmWorkers})

	WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (api *API) listDomains(w http.ResponseWriter, r *http.Request) {
	domains, err := api.queries.ListEnterpriseDomains(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, domains)
}

func (api *API) addDomain(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Domain == "" {
		WriteError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	d, err := api.queries.AddEnterpriseDomain(r.Context(), payload.Domain)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, d)
}

func (api *API) deleteDomain(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if err := api.queries.RemoveEnterpriseDomain(r.Context(), id); err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
