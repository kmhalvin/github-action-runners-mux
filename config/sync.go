package config

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
)

// MuxMeta holds the credentials required to deregister a runner from GitHub.
type MuxMeta struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

// SyncRunners reconciles the currently configured runners against the
// previously registered runners in /opt/runners. Any runner directory
// that has a .credentials file but is no longer in the config is considered
// stale and will be deregistered from GitHub and cleaned up.
func SyncRunners(runners []sqlc.Runner) {
	log.Println("[Sync] Starting configuration sync and stale runner reconciliation...")

	// Build a set of currently configured runner directories
	configuredDirs := make(map[string]bool)
	for _, r := range runners {
		if r.Dir == "" {
			continue
		}
		absPath, err := filepath.Abs(r.Dir)
		if err == nil {
			configuredDirs[absPath] = true
		} else {
			configuredDirs[r.Dir] = true
		}
	}

	// Assuming runners are stored in /opt/runners or derived from the first runner
	baseDir := "/opt/runners"
	for _, r := range runners {
		if r.Dir != "" {
			baseDir = filepath.Dir(r.Dir)
			break
		}
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return // Nothing to sync
		}
		log.Printf("[Sync] Warning: Failed to read %s: %v", baseDir, err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		runnerDir := filepath.Join(baseDir, entry.Name())
		absRunnerDir, err := filepath.Abs(runnerDir)
		if err != nil {
			absRunnerDir = runnerDir
		}

		credsFile := filepath.Join(absRunnerDir, ".credentials")
		if _, err := os.Stat(credsFile); os.IsNotExist(err) {
			continue // Not a registered runner directory
		}

		// If it's in the config, it's active.
		if configuredDirs[absRunnerDir] {
			continue
		}

		// Runner is stale. Attempt deregistration.
		deregisterStaleRunner(entry.Name(), absRunnerDir)
	}

	log.Println("[Sync] Reconciliation complete.")
}

func deregisterStaleRunner(name string, dir string) {
	log.Printf("[Sync] Found stale runner: %s. Attempting deregistration...", name)

	metaFile := filepath.Join(dir, ".mux-meta.json")
	data, err := os.ReadFile(metaFile)
	if err != nil {
		log.Printf("[Sync] Warning: Could not read %s: %v. Cleaning up directory without deregistration.", metaFile, err)
		os.RemoveAll(dir)
		return
	}

	var meta MuxMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		log.Printf("[Sync] Warning: Failed to parse %s: %v. Cleaning up directory without deregistration.", metaFile, err)
		os.RemoveAll(dir)
		return
	}

	if meta.Token == "" {
		log.Printf("[Sync] Warning: Missing token in %s. Cleaning up directory without deregistration.", metaFile)
		os.RemoveAll(dir)
		return
	}

	// Execute config.sh remove --token <TOKEN>
	cmd := exec.Command("./config.sh", "remove", "--token", meta.Token)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("[Sync] [%s] Executing ./config.sh remove...", name)
	if err := cmd.Run(); err != nil {
		log.Printf("[Sync] [%s] Warning: Failed to deregister from GitHub: %v. Proceeding to delete directory anyway.", name, err)
	} else {
		log.Printf("[Sync] [%s] Successfully deregistered from GitHub.", name)
	}

	if err := os.RemoveAll(dir); err != nil {
		log.Printf("[Sync] [%s] Warning: Failed to remove directory %s: %v", name, dir, err)
	} else {
		log.Printf("[Sync] [%s] Successfully removed stale directory.", name)
	}
}
