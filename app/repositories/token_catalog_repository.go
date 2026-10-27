package repositories

import (
	"context"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// WithinCatalog joins all catalog checks and writes to one transaction. Callers
// lock the affected chain rows before inspecting tokens. Unrelated token writers
// must follow the same locking protocol; this does not serialize rogue writers.
func (r *TokenRepository) WithinCatalog(ctx context.Context, fn func(context.Context) error) error {
	return r.Transaction(ctx, func(tx orm.Query) error { return fn(db.WithTx(ctx, tx)) })
}
