package repositories_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	evmtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/hdkey"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
	runtime "github.com/macrowallets/waas/app/services/withdrawrecoveryruntime"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

type recoveryRuntimeAdapter struct{ types.Chain }

func (recoveryRuntimeAdapter) EstimateGasPrice(context.Context) (*big.Int, error) {
	return big.NewInt(2), nil
}
func (recoveryRuntimeAdapter) EstimateTransferGasLimit(context.Context, types.TransferRequest) (uint64, error) {
	return 100, nil
}
func (recoveryRuntimeAdapter) EstimateL1DataFee(context.Context, types.TransferRequest) (*big.Int, error) {
	return big.NewInt(7), nil
}

type recoveryRuntimeRegistry struct {
	adapter types.Chain
	token   *types.Token
}

func (r recoveryRuntimeRegistry) ChainForWallet(*models.Wallet) (types.Chain, error) {
	return r.adapter, nil
}
func (r recoveryRuntimeRegistry) FindToken(string, string) (*types.Token, error) { return r.token, nil }

type recoveryRuntimeRPC struct {
	sendErr error
	sends   int
	raws    []string
}

func (r *recoveryRuntimeRPC) Call(_ context.Context, method string, out interface{}, params ...interface{}) error {
	var value interface{}
	switch method {
	case "eth_chainId":
		value = "0x66eee"
	case "eth_getTransactionCount":
		value = "0x0"
	case "eth_getTransactionByHash", "eth_getTransactionReceipt":
		value = nil
	case "eth_getBalance":
		value = "0x1000000"
	case "eth_call":
		value = "0xa7d8c0"
		if params[0].(map[string]string)["data"] == "0x313ce567" {
			value = "0x6"
		}
	case "eth_sendRawTransaction":
		r.sends++
		r.raws = append(r.raws, params[0].(string))
		if r.sendErr != nil {
			return r.sendErr
		}
		raw, _ := hex.DecodeString(strings.TrimPrefix(params[0].(string), "0x"))
		var tx evmtypes.Transaction
		if err := tx.UnmarshalBinary(raw); err != nil {
			return err
		}
		value = tx.Hash().Hex()
	default:
		return fmt.Errorf("unexpected synthetic RPC method")
	}
	data, _ := json.Marshal(value)
	return json.Unmarshal(data, out)
}

type recoveryRuntimeSigner struct {
	signed *types.SignedTx
	calls  int
}

func (s *recoveryRuntimeSigner) SignEVMRecoveryCall(context.Context, uuid.UUID, uuid.UUID, int64, chain.EVMCall, string) (*types.SignedTx, error) {
	s.calls++
	copy := *s.signed
	copy.RawBytes = append([]byte(nil), s.signed.RawBytes...)
	return &copy, nil
}

type recoveryRuntimeSeal struct{}

func (recoveryRuntimeSeal) Seal(s string) (string, error) { return "synthetic:" + s, nil }
func (recoveryRuntimeSeal) Open(s string) (string, error) {
	return strings.TrimPrefix(s, "synthetic:"), nil
}

// This exercises the real domain + PostgreSQL journal + runtime sender together.
// RPC/signing are synthetic; no external network or financial call is possible.
func TestRecoveryRuntimeResumesActualUnknownRepositoryStateWithSameBytes(t *testing.T) {
	store, row, b := recoveryRows(t)
	ctx := context.Background()
	q := facades.Orm().Query()
	parentKey, err := crypto.HexToECDSA(strings.Repeat("0", 63) + "1")
	require.NoError(t, err)
	public := crypto.CompressPubkey(&parentKey.PublicKey)
	code := strings.Repeat("11", 32)
	chainCode, _ := hex.DecodeString(code)
	child, err := hdkey.DeriveSecp256k1Child(public, chainCode, 1)
	require.NoError(t, err)
	private, err := hdkey.ChildPrivateKey(parentKey.D.FillBytes(make([]byte, 32)), child.Tweak)
	require.NoError(t, err)
	key, err := crypto.ToECDSA(private)
	require.NoError(t, err)
	address := crypto.PubkeyToAddress(key.PublicKey).Hex()
	_, err = q.Model(&models.Wallet{}).Where("id = ?", b.WalletID).Update(map[string]any{"mpc_public_key": hex.EncodeToString(public), "mpc_chain_code": code})
	require.NoError(t, err)
	_, err = q.Model(&models.Address{}).Where("id = ?", b.SourceAddressID).Update(map[string]any{"address": address, "derivation_type": "bip32"})
	require.NoError(t, err)
	_, err = q.Model(&models.Transaction{}).Where("id = ?", b.OriginalTransactionID).Update("from_address", address)
	require.NoError(t, err)
	require.NoError(t, q.Create(&models.Token{ID: uuid.New(), ChainID: b.Chain, Symbol: b.Asset, ContractAddress: b.TokenContract, Decimals: 6, Status: models.TokenStatusActive}))
	a, z := &recoveryRuntimeRPC{sendErr: errors.New("synthetic unknown transport")}, &recoveryRuntimeRPC{}
	observer, err := runtime.New(runtime.Dependencies{Attempts: store, Withdrawals: repositories.NewWithdrawalRepository(nil), Transactions: repositories.NewTransactionRepository(nil), Wallets: repositories.NewWalletRepository(nil), Addresses: repositories.NewAddressRepository(nil), Accounts: repositories.NewAccountRepository(nil), Chains: repositories.NewChainRepository(nil), Tokens: repositories.NewTokenRepository(nil), Registry: recoveryRuntimeRegistry{adapter: recoveryRuntimeAdapter{mocks.NewMockChain("arbitrum")}, token: &types.Token{ChainID: b.Chain, Symbol: b.Asset, Contract: b.TokenContract, Decimals: 6}}, Endpoints: [2]runtime.Endpoint{{Origin: "https://a.test", Client: a}, {Origin: "https://b.test", Client: z}}, ProviderOrigin: "https://provider.test", CodeRevision: "synthetic-revision"})
	require.NoError(t, err)
	b, err = observer.ReadBinding(ctx, b.OperationID, b.OriginalTransactionID)
	require.NoError(t, err)
	units, _ := new(big.Int).SetString(b.AmountRaw, 10)
	data := make([]byte, 68)
	copy(data, []byte{0xa9, 0x05, 0x9c, 0xbb})
	copy(data[16:36], common.HexToAddress(b.Recipient).Bytes())
	units.FillBytes(data[36:])
	to := common.HexToAddress(b.TokenContract)
	signed, err := evmtypes.SignTx(evmtypes.NewTx(&evmtypes.LegacyTx{Nonce: *b.Nonce, Gas: 100, GasPrice: big.NewInt(2), To: &to, Value: big.NewInt(0), Data: data}), evmtypes.LatestSignerForChainID(big.NewInt(b.NetworkID)), key)
	require.NoError(t, err)
	raw, err := signed.MarshalBinary()
	require.NoError(t, err)
	signer := &recoveryRuntimeSigner{signed: &types.SignedTx{ChainID: b.Chain, RawBytes: raw, TxHash: signed.Hash().Hex()}}
	service, err := recovery.NewService(recovery.Dependencies{Store: store, Observer: observer, Sender: observer, Signer: signer, Sealer: recoveryRuntimeSeal{}, CodeRevision: "synthetic-revision"})
	require.NoError(t, err)
	proposal, err := service.Inspect(ctx, b, recovery.FeeEnvelope{MaxGasLimit: 100, MaxGasPriceWei: "2", MaxNativeFeeWei: "207"}, nil, "synthetic-preparer")
	require.NoError(t, err)
	hash, _ := recovery.Hash(proposal)
	review := recovery.Review{ProposalHash: hash, EvidenceHash: strings.Repeat("a", 64), ReviewedBy: "independent-reviewer", ReviewedAt: time.Now().UTC(), ExpiresAt: proposal.ExpiresAt, Approved: true}
	result, err := service.Apply(ctx, *proposal, review, "synthetic-operator", "synthetic-passphrase")
	require.ErrorIs(t, err, recovery.ErrNeedsReconciliation)
	require.Equal(t, models.RecoverySendUnknown, result.State)
	persisted, err := repositories.NewWithdrawalRepository(nil).FindByID(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, models.WithdrawalStatusBroadcast, persisted.Status)
	candidate, err := repositories.NewTransactionRepository(nil).FindByID(ctx, *persisted.TransactionID)
	require.NoError(t, err)
	require.Contains(t, candidate.RawPayload, `"broadcast_outcome": "unknown"`)
	a.sendErr = nil
	result, err = service.Resume(ctx, b.OperationID, "synthetic-operator", "synthetic-passphrase")
	require.NoError(t, err)
	require.Equal(t, models.RecoveryBroadcast, result.State)
	require.Equal(t, 1, signer.calls)
	require.Equal(t, 2, a.sends)
	require.Zero(t, z.sends)
	require.Equal(t, a.raws[0], a.raws[1])
	require.Equal(t, signed.Hash().Hex(), result.CandidateHash)
	_ = row
}
