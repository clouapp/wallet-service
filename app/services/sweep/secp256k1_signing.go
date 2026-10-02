package sweep

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/hdkey"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

const bip32DerivationType = "bip32"

// secp256k1Signer is the key a secp256k1 transaction must be signed with: the wallet
// key for the base address, or its BIP-32 child for a derived address. Tweak is nil
// for the wallet key.
type secp256k1Signer struct {
	publicKey []byte
	tweak     *big.Int
}

type testnetReporter interface {
	IsTestnet() bool
}

// signedTransactionVerifier proves, from the signed bytes alone, that a transaction
// spends from a given address. Every secp256k1 adapter must implement it: nothing is
// broadcast that has not passed it.
type signedTransactionVerifier interface {
	VerifySignedTransaction(unsigned *types.UnsignedTx, signed *types.SignedTx, from string) error
}

func verifySignedBy(adapter types.Chain, unsigned *types.UnsignedTx, signed *types.SignedTx, from string) error {
	verifier, ok := adapter.(signedTransactionVerifier)
	if !ok {
		return fmt.Errorf("chain %s cannot verify signed transactions; refusing to broadcast", adapter.ID())
	}
	if err := verifier.VerifySignedTransaction(unsigned, signed, from); err != nil {
		return fmt.Errorf("signed transaction failed verification against %s: %w", from, err)
	}
	return nil
}

// resolveSecp256k1Signer re-derives the signer's public key from the wallet key and
// refuses to sign unless it produces the signer's address on this chain. A child row
// whose index or wallet does not match its address would otherwise get a signature
// from an unrelated key, or none that the chain accepts.
func resolveSecp256k1Signer(adapter types.Chain, wallet *models.Wallet, signer models.Address) (*secp256k1Signer, error) {
	if wallet == nil {
		return nil, fmt.Errorf("wallet is required to sign")
	}
	walletKey, err := hex.DecodeString(wallet.MPCPublicKey)
	if err != nil || len(walletKey) != hdkey.CompressedPublicKeySize {
		return nil, fmt.Errorf("wallet %s has no valid secp256k1 public key", wallet.ID)
	}
	if signer.Address == "" {
		return nil, fmt.Errorf("signer address is required")
	}
	if signer.WalletID != wallet.ID {
		return nil, fmt.Errorf("address %s belongs to wallet %s, not %s", signer.Address, signer.WalletID, wallet.ID)
	}
	if isGenesisSigner(wallet, signer) {
		if err := requireKeyOwnsAddress(adapter, wallet.Chain, walletKey, signer.Address); err != nil {
			return nil, err
		}
		return &secp256k1Signer{publicKey: walletKey}, nil
	}

	if signer.DerivationType != bip32DerivationType {
		return nil, fmt.Errorf("address %s has derivation type %q; only %s children can be signed", signer.Address, signer.DerivationType, bip32DerivationType)
	}
	if signer.DerivationIndex < 0 || int64(signer.DerivationIndex) >= int64(1)<<31 {
		return nil, fmt.Errorf("address %s has an out-of-range derivation index %d", signer.Address, signer.DerivationIndex)
	}
	chainCode, err := hex.DecodeString(wallet.MPCChainCode)
	if err != nil || len(chainCode) != hdkey.ChainCodeSize {
		return nil, fmt.Errorf("wallet %s has no valid chain code", wallet.ID)
	}
	child, err := hdkey.DeriveSecp256k1Child(walletKey, chainCode, uint32(signer.DerivationIndex))
	if err != nil {
		return nil, fmt.Errorf("derive signer for %s: %w", signer.Address, err)
	}
	if err := requireKeyOwnsAddress(adapter, wallet.Chain, child.PublicKey, signer.Address); err != nil {
		return nil, err
	}
	return &secp256k1Signer{publicKey: child.PublicKey, tweak: child.Tweak}, nil
}

func requireKeyOwnsAddress(adapter types.Chain, chainID string, publicKey []byte, address string) error {
	testnet := false
	if reporter, ok := adapter.(testnetReporter); ok {
		testnet = reporter.IsTestnet()
	}
	derived, err := addressing.DeriveAddressOnNetwork(chainID, testnet, publicKey)
	if err != nil {
		return fmt.Errorf("derive address for %s: %w", address, err)
	}
	if !sameChainAddress(derived, address) {
		return fmt.Errorf("signing key does not own address %s", address)
	}
	return nil
}

// sameChainAddress compares hex EVM addresses case-insensitively (EIP-55 casing is
// presentation only) and every other encoding exactly.
func sameChainAddress(a, b string) bool {
	if strings.HasPrefix(a, "0x") && strings.HasPrefix(b, "0x") {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// signSecp256k1MPC runs the threshold ceremony; for a child the parties apply the
// tweak to their shares, so neither the wallet key nor the child key is assembled.
func (s *service) signSecp256k1MPC(ctx context.Context, adapter types.Chain, keys walletKeys, key *secp256k1Signer, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	signature, err := s.mpc.Sign(ctx, mpcpkg.CurveSecp256k1, keys.shareA, keys.shareB, mpcpkg.SignInputs{
		TxHashes:           [][]byte{unsigned.RawBytes},
		KeyDerivationDelta: key.tweak,
		ExpectedPublicKey:  key.publicKey,
	})
	if err != nil {
		return nil, err
	}
	return finalizeMPCTransaction(adapter, unsigned, signature, key.publicKey)
}

// signSecp256k1Local signs on chains whose adapter needs the private key (Bitcoin).
// The wallet key is reconstructed and checked against the wallet public key; a child
// key is the wallet key plus the BIP-32 tweak and is checked against the child public
// key. Both are zeroed when signing returns.
func (s *service) signSecp256k1Local(ctx context.Context, adapter types.Chain, keys walletKeys, key *secp256k1Signer, walletPublicKey string, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	walletKey, err := s.mpc.ReconstructSecp256k1PrivateKey(keys.shareA, keys.shareB)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(walletKey)
	if err := requirePrivateKeyMatches(walletKey, walletPublicKey); err != nil {
		return nil, err
	}
	if key.tweak == nil {
		return adapter.SignTransaction(ctx, unsigned, walletKey)
	}
	childKey, err := hdkey.ChildPrivateKey(walletKey, key.tweak)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(childKey)
	if err := requirePrivateKeyMatches(childKey, hex.EncodeToString(key.publicKey)); err != nil {
		return nil, err
	}
	return adapter.SignTransaction(ctx, unsigned, childKey)
}

func requirePrivateKeyMatches(privateKey []byte, publicKeyHex string) error {
	expected, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("decode expected public key: %w", err)
	}
	derived, err := hdkey.PublicKeyOf(privateKey)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(derived, expected) != 1 {
		return fmt.Errorf("reconstructed signing key does not match the expected public key")
	}
	return nil
}
