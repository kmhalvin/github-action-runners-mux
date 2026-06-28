package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const DrainTimeout = 30 * time.Minute

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// 1. Load Configuration
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("Fatal: %v", err)
	}

	// 2. Initialize Orchestrator
	maxWorkers := cfg.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 5 // Default
	}
	warmWorkers := min(max(cfg.WarmWorkers, 0), maxWorkers)

	orch, err := NewOrchestrator(maxWorkers, warmWorkers)
	if err != nil {
		log.Fatalf("Fatal: failed to initialize Orchestrator: %v", err)
	}

	// 3. Initialize Multiplexer
	mux := NewMultiplexer(orch)

	// 4. Start Runners
	var wg sync.WaitGroup
	if err := mux.StartAll(ctx, cfg, &wg); err != nil {
		log.Fatalf("Fatal: %v", err)
	}

	// 5. Graceful Shutdown & Lifecycle Management
	log.Printf("All listeners started. Waiting for interrupt signal to shutdown...")
	<-ctx.Done()
	log.Printf("Interrupt signal received, initiating graceful shutdown...")

	// Drain logic
	// Create a context for the hard timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), DrainTimeout)
	defer shutdownCancel()

	doneCh := make(chan struct{})

	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		log.Printf("Graceful shutdown completed successfully.")
	case <-shutdownCtx.Done():
		log.Printf("Hard drain timeout reached (30m). Exiting.")
	}
}
