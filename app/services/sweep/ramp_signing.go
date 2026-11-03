package sweep

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/gagliardetto/solana-go"
	"github.com/macrowallets/waas/app/models"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/solanatx"
)

// SignStoredRampAction is the custody adapter for a funding authorization the
// ramp engine already validated and stored. No HTTP endpoint accepts these
// transaction bytes or a fingerprint from a caller.
func SignStoredRampAction(ctx context.Context, box *Box, wallet *models.Wallet, signer models.Address, payload, fingerprint, passphrase string) (string, error) {
	s, ok := box.Service.(*service)
	if !ok {
		return "", fmt.Errorf("prepared funding custody unavailable")
	}
	tx, message, err := solanatx.Unsigned(payload)
	if err != nil {
		return "", err
	}
	if solanatx.Fingerprint(message) != fingerprint || tx.Message.Header.NumRequiredSignatures != 1 || len(tx.Message.AccountKeys) == 0 || tx.Message.AccountKeys[0].String() != signer.Address {
		return "", fmt.Errorf("stored ramp action does not match its authorized signer")
	}
	if wallet.MPCCurve != "ed25519" || signer.WalletID != wallet.ID {
		return "", fmt.Errorf("ramp signing wallet mismatch")
	}
	var signature []byte
	if isGenesisSigner(wallet, signer) {
		shareA, err := wallet.DecryptShareA(passphrase)
		if err != nil {
			return "", err
		}
		defer zeroBytes(shareA)
		shareB, err := s.fetchShareB(ctx, wallet)
		if err != nil {
			return "", err
		}
		defer zeroBytes(shareB)
		scalar, err := s.mpc.ReconstructEd25519Scalar(shareA, shareB)
		if err != nil {
			return "", err
		}
		defer zeroBytes(scalar)
		publicKey, err := hex.DecodeString(wallet.MPCPublicKey)
		if err != nil || len(publicKey) != 32 || subtle.ConstantTimeCompare(publicKey, tx.Message.AccountKeys[0][:]) != 1 {
			return "", fmt.Errorf("ramp public key mismatch")
		}
		signature, err = mpcpkg.SignEd25519WithScalar(scalar, publicKey, message)
		if err != nil {
			return "", err
		}
	} else {
		seed, err := decryptChildSeed(signer, passphrase)
		if err != nil {
			return "", err
		}
		defer zeroBytes(seed)
		signature, _, err = mpcpkg.SignEd25519Seed(seed, message)
		if err != nil {
			return "", err
		}
	}
	if len(signature) != 64 {
		return "", fmt.Errorf("invalid ramp signature")
	}
	tx.Signatures[0] = solana.SignatureFromBytes(signature)
	raw, err := tx.MarshalBinary()
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	if _, err := solanatx.VerifySigned(encoded, fingerprint); err != nil {
		return "", err
	}
	return encoded, nil
}
