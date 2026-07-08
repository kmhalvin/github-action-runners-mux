package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/kmhalvin/github-action-runners-mux/config"
	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
)

// ImportFromYAML reads the legacy config.yaml and imports it into SQLite if the DB is empty.
// After successful import, it renames config.yaml to config.yaml.bak.
func ImportFromYAML(ctx context.Context, db *sql.DB, queries *sqlc.Queries, yamlPath string) error {
	// Only import if runners table is empty
	runners, err := queries.ListRunners(ctx)
	if err != nil {
		return fmt.Errorf("failed to query runners count: %w", err)
	}
	if len(runners) > 0 {
		return nil // DB already populated, skip import
	}

	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		return nil // No yaml to import
	}

	log.Printf("Importing configuration from %s into SQLite...", yamlPath)

	cfg, err := config.LoadConfig(yamlPath)
	if err != nil {
		return fmt.Errorf("failed to load yaml config: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	qtx := queries.WithTx(tx)

	// Import settings
	err = qtx.UpsertSetting(ctx, sqlc.UpsertSettingParams{
		Key:   "max_workers",
		Value: fmt.Sprintf("%d", cfg.MaxWorkers),
	})
	if err != nil {
		return fmt.Errorf("failed to import max_workers: %w", err)
	}

	err = qtx.UpsertSetting(ctx, sqlc.UpsertSettingParams{
		Key:   "warm_workers",
		Value: fmt.Sprintf("%d", cfg.WarmWorkers),
	})
	if err != nil {
		return fmt.Errorf("failed to import warm_workers: %w", err)
	}

	// Import runners
	for _, r := range cfg.Runners {
		mode := r.Mode
		if mode == "" {
			mode = "standalone"
		}

		labels := ""
		if len(r.Labels) > 0 {
			labels = strings.Join(r.Labels, ",")
		}

		group := r.Group
		if group == "" {
			group = "Default"
		}

		maxRunners := 0
		if r.MaxRunners > 0 {
			maxRunners = r.MaxRunners
		}

		var token, dir, pat, ssName string

		switch mode {
		case "standalone":
			token = r.Token
			dir = r.Dir
		case "scaleset":
			pat = r.PAT
			ssName = r.ScaleSetName
		}

		_, err = qtx.CreateRunner(ctx, sqlc.CreateRunnerParams{
			Name:         string(r.Name),
			Mode:         mode,
			Url:          r.URL,
			Token:        token,
			Dir:          dir,
			Pat:          pat,
			ScaleSetName: ssName,
			MaxRunners:   int64(maxRunners),
			Labels:       labels,
			RunnerGroup:  group,
		})
		if err != nil {
			return fmt.Errorf("failed to import runner %s: %w", r.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Rename the file to .bak
	bakPath := yamlPath + ".bak"
	if err := os.Rename(yamlPath, bakPath); err != nil {
		log.Printf("Warning: failed to rename %s to %s: %v", yamlPath, bakPath, err)
	} else {
		log.Printf("Successfully imported config to DB and renamed YAML to %s", filepath.Base(bakPath))
	}

	return nil
}
