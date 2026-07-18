package dashboard

import (
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/kmhalvin/github-action-runners-mux/web"
)

func ServeDashboard(api *API, port string) {
	// API router — auth is applied per-route inside MountRoutes
	apiRouter := http.NewServeMux()
	api.MountRoutes(apiRouter)

	// Static file server — no auth (SPA must load before login)
	subFS, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		log.Printf("[Dashboard] Failed to load embedded assets: %v", err)
		return
	}
	fileServer := http.FileServer(http.FS(subFS))

	// Read index.html once
	indexHTML, err := fs.ReadFile(subFS, "index.html")
	if err != nil {
		log.Printf("[Dashboard] Failed to read index.html: %v", err)
		return
	}

	staticHandler := func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the exact file
		f, err := subFS.Open(strings.TrimPrefix(r.URL.Path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fallback to index.html for SPA routing
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	}

	// Top-level router: /api/ goes to authed API, everything else to static
	router := http.NewServeMux()
	router.Handle("/api/", apiRouter)
	router.HandleFunc("/", staticHandler)

	// CORS middleware
	corsRouter := func(next http.Handler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		}
	}

	log.Printf("[Dashboard] Server starting on %s", port)
	if err := http.ListenAndServe(port, corsRouter(router)); err != nil {
		log.Printf("[Dashboard] Server failed: %v", err)
	}
}
