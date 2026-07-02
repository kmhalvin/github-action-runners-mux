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

type MuxMeta struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

// InitializeEnvironment checks if the runner is registered and runs config.sh if needed.
func InitializeEnvironment(cfg *config.RunnerConfig) error {
	// Ensure the directory exists
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", cfg.Dir, err)
	}

	credsFile := filepath.Join(cfg.Dir, ".credentials")
	if _, err := os.Stat(credsFile); err == nil {
		log.Printf("[%s] Runner already registered (found .credentials)", cfg.Name)
		return nil
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

		// Shim Injection
		shimSrc := "/usr/local/bin/worker-shim"
		workerPath := filepath.Join(cfg.Dir, "bin", "Runner.Worker")

		log.Printf("[%s] Injecting User-Space Shim...", cfg.Name)
		shimCp := exec.Command("cp", shimSrc, workerPath)
		if err := shimCp.Run(); err != nil {
			return fmt.Errorf("failed to inject shim binary: %w", err)
		}
		_ = os.Chmod(workerPath, 0755)
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

	// Save the token and URL for future deregistration (if the runner is removed from config)
	meta := MuxMeta{
		Token: cfg.Token,
		URL:   cfg.URL,
	}
	metaData, _ := json.Marshal(meta)
	_ = os.WriteFile(filepath.Join(cfg.Dir, ".mux-meta.json"), metaData, 0644)

	log.Printf("[%s] Runner successfully registered", cfg.Name)
	return nil
}


