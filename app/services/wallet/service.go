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
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/chain"
	mpc "github.com/macromarkets/vault/app/services/mpc"
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

func (s *Service) CreateWallet(ctx context.Context, chainID, label, passphrase string) (*CreateWalletResult, error) {
	if len(passphrase) < 12 {
		return nil, fmt.Errorf("passphrase must be at least 12 characters")
	}

	// Normalise chain ID
	chainID = strings.ToLower(chainID)
	if chainID == "matic" {
		chainID = "polygon"
	}

	if _, err := s.registry.Chain(chainID); err != nil {
		return nil, fmt.Errorf("unknown chain: %s", chainID)
	}

	// Guard: one wallet per chain
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
	keygenResult, err := s.mpcService.Keygen(ctx, curve)
	if err != nil {
		return nil, fmt.Errorf("mpc keygen: %w", err)
	}

	// 2. Encrypt customer share (ShareA) with passphrase
	enc, err := mpc.EncryptShare(keygenResult.ShareA, passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypt share: %w", err)
	}

	// 3. Format EncryptedUserKey JSON
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

	// 4. Encrypt passphrase with service key (section D)
	encPasscode, err := mpc.EncryptWithServiceKey([]byte(passphrase), os.Getenv("WALLET_SERVICE_KEY"))
	if err != nil {
		return nil, fmt.Errorf("encrypt passcode: %w", err)
	}

	// 5. Generate 6-digit activation code
	n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return nil, fmt.Errorf("generate activation code: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())

	// 6. Store service share (ShareB) in Secrets Manager
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

	// All steps after CreateSecret: log orphaned ARN on any failure
	onPostSecretErr := func(err error) (*CreateWalletResult, error) {
		slog.Warn("orphaned secret ARN after wallet creation failure", "arn", secretARN, "error", err)
		return nil, err
	}

	// 7. Derive deposit address
	depositAddress, err := deriveAddress(chainID, keygenResult.CombinedPubKey)
	if err != nil {
		return onPostSecretErr(fmt.Errorf("derive address: %w", err))
	}

	// 8. Persist wallet
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
		DepositAddress:   depositAddress,
		Status:           "pending",
		ActivationCode:   &codeStr,
	}
	if err := facades.Orm().Query().Create(w); err != nil {
		return onPostSecretErr(fmt.Errorf("create wallet: %w", err))
	}

	// 9. Cache deposit address in Redis (non-fatal)
	if s.rdb != nil {
		if err := s.rdb.SAdd(ctx, "vault:addresses:"+chainID, depositAddress).Err(); err != nil {
			slog.Warn("redis cache failed", "error", err)
		}
	}

	return &CreateWalletResult{
		Wallet:            w,
		EncryptedUserKey:  string(ukJSON),
		ServicePublicKey:  hex.EncodeToString(keygenResult.CombinedPubKey),
		EncryptedPasscode: encPasscode,
		ActivationCode:    code,
	}, nil
}

// ActivateWallet validates the activation code and transitions the wallet to active.
func (s *Service) ActivateWallet(ctx context.Context, walletID uuid.UUID, code string) (*models.Wallet, error) {
	var w models.Wallet
	if err := facades.Orm().Query().Where("id", walletID).First(&w); err != nil {
		return nil, ErrWalletNotFound
	}
	if w.ID == uuid.Nil {
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

	if _, err := facades.Orm().Query().Model(&w).Where("id = ?", w.ID).Update(map[string]interface{}{
		"status":          "active",
		"activation_code": nil,
	}); err != nil {
		return nil, fmt.Errorf("activate wallet: %w", err)
	}
	w.Status = "active"
	w.ActivationCode = nil
	return &w, nil
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
