package wallets

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// seedAPIWalletForAccount inserts a Wallet bound to accountID with real
// secp256k1 pubkey + chain code so controllers that invoke BIP32 derivation
// (GenerateAddress on secp256k1 chains) succeed. Returns the wallet ID as a
// string for URL interpolation.
func seedAPIWalletForAccount(t *testing.T, accountID uuid.UUID, chain, label string) string {
	t.Helper()

	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new private key: %v", err)
	}
	pubCompressed := priv.PubKey().SerializeCompressed()
	chainCode := sha256.Sum256([]byte("addresses-suite-chain-code-" + label))

	walletID := uuid.New()
	acct := accountID
	w := &models.Wallet{
		ID:               walletID,
		Chain:            chain,
		Label:            label,
		MPCCustomerShare: "deadbeef",
		MPCShareIV:       "cafebabe",
		MPCShareSalt:     "feedface",
		MPCSecretARN:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		MPCPublicKey:     hex.EncodeToString(pubCompressed),
		MPCChainCode:     hex.EncodeToString(chainCode[:]),
		MPCCurve:         "secp256k1",
		AccountID:        &acct,
	}
	if err := facades.Orm().Query().Create(w); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}
	return walletID.String()
}
