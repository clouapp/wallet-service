// Package requestctx reads the request values middleware stores.
// The keys stay the strings SessionAuth, AccountHeader, AccountContext,
// APITokenAuth, WalletContext, and APIWalletContext already write.
// Only those middlewares write them; this package does not.
package requestctx

import (
	"context"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

const (
	// KeyUserID is the dashboard user id SessionAuth stores.
	KeyUserID = "user_id"
	// KeyUser is the dashboard user SessionAuth stores.
	KeyUser = "user"
	// KeyAccount is the account AccountContext and AccountHeader store.
	KeyAccount = "account"
	// KeyAccountID is the account id AccountHeader and APITokenAuth store.
	KeyAccountID = "account_id"
	// KeyAccountRole is the membership role AccountContext and AccountHeader store.
	KeyAccountRole = "account_role"
	// KeyAccountEnvironment is the account environment AccountHeader stores.
	KeyAccountEnvironment = "account_environment"
	// KeyWallet is the wallet WalletContext and APIWalletContext store.
	KeyWallet = "wallet"
	// KeyWalletID is the wallet id those wallet middlewares store.
	KeyWalletID = "wallet_id"
	// KeyAPIToken is the access token APITokenAuth stores.
	KeyAPIToken = "api_token"
)

// UserID is the dashboard user id. A missing or wrong-typed value is uuid.Nil, false.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	return uuidValue(ctx, KeyUserID)
}

// MustUserID is the dashboard user id. It panics when the value is missing or
// not a UUID, the same way a bare type assertion did.
func MustUserID(ctx context.Context) uuid.UUID {
	return present(ctx, KeyUserID).(uuid.UUID)
}

// User is the dashboard user. A missing or wrong-typed value is nil, false.
func User(ctx context.Context) (*models.User, bool) {
	if ctx == nil {
		return nil, false
	}
	user, ok := ctx.Value(KeyUser).(*models.User)
	return user, ok
}

// MustUser is the dashboard user. It panics when the value is missing or not a *models.User.
func MustUser(ctx context.Context) *models.User {
	return present(ctx, KeyUser).(*models.User)
}

// Account is the account in scope. A missing or wrong-typed value is nil, false.
func Account(ctx context.Context) (*models.Account, bool) {
	if ctx == nil {
		return nil, false
	}
	account, ok := ctx.Value(KeyAccount).(*models.Account)
	return account, ok
}

// MustAccount is the account in scope. It panics when the value is missing or not a *models.Account.
func MustAccount(ctx context.Context) *models.Account {
	return present(ctx, KeyAccount).(*models.Account)
}

// AccountRole is the caller's membership role. A missing or wrong-typed value is "", false.
func AccountRole(ctx context.Context) (string, bool) {
	return stringValue(ctx, KeyAccountRole)
}

// AccountID is the account id in scope. A missing or wrong-typed value is uuid.Nil, false.
func AccountID(ctx context.Context) (uuid.UUID, bool) {
	return uuidValue(ctx, KeyAccountID)
}

// AccountEnvironment is the account environment. A missing or wrong-typed value is "", false.
func AccountEnvironment(ctx context.Context) (string, bool) {
	return stringValue(ctx, KeyAccountEnvironment)
}

// APIToken is the access token APITokenAuth stored. A missing or wrong-typed
// value is nil, false.
func APIToken(ctx context.Context) (*models.AccessToken, bool) {
	if ctx == nil {
		return nil, false
	}
	token, ok := ctx.Value(KeyAPIToken).(*models.AccessToken)
	return token, ok
}

// Wallet is the wallet in scope. A missing or wrong-typed value is nil, false.
func Wallet(ctx context.Context) (*models.Wallet, bool) {
	if ctx == nil {
		return nil, false
	}
	wallet, ok := ctx.Value(KeyWallet).(*models.Wallet)
	return wallet, ok
}

// MustWallet is the wallet in scope. It panics when the value is missing or not a *models.Wallet.
func MustWallet(ctx context.Context) *models.Wallet {
	return present(ctx, KeyWallet).(*models.Wallet)
}

func uuidValue(ctx context.Context, key string) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	id, ok := ctx.Value(key).(uuid.UUID)
	return id, ok
}

func stringValue(ctx context.Context, key string) (string, bool) {
	if ctx == nil {
		return "", false
	}
	value, ok := ctx.Value(key).(string)
	return value, ok
}

func present(ctx context.Context, key string) any {
	if ctx == nil {
		return nil
	}
	return ctx.Value(key)
}
