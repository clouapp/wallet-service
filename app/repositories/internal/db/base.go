// Package db is the query seam, transactions and Postgres error translation
// shared by app/repositories. The internal path blocks imports from outside
// that folder.
package db

import (
	"context"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"
)

// Base is the query seam every repository embeds. Nil means a fresh query per
// call; non-nil binds one transaction shared across repositories.
type Base struct {
	query orm.Query
}

// NewBase wraps an orm.Query. Pass nil for a top-level repository.
func NewBase(query orm.Query) Base {
	return Base{query: query}
}

type txKey struct{}

// WithTx returns a context whose queries join tx. Repositories built without
// their own query pick it up in Query, so one transaction covers every store
// the service calls with that context.
func WithTx(ctx context.Context, tx orm.Query) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, txKey{}, tx)
}

func txFrom(ctx context.Context) orm.Query {
	if ctx == nil {
		return nil
	}
	tx, _ := ctx.Value(txKey{}).(orm.Query)
	return tx
}

// Query returns a context-bound query, keeping an open transaction when this
// repository was built with one. A transaction stored on ctx wins over a fresh
// query so callers that share the context share the transaction.
func (b Base) Query(ctx context.Context) orm.Query {
	if ctx == nil {
		ctx = context.Background()
	}
	if b.query != nil {
		if q, ok := b.query.(orm.QueryWithContext); ok {
			return q.WithContext(ctx)
		}
		return b.query
	}
	if tx := txFrom(ctx); tx != nil {
		return tx
	}
	return facades.Orm().WithContext(ctx).Query()
}

// Transaction runs fn atomically, joining a transaction already open instead
// of starting a second one.
func (b Base) Transaction(ctx context.Context, fn func(tx orm.Query) error) error {
	query := b.Query(ctx)
	if query.InTransaction() {
		return fn(query)
	}
	tx, err := query.BeginTransaction()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	// A panic in fn must not leave the transaction open on its connection, so
	// roll back before it keeps unwinding. Goravel's own Orm().Transaction
	// swallows the panic and drops the original error when the rollback fails,
	// and it would not join a transaction already on ctx.
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		}
	}()
	if err := fn(tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fmt.Errorf("%w (rollback: %w)", err, rollbackErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
