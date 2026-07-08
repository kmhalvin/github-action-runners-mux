package dashboard

import (
	"database/sql"
	"encoding/json"
	"net/http"

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
