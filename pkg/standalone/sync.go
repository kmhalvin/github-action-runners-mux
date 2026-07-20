package standalone

import (
	"log"
	"os"
	"path/filepath"

	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
)

// SyncStaleRunners reconciles the currently configured runners against the
// previously registered runners in /opt/runners. Any runner directory that has
// a .credentials file but is no longer in the config is considered stale and
// will be deregistered from GitHub and cleaned up via Deregister.
func (m *StandaloneManager) SyncStaleRunners(runners []sqlc.Runner) {
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

		// Runner is stale. No token is stored anymore (tokens expire after 1 hour),
		// so we can't deregister from GitHub automatically. Just clean up the directory.
		log.Printf("[Sync] Found stale runner: %s. Cleaning up directory (deregistration from GitHub must be done manually).", entry.Name())
		os.RemoveAll(absRunnerDir)

	}

	log.Println("[Sync] Reconciliation complete.")
}
