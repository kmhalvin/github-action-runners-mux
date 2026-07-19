package main

import (
	"encoding/base64"
	"log"
	"os"
	"path/filepath"
)

var runnerConfigFileNames = []string{
	".runner",
	".credentials",
}

func readRunnerConfigFiles(dir string) map[string]string {
	configFiles := make(map[string]string)
	for _, fname := range runnerConfigFileNames {
		data, err := os.ReadFile(filepath.Join(dir, fname))
		if err != nil {
			log.Printf("[Worker Shim] Warning: could not read %s from %s: %v", fname, dir, err)
			continue
		}
		configFiles[fname] = base64.StdEncoding.EncodeToString(data)
	}

	if len(configFiles) == 0 {
		return nil
	}

	log.Printf("[Worker Shim] Read %d config files from %s", len(configFiles), dir)
	return configFiles
}
