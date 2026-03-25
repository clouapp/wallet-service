// Package seeds provides deterministic seed data for local development and testing.
// Run via: make db-seed
package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"golang.org/x/crypto/bcrypt"

	"github.com/macromarkets/vault/app/models"
)

// predefined UUIDs so the seed is idempotent (same IDs every run)
var (
	adminUserID   = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	aliceUserID   = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	bobUserID     = uuid.MustParse("00000000-0000-0000-0000-000000000003")
	acmeAccountID = uuid.MustParse("00000000-0000-0000-0000-000000000010")
	ethWalletID   = uuid.MustParse("00000000-0000-0000-0000-000000000020")
	btcWalletID   = uuid.MustParse("00000000-0000-0000-0000-000000000021")
	polyWalletID  = uuid.MustParse("00000000-0000-0000-0000-000000000022")
)

// Run inserts seed data. It is idempotent: existing rows (matched by primary key)
// are skipped so the command is safe to re-run.
func Run(ctx context.Context) error {
	slog.Info("seeding database…")

	if err := seedUsers(ctx); err != nil {
		return fmt.Errorf("seed users: %w", err)
	}
	if err := seedAccounts(ctx); err != nil {
		return fmt.Errorf("seed accounts: %w", err)
	}
	if err := seedWallets(ctx); err != nil {
		return fmt.Errorf("seed wallets: %w", err)
	}

	slog.Info("seed complete ✓")
	printCredentials()
	return nil
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

func seedUsers(ctx context.Context) error {
	users := []struct {
		id       uuid.UUID
		email    string
		password string
		fullName string
	}{
		{adminUserID, "admin@vault.dev", "Password123!", "Admin User"},
		{aliceUserID, "alice@vault.dev", "Password123!", "Alice Smith"},
		{bobUserID, "bob@vault.dev", "Password123!", "Bob Jones"},
	}

	for _, u := range users {
		var existing models.User
		if err := facades.Orm().Query().Where("id", u.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			slog.Info("user already exists, skipping", "email", u.email)
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(u.password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		user := models.User{
			ID:           u.id,
			Email:        u.email,
			PasswordHash: string(hash),
			FullName:     u.fullName,
			Status:       "active",
		}
		if err := facades.Orm().Query().Create(&user); err != nil {
			return fmt.Errorf("create user %s: %w", u.email, err)
		}
		slog.Info("created user", "email", u.email)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Accounts + account_users
// ---------------------------------------------------------------------------

func seedAccounts(ctx context.Context) error {
	var existing models.Account
	if err := facades.Orm().Query().Where("id", acmeAccountID).First(&existing); err == nil && existing.ID != uuid.Nil {
		slog.Info("account already exists, skipping", "name", existing.Name)
	} else {
		acct := models.Account{
			ID:             acmeAccountID,
			Name:           "Acme Corp",
			Status:         "active",
			ViewAllWallets: true,
		}
		if err := facades.Orm().Query().Create(&acct); err != nil {
			return fmt.Errorf("create account: %w", err)
		}
		slog.Info("created account", "name", acct.Name)
	}

	// Account memberships
	members := []struct {
		id      uuid.UUID
		userID  uuid.UUID
		role    string
	}{
		{uuid.MustParse("00000000-0000-0000-0000-000000000030"), adminUserID, "owner"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000031"), aliceUserID, "admin"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000032"), bobUserID, "auditor"},
	}
	for _, m := range members {
		var existing models.AccountUser
		if err := facades.Orm().Query().Where("id", m.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			continue
		}
		au := models.AccountUser{
			ID:        m.id,
			AccountID: acmeAccountID,
			UserID:    m.userID,
			Role:      m.role,
			Status:    "active",
		}
		if err := facades.Orm().Query().Create(&au); err != nil {
			return fmt.Errorf("create account_user %s: %w", m.role, err)
		}
		slog.Info("added user to account", "role", m.role)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Wallets + wallet_users
// ---------------------------------------------------------------------------

// placeholder MPC values — realistic hex lengths, not real key material
const (
	fakePubKey    = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	fakeShareHex  = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	fakeIVHex     = "000102030405060708090a0b"
	fakeSaltHex   = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	fakeSecretARN = "arn:aws:secretsmanager:us-east-1:000000000000:secret:vault/wallet/seed/share-b"
)

func seedWallets(_ context.Context) error {
	wallets := []struct {
		id             uuid.UUID
		chain          string
		label          string
		depositAddress string
		curve          string
	}{
		{ethWalletID, "eth", "Primary ETH Wallet", "0xDEADbeef00000000000000000000000000000001", "secp256k1"},
		{btcWalletID, "btc", "Primary BTC Wallet", "bc1qseed000000000000000000000000000000000001", "secp256k1"},
		{polyWalletID, "polygon", "Polygon Wallet", "0xDEADbeef00000000000000000000000000000002", "secp256k1"},
	}

	accountID := acmeAccountID
	for _, w := range wallets {
		var existing models.Wallet
		if err := facades.Orm().Query().Where("id", w.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			slog.Info("wallet already exists, skipping", "label", w.label)
			continue
		}
		wallet := models.Wallet{
			ID:               w.id,
			Chain:            w.chain,
			Label:            w.label,
			MPCCustomerShare: fakeShareHex,
			MPCShareIV:       fakeIVHex,
			MPCShareSalt:     fakeSaltHex,
			MPCSecretARN:     fakeSecretARN,
			MPCPublicKey:     fakePubKey,
			MPCCurve:         w.curve,
			DepositAddress:   w.depositAddress,
			AccountID:        &accountID,
			Status:           "active",
			RequiredApprovals: 1,
		}
		if err := facades.Orm().Query().Create(&wallet); err != nil {
			return fmt.Errorf("create wallet %s: %w", w.label, err)
		}
		slog.Info("created wallet", "label", w.label, "chain", w.chain)
	}

	// Add alice and bob as wallet users on all three wallets
	walletUserSeeds := []struct {
		id       uuid.UUID
		walletID uuid.UUID
		userID   uuid.UUID
		roles    string
	}{
		{uuid.MustParse("00000000-0000-0000-0000-000000000040"), ethWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000041"), ethWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000042"), btcWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000043"), btcWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000044"), polyWalletID, aliceUserID, "viewer"},
	}
	for _, wu := range walletUserSeeds {
		var existing models.WalletUser
		if err := facades.Orm().Query().Where("id", wu.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			continue
		}
		walletUser := models.WalletUser{
			ID:       wu.id,
			WalletID: wu.walletID,
			UserID:   wu.userID,
			Roles:    wu.roles,
			Status:   "active",
		}
		if err := facades.Orm().Query().Create(&walletUser); err != nil {
			return fmt.Errorf("create wallet_user: %w", err)
		}
	}
	slog.Info("seeded wallet users")
	return nil
}

// ---------------------------------------------------------------------------

func printCredentials() {
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  Seed data created — login credentials")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  admin@vault.dev  / Password123!  (account owner)")
	fmt.Println("  alice@vault.dev  / Password123!  (account admin)")
	fmt.Println("  bob@vault.dev    / Password123!  (auditor)")
	fmt.Println()
	fmt.Println("  Account: Acme Corp")
	fmt.Println("  Wallets: ETH · BTC · Polygon (all active)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
}
