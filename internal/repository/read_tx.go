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

// DetailReadSnapshot keeps message metadata and read-time warnings consistent
// for full-access reads too. A bearer request already owns its authorization
// snapshot; the handler must neither replace nor release that transaction.
func (r *MessageRepository) DetailReadSnapshot(ctx context.Context) (context.Context, func(), error) {
	if _, ok := ctx.Value(readTxKey{}).(*sql.Tx); ok {
		return ctx, func() {}, nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ctx, nil, err
	}
	return WithReadTransaction(ctx, tx), func() { _ = tx.Rollback() }, nil
}
