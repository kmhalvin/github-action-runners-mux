package orchestrator

import (
	"context"
	"encoding/base64"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// runnerConfigFileNames are the config files Runner.Worker needs.
// Verified against the actions/runner source (ConfigurationStore.cs):
//   - .runner       — required by GetSettings(); missing → ArgumentNullException
//   - .credentials  — required for OAuth authentication during job execution
//
// .credentials_rsaparams and .agent are NOT needed (the latter doesn't even
// exist in the runner source).
var runnerConfigFileNames = []string{
	".runner",
	".credentials",
}

// readRunnerConfigFiles reads the specific runner's config files and returns
// them as a map of filename → base64-encoded content. These are injected into
// the worker container via the TCP header so the worker never needs to mount
// the shared volume (which would expose all runners' credentials).
//
// The dir parameter is authoritative — it comes from the shim's own executable
// path, so it's guaranteed to be the directory where config.sh wrote the files.
// If dir is empty (e.g. older shim), we fall back to looking up the runner by
// name in the config.
func (o *Orchestrator) readRunnerConfigFiles(name string, dir string) map[string]string {
	// Prefer the directory from the shim (authoritative — it's where the shim lives)
	if dir != "" {
		cleanDir := filepath.Clean(dir)
		if !strings.HasPrefix(cleanDir, "/opt/runners/") {
			log.Printf("[Orchestrator] Warning: runner %s dir is not under /opt/runners/: %s", name, cleanDir)
			dir = "" // fallback to DB lookup
		} else {
			dir = cleanDir
		}
	}

	if dir == "" {
		// Fallback: look up by name in DB
		if o.queries == nil {
			log.Printf("[Orchestrator] Warning: no queries and no dir provided for runner %s", name)
			return nil
		}

		runner, err := o.queries.GetRunnerByName(context.Background(), name)
		if err != nil {
			log.Printf("[Orchestrator] Warning: runner %s not found in DB and no dir provided", name)
			return nil
		}

		if runner.Dir != "" {
			dir = runner.Dir
		}

		if dir == "" {
			log.Printf("[Orchestrator] Warning: runner %s dir is empty in DB", name)
			return nil
		}
		log.Printf("[Orchestrator] Using DB-lookup dir for runner %s: %s", name, dir)
	}

	configFiles := make(map[string]string)
	for _, fname := range runnerConfigFileNames {
		data, err := os.ReadFile(filepath.Join(dir, fname))
		if err != nil {
			log.Printf("[Orchestrator] Warning: could not read %s for runner %s: %v", fname, name, err)
			continue
		}
		configFiles[fname] = base64.StdEncoding.EncodeToString(data)
	}

	if len(configFiles) == 0 {
		log.Printf("[Orchestrator] Warning: no config files found for runner %s in %s", name, dir)
		return nil
	}

	log.Printf("[Orchestrator] Read %d config files for runner %s from %s", len(configFiles), name, dir)
	return configFiles
}
