package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

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
	runners, err := api.runnerSvc.ListRunners(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, runners)
}

func (api *API) createRunner(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name         string   `json:"name"`
		Mode         string   `json:"mode"`
		URL          string   `json:"url"`
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

	if strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.Mode) == "" || strings.TrimSpace(payload.URL) == "" {
		WriteError(w, http.StatusBadRequest, "name, mode, and url are required")
		return
	}

	dir := ""
	if payload.Mode == "standalone" {
		dir = fmt.Sprintf("/opt/runners/%s", payload.Name)
	}

	labelsStr := ""
	if payload.Mode == "standalone" && len(payload.Labels) > 0 {
		labelsStr = strings.Join(payload.Labels, ",")
	}

	params := sqlc.CreateRunnerParams{
		Name:         payload.Name,
		Mode:         payload.Mode,
		URL:          payload.URL,
		Dir:          dir,
		PAT:          payload.PAT,
		ScaleSetName: payload.ScaleSetName,
		MaxRunners:   int64(payload.MaxRunners),
		Labels:       labelsStr,
		RunnerGroup:  payload.RunnerGroup,
	}

	var regToken string
	var err error
	if payload.Mode == "standalone" {
		regToken, err = api.generateRegToken(r, payload.URL)
		if err != nil {
			log.Printf("[API] Failed to generate registration token for %s: %v", payload.Name, err)
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error":       fmt.Sprintf("failed to generate registration token: %v", err),
				"runner_name": payload.Name,
			})
			return
		}
	}

	runner, err := api.runnerSvc.CreateRunner(r.Context(), params, regToken)
	if err != nil {
		log.Printf("[API] Failed to create runner %s: %v", payload.Name, err)
		if runner != nil {
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error":       fmt.Sprintf("failed to start runner: %v", err),
				"runner_name": payload.Name,
			})
			return
		}
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, runner)
}

func (api *API) getRunner(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	
	runner, err := api.runnerSvc.GetRunner(r.Context(), name)
	if err != nil {
		if errors.Is(err, mux.ErrRunnerNotFound) {
			WriteError(w, http.StatusNotFound, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	runner.CanManage = api.checkCanManage(r, runner.URL)
	WriteJSON(w, http.StatusOK, runner)
}

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

	// Fetch existing to check URL for permission check
	existing, err := api.runnerSvc.GetRunner(r.Context(), name)
	if err != nil {
		if errors.Is(err, mux.ErrRunnerNotFound) {
			WriteError(w, http.StatusNotFound, "runner not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, "db error")
		return
	}

	if !api.checkCanManage(r, existing.URL) {
		WriteError(w, http.StatusForbidden, "you don't have admin access to manage this runner")
		return
	}

	input := UpdateRunnerInput{
		PAT:          payload.PAT,
		ScaleSetName: payload.ScaleSetName,
		MaxRunners:   payload.MaxRunners,
		Labels:       payload.Labels,
		RunnerGroup:  payload.RunnerGroup,
	}

	if existing.Mode == "standalone" && !existing.IsRegistered {
		regToken, err := api.generateRegToken(r, existing.URL)
		if err != nil {
			log.Printf("[API] Failed to generate registration token for %s: %v", name, err)
			WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error":       fmt.Sprintf("failed to generate registration token: %v", err),
				"runner_name": name,
			})
			return
		}
		input.RegToken = regToken
	}

	runner, err := api.runnerSvc.UpdateRunner(r.Context(), name, input)
	if err != nil {
		if strings.Contains(err.Error(), "cannot edit runner") {
			WriteError(w, http.StatusConflict, err.Error())
			return
		}
		log.Printf("[API] Failed to update runner %s: %v", name, err)
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":       fmt.Sprintf("failed to start runner: %v", err),
			"runner_name": name,
		})
		return
	}

	WriteJSON(w, http.StatusOK, runner)
}

func (api *API) deleteRunner(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	force := r.URL.Query().Get("force") == "true"

	existing, err := api.runnerSvc.GetRunner(r.Context(), name)
	if err != nil {
		if errors.Is(err, mux.ErrRunnerNotFound) {
			WriteError(w, http.StatusNotFound, "runner not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, "db error")
		return
	}

	if !api.checkCanManage(r, existing.URL) {
		WriteError(w, http.StatusForbidden, "you don't have admin access to manage this runner")
		return
	}

	var deregToken string
	if existing.Mode == "standalone" {
		repoInfo, err := github.ParseRepoURL(existing.URL)
		if err == nil {
			oauthToken, _ := api.getOAuthTokenForHost(r, repoInfo.Host)
			if oauthToken != "" {
				regToken, _ := github.GetRegistrationToken(r.Context(), repoInfo.Host, repoInfo.Owner, repoInfo.Repo, oauthToken)
				deregToken = regToken
			}
		}
	}

	err = api.runnerSvc.DeleteRunner(r.Context(), name, force, deregToken)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

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

