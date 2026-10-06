package wallet

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/event"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/dtos"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chainregistry"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/mpcshare"
	"github.com/macrowallets/waas/pkg/types"
)

var (
	ErrWalletNotFound        = errors.New("wallet not found")
	ErrWalletAlreadyActive   = errors.New("wallet is not pending activation")
	ErrInvalidActivationCode = errors.New("invalid activation code")
)

// CreateWalletResult holds the wallet record, the combined public key, and the
// one-time activation code. The customer share, the passphrase, and the service
// share stay off this result.
type CreateWalletResult struct {
	Wallet           *models.Wallet
	ServicePublicKey string // hex of CombinedPubKey
	ActivationCode   string // 6-digit zero-padded decimal
}

// SecretStore stores and loads the service share. The provider supplies it;
// this package never imports the AWS SDK. A nil SecretStore means Secrets
// Manager is not configured. The service keeps the secret name and the bytes.
type SecretStore interface {
	Create(ctx context.Context, name string, secretBinary []byte) (arn string, err error)
	Binary(ctx context.Context, secretID string) ([]byte, error)
}

type webhookAddressSyncer interface {
	SyncChainAddresses(ctx context.Context, chainID string) error
}

// AddressCache adds one watched address with SADD. The provider supplies it;
// this package never imports the Redis client. A nil AddressCache means Redis
// is not configured. The service keeps the key.
type AddressCache interface {
	SAdd(ctx context.Context, key string, members ...any) error
}

// WalletStore is the wallet persistence this service uses.
type WalletStore interface {
	Create(ctx context.Context, wallet *models.Wallet) error
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
	FindAll(ctx context.Context) ([]models.Wallet, error)
	IncrementAddressIndex(ctx context.Context, id uuid.UUID) (int, error)
	SetDepositAddressID(ctx context.Context, id, addressID uuid.UUID) error
	SetMPCChainCode(ctx context.Context, id uuid.UUID, chainCode string) error
	Activate(ctx context.Context, id uuid.UUID, status string) error
}

// AddressStore is the address persistence this service uses.
type AddressStore interface {
	Create(ctx context.Context, addr *models.Address) error
	FindByID(ctx context.Context, id uuid.UUID) (*models.Address, error)
	SetLabel(ctx context.Context, id uuid.UUID, label string) error
	SetExternalUserID(ctx context.Context, id uuid.UUID, externalUserID string) error
	FindByChainAndAddress(ctx context.Context, chainID, address string) (*models.Address, error)
	FindByChainAndAddressAndAccount(ctx context.Context, chainID, address string, accountID uuid.UUID) (*models.Address, error)
	FindByExternalUserID(ctx context.Context, externalUserID string) ([]models.Address, error)
	FindByExternalUserIDAndAccount(ctx context.Context, externalUserID string, accountID uuid.UUID) ([]models.Address, error)
	FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.Address, error)
}

// chainLookup is the registered adapter wallet creation and address derivation read.
type chainLookup interface {
	Chain(id string) (types.Chain, error)
}

// mpcKeygen creates a wallet key and reconstructs the ed25519 master key child addresses derive from.
type mpcKeygen interface {
	Keygen(ctx context.Context, curve mpc.Curve) (*mpc.KeygenResult, error)
	ReconstructEd25519PrivateKey(shareA, shareB []byte) ([]byte, error)
}

// Deps is everything the wallet service needs. WebhookSync stays nil when unused.
type Deps struct {
	Registry     chainLookup
	AddressCache AddressCache
	MPC          mpcKeygen
	Secrets      SecretStore
	Wallets      WalletStore
	Addresses    AddressStore
	WebhookSync  webhookAddressSyncer
}

type Service struct {
	registry       chainLookup
	addresses      AddressCache
	mpcService     mpcKeygen
	secretsManager SecretStore
	walletRepo     WalletStore
	addressRepo    AddressStore
	webhookSyncSvc webhookAddressSyncer
}

// NewService builds a wallet service from Deps.
func NewService(deps Deps) *Service {
	return &Service{
		registry:       deps.Registry,
		addresses:      deps.AddressCache,
		mpcService:     deps.MPC,
		secretsManager: deps.Secrets,
		walletRepo:     deps.Wallets,
		addressRepo:    deps.Addresses,
		webhookSyncSvc: deps.WebhookSync,
	}
}

// cacheAddress records address under vault:addresses:<chain>. A nil cache is
// the same no-op as a nil Redis client. The Redis error is only logged.
func (s *Service) cacheAddress(ctx context.Context, chainID, address string) {
	if s.addresses == nil {
		return
	}
	if err := s.addresses.SAdd(ctx, "vault:addresses:"+chainID, address); err != nil {
		slog.Warn("redis cache failed", "error", err)
	}
}

func (s *Service) CreateWallet(ctx context.Context, accountID uuid.UUID, chainID, label, passphrase string) (*CreateWalletResult, error) {
	defer mpcshare.DiscardPassphrase(&passphrase)
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
		if errors.Is(err, chainregistry.ErrUnknownChain) {
			return nil, err
		}
		return nil, fmt.Errorf("create wallet: %w", err)
	}

	curve := curveForChain(chainID)

	keygenResult, err := s.mpcService.Keygen(ctx, curve)
	if err != nil {
		return nil, fmt.Errorf("mpc keygen: %w", err)
	}
	defer zeroBytes(keygenResult.ShareA)
	defer zeroBytes(keygenResult.ShareB)

	enc, err := mpc.EncryptShare(keygenResult.ShareA, passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypt share: %w", err)
	}
	defer zeroBytes(enc.Ciphertext)

	n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return nil, fmt.Errorf("generate activation code: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())

	walletID := uuid.New()
	secretName := fmt.Sprintf("vault/wallet/%s/share-b", walletID.String())
	secretARN, err := s.secretsManager.Create(ctx, secretName, keygenResult.ShareB)
	if err != nil {
		return nil, fmt.Errorf("store service share: %w", err)
	}

	onPostSecretErr := func(err error) (*CreateWalletResult, error) {
		slog.Warn("orphaned secret ARN after wallet creation failure", "arn", secretARN, "error", err)
		return nil, err
	}

	depositAddressStr, err := s.deriveChainAddress(chainID, keygenResult.CombinedPubKey)
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
		MPCChainCode:     hex.EncodeToString(keygenResult.ChainCode),
		AddressIndex:     0,
		AccountID:        &accountID,
		Status:           string(types.WalletStatusPending),
		ActivationCode:   &codeStr,
	}
	if err := s.walletRepo.Create(ctx, w); err != nil {
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
		DerivationType:  "genesis",
	}
	if err := s.addressRepo.Create(ctx, addr); err != nil {
		return onPostSecretErr(fmt.Errorf("create deposit address: %w", err))
	}

	if err := s.walletRepo.SetDepositAddressID(ctx, walletID, addressID); err != nil {
		return onPostSecretErr(fmt.Errorf("link deposit address: %w", err))
	}
	w.DepositAddressID = &addressID
	w.DepositAddress = addr

	s.cacheAddress(ctx, chainID, depositAddressStr)

	if s.webhookSyncSvc != nil {
		go func() {
			syncCtx := context.Background()
			if err := s.webhookSyncSvc.SyncChainAddresses(syncCtx, chainID); err != nil {
				slog.Error("webhook address sync failed", "chain", chainID, "error", err)
			}
		}()
	}

	_ = facades.Event().Job(&dtos.WalletCreated{}, []event.Arg{
		{Type: "string", Value: walletID.String()},
		{Type: "string", Value: chainID},
	}).Dispatch()

	return &CreateWalletResult{
		Wallet:           w,
		ServicePublicKey: hex.EncodeToString(keygenResult.CombinedPubKey),
		ActivationCode:   code,
	}, nil
}

func (s *Service) ActivateWallet(ctx context.Context, walletID uuid.UUID, code string) (*models.Wallet, error) {
	w, err := s.walletRepo.FindByID(ctx, walletID)
	if err != nil || w == nil {
		return nil, ErrWalletNotFound
	}
	if w.Status != string(types.WalletStatusPending) {
		return nil, ErrWalletAlreadyActive
	}
	if w.ActivationCode == nil {
		return nil, ErrWalletNotFound
	}
	if subtle.ConstantTimeCompare([]byte(*w.ActivationCode), []byte(code)) != 1 {
		return nil, ErrInvalidActivationCode
	}

	if err := s.walletRepo.Activate(ctx, w.ID, string(types.WalletStatusActive)); err != nil {
		return nil, fmt.Errorf("activate wallet: %w", err)
	}
	w.Status = string(types.WalletStatusActive)
	w.ActivationCode = nil

	_ = facades.Event().Job(&dtos.WalletActivated{}, []event.Arg{
		{Type: "string", Value: w.ID.String()},
		{Type: "string", Value: w.Chain},
	}).Dispatch()

	return w, nil
}

func curveForChain(chainID string) mpc.Curve {
	if chainID == models.ChainSOL || chainID == models.ChainTSOL {
		return mpc.CurveEd25519
	}
	return mpc.CurveSecp256k1
}

func (s *Service) GetWallet(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	w, err := s.walletRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (s *Service) ListWallets(ctx context.Context) ([]models.Wallet, error) {
	return s.walletRepo.FindAll(ctx)
}

func (s *Service) GenerateAddress(ctx context.Context, walletID uuid.UUID, externalUserID, label, metadata, passphrase string) (*models.Address, error) {
	defer mpcshare.DiscardPassphrase(&passphrase)
	if s.walletRepo == nil {
		return nil, fmt.Errorf("wallet not found")
	}
	w, err := s.walletRepo.FindByID(ctx, walletID)
	if err != nil || w == nil {
		return nil, fmt.Errorf("wallet not found")
	}

	curve := mpc.Curve(w.MPCCurve)
	if curve == mpc.CurveEd25519 && passphrase == "" {
		return nil, fmt.Errorf("passphrase is required for ed25519 address derivation")
	}

	newIndex, err := s.walletRepo.IncrementAddressIndex(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("increment address index: %w", err)
	}

	var addr *models.Address

	switch curve {
	case mpc.CurveSecp256k1:
		addr, err = s.generateSecp256k1Address(ctx, w, uint32(newIndex), externalUserID, label, metadata)
	case mpc.CurveEd25519:
		addr, err = s.generateEd25519Address(ctx, w, uint32(newIndex), externalUserID, label, metadata, passphrase)
	default:
		return nil, fmt.Errorf("unsupported curve: %s", w.MPCCurve)
	}

	if err != nil {
		return nil, err
	}

	s.cacheAddress(ctx, w.Chain, addr.Address)

	if s.webhookSyncSvc != nil {
		_ = s.webhookSyncSvc.SyncChainAddresses(ctx, w.Chain)
	}

	return addr, nil
}

func (s *Service) generateSecp256k1Address(ctx context.Context, w *models.Wallet, index uint32, externalUserID, label, metadata string) (*models.Address, error) {
	pubKey, err := hex.DecodeString(w.MPCPublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	chainCode, err := s.ensureChainCode(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("ensure chain code: %w", err)
	}

	child, err := deriveSecp256k1Child(pubKey, chainCode, index)
	if err != nil {
		return nil, fmt.Errorf("derive child key: %w", err)
	}

	addressStr, err := s.deriveChainAddress(w.Chain, child.ChildPubKey)
	if err != nil {
		return nil, fmt.Errorf("derive address: %w", err)
	}

	addr := &models.Address{
		ID:              uuid.New(),
		WalletID:        w.ID,
		Chain:           w.Chain,
		Address:         addressStr,
		DerivationIndex: int(index),
		ExternalUserID:  externalUserID,
		IsActive:        true,
		Label:           label,
		Metadata:        metadata,
		DerivationType:  "bip32",
	}
	if err := s.addressRepo.Create(ctx, addr); err != nil {
		return nil, fmt.Errorf("create address: %w", err)
	}
	return addr, nil
}

func (s *Service) generateEd25519Address(ctx context.Context, w *models.Wallet, index uint32, externalUserID, label, metadata, passphrase string) (*models.Address, error) {
	defer mpcshare.DiscardPassphrase(&passphrase)
	shareA, err := w.DecryptShareA(passphrase)
	if err != nil {
		if errors.Is(err, mpc.ErrInvalidPassphrase) {
			return nil, fmt.Errorf("invalid passphrase")
		}
		return nil, err
	}
	defer func() {
		for i := range shareA {
			shareA[i] = 0
		}
	}()

	shareB, err := s.secretsManager.Binary(ctx, w.MPCSecretARN)
	if err != nil {
		return nil, fmt.Errorf("fetch service share: %w", err)
	}
	defer func() {
		for i := range shareB {
			shareB[i] = 0
		}
	}()

	masterKey, err := s.mpcService.ReconstructEd25519PrivateKey(shareA, shareB)
	if err != nil {
		return nil, fmt.Errorf("reconstruct master key: %w", err)
	}
	defer func() {
		for i := range masterKey {
			masterKey[i] = 0
		}
	}()

	chainCode, err := s.ensureChainCode(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("ensure chain code: %w", err)
	}

	child, err := deriveEd25519Child(masterKey, chainCode, index)
	if err != nil {
		return nil, fmt.Errorf("derive child key: %w", err)
	}
	defer func() {
		for i := range child.ChildPrivateKey {
			child.ChildPrivateKey[i] = 0
		}
	}()

	privKey := ed25519.NewKeyFromSeed(child.ChildPrivateKey)
	pubKey := privKey.Public().(ed25519.PublicKey)
	defer func() {
		for i := range privKey {
			privKey[i] = 0
		}
	}()

	addressStr, err := deriveSolAddress([]byte(pubKey))
	if err != nil {
		return nil, fmt.Errorf("derive sol address: %w", err)
	}

	childEnc, err := mpc.EncryptShare(child.ChildPrivateKey, passphrase)
	if err != nil {
		return nil, fmt.Errorf("encrypt child key: %w", err)
	}
	defer zeroBytes(childEnc.Ciphertext)

	addr := &models.Address{
		ID:                  uuid.New(),
		WalletID:            w.ID,
		Chain:               w.Chain,
		Address:             addressStr,
		DerivationIndex:     int(index),
		ExternalUserID:      externalUserID,
		IsActive:            true,
		Label:               label,
		Metadata:            metadata,
		DerivationType:      "slip0010",
		EncryptedPrivateKey: hex.EncodeToString(childEnc.Ciphertext),
		EncryptionIV:        hex.EncodeToString(childEnc.IV),
		EncryptionSalt:      hex.EncodeToString(childEnc.Salt),
	}
	if err := s.addressRepo.Create(ctx, addr); err != nil {
		return nil, fmt.Errorf("create address: %w", err)
	}
	return addr, nil
}

// ensureChainCode returns the chain code for a wallet, generating one from
// the public key for legacy wallets that don't have one stored.
func (s *Service) ensureChainCode(ctx context.Context, w *models.Wallet) ([]byte, error) {
	if w.MPCChainCode != "" {
		return hex.DecodeString(w.MPCChainCode)
	}

	pubKey, err := hex.DecodeString(w.MPCPublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}

	h := hmac.New(sha512.New, []byte("vault-chain-code-backfill"))
	h.Write(pubKey)
	chainCode := h.Sum(nil)[32:]

	chainCodeHex := hex.EncodeToString(chainCode)
	if err := s.walletRepo.SetMPCChainCode(ctx, w.ID, chainCodeHex); err != nil {
		return nil, fmt.Errorf("persist chain code: %w", err)
	}
	w.MPCChainCode = chainCodeHex

	return chainCode, nil
}

func applyAddressFields(ctx context.Context, addresses AddressStore, addressID uuid.UUID, fields map[string]interface{}) error {
	for key := range fields {
		if key != "label" && key != "external_user_id" {
			return fmt.Errorf("update address: unsupported field %s", key)
		}
	}
	if label, ok := fields["label"]; ok {
		text, ok := label.(string)
		if !ok {
			return fmt.Errorf("update address: label must be a string")
		}
		if err := addresses.SetLabel(ctx, addressID, text); err != nil {
			return fmt.Errorf("update address: %w", err)
		}
	}
	if externalUserID, ok := fields["external_user_id"]; ok {
		text, ok := externalUserID.(string)
		if !ok {
			return fmt.Errorf("update address: external_user_id must be a string")
		}
		if err := addresses.SetExternalUserID(ctx, addressID, text); err != nil {
			return fmt.Errorf("update address: %w", err)
		}
	}
	return nil
}

func (s *Service) UpdateAddress(ctx context.Context, addressID uuid.UUID, fields map[string]interface{}) (*models.Address, error) {
	addr, err := s.addressRepo.FindByID(ctx, addressID)
	if err != nil || addr == nil {
		return nil, fmt.Errorf("address not found")
	}
	if err := applyAddressFields(ctx, s.addressRepo, addressID, fields); err != nil {
		return nil, err
	}

	updated, err := s.addressRepo.FindByID(ctx, addressID)
	if err != nil {
		return nil, fmt.Errorf("fetch updated address: %w", err)
	}
	return updated, nil
}

func (s *Service) LookupAddress(ctx context.Context, chainID, address string) (*models.Address, error) {
	return s.addressRepo.FindByChainAndAddress(ctx, chainID, address)
}

// LookupAddressForAccount resolves an on-chain address only if it belongs to a
// wallet owned by accountID. Returns (nil, nil) when the address doesn't exist
// OR belongs to a different account — callers must return a generic 404 so
// the two cases are indistinguishable to API clients (IDOR mitigation).
func (s *Service) LookupAddressForAccount(ctx context.Context, chainID, address string, accountID uuid.UUID) (*models.Address, error) {
	return s.addressRepo.FindByChainAndAddressAndAccount(ctx, chainID, address, accountID)
}

func (s *Service) ListUserAddresses(ctx context.Context, externalUserID string) ([]models.Address, error) {
	return s.addressRepo.FindByExternalUserID(ctx, externalUserID)
}

// ListUserAddressesForAccount returns addresses for an external_user_id limited
// to the caller's account. An empty slice is a legitimate response and must
// not be distinguished from "external_id exists but belongs to another account".
func (s *Service) ListUserAddressesForAccount(ctx context.Context, externalUserID string, accountID uuid.UUID) ([]models.Address, error) {
	return s.addressRepo.FindByExternalUserIDAndAccount(ctx, externalUserID, accountID)
}

func (s *Service) ListWalletAddresses(ctx context.Context, walletID uuid.UUID) ([]models.Address, error) {
	return s.addressRepo.FindByWalletID(ctx, walletID)
}
