package mocks

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/macromarkets/vault/app/models"
)

// ---------------------------------------------------------------------------
// TestDB — sets up a real Postgres test database or skips.
// Uses TEST_DATABASE_URL env var. Each test gets a clean schema.
// ---------------------------------------------------------------------------

func TestDB(t *testing.T) *sqlx.DB {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://vault:vault@localhost:5432/vault_test?sslmode=disable"
	}

	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Skipf("skipping DB test — cannot connect: %v", err)
	}

	// Clean and recreate schema
	cleanSchema(t, db)
	createSchema(t, db)

	t.Cleanup(func() {
		cleanSchema(t, db)
		db.Close()
	})

	return db
}

func cleanSchema(t *testing.T, db *sqlx.DB) {
	t.Helper()
	tables := []string{"webhook_events", "webhook_configs", "transactions", "addresses", "withdrawal_policies", "withdrawal_whitelist", "wallets"}
	for _, table := range tables {
		db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", table))
	}
}

func createSchema(t *testing.T, db *sqlx.DB) {
	t.Helper()
	schema := `
	CREATE EXTENSION IF NOT EXISTS "pgcrypto";
	CREATE TABLE wallets (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(), chain VARCHAR(10) NOT NULL UNIQUE,
		label VARCHAR(100) DEFAULT '', master_pubkey TEXT NOT NULL, key_vault_ref VARCHAR(255) NOT NULL,
		derivation_path TEXT NOT NULL, address_index INTEGER DEFAULT 0, created_at TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE TABLE addresses (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(), wallet_id UUID NOT NULL REFERENCES wallets(id),
		chain VARCHAR(10) NOT NULL, address VARCHAR(255) NOT NULL, derivation_index INTEGER NOT NULL,
		external_user_id VARCHAR(255) NOT NULL, metadata JSONB DEFAULT '{}', is_active BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMPTZ DEFAULT NOW(), UNIQUE(chain, address), UNIQUE(wallet_id, derivation_index)
	);
	CREATE INDEX idx_addresses_user ON addresses(external_user_id);
	CREATE INDEX idx_addresses_chain_addr ON addresses(chain, address);
	CREATE TABLE transactions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(), address_id UUID REFERENCES addresses(id),
		wallet_id UUID NOT NULL REFERENCES wallets(id), external_user_id VARCHAR(255) DEFAULT '',
		chain VARCHAR(10) NOT NULL, tx_type VARCHAR(10) NOT NULL, tx_hash VARCHAR(255),
		from_address VARCHAR(255), to_address VARCHAR(255) NOT NULL, amount NUMERIC(36,18) NOT NULL,
		asset VARCHAR(20) NOT NULL, token_contract VARCHAR(255), confirmations INTEGER DEFAULT 0,
		required_confs INTEGER NOT NULL DEFAULT 1, status VARCHAR(20) NOT NULL DEFAULT 'pending',
		fee NUMERIC(36,18), block_number BIGINT, block_hash VARCHAR(255), error_message TEXT,
		idempotency_key VARCHAR(255) UNIQUE, created_at TIMESTAMPTZ DEFAULT NOW(), confirmed_at TIMESTAMPTZ
	);
	CREATE INDEX idx_tx_status ON transactions(status);
	CREATE INDEX idx_tx_chain_hash ON transactions(chain, tx_hash);
	CREATE INDEX idx_tx_user ON transactions(external_user_id);
	CREATE TABLE webhook_configs (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(), url VARCHAR(500) NOT NULL,
		secret VARCHAR(255) NOT NULL, events TEXT[] NOT NULL, is_active BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE TABLE webhook_events (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(), transaction_id UUID NOT NULL REFERENCES transactions(id),
		event_type VARCHAR(50) NOT NULL, payload JSONB NOT NULL, delivery_url VARCHAR(500) NOT NULL,
		delivery_status VARCHAR(20) DEFAULT 'pending', attempts INTEGER DEFAULT 0, max_attempts INTEGER DEFAULT 10,
		next_retry_at TIMESTAMPTZ, last_error TEXT, delivered_at TIMESTAMPTZ, created_at TIMESTAMPTZ DEFAULT NOW()
	);`

	_, err := db.Exec(schema)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Fixtures — insert test data and return references
// ---------------------------------------------------------------------------

func InsertWallet(t *testing.T, db *sqlx.DB, chainID string) models.Wallet {
	t.Helper()
	w := models.Wallet{
		ID: uuid.New(), Chain: chainID, Label: chainID + " test wallet",
		MasterPubkey: "xpub-test", KeyVaultRef: "kms://test",
		DerivationPath: "m/44'/60'/0'/0", AddressIndex: 0, CreatedAt: time.Now().UTC(),
	}
	_, err := db.NamedExec(`
		INSERT INTO wallets (id, chain, label, master_pubkey, key_vault_ref, derivation_path, address_index, created_at)
		VALUES (:id, :chain, :label, :master_pubkey, :key_vault_ref, :derivation_path, :address_index, :created_at)`, &w)
	if err != nil {
		t.Fatalf("insert wallet: %v", err)
	}
	return w
}

func InsertAddress(t *testing.T, db *sqlx.DB, walletID uuid.UUID, chainID, address, userID string, index int) models.Address {
	t.Helper()
	a := models.Address{
		ID: uuid.New(), WalletID: walletID, Chain: chainID, Address: address,
		DerivationIndex: index, ExternalUserID: userID, IsActive: true, CreatedAt: time.Now().UTC(),
	}
	_, err := db.NamedExec(`
		INSERT INTO addresses (id, wallet_id, chain, address, derivation_index, external_user_id, is_active, created_at)
		VALUES (:id, :wallet_id, :chain, :address, :derivation_index, :external_user_id, :is_active, :created_at)`, &a)
	if err != nil {
		t.Fatalf("insert address: %v", err)
	}
	return a
}

func InsertTransaction(t *testing.T, db *sqlx.DB, walletID uuid.UUID, addrID *uuid.UUID, chainID, txType, status, asset, amount string, blockNum int64) models.Transaction {
	t.Helper()
	tx := models.Transaction{
		ID: uuid.New(), AddressID: addrID, WalletID: walletID,
		ExternalUserID: "user_test", Chain: chainID, TxType: txType,
		TxHash:    sql.NullString{String: "0xtesthash" + uuid.NewString()[:8], Valid: true},
		ToAddress: "0xtoaddr", Amount: amount, Asset: asset,
		Confirmations: 0, RequiredConfs: 3, Status: status,
		BlockNumber: sql.NullInt64{Int64: blockNum, Valid: blockNum > 0},
		CreatedAt:   time.Now().UTC(),
	}
	_, err := db.NamedExec(`
		INSERT INTO transactions (id, address_id, wallet_id, external_user_id, chain, tx_type, tx_hash,
			to_address, amount, asset, confirmations, required_confs, status, block_number, created_at)
		VALUES (:id, :address_id, :wallet_id, :external_user_id, :chain, :tx_type, :tx_hash,
			:to_address, :amount, :asset, :confirmations, :required_confs, :status, :block_number, :created_at)`, &tx)
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	return tx
}

func InsertWebhookConfig(t *testing.T, db *sqlx.DB, url, secret string, events []string) models.WebhookConfig {
	t.Helper()
	cfg := models.WebhookConfig{
		ID: uuid.New(), URL: url, Secret: secret,
		Events: pgArray(events), IsActive: true, CreatedAt: time.Now().UTC(),
	}
	_, err := db.Exec(`INSERT INTO webhook_configs (id, url, secret, events, is_active, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		cfg.ID, cfg.URL, cfg.Secret, cfg.Events, cfg.IsActive, cfg.CreatedAt)
	if err != nil {
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
