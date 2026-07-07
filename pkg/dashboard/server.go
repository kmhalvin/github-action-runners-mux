package dashboard

import (
	"log"
	"net/http"
	"strings"
)

func ServeDashboard(api *API, port string) {
	router := http.NewServeMux()

	// Mount API Routes
	api.MountRoutes(router)

	// Fallback to embedded static files (to be added in Phase 2)
	// For now, return a placeholder for the frontend
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<h1>Dashboard Backend is Running</h1><p>Frontend (Phase 2) will be embedded here.</p>`))
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
