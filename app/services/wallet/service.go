package wallet

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/chain"
	mpc "github.com/macromarkets/vault/app/services/mpc"
)

// secretsManagerAPI is a subset of secretsmanager.Client used by the wallet service,
// defined as an interface to allow test mocking.
type secretsManagerAPI interface {
	CreateSecret(ctx context.Context, input *secretsmanager.CreateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error)
}

type Service struct {
	registry       *chain.Registry
	rdb            *redis.Client // optional: address cache
	mpcService     mpc.Service
	secretsManager secretsManagerAPI
}

func NewService(registry *chain.Registry, rdb *redis.Client, mpcSvc mpc.Service, sm *secretsmanager.Client) *Service {
	return &Service{
		registry:       registry,
		rdb:            rdb,
		mpcService:     mpcSvc,
		secretsManager: sm,
	}
}

func (s *Service) CreateWallet(ctx context.Context, chainID, label, passphrase string) (*models.Wallet, error) {
	if len(passphrase) < 12 {
		return nil, fmt.Errorf("passphrase must be at least 12 characters")
	}
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

	curve := curveForChain(chainID)

	// 1. MPC keygen
	result, err := s.mpcService.Keygen(ctx, curve)
	if err != nil {
		return nil, fmt.Errorf("mpc keygen: %w", err)
	}

	// 2. Encrypt customer share with passphrase
	enc, err := mpc.EncryptShare(result.ShareA, passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypt share: %w", err)
	}

	// 3. Store service share in AWS Secrets Manager
	walletID := uuid.New()
	secretName := fmt.Sprintf("vault/wallet/%s/share-b", walletID.String())
	out, err := s.secretsManager.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(secretName),
		SecretBinary: result.ShareB,
	})
	if err != nil {
		return nil, fmt.Errorf("store service share: %w", err)
	}
	secretARN := aws.ToString(out.ARN)

	// 4. Derive deposit address from combined public key
	depositAddress, err := deriveAddress(chainID, result.CombinedPubKey)
	if err != nil {
		return nil, fmt.Errorf("derive address: %w", err)
	}

	w := &models.Wallet{
		ID:               walletID,
		Chain:            chainID,
		Label:            label,
		MPCCustomerShare: hex.EncodeToString(enc.Ciphertext),
		MPCShareIV:       hex.EncodeToString(enc.IV),
		MPCShareSalt:     hex.EncodeToString(enc.Salt),
		MPCSecretARN:     secretARN,
		MPCPublicKey:     hex.EncodeToString(result.CombinedPubKey),
		MPCCurve:         string(curve),
		DepositAddress:   depositAddress,
	}

	if err := facades.Orm().Query().Create(w); err != nil {
		return nil, err
	}

	// Cache deposit address in Redis for fast lookup
	if s.rdb != nil {
		s.rdb.SAdd(ctx, "vault:addresses:"+chainID, depositAddress)
	}

	return w, nil
}

func curveForChain(chainID string) mpc.Curve {
	if chainID == "sol" {
		return mpc.CurveEd25519
	}
	return mpc.CurveSecp256k1
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

// GenerateAddress is not supported for MPC wallets in v1.
func (s *Service) GenerateAddress(ctx context.Context, walletID uuid.UUID, externalUserID, metadata string) (*models.Address, error) {
	return nil, fmt.Errorf("address derivation not supported for MPC wallets in v1")
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
