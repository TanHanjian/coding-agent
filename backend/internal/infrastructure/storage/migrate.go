package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Migration struct {
	Version  int
	SQL      string
	Checksum string
}

func Migrate(ctx context.Context, db *sql.DB) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid migration version %q: %w", entry.Name(), err)
		}
		sqlBytes, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		sum := sha256.Sum256(sqlBytes)
		migrations = append(migrations, Migration{Version: version, SQL: string(sqlBytes), Checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL, checksum TEXT NOT NULL)"); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	var current int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for _, migration := range migrations {
		if migration.Version > current {
			break
		}
		var checksum string
		if err := tx.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version = ?", migration.Version).Scan(&checksum); err != nil {
			return fmt.Errorf("read migration %d: %w", migration.Version, err)
		}
		if checksum != migration.Checksum {
			return fmt.Errorf("migration %d checksum mismatch", migration.Version)
		}
	}
	for _, migration := range migrations {
		if migration.Version <= current {
			continue
		}
		if migration.Version != current+1 {
			return fmt.Errorf("missing migration version %d", current+1)
		}
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			return fmt.Errorf("apply migration %d: %w", migration.Version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at, checksum) VALUES (?, ?, ?)", migration.Version, time.Now().UTC().Format(time.RFC3339Nano), migration.Checksum); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.Version, err)
		}
		current = migration.Version
	}
	var maxVersion int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&maxVersion); err != nil {
		return err
	}
	supportedVersion := 0
	if len(migrations) > 0 {
		supportedVersion = migrations[len(migrations)-1].Version
	}
	if maxVersion > supportedVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", maxVersion, supportedVersion)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}
