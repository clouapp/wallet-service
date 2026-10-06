package sweep

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/hex"
	"fmt"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

// solanaAssembler is implemented by the Solana adapter. It exposes the message
// to sign and attaches a signature. It never receives a seed or a scalar.
type solanaAssembler interface {
	SolanaSigningView(unsigned *types.UnsignedTx) (message, feePayer []byte, err error)
	AssembleSolana(unsigned *types.UnsignedTx, signature []byte) (*types.SignedTx, error)
}

// signEd25519 signs for the genesis address with the scalar reconstructed from both
// shares, and for a child address with the SLIP-0010 seed stored encrypted on its row.
// The two keys are unrelated: children were derived from the legacy share sum, which
// is not the genesis private key.
func (s *service) signEd25519(ctx context.Context, adapter types.Chain, keys walletKeys, wallet *models.Wallet, signer models.Address, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	if isGenesisSigner(wallet, signer) {
		return s.signEd25519Genesis(ctx, adapter, keys, wallet, unsigned)
	}
	seed, err := decryptChildSeed(signer, keys.passphrase)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(seed)
	return signSolanaChild(adapter, seed, unsigned)
}

func (s *service) signEd25519Genesis(ctx context.Context, adapter types.Chain, keys walletKeys, wallet *models.Wallet, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	publicKey, err := hex.DecodeString(wallet.MPCPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("wallet %s has no valid ed25519 public key", wallet.ID)
	}
	scalar, err := s.mpc.ReconstructEd25519Scalar(keys.shareA, keys.shareB)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(scalar)
	assembler, message, feePayer, err := solanaSigningMaterial(adapter, unsigned)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(feePayer, publicKey) != 1 {
		return nil, fmt.Errorf("sol sign: fee payer is not the signing key")
	}
	signature, err := mpcpkg.SignEd25519WithScalar(scalar, publicKey, message)
	if err != nil {
		return nil, fmt.Errorf("sol sign: %w", err)
	}
	return assembler.AssembleSolana(unsigned, signature)
}

func signSolanaChild(adapter types.Chain, seed []byte, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	assembler, message, feePayer, err := solanaSigningMaterial(adapter, unsigned)
	if err != nil {
		return nil, err
	}
	signature, publicKey, err := mpcpkg.SignEd25519Seed(seed, message)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(feePayer, publicKey) != 1 {
		return nil, fmt.Errorf("sol sign: fee payer is not the signing key")
	}
	return assembler.AssembleSolana(unsigned, signature)
}

func solanaSigningMaterial(adapter types.Chain, unsigned *types.UnsignedTx) (solanaAssembler, []byte, []byte, error) {
	assembler, ok := adapter.(solanaAssembler)
	if !ok {
		return nil, nil, nil, fmt.Errorf("chain %s cannot assemble a solana transaction", adapter.ID())
	}
	message, feePayer, err := assembler.SolanaSigningView(unsigned)
	if err != nil {
		return nil, nil, nil, err
	}
	return assembler, message, feePayer, nil
}

func isGenesisSigner(wallet *models.Wallet, signer models.Address) bool {
	if wallet == nil || wallet.DepositAddress == nil {
		return false
	}
	return signer.ID == wallet.DepositAddress.ID && signer.Address == wallet.DepositAddress.Address
}

// decryptChildSeed opens the SLIP-0010 seed stored on a derived address row. The
// caller must zero the returned seed.
func decryptChildSeed(address models.Address, passphrase string) ([]byte, error) {
	if address.EncryptedPrivateKey == "" || address.EncryptionIV == "" || address.EncryptionSalt == "" {
		return nil, fmt.Errorf("address %s has no stored signing key", address.Address)
	}
	if passphrase == "" {
		return nil, fmt.Errorf("a passphrase is required to sign for address %s", address.Address)
	}
	ciphertext, err := hex.DecodeString(address.EncryptedPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("address %s: stored key is not hex", address.Address)
	}
	iv, err := hex.DecodeString(address.EncryptionIV)
	if err != nil {
		return nil, fmt.Errorf("address %s: stored iv is not hex", address.Address)
	}
	salt, err := hex.DecodeString(address.EncryptionSalt)
	if err != nil {
		return nil, fmt.Errorf("address %s: stored salt is not hex", address.Address)
	}
	seed, err := mpcpkg.DecryptShare(&mpcpkg.EncryptedShare{Ciphertext: ciphertext, IV: iv, Salt: salt}, passphrase)
	if err != nil {
		return nil, fmt.Errorf("address %s: %w", address.Address, err)
	}
	if len(seed) != ed25519.SeedSize {
		zeroBytes(seed)
		return nil, fmt.Errorf("address %s: stored key has %d bytes, want %d", address.Address, len(seed), ed25519.SeedSize)
	}
	if !seedOwnsAddress(seed, address.Address) {
		zeroBytes(seed)
		return nil, fmt.Errorf("address %s: stored key belongs to another address", address.Address)
	}
	return seed, nil
}

func seedOwnsAddress(seed []byte, address string) bool {
	privateKey := ed25519.NewKeyFromSeed(seed)
	defer zeroBytes(privateKey)
	derived, err := addressing.DeriveSolAddress(privateKey.Public().(ed25519.PublicKey))
	return err == nil && derived == address
}
