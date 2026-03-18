package wallet

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/chain"
)

type Service struct {
	registry *chain.Registry
	rdb      *redis.Client // optional: address cache
}

func NewService(registry *chain.Registry, rdb *redis.Client) *Service {
	return &Service{registry: registry, rdb: rdb}
}

func (s *Service) CreateWallet(ctx context.Context, chainID, label string) (*models.Wallet, error) {
	if _, err := s.registry.Chain(chainID); err != nil {
		return nil, fmt.Errorf("unknown chain: %s", chainID)
	}

	// Check if wallet already exists for this chain
	var count int64
	count, err := facades.Orm().Query().Model(&models.Wallet{}).Where("chain", chainID).Count()
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, fmt.Errorf("wallet for chain %s already exists", chainID)
	}

	w := &models.Wallet{
		ID:             uuid.New(),
		Chain:          chainID,
		Label:          label,
		MasterPubkey:   "xpub-placeholder",
		KeyVaultRef:    "kms://master-key",
		DerivationPath: derivationPath(chainID),
		AddressIndex:   0,
	}

	if err := facades.Orm().Query().Create(w); err != nil {
		return nil, err
	}
	return w, nil
}

func (s *Service) GetWallet(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	var w models.Wallet
	if err := facades.Orm().Query().Find(&w, id); err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Service) ListWallets(ctx context.Context) ([]models.Wallet, error) {
	var ws []models.Wallet
	if err := facades.Orm().Query().Order("created_at").Find(&ws); err != nil {
		return nil, err
	}
	return ws, nil
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

	var addr *models.Address

	// Use transaction for atomic operation
	err = facades.Orm().Transaction(func(tx orm.Query) error {
		// Lock wallet row and get current index
		var wallet models.Wallet
		if err := tx.LockForUpdate().Find(&wallet, walletID); err != nil {
			return err
		}
		idx := wallet.AddressIndex

		// Derive new address
		masterKey := []byte("placeholder-master-key") // TODO: KMS
		address, err := adapter.DeriveAddress(masterKey, uint32(idx))
		if err != nil {
			return err
		}

		// Create address record
		addr = &models.Address{
			ID:              uuid.New(),
			WalletID:        walletID,
			Chain:           w.Chain,
			Address:         address,
			DerivationIndex: idx,
			ExternalUserID:  externalUserID,
			Metadata:        metadata,
			IsActive:        true,
		}

		if err := tx.Create(addr); err != nil {
			return err
		}

		// Increment address index
		if _, err := tx.Model(&models.Wallet{}).Where("id", walletID).Update("address_index", idx+1); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Cache in Redis for fast deposit matching
	if s.rdb != nil {
		s.rdb.SAdd(ctx, "vault:addresses:"+w.Chain, addr.Address)
	}

	return addr, nil
}

func (s *Service) LookupAddress(ctx context.Context, chainID, address string) (*models.Address, error) {
	var addr models.Address
	if err := facades.Orm().Query().Where("chain", chainID).Where("address", address).First(&addr); err != nil {
		return nil, err
	}
	return &addr, nil
}

func (s *Service) ListUserAddresses(ctx context.Context, externalUserID string) ([]models.Address, error) {
	var addrs []models.Address
	if err := facades.Orm().Query().Where("external_user_id", externalUserID).Order("created_at").Find(&addrs); err != nil {
		return nil, err
	}
	return addrs, nil
}

func (s *Service) ListWalletAddresses(ctx context.Context, walletID uuid.UUID) ([]models.Address, error) {
	var addrs []models.Address
	if err := facades.Orm().Query().Where("wallet_id", walletID).Order("derivation_index").Find(&addrs); err != nil {
		return nil, err
	}
	return addrs, nil
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
