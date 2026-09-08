package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "data", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db.DB); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := testDB(t)
	if err := Migrate(context.Background(), db.DB); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected three migrations, got %d", count)
	}
}

func TestWithinTxCommitAndRollback(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec("CREATE TABLE values_test (value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if err := db.WithinTx(context.Background(), func(_ context.Context, tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO values_test(value) VALUES (?)", "committed")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.WithinTx(context.Background(), func(_ context.Context, tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO values_test(value) VALUES (?)", "rolled back")
		if err != nil {
			return err
		}
		return errors.New("expected rollback")
	}); err == nil {
		t.Fatal("expected rollback error")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM values_test").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one committed row, got %d", count)
	}
}

func TestSQLiteForeignKeysEnabled(t *testing.T) {
	db := testDB(t)
	var enabled int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatalf("expected foreign keys enabled, got %d", enabled)
	}
}
