package requestctx

import (
	"context"
	"testing"

	"github.com/google/uuid"
	frameworkhttp "github.com/goravel/framework/http"

	"github.com/macrowallets/waas/app/models"
)

func TestReadersSeeTheHistoricalStringKeys(t *testing.T) {
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	accountID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	walletID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	user := &models.User{ID: userID}
	account := &models.Account{ID: accountID, Environment: models.EnvironmentTest}
	wallet := &models.Wallet{ID: walletID}
	token := &models.AccessToken{ID: uuid.MustParse("44444444-4444-4444-4444-444444444444"), AccountID: accountID}

	ctx := context.Background()
	ctx = context.WithValue(ctx, "user_id", userID)
	ctx = context.WithValue(ctx, "user", user)
	ctx = context.WithValue(ctx, "account", account)
	ctx = context.WithValue(ctx, "account_id", accountID)
	ctx = context.WithValue(ctx, "account_role", "admin")
	ctx = context.WithValue(ctx, "account_environment", "test")
	ctx = context.WithValue(ctx, "wallet", wallet)
	ctx = context.WithValue(ctx, "wallet_id", walletID)
	ctx = context.WithValue(ctx, "api_token", token)

	if got, ok := UserID(ctx); !ok || got != userID {
		t.Fatalf("UserID = %v, %v", got, ok)
	}
	if got := MustUserID(ctx); got != userID {
		t.Fatalf("MustUserID = %v", got)
	}
	if got, ok := User(ctx); !ok || got != user {
		t.Fatalf("User = %v, %v", got, ok)
	}
	if got := MustUser(ctx); got != user {
		t.Fatalf("MustUser = %v", got)
	}
	if got, ok := Account(ctx); !ok || got != account {
		t.Fatalf("Account = %v, %v", got, ok)
	}
	if got := MustAccount(ctx); got != account {
		t.Fatalf("MustAccount = %v", got)
	}
	if got, ok := AccountRole(ctx); !ok || got != "admin" {
		t.Fatalf("AccountRole = %q, %v", got, ok)
	}
	if got, ok := AccountID(ctx); !ok || got != accountID {
		t.Fatalf("AccountID = %v, %v", got, ok)
	}
	if got, ok := AccountEnvironment(ctx); !ok || got != "test" {
		t.Fatalf("AccountEnvironment = %q, %v", got, ok)
	}
	if got, ok := Wallet(ctx); !ok || got != wallet {
		t.Fatalf("Wallet = %v, %v", got, ok)
	}
	if got := MustWallet(ctx); got != wallet {
		t.Fatalf("MustWallet = %v", got)
	}
	if got, ok := ctx.Value(KeyAPIToken).(*models.AccessToken); !ok || got != token {
		t.Fatalf("api token = %v, %v", got, ok)
	}
	if got, ok := ctx.Value(KeyWalletID).(uuid.UUID); !ok || got != walletID {
		t.Fatalf("wallet id = %v, %v", got, ok)
	}
}

func TestMissingValuesStayAbsent(t *testing.T) {
	ctx := context.Background()
	if _, ok := UserID(ctx); ok {
		t.Fatal("missing user id reported present")
	}
	if _, ok := UserID(nil); ok {
		t.Fatal("nil context reported a user id")
	}
	if user, ok := User(ctx); ok || user != nil {
		t.Fatal("missing user reported present")
	}
	if account, ok := Account(nil); ok || account != nil {
		t.Fatal("nil context reported an account")
	}
	if role, ok := AccountRole(ctx); ok || role != "" {
		t.Fatal("missing role reported present")
	}
	if _, ok := AccountID(ctx); ok {
		t.Fatal("missing account id reported present")
	}
	if env, ok := AccountEnvironment(ctx); ok || env != "" {
		t.Fatal("missing environment reported present")
	}
	if wallet, ok := Wallet(ctx); ok || wallet != nil {
		t.Fatal("missing wallet reported present")
	}
}

func TestMustPanicsWhenTheValueIsMissing(t *testing.T) {
	ctx := context.Background()
	assertPanics(t, func() { MustUserID(ctx) })
	assertPanics(t, func() { MustUser(ctx) })
	assertPanics(t, func() { MustAccount(ctx) })
	assertPanics(t, func() { MustWallet(ctx) })
}

func TestFrameworkContextCopiesTheSameKeys(t *testing.T) {
	ctx := frameworkhttp.NewContext()
	userID := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	ctx.WithValue(KeyUserID, userID)
	ctx.WithValue(KeyAccountRole, "owner")

	if got, ok := UserID(ctx); !ok || got != userID {
		t.Fatalf("framework context UserID = %v, %v", got, ok)
	}
	if got, ok := UserID(ctx.Context()); !ok || got != userID {
		t.Fatalf("copied context UserID = %v, %v", got, ok)
	}
	if got, ok := AccountRole(ctx.Context()); !ok || got != "owner" {
		t.Fatalf("copied context AccountRole = %q, %v", got, ok)
	}
}

func assertPanics(t *testing.T, fn func()) {
	t.Helper()
	deferred := func() {
		if recover() == nil {
			t.Fatal("expected a panic")
		}
	}
	defer deferred()
	fn()
}
