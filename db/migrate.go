package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var fs embed.FS

// RunMigrations applies all pending migrations from the embedded filesystem
func RunMigrations(db *sql.DB) error {
	d, err := iofs.New(fs, "migrations")
	if err != nil {
		return fmt.Errorf("failed to load embedded migrations: %w", err)
	}

	driver, err := sqlite3.WithInstance(db, &sqlite3.Config{})
	if err != nil {
		return fmt.Errorf("failed to create sqlite3 migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance(
		"iofs",
		d,
		"sqlite3",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to initialize migration instance: %w", err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	if errors.Is(err, migrate.ErrNoChange) {
		log.Println("Database is up to date.")
	} else {
		log.Println("Database migrations applied successfully.")
	}

	return nil
}
