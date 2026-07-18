package dashboard

import (
	"context"
	"database/sql"
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
	"github.com/kmhalvin/github-action-runners-mux/pkg/github"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

// generateRegToken generates a registration token for standalone runner
// registration using the user's OAuth token from the cookie.
func (api *API) generateRegToken(r *http.Request, runnerURL string) (string, error) {
	repoInfo, err := github.ParseRepoURL(runnerURL)
	if err != nil {
		return "", fmt.Errorf("invalid runner URL: %w", err)
	}

	oauthToken, err := api.getOAuthTokenForHost(r, repoInfo.Host)
	if err != nil {
		return "", fmt.Errorf("not signed in to %s — please re-login: %w", repoInfo.Host, err)
	}

	return github.GetRegistrationToken(r.Context(), repoInfo.Host, repoInfo.Owner, repoInfo.Repo, oauthToken)
}

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
		HasPat        bool            `json:"has_pat"`
	}

	results := []Combined{}
	for _, dbR := range runners {
		hasPat := dbR.Pat != ""
		dbR.Pat = ""
		c := Combined{Runner: dbR, State: mux.StateOffline, HasPat: hasPat}
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
	labelsStr := ""
	if payload.Mode == "standalone" && len(payload.Labels) > 0 {
		labelsStr = strings.Join(payload.Labels, ",")
	}

	dbRunner, err := api.queries.CreateRunner(r.Context(), sqlc.CreateRunnerParams{
		Name:         payload.Name,
		Mode:         payload.Mode,
		Url:          payload.URL,
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
	var cfgLabels []string
	if payload.Mode == "standalone" {
		cfgLabels = payload.Labels
	}
	cfg := config.RunnerConfig{
		Name:         payload.Name,
		Mode:         payload.Mode,
		URL:          payload.URL,
		Dir:          payload.Dir,
		PAT:          payload.PAT,
		ScaleSetName: payload.ScaleSetName,
		MaxRunners:   payload.MaxRunners,
		Labels:       cfgLabels,
		Group:        payload.RunnerGroup,
	}

	// For standalone mode, generate registration token using OAuth token
	if payload.Mode == "standalone" {
		regToken, err := api.generateRegToken(r, payload.URL)
		if err != nil {
			log.Printf("[API] Failed to generate registration token for %s: %v", payload.Name, err)
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error":       fmt.Sprintf("failed to generate registration token: %v", err),
				"runner_name": payload.Name,
			})
			return
		}
		cfg.Token = regToken
	}

	// Attempt to start the runner. If it fails, keep the DB record so the user
	// can edit and retry from the form, but return an error so the frontend
	// stays on the form page instead of redirecting to Overview.
	err = api.mux.AddRunner(context.Background(), cfg)
	if err != nil {
		log.Printf("[API] Failed to start runner %s: %v", payload.Name, err)
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":       fmt.Sprintf("failed to start runner: %v", err),
			"runner_name": payload.Name,
		})
		return
	}

	dbRunner.Pat = ""
	WriteJSON(w, http.StatusCreated, dbRunner)
}

// getRunner returns the full runner record combined with live mux status
// (state, active_workers, error) for the detail/edit form.
func (api *API) getRunner(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	dbRunner, err := api.queries.GetRunnerByName(r.Context(), name)
	if err != nil {
		WriteError(w, http.StatusNotFound, "runner not found")
		return
	}

	type Combined struct {
		sqlc.Runner
		State         mux.RunnerState `json:"state"`
		ActiveWorkers int             `json:"active_workers"`
		Error         string          `json:"error,omitempty"`
		HasPat        bool            `json:"has_pat"`
		CanManage     bool            `json:"can_manage"`
		IsRegistered  bool            `json:"is_registered"`
	}

	hasPat := dbRunner.Pat != ""
	dbRunner.Pat = ""
	c := Combined{Runner: dbRunner, State: mux.StateOffline, HasPat: hasPat}

	liveStatuses := api.mux.GetRunnerStatuses()
	for _, s := range liveStatuses {
		if s.Name == name {
			c.State = s.State
			c.ActiveWorkers = s.ActiveWorkers
			c.Error = s.Error
			break
		}
	}

	// Compute can_manage: config-driven admin OR repo/org admin
	c.CanManage = api.checkCanManage(r, dbRunner.Url)

	// For standalone runners, check if already registered (has .credentials file).
	// Scaleset runners don't use .credentials, so is_registered is always false.
	if dbRunner.Mode == "standalone" && dbRunner.Dir != "" {
		if _, err := os.Stat(filepath.Join(dbRunner.Dir, ".credentials")); err == nil {
			c.IsRegistered = true
		}
	}

	WriteJSON(w, http.StatusOK, c)
}

// updateRunner updates an existing runner's config and retries registration.
// Only allowed when the runner is in a failed/offline state (not registered).
func (api *API) updateRunner(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var payload struct {
		Token        string   `json:"token,omitempty"`
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

	// Fetch existing runner
	dbRunner, err := api.queries.GetRunnerByName(r.Context(), name)
	if err != nil {
		WriteError(w, http.StatusNotFound, "runner not found")
		return
	}

	// Permission check: must be admin or have repo/org admin access
	if !api.checkCanManage(r, dbRunner.Url) {
		WriteError(w, http.StatusForbidden, "you don't have admin access to manage this runner")
		return
	}

	// Check if runner is currently running/registered — don't allow edit if live
	liveStatuses := api.mux.GetRunnerStatuses()
	for _, s := range liveStatuses {
		if s.Name == name && (s.State == mux.StateOnline || s.State == mux.StateBusy || s.State == mux.StateRegistering || s.State == mux.StatePaused || s.State == mux.StateDraining) {
			WriteError(w, http.StatusConflict, fmt.Sprintf("cannot edit runner %s while it is %s — remove and recreate instead", name, s.State))
			return
		}
	}

	// Check if standalone runner is already registered (has .credentials file).
	alreadyRegistered := false
	if dbRunner.Mode == "standalone" && dbRunner.Dir != "" {
		if _, err := os.Stat(filepath.Join(dbRunner.Dir, ".credentials")); err == nil {
			alreadyRegistered = true
		}
	}

	var updatedRunner sqlc.Runner
	if alreadyRegistered && payload.PAT == "" && payload.MaxRunners == 0 && payload.RunnerGroup == "" && payload.ScaleSetName == "" && len(payload.Labels) == 0 {
		// No modifications and already registered — just retry with existing config
		updatedRunner = dbRunner
	} else {
		// Build update params using COALESCE (only update non-empty fields)
		// Note: URL is immutable after creation — not updatable.
		updateParams := sqlc.UpdateRunnerParams{
			ID: dbRunner.ID,
		}
		if payload.PAT != "" {
			updateParams.Pat = sql.NullString{String: payload.PAT, Valid: true}
		}
		if payload.MaxRunners > 0 {
			updateParams.MaxRunners = sql.NullInt64{Int64: int64(payload.MaxRunners), Valid: true}
		}
		if payload.RunnerGroup != "" {
			updateParams.RunnerGroup = sql.NullString{String: payload.RunnerGroup, Valid: true}
		}
		if len(payload.Labels) > 0 {
			updateParams.Labels = sql.NullString{String: strings.Join(payload.Labels, ","), Valid: true}
		}

		updatedRunner, err = api.queries.UpdateRunner(r.Context(), updateParams)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update runner: %v", err))
			return
		}
	}

	// Build config for retry
	var cfgLabels []string
	if updatedRunner.Mode == "standalone" && updatedRunner.Labels != "" {
		cfgLabels = strings.Split(updatedRunner.Labels, ",")
	}
	cfg := config.RunnerConfig{
		Name:         updatedRunner.Name,
		Mode:         updatedRunner.Mode,
		URL:          updatedRunner.Url,
		Dir:          updatedRunner.Dir,
		PAT:          updatedRunner.Pat,
		ScaleSetName: updatedRunner.ScaleSetName,
		MaxRunners:   int(updatedRunner.MaxRunners),
		Labels:       cfgLabels,
		Group:        updatedRunner.RunnerGroup,
	}

	// For standalone mode, only generate a registration token if the runner
	// is not yet registered. If already registered (has .credentials), the
	// listener will restart using existing credentials without needing a token.
	if updatedRunner.Mode == "standalone" && !alreadyRegistered {
		regToken, err := api.generateRegToken(r, updatedRunner.Url)
		if err != nil {
			log.Printf("[API] Failed to generate registration token for %s: %v", name, err)
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error":       fmt.Sprintf("failed to generate registration token: %v", err),
				"runner_name": name,
			})
			return
		}
		cfg.Token = regToken
	}

	// Retry registration
	err = api.mux.AddRunner(context.Background(), cfg)
	if err != nil {
		log.Printf("[API] Failed to retry runner %s: %v", name, err)
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":       fmt.Sprintf("failed to start runner: %v", err),
			"runner_name": name,
		})
		return
	}

	updatedRunner.Pat = ""
	WriteJSON(w, http.StatusOK, updatedRunner)
}

func (api *API) deleteRunner(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	force := r.URL.Query().Get("force") == "true"
	token := r.URL.Query().Get("token")

	dbRunner, err := api.queries.GetRunnerByName(r.Context(), name)
	if err != nil {
		WriteError(w, http.StatusNotFound, "runner not found")
		return
	}

	// Permission check: must be admin or have repo/org admin access
	if !api.checkCanManage(r, dbRunner.Url) {
		WriteError(w, http.StatusForbidden, "you don't have admin access to manage this runner")
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

	// Extract the OAuth token before starting the goroutine, because the
	// HTTP request context will be canceled once the handler returns.
	var oauthToken string
	if dbRunner.Mode == "standalone" {
		repoInfo, err := github.ParseRepoURL(dbRunner.Url)
		if err == nil {
			oauthToken, _ = api.getOAuthTokenForHost(r, repoInfo.Host)
		}
	}

	// Deregister from GitHub and clean up in the background.
	// For standalone: runs config.sh remove --token, then removes the directory (Deregister handles both).
	// For scaleset: deletes the scale set from GitHub via the API.
	go func(rName, rMode, rToken, rOAuthToken string) {
		// Small delay to let draining finish if not forced
		time.Sleep(2 * time.Second)
		_ = api.mux.RemoveRunner(context.Background(), rName, true, rMode) // Ensure killed

		// Build config from DB record for deregistration.
		deregCfg := config.RunnerConfig{
			Name:         dbRunner.Name,
			Mode:         dbRunner.Mode,
			URL:          dbRunner.Url,
			Dir:          dbRunner.Dir,
			PAT:          dbRunner.Pat,
			ScaleSetName: dbRunner.ScaleSetName,
			Group:        dbRunner.RunnerGroup,
		}

		// For standalone mode, generate a fresh registration token for deregistration
		// using the OAuth token extracted before the request completed.
		if dbRunner.Mode == "standalone" && rOAuthToken != "" {
			repoInfo, err := github.ParseRepoURL(dbRunner.Url)
			if err != nil {
				log.Printf("[API] Warning: failed to parse URL for deregistration of %s: %v", rName, err)
			} else {
				regToken, err := github.GetRegistrationToken(context.Background(), repoInfo.Host, repoInfo.Owner, repoInfo.Repo, rOAuthToken)
				if err != nil {
					log.Printf("[API] Warning: failed to generate registration token for deregistration of %s: %v", rName, err)
				} else {
					deregCfg.Token = regToken
				}
			}
		}

		if err := api.mux.Deregister(deregCfg); err != nil {
			log.Printf("[API] Warning: failed to deregister runner %s from GitHub: %v", rName, err)
		}
	}(name, dbRunner.Mode, token, oauthToken)

	WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// checkCanManage returns true if the user is a configured admin OR has admin
// access to the repo/org that the runner URL points to.
func (api *API) checkCanManage(r *http.Request, runnerURL string) bool {
	users := getUsersFromContext(r)
	if users == nil {
		// No auth configured — allow all
		return true
	}

	// Admin configured in auth.yaml can manage everything
	if api.isAdminFromContext(r) {
		return true
	}

	// Check repo/org admin access using the token for the runner's host
	repoInfo, err := github.ParseRepoURL(runnerURL)
	if err != nil {
		return false
	}

	username, ok := users[repoInfo.Host]
	if !ok {
		return false
	}

	oauthToken, err := api.getOAuthTokenForHost(r, repoInfo.Host)
	if err != nil {
		return false
	}

	canManage, err := github.CheckAdminAccess(r.Context(), repoInfo.Host, repoInfo.Owner, repoInfo.Repo, username, oauthToken)
	if err != nil {
		return false
	}
	return canManage
}
