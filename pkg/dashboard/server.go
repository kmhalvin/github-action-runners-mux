package dashboard

import (
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/kmhalvin/github-action-runners-mux/web"
)

func ServeDashboard(api *API, port string) {
	router := http.NewServeMux()

	// Mount API Routes
	api.MountRoutes(router)

	// Setup embedded filesystem
	subFS, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		log.Fatalf("[Dashboard] Failed to load embedded assets: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))

	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Try to serve the exact file
		f, err := subFS.Open(strings.TrimPrefix(r.URL.Path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fallback to index.html for SPA routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	// Add basic CORS middleware for dev
	corsRouter := func(next http.Handler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		}
	}

	log.Printf("[Dashboard] Server starting on %s", port)
	if err := http.ListenAndServe(port, corsRouter(router)); err != nil {
		log.Fatalf("[Dashboard] Server failed: %v", err)
	}
}
