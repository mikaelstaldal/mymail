package repository

import (
	"context"
	"database/sql"
)

type readTxKey struct{}

// WithReadTransaction makes the bearer middleware's authorization snapshot
// available to the repository reads that assemble a scoped response.
func WithReadTransaction(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, readTxKey{}, tx)
}

type readQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryer(ctx context.Context, db *sql.DB) readQueryer {
	if tx, ok := ctx.Value(readTxKey{}).(*sql.Tx); ok {
		return tx
	}
	return db
}
