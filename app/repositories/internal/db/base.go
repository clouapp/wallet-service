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

// Query returns a context-bound query, keeping an open transaction when this
// repository was built with one.
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
	return facades.Orm().WithContext(ctx).Query()
}

// Bound returns the query this repository was built with, including nil.
// Siblings on the same transaction take this, not Query.
func (b Base) Bound() orm.Query {
	return b.query
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
