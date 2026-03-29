package wallet

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	mpc "github.com/macrowallets/waas/app/services/mpc"
)

var (
	ErrWalletNotFound        = errors.New("wallet not found")
	ErrWalletAlreadyActive   = errors.New("wallet is not pending activation")
	ErrInvalidActivationCode = errors.New("invalid activation code")
)

// CreateWalletResult holds the wallet record plus one-time KeyCard data.
type CreateWalletResult struct {
	Wallet            *models.Wallet
	EncryptedUserKey  string // JSON {iv,salt,ct,cipher,kdf} — AES-256-GCM/Argon2id, base64
	ServicePublicKey  string // hex of CombinedPubKey
	EncryptedPasscode string // JSON {iv,ct,cipher} — AES-256-GCM with service key, base64
	ActivationCode    string // 6-digit zero-padded decimal
}

// secretsManagerAPI is a subset of secretsmanager.Client used by the wallet service,
// defined as an interface to allow test mocking.
type secretsManagerAPI interface {
	CreateSecret(ctx context.Context, input *secretsmanager.CreateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error)
}

type webhookAddressSyncer interface {
	SyncChainAddresses(ctx context.Context, chainID string) error
}

type Service struct {
	registry       *chain.Registry
	rdb            *redis.Client
	mpcService     mpc.Service
	secretsManager secretsManagerAPI
	walletRepo     repositories.WalletRepository
	addressRepo    repositories.AddressRepository
	webhookSyncSvc webhookAddressSyncer
}

func NewService(registry *chain.Registry, rdb *redis.Client, mpcSvc mpc.Service, sm *secretsmanager.Client, walletRepo repositories.WalletRepository, addressRepo repositories.AddressRepository) *Service {
	return &Service{
		registry:       registry,
		rdb:            rdb,
		mpcService:     mpcSvc,
		secretsManager: sm,
		walletRepo:     walletRepo,
		addressRepo:    addressRepo,
	}
}

func (s *Service) SetWebhookSync(syncSvc webhookAddressSyncer) {
	s.webhookSyncSvc = syncSvc
}

func (s *Service) CreateWallet(ctx context.Context, accountID uuid.UUID, chainID, label, passphrase string) (*CreateWalletResult, error) {
	if accountID == uuid.Nil {
		return nil, fmt.Errorf("account_id is required")
	}
	if len(passphrase) < 12 {
		return nil, fmt.Errorf("passphrase must be at least 12 characters")
	}

	chainID = strings.ToLower(chainID)
	if chainID == models.ChainMatic {
		chainID = models.ChainPolygon
	}

	if _, err := s.registry.Chain(chainID); err != nil {
		return nil, fmt.Errorf("unknown chain: %s", chainID)
	}

	curve := curveForChain(chainID)

	keygenResult, err := s.mpcService.Keygen(ctx, curve)
	if err != nil {
		return nil, fmt.Errorf("mpc keygen: %w", err)
	}

	enc, err := mpc.EncryptShare(keygenResult.ShareA, passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypt share: %w", err)
	}

	type userKeyPayload struct {
		IV     string `json:"iv"`
		Salt   string `json:"salt"`
		CT     string `json:"ct"`
		Cipher string `json:"cipher"`
		KDF    string `json:"kdf"`
	}
	ukp := userKeyPayload{
		IV:     base64.StdEncoding.EncodeToString(enc.IV),
		Salt:   base64.StdEncoding.EncodeToString(enc.Salt),
		CT:     base64.StdEncoding.EncodeToString(enc.Ciphertext),
		Cipher: "aes-256-gcm",
		KDF:    "argon2id",
	}
	ukJSON, err := json.Marshal(ukp)
	if err != nil {
		return nil, fmt.Errorf("marshal user key: %w", err)
	}

	encPasscode, err := mpc.EncryptWithServiceKey([]byte(passphrase), os.Getenv("WALLET_SERVICE_KEY"))
	if err != nil {
		return nil, fmt.Errorf("encrypt passcode: %w", err)
	}

	n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return nil, fmt.Errorf("generate activation code: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())

	walletID := uuid.New()
	secretName := fmt.Sprintf("vault/wallet/%s/share-b", walletID.String())
	out, err := s.secretsManager.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(secretName),
		SecretBinary: keygenResult.ShareB,
	})
	if err != nil {
		return nil, fmt.Errorf("store service share: %w", err)
	}
	secretARN := aws.ToString(out.ARN)

	onPostSecretErr := func(err error) (*CreateWalletResult, error) {
		slog.Warn("orphaned secret ARN after wallet creation failure", "arn", secretARN, "error", err)
		return nil, err
	}

	depositAddressStr, err := deriveAddress(chainID, keygenResult.CombinedPubKey)
	if err != nil {
		return onPostSecretErr(fmt.Errorf("derive address: %w", err))
	}

	codeStr := code
	w := &models.Wallet{
		ID:               walletID,
		Chain:            chainID,
		Label:            label,
		MPCCustomerShare: hex.EncodeToString(enc.Ciphertext),
		MPCShareIV:       hex.EncodeToString(enc.IV),
		MPCShareSalt:     hex.EncodeToString(enc.Salt),
		MPCSecretARN:     secretARN,
		MPCPublicKey:     hex.EncodeToString(keygenResult.CombinedPubKey),
		MPCCurve:         string(curve),
		AccountID:        &accountID,
		Status:           "pending",
		ActivationCode:   &codeStr,
	}
	if err := s.walletRepo.Create(w); err != nil {
		return onPostSecretErr(fmt.Errorf("create wallet: %w", err))
	}

	addressID := uuid.New()
	addr := &models.Address{
		ID:              addressID,
		WalletID:        walletID,
		Chain:           chainID,
		Address:         depositAddressStr,
		DerivationIndex: 0,
		ExternalUserID:  "system",
		IsActive:        true,
		Label:           "Deposit Address",
	}
	if err := s.addressRepo.Create(addr); err != nil {
		return onPostSecretErr(fmt.Errorf("create deposit address: %w", err))
	}

	if err := s.walletRepo.UpdateField(walletID, "deposit_address_id", addressID); err != nil {
		return onPostSecretErr(fmt.Errorf("link deposit address: %w", err))
	}
	w.DepositAddressID = &addressID
	w.DepositAddress = addr

	if s.rdb != nil {
		if err := s.rdb.SAdd(ctx, "vault:addresses:"+chainID, depositAddressStr).Err(); err != nil {
			slog.Warn("redis cache failed", "error", err)
		}
	}

	if s.webhookSyncSvc != nil {
		go func() {
			syncCtx := context.Background()
			if err := s.webhookSyncSvc.SyncChainAddresses(syncCtx, chainID); err != nil {
				slog.Error("webhook address sync failed", "chain", chainID, "error", err)
			}
		}()
	}

	return &CreateWalletResult{
		Wallet:            w,
		EncryptedUserKey:  string(ukJSON),
		ServicePublicKey:  hex.EncodeToString(keygenResult.CombinedPubKey),
		EncryptedPasscode: encPasscode,
		ActivationCode:    code,
	}, nil
}

func (s *Service) ActivateWallet(ctx context.Context, walletID uuid.UUID, code string) (*models.Wallet, error) {
	w, err := s.walletRepo.FindByID(walletID)
	if err != nil || w == nil {
		return nil, ErrWalletNotFound
	}
	if w.Status != "pending" {
		return nil, ErrWalletAlreadyActive
	}
	if w.ActivationCode == nil {
		return nil, ErrWalletNotFound
	}
	if subtle.ConstantTimeCompare([]byte(*w.ActivationCode), []byte(code)) != 1 {
		return nil, ErrInvalidActivationCode
	}

	if err := s.walletRepo.UpdateFields(w.ID, map[string]interface{}{
		"status":          "active",
		"activation_code": nil,
	}); err != nil {
		return nil, fmt.Errorf("activate wallet: %w", err)
	}
	w.Status = "active"
	w.ActivationCode = nil
	return w, nil
}

func curveForChain(chainID string) mpc.Curve {
	if chainID == models.ChainSOL || chainID == models.ChainTSOL {
		return mpc.CurveEd25519
	}
	return mpc.CurveSecp256k1
}

func (s *Service) GetWallet(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	w, err := s.walletRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (s *Service) ListWallets(ctx context.Context) ([]models.Wallet, error) {
	return s.walletRepo.FindAll()
}

// GenerateAddress is not supported for MPC wallets in v1.
func (s *Service) GenerateAddress(ctx context.Context, walletID uuid.UUID, externalUserID, metadata string) (*models.Address, error) {
	return nil, fmt.Errorf("address derivation not supported for MPC wallets in v1")
}

func (s *Service) LookupAddress(ctx context.Context, chainID, address string) (*models.Address, error) {
	return s.addressRepo.FindByChainAndAddress(chainID, address)
}

func (s *Service) ListUserAddresses(ctx context.Context, externalUserID string) ([]models.Address, error) {
	return s.addressRepo.FindByExternalUserID(externalUserID)
}

func (s *Service) ListWalletAddresses(ctx context.Context, walletID uuid.UUID) ([]models.Address, error) {
	return s.addressRepo.FindByWalletID(walletID)
}
