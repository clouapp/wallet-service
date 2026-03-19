package wallet

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/chain"
	mpc "github.com/macromarkets/vault/app/services/mpc"
)

type Service struct {
	registry       *chain.Registry
	rdb            *redis.Client // optional: address cache
	mpcService     mpc.Service
	secretsManager *secretsmanager.Client
}

func NewService(registry *chain.Registry, rdb *redis.Client, mpcSvc mpc.Service, sm *secretsmanager.Client) *Service {
	return &Service{
		registry:       registry,
		rdb:            rdb,
		mpcService:     mpcSvc,
		secretsManager: sm,
	}
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
		ID:     uuid.New(),
		Chain:  chainID,
		Label:  label,
		// TODO: Task 9 will implement MPC wallet creation
		// MPC fields will be set during wallet creation with MPC ceremony
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
// TODO: Task 9 will implement MPC address generation
func (s *Service) GenerateAddress(ctx context.Context, walletID uuid.UUID, externalUserID, metadata string) (*models.Address, error) {
	// Placeholder: Task 9 will implement MPC address generation
	return nil, fmt.Errorf("MPC address generation not yet implemented")
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

// TODO: derivationPath is no longer used with MPC wallets. Removed in Task 6.
