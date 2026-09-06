package storage

import (
	"context"
	"database/sql"
)

type TxManager interface {
	WithinTx(ctx context.Context, fn func(context.Context, *sql.Tx) error) error
}

func (db *DB) WithinTx(ctx context.Context, fn func(context.Context, *sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback()
			panic(recovered)
		}
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()
	err = fn(ctx, tx)
	return err
}
