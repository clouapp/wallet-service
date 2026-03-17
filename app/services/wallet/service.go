package wallet

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/chain"
)

type Service struct {
	db       *sqlx.DB
	registry *chain.Registry
	rdb      *redis.Client // optional: address cache
}

func NewService(db *sqlx.DB, registry *chain.Registry, rdb *redis.Client) *Service {
	return &Service{db: db, registry: registry, rdb: rdb}
}

func (s *Service) CreateWallet(ctx context.Context, chainID, label string) (*models.Wallet, error) {
	if _, err := s.registry.Chain(chainID); err != nil {
		return nil, fmt.Errorf("unknown chain: %s", chainID)
	}

	var count int
	if err := s.db.GetContext(ctx, &count, "SELECT COUNT(*) FROM wallets WHERE chain = $1", chainID); err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, fmt.Errorf("wallet for chain %s already exists", chainID)
	}

	w := &models.Wallet{
		ID: uuid.New(), Chain: chainID, Label: label,
		MasterPubkey: "xpub-placeholder", KeyVaultRef: "kms://master-key",
		DerivationPath: derivationPath(chainID), AddressIndex: 0,
		CreatedAt: time.Now().UTC(),
	}

	_, err := s.db.NamedExecContext(ctx, `
		INSERT INTO wallets (id, chain, label, master_pubkey, key_vault_ref, derivation_path, address_index, created_at)
		VALUES (:id, :chain, :label, :master_pubkey, :key_vault_ref, :derivation_path, :address_index, :created_at)`, w)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (s *Service) GetWallet(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	var w models.Wallet
	return &w, s.db.GetContext(ctx, &w, "SELECT * FROM wallets WHERE id = $1", id)
}

func (s *Service) ListWallets(ctx context.Context) ([]models.Wallet, error) {
	var ws []models.Wallet
	return ws, s.db.SelectContext(ctx, &ws, "SELECT * FROM wallets ORDER BY created_at")
}

// GenerateAddress derives a new deposit address for an external user.
// Atomic: locks wallet row, increments index, inserts address.
func (s *Service) GenerateAddress(ctx context.Context, walletID uuid.UUID, externalUserID, metadata string) (*models.Address, error) {
	w, err := s.GetWallet(ctx, walletID)
	if err != nil {
		return nil, err
	}
	adapter, err := s.registry.Chain(w.Chain)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var idx int
	if err := tx.GetContext(ctx, &idx, "SELECT address_index FROM wallets WHERE id = $1 FOR UPDATE", walletID); err != nil {
		return nil, err
	}

	masterKey := []byte("placeholder-master-key") // TODO: KMS
	address, err := adapter.DeriveAddress(masterKey, uint32(idx))
	if err != nil {
		return nil, err
	}

	addr := &models.Address{
		ID: uuid.New(), WalletID: walletID, Chain: w.Chain,
		Address: address, DerivationIndex: idx, ExternalUserID: externalUserID,
		Metadata: sql.NullString{String: metadata, Valid: metadata != ""},
		IsActive: true, CreatedAt: time.Now().UTC(),
	}

	if _, err := tx.NamedExecContext(ctx, `
		INSERT INTO addresses (id, wallet_id, chain, address, derivation_index, external_user_id, metadata, is_active, created_at)
		VALUES (:id, :wallet_id, :chain, :address, :derivation_index, :external_user_id, :metadata, :is_active, :created_at)`, addr); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, "UPDATE wallets SET address_index = address_index + 1 WHERE id = $1", walletID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Cache in Redis for fast deposit matching
	if s.rdb != nil {
		s.rdb.SAdd(ctx, "vault:addresses:"+w.Chain, address)
	}

	return addr, nil
}

func (s *Service) LookupAddress(ctx context.Context, chainID, address string) (*models.Address, error) {
	var addr models.Address
	return &addr, s.db.GetContext(ctx, &addr, "SELECT * FROM addresses WHERE chain = $1 AND address = $2", chainID, address)
}

func (s *Service) ListUserAddresses(ctx context.Context, externalUserID string) ([]models.Address, error) {
	var addrs []models.Address
	return addrs, s.db.SelectContext(ctx, &addrs, "SELECT * FROM addresses WHERE external_user_id = $1 ORDER BY created_at", externalUserID)
}

func (s *Service) ListWalletAddresses(ctx context.Context, walletID uuid.UUID) ([]models.Address, error) {
	var addrs []models.Address
	return addrs, s.db.SelectContext(ctx, &addrs, "SELECT * FROM addresses WHERE wallet_id = $1 ORDER BY derivation_index", walletID)
}

func derivationPath(chainID string) string {
	switch chainID {
	case "btc":
		return "m/84'/0'/0'/0"
	case "eth", "polygon":
		return "m/44'/60'/0'/0"
	case "sol":
		return "m/44'/501'"
	default:
		return "m/44'/0'/0'/0"
	}
}
