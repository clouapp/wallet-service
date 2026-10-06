package mocks

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/testenv"
)

// ---------------------------------------------------------------------------
// TestDB — sets up Goravel ORM with test database
// Uses the pre-boot .env.testing connection. Each test gets empty tables.
// ---------------------------------------------------------------------------

// migrationsTable keeps the applied migrations when the tables are emptied.
const migrationsTable = "migrations"

// freshSchemaOnce migrates the database fresh once per test binary outside worker
// mode; a worker clone is already a copy of the freshly migrated template.
var (
	freshSchemaOnce sync.Once
	freshSchemaErr  error
)

// TestDB gives the test empty tables on a migrated schema: the schema is migrated
// fresh once per test binary, then every table but migrations is truncated before
// and after each test. Assumes Goravel is already booted via TestMain. Tests that
// change the schema itself use TestDBFreshSchema.
func TestDB(t *testing.T) {
	t.Helper()

	connectTestDB(t)
	freshSchemaOnce.Do(func() {
		if !testenv.WorkerMode() {
			freshSchemaErr = facades.Artisan().Call("migrate:fresh")
		}
	})
	if freshSchemaErr != nil {
		t.Fatalf("migration failed: %v", freshSchemaErr)
	}
	truncateTables(t)
	t.Cleanup(func() {
		requireSafeTestDatabase(t)
		truncateTables(t)
	})
}

// TestDBFreshSchema migrates the schema fresh before and after the test, for tests
// that run migrations up and down.
func TestDBFreshSchema(t *testing.T) {
	t.Helper()

	connectTestDB(t)
	if err := facades.Artisan().Call("migrate:fresh"); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	t.Cleanup(func() {
		requireSafeTestDatabase(t)
		if err := facades.Artisan().Call("migrate:fresh"); err != nil {
			t.Errorf("test database cleanup migration failed: %v", err)
		}
	})
}

// emptyTablesStatement deletes every row of the current schema but the migrations
// table and restarts the sequences those tables own, in one round trip. Foreign keys
// are not checked while it runs (session_replication_role, superuser only), so the
// order does not matter. DELETE on these small tables takes milliseconds where
// TRUNCATE, which rewrites and fsyncs every table, takes ~0.5 s per test.
const emptyTablesStatement = `DO $$
DECLARE r record;
BEGIN
	PERFORM set_config('session_replication_role', 'replica', true);
	FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = current_schema() AND tablename <> '` + migrationsTable + `' LOOP
		EXECUTE format('DELETE FROM %I', r.tablename);
	END LOOP;
	FOR r IN SELECT s.relname FROM pg_class s
		JOIN pg_namespace n ON n.oid = s.relnamespace
		JOIN pg_depend d ON d.objid = s.oid AND d.classid = 'pg_class'::regclass AND d.refclassid = 'pg_class'::regclass
		JOIN pg_class owner ON owner.oid = d.refobjid
		WHERE s.relkind = 'S' AND n.nspname = current_schema() AND owner.relname <> '` + migrationsTable + `' LOOP
		EXECUTE format('ALTER SEQUENCE %I RESTART', r.relname);
	END LOOP;
END $$`

// truncateTables empties every table of the current schema except migrations and
// restarts their sequences; without superuser rights it falls back to TRUNCATE.
func truncateTables(t *testing.T) {
	t.Helper()

	if _, err := facades.Orm().Query().Exec(emptyTablesStatement); err == nil {
		return
	}
	var tables []string
	if err := facades.Orm().Query().Raw(
		`SELECT quote_ident(tablename) FROM pg_tables WHERE schemaname = current_schema() AND tablename <> ? ORDER BY tablename`,
		migrationsTable,
	).Scan(&tables); err != nil {
		t.Fatalf("list test tables: %v", err)
	}
	if len(tables) == 0 {
		return
	}
	if _, err := facades.Orm().Query().Exec("TRUNCATE TABLE " + strings.Join(tables, ", ") + " RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate test tables: %v", err)
	}
}

func connectTestDB(t *testing.T) {
	t.Helper()

	requireSafeTestDatabase(t)

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
}

func requireSafeTestDatabase(t *testing.T) {
	t.Helper()

	configuration := testenv.Configuration{
		AppEnvironment: os.Getenv("APP_ENV"),
		DatabaseName:   facades.Config().GetString("database.connections.postgres.database"),
	}
	if err := testenv.ValidateConfiguration(configuration); err != nil {
		t.Fatalf("%v", err)
	}
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
	// The wallet goes first: addresses.wallet_id references wallets, while
	// wallets.deposit_address_id carries no foreign key.
	if err := facades.Orm().Query().Create(&w); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}
	if err := facades.Orm().Query().Create(&addr); err != nil {
		t.Fatalf("insert deposit address: %v", err)
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

// InsertUser creates an active User row, for rows that reference users
// (account_users, tokens, recovery codes).
func InsertUser(t *testing.T) models.User {
	t.Helper()
	id := uuid.New()
	user := models.User{
		ID:           id,
		Email:        id.String() + "@example.com",
		PasswordHash: "hash",
		Status:       "active",
		Preferences:  &models.UserPreferences{},
	}
	if err := facades.Orm().Query().Create(&user); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return user
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
		Direction:      directionForTxType(txType),
		Source:         models.TxSourceChain,
		RawPayload:     emptyJSONObject,
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

const emptyJSONObject = "{}"

func directionForTxType(txType string) string {
	switch txType {
	case models.TxTypeDeposit:
		return models.TxDirectionInbound
	case models.TxTypeWithdrawal:
		return models.TxDirectionOutbound
	default:
		return models.TxDirectionUnknown
	}
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

// InsertScopedWebhookConfig inserts a config owned by an account and/or limited to a
// wallet; nil for both is a legacy config without an owner.
func InsertScopedWebhookConfig(t *testing.T, url, secret string, events []string, accountID, walletID *uuid.UUID) models.WebhookConfig {
	t.Helper()
	cfg := models.WebhookConfig{
		ID:        uuid.New(),
		URL:       url,
		Secret:    secret,
		Events:    pgArray(events),
		IsActive:  true,
		AccountID: accountID,
		WalletID:  walletID,
	}
	if err := facades.Orm().Query().Create(&cfg); err != nil {
		t.Fatalf("insert scoped webhook config: %v", err)
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
