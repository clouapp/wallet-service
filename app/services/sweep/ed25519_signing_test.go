package sweep

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	walletsvc "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	solE2EPassphrase  = "correct-horse-battery-staple-sol"
	solE2EBlockhash   = "4sGjMW1sUnHzSxGspuhpqLDx6wiyjNtZAMdL4VZHirAn"
	solE2EDestination = "BLicfvBtbWjmTCXw3z4YxzNM3AWAaEpCQGczskP9Nc8S"
	solE2EChildIndex  = 7
)

// solanaWalletFixture is a real 2-of-2 ed25519 MPC wallet: genesis address from the
// keygen public key, and one child produced by the production derivation path.
type solanaWalletFixture struct {
	tss            *mpcpkg.TSSService
	wallet         *models.Wallet
	shareB         []byte
	child          models.Address
	addressService *walletsvc.Service
	walletRepo     *indexedWalletRepo
}

// copyingSecrets hands out a fresh copy per read, like AWS does; callers zero what they get.
type copyingSecrets struct{ *mocks.MockSecretsManager }

func (c copyingSecrets) GetSecretValue(ctx context.Context, input *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	out, err := c.MockSecretsManager.GetSecretValue(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	copied := *out
	copied.SecretBinary = append([]byte(nil), out.SecretBinary...)
	return &copied, nil
}

type indexedWalletRepo struct {
	*fakeWalletRepo
	next int
}

func (r *indexedWalletRepo) IncrementAddressIndex(context.Context, uuid.UUID) (int, error) {
	return r.next, nil
}

func newSolanaWalletFixture(t *testing.T) *solanaWalletFixture {
	t.Helper()
	ctx := context.Background()
	tss := mpcpkg.NewTSSService()
	keys, err := tss.Keygen(ctx, mpcpkg.CurveEd25519)
	if err != nil {
		t.Fatal(err)
	}
	encryptedShareA, err := mpcpkg.EncryptShare(keys.ShareA, solE2EPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	genesisAddress, err := addressing.DeriveSolAddress(keys.CombinedPubKey)
	if err != nil {
		t.Fatal(err)
	}

	walletID := uuid.New()
	secrets := mocks.NewMockSecretsManager()
	secret, err := secrets.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String("sol-e2e-" + walletID.String()),
		SecretBinary: append([]byte(nil), keys.ShareB...),
	})
	if err != nil {
		t.Fatal(err)
	}

	base := models.Address{ID: uuid.New(), WalletID: walletID, Chain: models.ChainSOL, Address: genesisAddress, DerivationType: "genesis"}
	wallet := &models.Wallet{
		ID:               walletID,
		Chain:            models.ChainSOL,
		MPCCustomerShare: hex.EncodeToString(encryptedShareA.Ciphertext),
		MPCShareIV:       hex.EncodeToString(encryptedShareA.IV),
		MPCShareSalt:     hex.EncodeToString(encryptedShareA.Salt),
		MPCSecretARN:     aws.ToString(secret.ARN),
		MPCPublicKey:     hex.EncodeToString(keys.CombinedPubKey),
		MPCCurve:         string(mpcpkg.CurveEd25519),
		MPCChainCode:     hex.EncodeToString(keys.ChainCode),
		DepositAddressID: &base.ID,
		DepositAddress:   &base,
	}

	walletRepo := &indexedWalletRepo{fakeWalletRepo: &fakeWalletRepo{wallet: wallet}, next: solE2EChildIndex}
	fixture := &solanaWalletFixture{
		tss:            tss,
		wallet:         wallet,
		shareB:         append([]byte(nil), keys.ShareB...),
		addressService: walletsvc.NewService(walletsvc.Deps{
			Registry:  chain.NewRegistry(),
			MPC:       tss,
			Secrets:   copyingSecrets{secrets},
			Wallets:   walletRepo,
			Addresses: &fakeAddressRepo{},
		}),
		walletRepo:     walletRepo,
	}
	fixture.child = fixture.deriveChild(t, solE2EChildIndex)
	return fixture
}

// deriveChild runs the production ed25519 child derivation for index.
func (f *solanaWalletFixture) deriveChild(t *testing.T, index int) models.Address {
	t.Helper()
	f.walletRepo.next = index
	child, err := f.addressService.GenerateAddress(context.Background(), f.wallet.ID, "user-sol", "", "{}", solE2EPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if child.Address == f.wallet.DepositAddress.Address || child.EncryptedPrivateKey == "" {
		t.Fatalf("unexpected child row: %+v", child)
	}
	return *child
}

func (f *solanaWalletFixture) credentials(t *testing.T, passphrase string) SigningCredentials {
	t.Helper()
	shareA, err := f.wallet.DecryptShareA(solE2EPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	return SigningCredentials{ShareA: shareA, Passphrase: passphrase}
}

// solanaSigningChain signs with the production Solana code but never touches RPC.
type solanaSigningChain struct {
	*mocks.MockChain
	live *chain.SolanaLive
}

func (c *solanaSigningChain) SignTransactionWithScalar(ctx context.Context, unsigned *types.UnsignedTx, scalar, publicKey []byte) (*types.SignedTx, error) {
	return c.live.SignTransactionWithScalar(ctx, unsigned, scalar, publicKey)
}

func solanaTransferMessage(from, to string, amount *big.Int) (*types.UnsignedTx, error) {
	fromKey, err := solana.PublicKeyFromBase58(from)
	if err != nil {
		return nil, err
	}
	toKey, err := solana.PublicKeyFromBase58(to)
	if err != nil {
		return nil, err
	}
	ix := system.NewTransferInstruction(amount.Uint64(), fromKey, toKey).Build()
	tx, err := solana.NewTransaction([]solana.Instruction{ix}, solana.MustHashFromBase58(solE2EBlockhash), solana.TransactionPayer(fromKey))
	if err != nil {
		return nil, err
	}
	raw, err := tx.Message.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return &types.UnsignedTx{ChainID: models.ChainSOL, RawBytes: raw}, nil
}

func newSolanaSigningChain(broadcasts *[]*types.SignedTx) *solanaSigningChain {
	mockChain := sweepMockChain(models.ChainSOL, models.NativeSOL)
	live := &chain.SolanaLive{}
	mockChain.BuildTransferFn = func(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
		return solanaTransferMessage(req.From, req.To, req.Amount)
	}
	mockChain.BuildSweepFn = func(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
		unsigned, err := solanaTransferMessage(req.From, req.To, req.Amount)
		if err != nil {
			return nil, err
		}
		return []types.UnsignedTx{*unsigned}, nil
	}
	mockChain.SignTransactionFn = live.SignTransaction
	mockChain.BroadcastTransactionFn = func(ctx context.Context, signed *types.SignedTx) (string, error) {
		*broadcasts = append(*broadcasts, signed)
		return signed.TxHash, nil
	}
	return &solanaSigningChain{MockChain: mockChain, live: live}
}

func newSolanaExecutor(f *solanaWalletFixture, adapter types.Chain) *service {
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	return &service{
		registry:   registry,
		mpc:        f.tss,
		walletRepo: &fakeWalletRepo{wallet: f.wallet},
		txRepo:     &fakeTxRepo{},
		fetchShareBFn: func(context.Context, *models.Wallet) ([]byte, error) {
			return append([]byte(nil), f.shareB...), nil
		},
	}
}

func assertSolanaSignedBy(t *testing.T, signed *types.SignedTx, address string) {
	t.Helper()
	tx, err := solana.TransactionFromDecoder(bin.NewBinDecoder(signed.RawBytes))
	if err != nil {
		t.Fatal(err)
	}
	signer := solana.MustPublicKeyFromBase58(address)
	if len(tx.Signatures) != 1 || len(tx.Message.AccountKeys) == 0 || !tx.Message.AccountKeys[0].Equals(signer) {
		t.Fatalf("transaction is not a single-signer transfer from %s", address)
	}
	message, err := tx.Message.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(signer[:], message, tx.Signatures[0][:]) {
		t.Fatalf("signature does not verify against %s", address)
	}
}

func TestExecutePlan_SolanaGenesisSignsWithReconstructedScalar(t *testing.T) {
	fixture := newSolanaWalletFixture(t)
	var broadcasts []*types.SignedTx
	svc := newSolanaExecutor(fixture, newSolanaSigningChain(&broadcasts))
	base := *fixture.wallet.DepositAddress
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainSOL, Asset: models.NativeSOL,
		Amount: big.NewInt(20_000_000), Strategy: StrategyDirectFromBase, SourceAddress: &base}

	result, err := svc.ExecutePlan(context.Background(), plan, fixture.credentials(t, solE2EPassphrase), uuid.New(), solE2EDestination, "user-sol")
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 1 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
	assertSolanaSignedBy(t, broadcasts[0], base.Address)
	if result.FinalWithdrawTx == nil || result.FinalWithdrawTx.TxHash != broadcasts[0].TxHash {
		t.Fatal("withdrawal row must carry the broadcast signature")
	}
}

func TestExecutePlan_SolanaChildSignsWithStoredSeed(t *testing.T) {
	fixture := newSolanaWalletFixture(t)
	var broadcasts []*types.SignedTx
	svc := newSolanaExecutor(fixture, newSolanaSigningChain(&broadcasts))
	child := fixture.child
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainSOL, Asset: models.NativeSOL,
		Amount: big.NewInt(20_000_000), Strategy: StrategyDirectFromChild, SourceAddress: &child}

	if _, err := svc.ExecutePlan(context.Background(), plan, fixture.credentials(t, solE2EPassphrase), uuid.New(), solE2EDestination, "user-sol"); err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 1 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
	assertSolanaSignedBy(t, broadcasts[0], child.Address)
}

func TestExecutePlan_SolanaMultiSweepSignsChildThenGenesis(t *testing.T) {
	fixture := newSolanaWalletFixture(t)
	var broadcasts []*types.SignedTx
	svc := newSolanaExecutor(fixture, newSolanaSigningChain(&broadcasts))
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainSOL, Asset: models.NativeSOL,
		Amount: big.NewInt(30_000_000), Strategy: StrategyMultiSweep,
		Sweeps: []PlannedSweep{{From: fixture.child, Amount: big.NewInt(10_000_000)}}}

	result, err := svc.ExecutePlan(context.Background(), plan, fixture.credentials(t, solE2EPassphrase), uuid.New(), solE2EDestination, "user-sol")
	if err != nil {
		t.Fatal(err)
	}
	if result.FailedStep != nil {
		t.Fatalf("sweep failed: %s", result.FailedStep.LastError)
	}
	if len(broadcasts) != 2 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
	assertSolanaSignedBy(t, broadcasts[0], fixture.child.Address)
	assertSolanaSignedBy(t, broadcasts[1], fixture.wallet.DepositAddress.Address)
}

func TestExecutePlan_SolanaChildRejectsWrongPassphrase(t *testing.T) {
	fixture := newSolanaWalletFixture(t)
	var broadcasts []*types.SignedTx
	svc := newSolanaExecutor(fixture, newSolanaSigningChain(&broadcasts))
	child := fixture.child
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainSOL, Asset: models.NativeSOL,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromChild, SourceAddress: &child}

	for _, passphrase := range []string{"", "not-the-passphrase"} {
		if _, err := svc.ExecutePlan(context.Background(), plan, fixture.credentials(t, passphrase), uuid.New(), solE2EDestination, "user-sol"); err == nil {
			t.Fatalf("passphrase %q must not unlock the child key", passphrase)
		}
	}
	if len(broadcasts) != 0 {
		t.Fatalf("nothing may be broadcast, got %d", len(broadcasts))
	}
}

func TestExecutePlan_SolanaChildWithoutStoredKeyFails(t *testing.T) {
	fixture := newSolanaWalletFixture(t)
	var broadcasts []*types.SignedTx
	svc := newSolanaExecutor(fixture, newSolanaSigningChain(&broadcasts))
	child := fixture.child
	child.EncryptedPrivateKey = ""
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainSOL, Asset: models.NativeSOL,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromChild, SourceAddress: &child}

	if _, err := svc.ExecutePlan(context.Background(), plan, fixture.credentials(t, solE2EPassphrase), uuid.New(), solE2EDestination, "user-sol"); err == nil {
		t.Fatal("expected an error for a child without a stored key")
	}
	if len(broadcasts) != 0 {
		t.Fatalf("nothing may be broadcast, got %d", len(broadcasts))
	}
}

func TestExecutePlan_SolanaGenesisNeedsScalarSigner(t *testing.T) {
	fixture := newSolanaWalletFixture(t)
	var broadcasts []*types.SignedTx
	signingChain := newSolanaSigningChain(&broadcasts)
	svc := newSolanaExecutor(fixture, signingChain.MockChain)
	base := *fixture.wallet.DepositAddress
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainSOL, Asset: models.NativeSOL,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromBase, SourceAddress: &base}

	if _, err := svc.ExecutePlan(context.Background(), plan, fixture.credentials(t, solE2EPassphrase), uuid.New(), solE2EDestination, "user-sol"); err == nil {
		t.Fatal("expected an error when the chain cannot sign with a scalar")
	}
	if signingChain.SignTransactionCalls != 0 || len(broadcasts) != 0 {
		t.Fatal("genesis must never fall back to seed signing")
	}
}

func runSolanaPlanExpectingNoBroadcast(t *testing.T, fixture *solanaWalletFixture, plan *Plan, wantError string) {
	t.Helper()
	var broadcasts []*types.SignedTx
	svc := newSolanaExecutor(fixture, newSolanaSigningChain(&broadcasts))
	_, err := svc.ExecutePlan(context.Background(), plan, fixture.credentials(t, solE2EPassphrase), uuid.New(), solE2EDestination, "user-sol")
	if err == nil || !strings.Contains(err.Error(), wantError) {
		t.Fatalf("expected an error containing %q, got %v", wantError, err)
	}
	if len(broadcasts) != 0 {
		t.Fatalf("nothing may be broadcast, got %d", len(broadcasts))
	}
}

func TestExecutePlan_SolanaBaseRowWithForeignAddressIsNotSignedAsGenesis(t *testing.T) {
	fixture := newSolanaWalletFixture(t)
	impostor := *fixture.wallet.DepositAddress
	impostor.Address = fixture.child.Address
	runSolanaPlanExpectingNoBroadcast(t, fixture, &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainSOL, Asset: models.NativeSOL,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromBase, SourceAddress: &impostor}, "has no stored signing key")
}

func TestExecutePlan_SolanaWalletPublicKeyMismatchFails(t *testing.T) {
	fixture := newSolanaWalletFixture(t)
	other, err := fixture.tss.Keygen(context.Background(), mpcpkg.CurveEd25519)
	if err != nil {
		t.Fatal(err)
	}
	fixture.wallet.MPCPublicKey = hex.EncodeToString(other.CombinedPubKey)
	base := *fixture.wallet.DepositAddress
	runSolanaPlanExpectingNoBroadcast(t, fixture, &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainSOL, Asset: models.NativeSOL,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromBase, SourceAddress: &base}, "fee payer is not the signing key")
}

func TestExecutePlan_SolanaChildWithAnotherChildsSeedFails(t *testing.T) {
	fixture := newSolanaWalletFixture(t)
	sibling := fixture.deriveChild(t, solE2EChildIndex+1)
	swapped := fixture.child
	swapped.EncryptedPrivateKey, swapped.EncryptionIV, swapped.EncryptionSalt = sibling.EncryptedPrivateKey, sibling.EncryptionIV, sibling.EncryptionSalt
	runSolanaPlanExpectingNoBroadcast(t, fixture, &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainSOL, Asset: models.NativeSOL,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromChild, SourceAddress: &swapped}, "stored key belongs to another address")
}

func TestExecutePlan_SolanaChildWithCorruptIVFails(t *testing.T) {
	fixture := newSolanaWalletFixture(t)
	corrupt := fixture.child
	corrupt.EncryptionIV = "00"
	runSolanaPlanExpectingNoBroadcast(t, fixture, &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainSOL, Asset: models.NativeSOL,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromChild, SourceAddress: &corrupt}, "iv must be 12 bytes")
}
