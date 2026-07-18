package standalone

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kmhalvin/github-action-runners-mux/config"
)


// InitializeEnvironment checks if the runner is registered and runs config.sh if needed.
// On every startup it re-injects the worker-shim so that the shim binary always
// matches the current proxy image (important when the image is updated — existing
// runners on the runner-data volume would otherwise keep the old shim).
func InitializeEnvironment(cfg *config.RunnerConfig) error {
	// Ensure the directory exists
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", cfg.Dir, err)
	}

	// Always save/update the meta file (especially important for upgrading older registered runners)
	meta := config.MuxMeta{
		RunnerName: cfg.Name,
		URL:        cfg.URL,
	}
	metaData, _ := json.Marshal(meta)
	_ = os.WriteFile(filepath.Join(cfg.Dir, ".mux-meta.json"), metaData, 0644)

	credsFile := filepath.Join(cfg.Dir, ".credentials")
	alreadyRegistered := false
	if _, err := os.Stat(credsFile); err == nil {
		log.Printf("[%s] Runner already registered (found .credentials)", cfg.Name)
		alreadyRegistered = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to check for .credentials: %w", err)
	}

	// Native Template Provisioning
	// Check if the directory is already populated with the runner template
	configScript := filepath.Join(cfg.Dir, "config.sh")
	if _, err := os.Stat(configScript); os.IsNotExist(err) {
		log.Printf("[%s] Runner template not found. Copying from /actions-runner...", cfg.Name)
		cpCmd := exec.Command("cp", "-a", "/actions-runner/.", cfg.Dir+"/")
		if err := cpCmd.Run(); err != nil {
			return fmt.Errorf("failed to copy runner template to %s: %w", cfg.Dir, err)
		}
	}

	// ── Always (re)inject the worker-shim ──────────────────────────────────
	// This runs on every startup, even for already-registered runners, so the
	// shim binary always matches the current proxy image. Without this, updating
	// the proxy image would leave existing runners with a stale shim on the
	// runner-data volume, causing protocol mismatches (e.g. the new
	// worker-launcher expects a framed header that the old shim doesn't send).
	shimSrc := "/usr/local/bin/worker-shim"
	workerPath := filepath.Join(cfg.Dir, "bin", "Runner.Worker")

	log.Printf("[%s] Refreshing worker-shim...", cfg.Name)
	if err := os.MkdirAll(filepath.Dir(workerPath), 0755); err != nil {
		return fmt.Errorf("failed to create bin directory: %w", err)
	}
	shimCp := exec.Command("cp", shimSrc, workerPath)
	if err := shimCp.Run(); err != nil {
		return fmt.Errorf("failed to inject shim binary: %w", err)
	}
	_ = os.Chmod(workerPath, 0755)

	// If already registered, we're done — config.sh already ran on a previous boot.
	if alreadyRegistered {
		return nil
	}

	log.Printf("[%s] Runner not registered. Executing config.sh...", cfg.Name)

	if cfg.Token == "" {
		return fmt.Errorf("[%s] runner is not registered and no token was provided in config", cfg.Name)
	}

	args := []string{
		"--url", cfg.URL,
		"--token", cfg.Token,
		"--name", string(cfg.Name),
		"--work", "_work",
		"--unattended",
		"--replace",
		"--disableupdate", // Disable self-mutation mid-flight
	}

	if len(cfg.Labels) > 0 {
		args = append(args, "--labels", strings.Join(cfg.Labels, ","))
	}

	if cfg.Group != "" {
		args = append(args, "--runnergroup", cfg.Group)
	}

	cmd := exec.Command("./config.sh", args...)
	cmd.Dir = cfg.Dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("[%s] failed to register runner: %w", cfg.Name, err)
	}

	log.Printf("[%s] Runner successfully registered", cfg.Name)
	return nil
}
