package mocks

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// ---------------------------------------------------------------------------
// TestDB — sets up Goravel ORM with test database
// Uses TEST_DATABASE_URL env var. Each test gets a clean schema.
// ---------------------------------------------------------------------------

// TestDB sets up test database. Assumes Goravel is already booted via TestMain.
func TestDB(t *testing.T) {
	t.Helper()

	// Set test database connection
	testDSN := os.Getenv("TEST_DATABASE_URL")
	if testDSN == "" {
		testDSN = "postgres://vault:vault@localhost:5432/vault_test?sslmode=disable"
	}

	// Override database config for testing
	facades.Config().Add("database", map[string]any{
		"default": "postgres",
		"connections": map[string]any{
			"postgres": map[string]any{
				"driver":   "postgres",
				"host":     "localhost",
				"port":     5432,
				"database": "vault_test",
				"username": "vault",
				"password": "vault",
			},
		},
	})

	// Verify database connectivity.
	//
	// By default, tests are skipped when the DB is unavailable so the suite
	// remains runnable on developer machines without Docker. CI must opt in
	// to strict enforcement by exporting TEST_DB_REQUIRED=1, which upgrades
	// every skip path to a hard failure so broken DB-backed tests can't hide.
	required := os.Getenv("TEST_DB_REQUIRED") == "1"
	fail := func(format string, args ...any) {
		if required {
			t.Fatalf(format, args...)
			return
		}
		t.Skipf(format, args...)
	}

	orm := facades.Orm()
	if orm == nil {
		fail("test DB unavailable — Goravel ORM not initialized (set TEST_DB_REQUIRED=1 to fail instead of skip)")
		return
	}
	db, err := orm.DB()
	if err != nil {
		fail("test DB unavailable — cannot connect: %v (set TEST_DB_REQUIRED=1 to fail instead of skip)", err)
		return
	}
	if err := db.Ping(); err != nil {
		fail("test DB unavailable — ping failed: %v (set TEST_DB_REQUIRED=1 to fail instead of skip)", err)
		return
	}

	// Run migrations to set up schema
	if err := facades.Artisan().Call("migrate:fresh"); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	t.Cleanup(func() {
		// Clean up after test
		facades.Artisan().Call("migrate:fresh")
	})
}

// ---------------------------------------------------------------------------
// Fixtures — insert test data and return references
// ---------------------------------------------------------------------------

func InsertWallet(t *testing.T, chainID string) models.Wallet {
	t.Helper()
	return InsertWalletWithAccount(t, chainID, nil)
}

// InsertWalletWithAccount creates a wallet optionally bound to accountID.
// Pass a nil accountID to leave the wallet unassigned (legacy/test default).
func InsertWalletWithAccount(t *testing.T, chainID string, accountID *uuid.UUID) models.Wallet {
	t.Helper()
	walletID := uuid.New()
	addrID := uuid.New()
	addr := models.Address{
		ID:              addrID,
		WalletID:        walletID,
		Chain:           chainID,
		Address:         "0x" + uuid.NewString()[:16],
		DerivationIndex: 0,
		ExternalUserID:  "system",
		IsActive:        true,
		Label:           "Deposit Address",
	}
	if err := facades.Orm().Query().Create(&addr); err != nil {
		t.Fatalf("insert deposit address: %v", err)
	}
	w := models.Wallet{
		ID:               walletID,
		Chain:            chainID,
		Label:            chainID + " test wallet",
		MPCCustomerShare: "deadbeef",
		MPCShareIV:       "cafebabe",
		MPCShareSalt:     "feedface",
		MPCSecretARN:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		MPCPublicKey:     "02abc123def456",
		MPCCurve:         "secp256k1",
		DepositAddressID: &addrID,
		AccountID:        accountID,
	}
	if err := facades.Orm().Query().Create(&w); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}
	w.DepositAddress = &addr
	return w
}

// InsertAccount creates a minimal Account row for IDOR / multi-tenant tests.
func InsertAccount(t *testing.T, name string) models.Account {
	t.Helper()
	acc := models.Account{
		ID:          uuid.New(),
		Name:        name,
		Status:      "active",
		Environment: "prod",
	}
	if err := facades.Orm().Query().Create(&acc); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	return acc
}

func InsertAddress(t *testing.T, walletID uuid.UUID, chainID, address, userID string, index int) models.Address {
	t.Helper()
	a := models.Address{
		ID:              uuid.New(),
		WalletID:        walletID,
		Chain:           chainID,
		Address:         address,
		DerivationIndex: index,
		ExternalUserID:  userID,
		IsActive:        true,
	}
	if err := facades.Orm().Query().Create(&a); err != nil {
		t.Fatalf("insert address: %v", err)
	}
	return a
}

func InsertTransaction(t *testing.T, walletID uuid.UUID, addrID *uuid.UUID, chainID, txType, status, asset, amount string, blockNum int64) models.Transaction {
	t.Helper()
	txHash := "0xtesthash" + uuid.NewString()[:8]
	tx := models.Transaction{
		ID:             uuid.New(),
		AddressID:      addrID,
		WalletID:       walletID,
		ExternalUserID: "user_test",
		Chain:          chainID,
		TxType:         txType,
		TxHash:         txHash,
		ToAddress:      "0xtoaddr",
		Amount:         amount,
		Asset:          asset,
		Confirmations:  0,
		RequiredConfs:  3,
		Status:         status,
		BlockNumber:    blockNum,
	}
	if err := facades.Orm().Query().Create(&tx); err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	return tx
}

func InsertWebhookConfig(t *testing.T, url, secret string, events []string) models.WebhookConfig {
	t.Helper()
	cfg := models.WebhookConfig{
		ID:       uuid.New(),
		URL:      url,
		Secret:   secret,
		Events:   pgArray(events),
		IsActive: true,
	}
	if err := facades.Orm().Query().Create(&cfg); err != nil {
		t.Fatalf("insert webhook config: %v", err)
	}
	return cfg
}

func pgArray(arr []string) string {
	s := "{"
	for i, v := range arr {
		if i > 0 {
			s += ","
		}
		s += `"` + v + `"`
	}
	return s + "}"
}
