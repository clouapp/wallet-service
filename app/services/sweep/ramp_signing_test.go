package sweep

import (
	"context"
	"encoding/base64"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/solanatx"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStoredRampAction_SignsExactMessageAndRefusesMutation(t *testing.T) {
	f := newSolanaWalletFixture(t)
	s := newSolanaExecutor(f, newSolanaSigningChain(nil))
	owner := solana.MustPublicKeyFromBase58(f.wallet.DepositAddress.Address)
	tx, err := solana.NewTransaction([]solana.Instruction{system.NewTransferInstruction(1, owner, solana.NewWallet().PublicKey()).Build()}, solana.Hash{1}, solana.TransactionPayer(owner))
	require.NoError(t, err)
	tx.Signatures = make([]solana.Signature, 1)
	raw, err := tx.MarshalBinary()
	require.NoError(t, err)
	payload := base64.StdEncoding.EncodeToString(raw)
	_, msg, err := solanatx.Unsigned(payload)
	require.NoError(t, err)
	fingerprint := solanatx.Fingerprint(msg)
	signed, err := SignStoredRampAction(context.Background(), &Box{Service: s}, f.wallet, *f.wallet.DepositAddress, payload, fingerprint, solE2EPassphrase)
	require.NoError(t, err)
	_, err = solanatx.VerifySigned(signed, fingerprint)
	require.NoError(t, err)
	_, err = SignStoredRampAction(context.Background(), &Box{Service: s}, f.wallet, *f.wallet.DepositAddress, payload, "tampered", solE2EPassphrase)
	require.Error(t, err)
	_, err = SignStoredRampAction(context.Background(), &Box{Service: s}, f.wallet, *f.wallet.DepositAddress, payload, fingerprint, "wrong-passphrase")
	require.Error(t, err)
	other := *f.wallet.DepositAddress
	other.Address = solana.NewWallet().PublicKey().String()
	_, err = SignStoredRampAction(context.Background(), &Box{Service: s}, f.wallet, other, payload, fingerprint, solE2EPassphrase)
	require.Error(t, err)
}
