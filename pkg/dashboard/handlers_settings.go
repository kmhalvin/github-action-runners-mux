package dashboard

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
)

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
		"max_workers":  max,
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

	WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
