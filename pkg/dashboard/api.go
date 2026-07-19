package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	cache "github.com/go-pkgz/expirable-cache/v3"
	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"github.com/kmhalvin/github-action-runners-mux/orchestrator"
	"github.com/kmhalvin/github-action-runners-mux/pkg/github"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

// Context keys for user info passed through middleware
type contextKey string

const (
	usersKey contextKey = "users" // map[host]username
)

// UserMap holds the usernames for all hosts the user is logged into.
type UserMap map[string]string

type API struct {
	db        *sql.DB
	queries   *sqlc.Queries
	mux       *mux.Multiplexer
	orch      *orchestrator.Orchestrator
	authCfg   *config.AuthConfig
	userCache cache.Cache[string, string] // token → username, TTL 5 min
	runnerSvc *RunnerService
}

func NewAPI(db *sql.DB, queries *sqlc.Queries, mx *mux.Multiplexer, orch *orchestrator.Orchestrator, authCfg *config.AuthConfig) *API {
	userCache := cache.NewCache[string, string]().WithTTL(5 * time.Minute)
	return &API{
		db:        db,
		queries:   queries,
		mux:       mx,
		orch:      orch,
		authCfg:   authCfg,
		userCache: userCache,
		runnerSvc: NewRunnerService(queries, mx),
	}
}

// getUsersFromContext extracts the map of host → username from the request
// context, set by AuthMiddleware. Returns nil if no auth is configured.
func getUsersFromContext(r *http.Request) UserMap {
	if v, ok := r.Context().Value(usersKey).(UserMap); ok {
		return v
	}
	return nil
}

// isAdminFromContext checks if any of the user's logged-in hosts match the
// config-driven admin check.
func (api *API) isAdminFromContext(r *http.Request) bool {
	users := getUsersFromContext(r)
	if users == nil {
		return false // No auth configured
	}
	for host, username := range users {
		if api.authCfg.IsAdmin(username, host) {
			return true
		}
	}
	return false
}

// getOrCreateUsername looks up the cached username for a token, or fetches
// it from the GitHub API and caches it for 5 minutes.
func (api *API) getOrCreateUsername(r *http.Request, host, token string) (string, error) {
	if cached, ok := api.userCache.Get(token); ok {
		return cached, nil
	}

	username, err := github.GetAuthenticatedUser(r.Context(), host, token)
	if err != nil {
		return "", err
	}

	api.userCache.Set(token, username, 5*time.Minute)
	return username, nil
}

// WriteJSON is a helper to write JSON responses
func WriteJSON(w http.ResponseWriter, status int, data any) {
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

// healthz is a simple liveness/readiness probe for container orchestration.
// It pings the database to verify connectivity.
func (api *API) healthz(w http.ResponseWriter, r *http.Request) {
	if err := api.db.PingContext(r.Context()); err != nil {
		WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "error": err.Error()})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *API) MountRoutes(router *http.ServeMux) {
	// Health check — no auth required
	router.HandleFunc("GET /api/v1/healthz", api.healthz)

	// Public routes — no auth required (anonymous can view overview)
	router.HandleFunc("GET /api/v1/runners", api.listRunners)
	router.HandleFunc("GET /api/v1/status", api.getStatus)

	// Auth routes — no auth required
	router.HandleFunc("GET /api/v1/auth/hosts", api.listAuthHosts)
	router.HandleFunc("POST /api/v1/auth/token", api.exchangeToken)
	router.HandleFunc("GET /api/v1/auth/status", api.authStatus)
	router.HandleFunc("POST /api/v1/auth/logout", api.logout)

	// Protected routes — auth required
	router.Handle("POST /api/v1/runners", api.AuthMiddleware(http.HandlerFunc(api.createRunner)))
	router.Handle("GET /api/v1/runners/{name}", api.AuthMiddleware(http.HandlerFunc(api.getRunner)))
	router.Handle("PUT /api/v1/runners/{name}", api.AuthMiddleware(http.HandlerFunc(api.updateRunner)))
	router.Handle("DELETE /api/v1/runners/{name}", api.AuthMiddleware(http.HandlerFunc(api.deleteRunner)))
	// Settings — admin only
	router.Handle("GET /api/v1/settings", api.AdminMiddleware(http.HandlerFunc(api.getSettings)))
	router.Handle("PUT /api/v1/settings", api.AdminMiddleware(http.HandlerFunc(api.updateSettings)))
	router.Handle("GET /api/v1/github/repos", api.AuthMiddleware(http.HandlerFunc(api.listGitHubRepos)))
}

// AuthMiddleware checks that the user is logged in to at least one host.
// It fetches the GitHub username (cached) for each logged-in host and stores
// a map of host → username in the request context for downstream handlers.
// If auth is not configured (no auth.yaml), all requests are allowed.
func (api *API) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If no auth config, allow all
		if api.authCfg == nil || len(api.authCfg.OAuthApps) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		// Collect usernames for all logged-in hosts
		users := UserMap{}
		loggedIn := false
		for _, app := range api.authCfg.OAuthApps {
			cookieName := CookieNameForHost(app.Host)
			cookie, err := r.Cookie(cookieName)
			if err != nil || cookie.Value == "" {
				continue
			}
			loggedIn = true
			// Fetch and cache username; skip on error
			if username, err := api.getOrCreateUsername(r, app.Host, cookie.Value); err == nil {
				users[app.Host] = username
			}
		}

		if !loggedIn {
			WriteError(w, http.StatusUnauthorized, "not logged in")
			return
		}

		ctx := context.WithValue(r.Context(), usersKey, users)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AdminMiddleware wraps AuthMiddleware and additionally requires the user to
// be configured as an admin in auth.yaml on at least one host.
func (api *API) AdminMiddleware(next http.Handler) http.Handler {
	return api.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		users := getUsersFromContext(r)
		if users == nil {
			// No auth configured — allow all
			next.ServeHTTP(w, r)
			return
		}
		if api.isAdminFromContext(r) {
			next.ServeHTTP(w, r)
			return
		}
		WriteError(w, http.StatusForbidden, "admin access required")
	}))
}
