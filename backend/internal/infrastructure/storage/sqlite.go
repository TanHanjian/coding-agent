package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
	orm *gorm.DB
}

func Open(ctx context.Context, databasePath string) (*DB, error) {
	if err := mkdirAll(filepath.Dir(databasePath)); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure sqlite: %w", err)
		}
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	orm, err := gorm.Open(sqlite.New(sqlite.Config{Conn: db, DriverName: "sqlite"}), &gorm.Config{
		Logger:                 logger.Default.LogMode(logger.Silent),
		SkipDefaultTransaction: true,
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize sqlite ORM: %w", err)
	}
	return &DB{DB: db, orm: orm}, nil
}

// ORM returns a context-bound handle using the existing modernc connection pool.
func (db *DB) ORM(ctx context.Context) *gorm.DB { return db.orm.WithContext(ctx) }

// WithinTx runs fn with a context-bound ORM handle; all operations using that
// handle share the same transaction. A nil callback error commits the transaction;
// a callback error, panic, or commit failure rolls it back. Panics are rethrown.
func (db *DB) WithinTx(ctx context.Context, fn func(context.Context, *gorm.DB) error) error {
	return db.ORM(ctx).Transaction(func(tx *gorm.DB) error { return fn(ctx, tx) })
}

func (db *DB) Close() error { return db.DB.Close() }

type TxManager interface {
	WithinTx(ctx context.Context, fn func(context.Context, *gorm.DB) error) error
}
