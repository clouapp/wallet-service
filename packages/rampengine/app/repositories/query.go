package repositories

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/goravel/framework/contracts/database/orm"
	frameworkerrors "github.com/goravel/framework/errors"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm/schema"

	"github.com/macrowallets/waas/packages/rampengine/app/identity"
)

// first loads the one row query matches, or (nil, nil) when none does: the
// contract every Find* in this package promises. It goes through FirstOrFail
// because First answers a miss with a nil error and a zero dest
// (framework database/gorm/query.go:296-309); FirstOrFail runs the same
// LIMIT 1 and says OrmRecordNotFound instead. Checking First's error alone
// once handed a caller an empty ramp estimate — no rail, no provider, no fee —
// as if it were a real quote.
func first[T any](query orm.Query, what string) (*T, error) {
	var row T
	err := query.FirstOrFail(&row)
	if errors.Is(err, frameworkerrors.OrmRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return &row, nil
}

// likeContains is the ILIKE pattern that finds term anywhere in a column. The
// term's own `\`, `%` and `_` are escaped, so an operator searching for `a_b`
// does not get `axb`, nor every row for a lone `%`. Backslash is Postgres's
// default LIKE escape, so the query needs no ESCAPE clause.
func likeContains(term string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term) + "%"
}

// takenBy is what each identity unique index answers when it refuses a write.
var takenBy = map[string]error{
	"users_handle_unique":                                     identity.ErrHandleTaken,
	"users_email_unique":                                      identity.ErrEmailTaken,
	"admins_email_unique":                                     identity.ErrEmailTaken,
	"customer_individual_profiles_tax_id_hash_unique":         ErrTaxIDInUse,
	"customer_individual_profiles_phone_hash_unique":          ErrPhoneInUse,
	"customer_provider_integrations_external_customer_unique": ErrExternalCustomerLinkedElsewhere,
	"customer_wallets_chain_address_unique":                   ErrWalletTaken,
	"customers_business_tax_id_hash_unique":                   ErrTaxIDHeld,
}

// taken answers a write an identity unique index refused with what was taken.
// Two requests can both pass the service's availability read; the index is
// what settles the race, and the loser gets the answer that read would give.
func taken(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return err
	}
	if sentinel, ok := takenBy[pgErr.ConstraintName]; ok {
		return sentinel
	}
	return err
}

// updateLive writes every column of row to the row with its id while that row
// is live: an UPDATE the soft-delete scope limits to deleted_at IS NULL, which
// can never insert. It reports whether a row matched. It is what a full-row
// write on a soft-deletable table uses instead of Save, whose upsert brings a
// row soft-deleted since it was read back to life.
//
// The columns are named one by one, never "*": gorm reads Select("*") as
// unrestricted and saves every association the row carries — a preloaded
// User would be inserted, and the foreign key moved to it.
func updateLive(q orm.Query, row any) (bool, error) {
	columns, err := ownColumns(row)
	if err != nil {
		return false, err
	}
	res, err := q.Model(row).Select(columns...).Update(row)
	if err != nil {
		return false, err
	}
	return res.RowsAffected > 0, nil
}

// modelSchemas caches what ownColumns parses, per model type.
var modelSchemas sync.Map

// ownColumns is the model's own columns as gorm maps them — the associations
// are fields with no column, so they are not among them. The naming strategy
// is gorm's default, which is the one config/database.go leaves the
// connection with (no NoLowerCase, no NameReplacer; the prefix and singular
// settings name tables, not columns).
func ownColumns(row any) ([]string, error) {
	parsed, err := schema.Parse(row, &modelSchemas, schema.NamingStrategy{})
	if err != nil {
		return nil, fmt.Errorf("repositories: columns of %T: %w", row, err)
	}
	return parsed.DBNames, nil
}
