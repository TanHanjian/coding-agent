package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"modernc.org/sqlite"
)

func TestMigrateRejectsInvalidExistingHistory(t *testing.T) {
	for _, tc := range []struct {
		name       string
		corruptSQL string
		wantError  string
	}{
		{"checksum mismatch", "UPDATE schema_migrations SET checksum = 'changed' WHERE version = 1", "migration 1 checksum mismatch"},
		{"missing first applied version", "DELETE FROM schema_migrations WHERE version = 1", "read migration 1"},
		{"missing middle applied version", "DELETE FROM schema_migrations WHERE version = 4", "read migration 4"},
		{"future version", "INSERT INTO schema_migrations(version, applied_at, checksum) SELECT MAX(version) + 1, 'future', 'future' FROM schema_migrations", "is newer than supported version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			seedMigrationPreservedData(t, db)
			if _, err := db.Exec(tc.corruptSQL); err != nil {
				t.Fatal(err)
			}
			before := snapshotMigrationState(t, db)

			err := Migrate(context.Background(), db)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected %q, got %v", tc.wantError, err)
			}
			assertMigrationStateUnchanged(t, db, before)
		})
	}
}

func TestApplyMigrationsRollsBackEntireSequence(t *testing.T) {
	for _, migrated := range []bool{false, true} {
		state := "empty database"
		if migrated {
			state = "migrated database"
		}
		t.Run(state, func(t *testing.T) {
			for _, failure := range []string{"version gap", "SQL failure", "metadata failure", "deferred foreign key commit failure"} {
				t.Run(failure, func(t *testing.T) {
					db := openMigrationTestDB(t)
					var migrations []Migration
					if migrated {
						if err := Migrate(context.Background(), db); err != nil {
							t.Fatal(err)
						}
						migrations = embeddedMigrationsForTest(t)
						seedMigrationPreservedData(t, db)
					}
					before := snapshotMigrationState(t, db)
					next := len(migrations) + 1
					firstSQL := `CREATE TABLE migration_probe (id INTEGER PRIMARY KEY, value TEXT NOT NULL);
						CREATE INDEX migration_probe_value ON migration_probe(value);
						INSERT INTO migration_probe(id, value) VALUES (1, 'pending');`
					if migrated {
						firstSQL += `UPDATE migration_preserved SET value = 'changed';`
					}
					migrations = append(migrations, migrationForTest(next, firstSQL))
					secondVersion := next + 1
					secondSQL := `ALTER TABLE migration_probe ADD COLUMN added TEXT;
						CREATE TABLE migration_later (id INTEGER PRIMARY KEY);`
					var wantError string
					switch failure {
					case "version gap":
						secondVersion++
						wantError = fmt.Sprintf("missing migration version %d", next+1)
					case "SQL failure":
						secondSQL += `INSERT INTO migration_missing_table VALUES (1);`
						wantError = fmt.Sprintf("apply migration %d", secondVersion)
					case "metadata failure":
						secondSQL += fmt.Sprintf(`CREATE TRIGGER migration_reject_metadata
							BEFORE INSERT ON schema_migrations WHEN NEW.version = %d
							BEGIN SELECT RAISE(ABORT, 'injected metadata failure'); END;`, secondVersion)
						wantError = fmt.Sprintf("record migration %d", secondVersion)
					case "deferred foreign key commit failure":
						secondSQL += `CREATE TABLE migration_child (
							id INTEGER PRIMARY KEY,
							parent_id INTEGER NOT NULL REFERENCES migration_later(id) DEFERRABLE INITIALLY DEFERRED
						);
							INSERT INTO migration_child(id, parent_id) VALUES (1, 999);`
						wantError = "commit migration"
					}
					migrations = append(migrations, migrationForTest(secondVersion, secondSQL))
					migrations = append(migrations, migrationForTest(secondVersion+1,
						`CREATE TABLE migration_final (id INTEGER PRIMARY KEY);`))

					err := applyMigrations(context.Background(), db, migrations)
					if err == nil || !strings.Contains(err.Error(), wantError) {
						t.Fatalf("expected %q, got %v", wantError, err)
					}
					if failure == "metadata failure" && !strings.Contains(err.Error(), "injected metadata failure") {
						t.Fatalf("expected injected metadata failure, got %v", err)
					}
					if failure == "deferred foreign key commit failure" {
						var sqliteErr *sqlite.Error
						if !errors.As(err, &sqliteErr) || sqliteErr.Code() != 787 { // SQLITE_CONSTRAINT_FOREIGNKEY
							t.Fatalf("expected deferred foreign key constraint at commit, got %v", err)
						}
					}
					assertMigrationStateUnchanged(t, db, before)
				})
			}
		})
	}
}

func openMigrationTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "migrate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func migrationForTest(version int, statement string) Migration {
	sum := sha256.Sum256([]byte(statement))
	return Migration{Version: version, SQL: statement, Checksum: hex.EncodeToString(sum[:])}
}

func embeddedMigrationsForTest(t *testing.T) []Migration {
	t.Helper()
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil {
			t.Fatal(err)
		}
		statement, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		migrations = append(migrations, migrationForTest(version, string(statement)))
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations
}

func seedMigrationPreservedData(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE migration_preserved (id INTEGER PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO migration_preserved(id, value) VALUES (1, 'original');`); err != nil {
		t.Fatal(err)
	}
}

type migrationStateSnapshot struct {
	Schema   [][]any
	Versions [][]any
	Data     [][]any
}

func snapshotMigrationState(t *testing.T, db *DB) migrationStateSnapshot {
	t.Helper()
	state := migrationStateSnapshot{
		Schema: migrationQueryForTest(t, db, `SELECT type, name, tbl_name, sql FROM sqlite_schema ORDER BY type, name`),
	}
	var hasVersions, hasData int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&hasVersions); err != nil {
		t.Fatal(err)
	}
	if hasVersions != 0 {
		state.Versions = migrationQueryForTest(t, db, `SELECT version, applied_at, checksum FROM schema_migrations ORDER BY version`)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'migration_preserved'`).Scan(&hasData); err != nil {
		t.Fatal(err)
	}
	if hasData != 0 {
		state.Data = migrationQueryForTest(t, db, `SELECT id, value FROM migration_preserved ORDER BY id`)
	}
	return state
}

func migrationQueryForTest(t *testing.T, db *DB, query string) [][]any {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertMigrationStateUnchanged(t *testing.T, db *DB, before migrationStateSnapshot) {
	t.Helper()
	after := snapshotMigrationState(t, db)
	if !reflect.DeepEqual(after.Schema, before.Schema) {
		t.Errorf("migration failure changed schema:\nbefore: %#v\nafter: %#v", before.Schema, after.Schema)
	}
	if !reflect.DeepEqual(after.Versions, before.Versions) {
		t.Errorf("migration failure changed version records:\nbefore: %#v\nafter: %#v", before.Versions, after.Versions)
	}
	if !reflect.DeepEqual(after.Data, before.Data) {
		t.Errorf("migration failure changed existing data:\nbefore: %#v\nafter: %#v", before.Data, after.Data)
	}
}
