package keyexport

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
)

// WalletSource loads wallets with their deposit address.
type WalletSource interface {
	FindAll() ([]models.Wallet, error)
	FindByID(id uuid.UUID) (*models.Wallet, error)
}

// AddressSource lists every address row of a wallet, active or retired.
type AddressSource interface {
	FindByWalletID(walletID uuid.UUID) ([]models.Address, error)
}

// NetworkResolver says where a chain record points.
type NetworkResolver interface {
	ResolveNetwork(chainID string) (Network, error)
}

// ShareBSource fetches the service share (Secrets Manager). The caller zeroes it.
type ShareBSource interface {
	FetchShareB(ctx context.Context, wallet models.Wallet) ([]byte, error)
}

// PassphraseSource supplies the passphrase that decrypts share A. attempt starts at 1;
// a source that cannot do better on a retry returns MaxAttempts 1.
type PassphraseSource interface {
	Passphrase(ctx context.Context, wallet models.Wallet, attempt int) (string, error)
	MaxAttempts() int
}

// ErrAborted stops the whole export (the terminal went away, the operator gave up);
// it is never turned into a per-wallet refusal.
var ErrAborted = errors.New("key export aborted")

// Reconstructor is the part of mpc.Service that rebuilds keys from both shares.
type Reconstructor interface {
	ReconstructSecp256k1PrivateKey(shareA, shareB []byte) ([]byte, error)
	ReconstructEd25519Scalar(shareA, shareB []byte) ([]byte, error)
	ReconstructEd25519PrivateKey(shareA, shareB []byte) ([]byte, error)
}

type Dependencies struct {
	Wallets   WalletSource
	Addresses AddressSource
	Networks  NetworkResolver
	ShareB    ShareBSource
	MPC       Reconstructor
	Now       func() time.Time
}

type Service struct {
	deps Dependencies
}

func NewService(deps Dependencies) (*Service, error) {
	if deps.Wallets == nil || deps.Addresses == nil || deps.Networks == nil || deps.ShareB == nil || deps.MPC == nil {
		return nil, errors.New("key export: wallets, addresses, networks, share B source and mpc are required")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps}, nil
}

// SelectWallets returns every wallet when ids is empty, otherwise exactly the given
// wallets in the given order; an unknown id fails the whole selection.
func (s *Service) SelectWallets(ids []uuid.UUID) ([]models.Wallet, error) {
	if len(ids) == 0 {
		wallets, err := s.deps.Wallets.FindAll()
		if err != nil {
			return nil, fmt.Errorf("load wallets: %w", err)
		}
		if len(wallets) == 0 {
			return nil, errors.New("the database has no wallets")
		}
		return wallets, nil
	}
	wallets := make([]models.Wallet, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, errors.New("wallet id must not be the nil UUID")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		wallet, err := s.deps.Wallets.FindByID(id)
		if err != nil || wallet == nil || wallet.ID != id {
			return nil, fmt.Errorf("wallet %s not found", id)
		}
		wallets = append(wallets, *wallet)
	}
	return wallets, nil
}

// Plan loads, for each wallet, its network and address rows. Wallets that cannot be
// exported for public reasons (unknown network, no addresses, curve and chain that
// disagree) are refused here, before any secret is touched.
func (s *Service) Plan(wallets []models.Wallet) ([]WalletPlan, []Refusal, error) {
	plans := make([]WalletPlan, 0, len(wallets))
	var refused []Refusal
	for _, wallet := range wallets {
		addresses, err := s.deps.Addresses.FindByWalletID(wallet.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("load addresses of wallet %s: %w", wallet.ID, err)
		}
		plan := WalletPlan{Wallet: wallet, Addresses: addresses}
		if err := s.completePlan(&plan); err != nil {
			refused = append(refused, newRefusal(wallet, err))
			continue
		}
		plans = append(plans, plan)
	}
	return plans, refused, nil
}

func (s *Service) completePlan(plan *WalletPlan) error {
	if len(plan.Addresses) == 0 {
		return refuse("wallet has no address rows")
	}
	network, err := s.deps.Networks.ResolveNetwork(plan.Wallet.Chain)
	if err != nil {
		return refuse("network of chain %s could not be resolved: %v", plan.Wallet.Chain, err)
	}
	if err := requireCurveMatchesAdapter(mpcpkg.Curve(plan.Wallet.MPCCurve), network.AdapterType); err != nil {
		return err
	}
	for _, address := range plan.Addresses {
		if address.WalletID != plan.Wallet.ID {
			return refuse("address %s belongs to wallet %s", address.Address, address.WalletID)
		}
	}
	plan.Network = network
	return nil
}

func requireCurveMatchesAdapter(curve mpcpkg.Curve, adapterType string) error {
	switch {
	case curve == mpcpkg.CurveSecp256k1 && (adapterType == models.AdapterTypeEVM || adapterType == models.AdapterTypeBitcoin || adapterType == models.AdapterTypeTron):
		return nil
	case curve == mpcpkg.CurveEd25519 && adapterType == models.AdapterTypeSolana:
		return nil
	default:
		return refuse("curve %q does not match adapter %q", curve, adapterType)
	}
}

// Result is what an export produced. Wipe zeroes every archive buffer.
type Result struct {
	Wallets []*WalletExport
	Refused []Refusal
}

func (r *Result) Wipe() {
	if r == nil {
		return
	}
	for _, wallet := range r.Wallets {
		wallet.wipe()
	}
}

// WalletExport is one exported wallet: its public plan and its rendered files.
type WalletExport struct {
	Plan         WalletPlan
	AddressCount int
	files        []ArchiveFile
}

func (w *WalletExport) wipe() {
	for i := range w.files {
		zeroBytes(w.files[i].Data)
	}
}

// Export reconstructs and verifies every planned wallet. A wallet whose passphrase,
// shares or reconstruction fail is refused and the others still export.
func (s *Service) Export(ctx context.Context, plans []WalletPlan, passphrases PassphraseSource) (*Result, error) {
	if passphrases == nil {
		return nil, errors.New("key export: a passphrase source is required")
	}
	result := &Result{}
	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			result.Wipe()
			return nil, err
		}
		exported, err := s.exportWallet(ctx, plan, passphrases)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				result.Wipe()
				return nil, ctxErr
			}
			if errors.Is(err, ErrAborted) {
				result.Wipe()
				return nil, err
			}
			result.Refused = append(result.Refused, newRefusal(plan.Wallet, err))
			continue
		}
		result.Wallets = append(result.Wallets, exported)
	}
	return result, nil
}

func (s *Service) exportWallet(ctx context.Context, plan WalletPlan, passphrases PassphraseSource) (*WalletExport, error) {
	secrets, err := s.unlock(ctx, plan.Wallet, passphrases)
	if err != nil {
		return nil, err
	}
	defer secrets.wipe()

	var keys []AddressKey
	var extraFiles []ArchiveFile
	switch mpcpkg.Curve(plan.Wallet.MPCCurve) {
	case mpcpkg.CurveSecp256k1:
		keys, err = s.secp256k1Keys(plan, secrets)
	case mpcpkg.CurveEd25519:
		keys, extraFiles, err = s.ed25519Keys(plan, secrets)
	default:
		err = refuse("unsupported curve %q", plan.Wallet.MPCCurve)
	}
	if err != nil {
		wipeFiles(extraFiles)
		return nil, err
	}
	files, err := renderWalletFiles(plan, keys, secrets, extraFiles)
	if err != nil {
		wipeFiles(extraFiles)
		return nil, err
	}
	return &WalletExport{Plan: plan, AddressCount: len(keys), files: files}, nil
}

// walletSecrets is what unlocking a wallet yields; wipe zeroes the shares.
type walletSecrets struct {
	shareA     []byte
	shareB     []byte
	passphrase string
}

func (w *walletSecrets) wipe() {
	zeroBytes(w.shareA)
	zeroBytes(w.shareB)
	w.passphrase = ""
}

func (s *Service) unlock(ctx context.Context, wallet models.Wallet, passphrases PassphraseSource) (*walletSecrets, error) {
	attempts := passphrases.MaxAttempts()
	if attempts < 1 {
		attempts = 1
	}
	var shareA []byte
	var passphrase string
	for attempt := 1; attempt <= attempts && shareA == nil; attempt++ {
		candidate, err := passphrases.Passphrase(ctx, wallet, attempt)
		if errors.Is(err, ErrAborted) {
			return nil, err
		}
		if err != nil {
			return nil, refuse("wallet passphrase unavailable: %v", err)
		}
		decrypted, err := wallet.DecryptShareA(candidate)
		switch {
		case errors.Is(err, mpcpkg.ErrInvalidPassphrase):
			continue
		case err != nil:
			return nil, refuse("share A stored on the wallet is unreadable")
		}
		shareA, passphrase = decrypted, candidate
	}
	if shareA == nil {
		return nil, refuse("the passphrase does not decrypt share A")
	}
	shareB, err := s.deps.ShareB.FetchShareB(ctx, wallet)
	if err != nil || len(shareB) == 0 {
		zeroBytes(shareA)
		zeroBytes(shareB)
		return nil, refuse("share B could not be fetched from Secrets Manager")
	}
	return &walletSecrets{shareA: shareA, shareB: shareB, passphrase: passphrase}, nil
}

func newRefusal(wallet models.Wallet, err error) Refusal {
	return Refusal{WalletID: wallet.ID, Label: wallet.Label, Chain: wallet.Chain, Reason: refusalReason(err)}
}

func newAddressKey(address models.Address, network addressNetwork, publicKey []byte, kind, source string) AddressKey {
	return AddressKey{
		AddressID:       address.ID.String(),
		Address:         address.Address,
		DerivationType:  address.DerivationType,
		DerivationIndex: address.DerivationIndex,
		IsActive:        address.IsActive,
		Label:           address.Label,
		ExternalUserID:  address.ExternalUserID,
		Network:         network.name,
		Testnet:         network.testnet,
		PublicKeyHex:    hex.EncodeToString(publicKey),
		KeyKind:         kind,
		KeySource:       source,
	}
}

func decodeHexField(value string, size int) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if len(decoded) != size {
		return nil, fmt.Errorf("want %d bytes, got %d", size, len(decoded))
	}
	return decoded, nil
}

func decodeOptionalChainCode(value string) []byte {
	decoded, err := decodeHexField(value, slip0010KeySize)
	if err != nil {
		return nil
	}
	return decoded
}

func wipeFiles(files []ArchiveFile) {
	for i := range files {
		zeroBytes(files[i].Data)
	}
}
