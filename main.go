package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const DrainTimeout = 30 * time.Minute
const DefaultMaxWorkers = 5

func getDBPath() string {
	if path := os.Getenv("DB_PATH"); path != "" {
		return path
	}
	return "github-mux.db"
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	app, err := NewApp(getDBPath(), "/etc/github-mux/auth.yaml")
	if err != nil {
		log.Fatalf("Fatal: failed to initialize app: %v", err)
	}

	if err := app.Start(ctx); err != nil {
		log.Fatalf("Fatal: failed to start app: %v", err)
	}

	log.Printf("System fully booted. Waiting for interrupt signal to shutdown...")
	<-ctx.Done()
	log.Printf("Interrupt signal received, initiating graceful shutdown...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), DrainTimeout)
	defer shutdownCancel()

	if err := app.Shutdown(shutdownCtx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	
	log.Printf("Graceful shutdown completed.")
}
