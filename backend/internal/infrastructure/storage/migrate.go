package storage

import (
	"context"
	"crypto/sha256"
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

type schemaMigrationRow struct {
	Version   int    `gorm:"column:version;primaryKey;autoIncrement:false"`
	AppliedAt string `gorm:"column:applied_at;autoCreateTime:false"`
	Checksum  string `gorm:"column:checksum"`
}

func (schemaMigrationRow) TableName() string { return "schema_migrations" }

func Migrate(ctx context.Context, db *DB) error {
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
	return applyMigrations(ctx, db, migrations)
}

func applyMigrations(ctx context.Context, db *DB, migrations []Migration) error {
	tx := db.ORM(ctx).Begin()
	if tx.Error != nil {
		return fmt.Errorf("begin migration: %w", tx.Error)
	}
	defer tx.Rollback()
	if err := tx.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL, checksum TEXT NOT NULL)").Error; err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	var current int
	if err := tx.Model(&schemaMigrationRow{}).Select("COALESCE(MAX(version), 0)").Scan(&current).Error; err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for _, migration := range migrations {
		if migration.Version > current {
			break
		}
		var row schemaMigrationRow
		if err := tx.Select("checksum").Take(&row, "version = ?", migration.Version).Error; err != nil {
			return fmt.Errorf("read migration %d: %w", migration.Version, err)
		}
		if row.Checksum != migration.Checksum {
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
		if err := tx.Exec(migration.SQL).Error; err != nil {
			return fmt.Errorf("apply migration %d: %w", migration.Version, err)
		}
		row := schemaMigrationRow{Version: migration.Version, AppliedAt: time.Now().UTC().Format(time.RFC3339Nano), Checksum: migration.Checksum}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("record migration %d: %w", migration.Version, err)
		}
		current = migration.Version
	}
	var maxVersion int
	if err := tx.Model(&schemaMigrationRow{}).Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
		return err
	}
	supportedVersion := 0
	if len(migrations) > 0 {
		supportedVersion = migrations[len(migrations)-1].Version
	}
	if maxVersion > supportedVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", maxVersion, supportedVersion)
	}
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}
