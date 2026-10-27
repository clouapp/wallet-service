package withdrawrecoveryruntime

import (
	"context"
	"crypto/ecdsa"
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
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/hdkey"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

type uuidRows[T any] map[uuid.UUID]*T

func (r uuidRows[T]) FindByID(_ context.Context, id uuid.UUID) (*T, error) {
	if v := r[id]; v != nil {
		return v, nil
	}
	return nil, errors.New("missing synthetic row")
}

type attemptRows map[uuid.UUID]*models.WithdrawalRecoveryAttempt

func (r attemptRows) FindByOperation(_ context.Context, id uuid.UUID) (*models.WithdrawalRecoveryAttempt, error) {
	return r[id], nil
}

type chainRows map[string]*models.Chain

func (r chainRows) FindByID(_ context.Context, id string) (*models.Chain, error) { return r[id], nil }

type tokenRows []*models.Token

func (r tokenRows) FindByChainID(context.Context, string) ([]models.Token, error) {
	var out []models.Token
	for _, t := range r {
		out = append(out, *t)
	}
	return out, nil
}

type quoteAdapter struct {
	types.Chain
	lastL1     types.TransferRequest
	quoteError error
	gasLimit   uint64
	l1Hook     func(types.TransferRequest) *big.Int
}

func (a *quoteAdapter) EstimateGasPrice(context.Context) (*big.Int, error) {
	return big.NewInt(2), a.quoteError
}
func (a *quoteAdapter) EstimateTransferGasLimit(context.Context, types.TransferRequest) (uint64, error) {
	if a.gasLimit != 0 {
		return a.gasLimit, a.quoteError
	}
	return 100, a.quoteError
}
func (a *quoteAdapter) EstimateL1DataFee(_ context.Context, req types.TransferRequest) (*big.Int, error) {
	a.lastL1 = req
	if a.l1Hook != nil {
		return a.l1Hook(req), a.quoteError
	}
	return big.NewInt(7), a.quoteError
}

type registry struct {
	adapter types.Chain
	token   *types.Token
}

func (r *registry) ChainForWallet(*models.Wallet) (types.Chain, error) { return r.adapter, nil }
func (r *registry) FindToken(string, string) (*types.Token, error)     { return r.token, nil }

type fakeRPC struct {
	network         int64
	latest, pending uint64
	native, tokens  string
	decimals        int
	known, receipts map[string]bool
	bad             error
	calls, sends    int
	raw             string
	before          func(string, []interface{})
	responseHash    string
}

func (r *fakeRPC) Call(_ context.Context, method string, out interface{}, args ...interface{}) error {
	r.calls++
	if r.before != nil {
		r.before(method, args)
	}
	if r.bad != nil {
		return r.bad
	}
	var value interface{}
	switch method {
	case "eth_chainId":
		value = fmt.Sprintf("0x%x", r.network)
	case "eth_getTransactionCount":
		n := r.latest
		if args[1] == "pending" {
			n = r.pending
		}
		value = fmt.Sprintf("0x%x", n)
	case "eth_getTransactionByHash":
		if r.known[args[0].(string)] {
			value = map[string]string{"hash": args[0].(string)}
		}
	case "eth_getTransactionReceipt":
		if r.receipts[args[0].(string)] {
			value = map[string]string{"transactionHash": args[0].(string)}
		}
	case "eth_getBalance":
		value = r.native
	case "eth_call":
		req := args[0].(map[string]string)
		value = r.tokens
		if req["data"] == "0x313ce567" {
			value = fmt.Sprintf("0x%x", r.decimals)
		}
	case "eth_sendRawTransaction":
		r.sends++
		r.raw = args[0].(string)
		var tx evmtypes.Transaction
		b, _ := hex.DecodeString(strings.TrimPrefix(r.raw, "0x"))
		if tx.UnmarshalBinary(b) != nil {
			return errors.New("invalid synthetic send")
		}
		value = tx.Hash().Hex()
		if r.responseHash != "" {
			value = r.responseHash
		}
	default:
		return errors.New("unexpected RPC method")
	}
	b, _ := json.Marshal(value)
	return json.Unmarshal(b, out)
}

type fixture struct {
	runtime      *Runtime
	deps         Dependencies
	withdrawal   *models.Withdrawal
	original     *models.Transaction
	wallet       *models.Wallet
	source       *models.Address
	account      *models.Account
	network      *models.Chain
	token        *models.Token
	registry     *registry
	adapter      *quoteAdapter
	rpcs         [2]*fakeRPC
	transactions uuidRows[models.Transaction]
	key          *ecdsa.PrivateKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := crypto.HexToECDSA(strings.Repeat("0", 63) + "1")
	require.NoError(t, err)
	pub := crypto.CompressPubkey(&key.PublicKey)
	code := strings.Repeat("11", 32)
	cc, _ := hex.DecodeString(code)
	child, err := hdkey.DeriveSecp256k1Child(pub, cc, 1)
	require.NoError(t, err)
	private, err := hdkey.ChildPrivateKey(key.D.FillBytes(make([]byte, 32)), child.Tweak)
	require.NoError(t, err)
	childKey, err := crypto.ToECDSA(private)
	require.NoError(t, err)
	accountID, walletID, addressID, opID, txID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	networkID := int64(421614)
	recipient := "0x" + strings.Repeat("2", 40)
	contract := "0x" + strings.Repeat("3", 40)
	f := &fixture{key: childKey}
	f.account = &models.Account{ID: accountID, Environment: models.EnvironmentTest, Status: models.StatusActive}
	f.wallet = &models.Wallet{ID: walletID, AccountID: &accountID, Chain: "arbitrum", Status: "active", Label: "arbitrum_deposit", MPCPublicKey: hex.EncodeToString(pub), MPCChainCode: code}
	f.source = &models.Address{ID: addressID, WalletID: walletID, Chain: "arbitrum", Address: crypto.PubkeyToAddress(childKey.PublicKey).Hex(), DerivationIndex: 1, DerivationType: "bip32", ExternalUserID: "2", IsActive: true}
	f.network = &models.Chain{ID: "arbitrum", NetworkID: &networkID, AdapterType: models.AdapterTypeEVM, Status: models.StatusActive, IsTestnet: true, RequiredConfirmations: 120, Finality: models.FinalityConfirmations}
	f.token = &models.Token{ID: uuid.New(), ChainID: "arbitrum", Symbol: "USDC", ContractAddress: contract, Decimals: 6, Status: models.TokenStatusActive}
	f.original = &models.Transaction{ID: txID, WalletID: walletID, AddressID: &addressID, Chain: "arbitrum", TxType: models.TxTypeWithdrawal, TxHash: "0x" + strings.Repeat("4", 64), FromAddress: f.source.Address, ToAddress: recipient, Amount: "6100000", Asset: "USDC", TokenContract: contract, Status: "failed", ErrorMessage: "broadcast_refused", RawPayload: `{"broadcast_outcome":"refused","sender_nonce":0}`, RequiredConfs: 120, Finality: models.FinalityConfirmations}
	f.withdrawal = &models.Withdrawal{ID: opID, WalletID: walletID, AccountID: &accountID, TransactionID: &txID, Status: "failed", Amount: "6.100000000000000000", DestinationAddress: recipient}
	f.transactions = uuidRows[models.Transaction]{txID: f.original}
	f.adapter = &quoteAdapter{Chain: mocks.NewMockChain("arbitrum")}
	f.registry = &registry{adapter: f.adapter, token: &types.Token{ChainID: "arbitrum", Symbol: "USDC", Contract: contract, Decimals: 6}}
	for i := range f.rpcs {
		f.rpcs[i] = &fakeRPC{network: networkID, native: "0x1000000", tokens: "0xa7d8c0", decimals: 6, known: map[string]bool{}, receipts: map[string]bool{}}
	}
	f.deps = Dependencies{Attempts: attemptRows{}, Withdrawals: uuidRows[models.Withdrawal]{opID: f.withdrawal}, Transactions: f.transactions, Wallets: uuidRows[models.Wallet]{walletID: f.wallet}, Addresses: uuidRows[models.Address]{addressID: f.source}, Accounts: uuidRows[models.Account]{accountID: f.account}, Chains: chainRows{"arbitrum": f.network}, Tokens: tokenRows{f.token}, Registry: f.registry, Endpoints: [2]Endpoint{{Origin: "https://rpc-a.test", Client: f.rpcs[0]}, {Origin: "https://rpc-b.test", Client: f.rpcs[1]}}, ProviderOrigin: "https://provider.test", CodeRevision: "synthetic-revision"}
	f.runtime, err = New(f.deps)
	require.NoError(t, err)
	return f
}
func (f *fixture) binding(t *testing.T) recovery.Binding {
	t.Helper()
	b, err := f.runtime.ReadBinding(context.Background(), f.withdrawal.ID, f.original.ID)
	require.NoError(t, err)
	return b
}
func (f *fixture) signed(t *testing.T, b recovery.Binding, mutate func(*evmtypes.LegacyTx)) []byte {
	t.Helper()
	n, _ := new(big.Int).SetString(b.AmountRaw, 10)
	data := make([]byte, 68)
	copy(data, []byte{0xa9, 0x05, 0x9c, 0xbb})
	copy(data[16:36], common.HexToAddress(b.Recipient).Bytes())
	n.FillBytes(data[36:])
	to := common.HexToAddress(b.TokenContract)
	tx := &evmtypes.LegacyTx{Nonce: *b.Nonce, To: &to, Value: big.NewInt(0), Gas: 100, GasPrice: big.NewInt(2), Data: data}
	if mutate != nil {
		mutate(tx)
	}
	signed, err := evmtypes.SignTx(evmtypes.NewTx(tx), evmtypes.LatestSignerForChainID(big.NewInt(b.NetworkID)), f.key)
	require.NoError(t, err)
	raw, err := signed.MarshalBinary()
	require.NoError(t, err)
	return raw
}
func (f *fixture) link(t *testing.T, b recovery.Binding, raw []byte) {
	t.Helper()
	var signed evmtypes.Transaction
	require.NoError(t, signed.UnmarshalBinary(raw))
	id := uuid.New()
	attemptID := uuid.New()
	parent := f.original.ID
	source := f.source.ID
	op := b.OperationID.String()
	f.transactions[id] = &models.Transaction{ID: id, WalletID: b.WalletID, AddressID: &source, ParentTransactionID: &parent, IdempotencyKey: &op, TxType: models.TxTypeWithdrawal, Status: "pending", ExternalUserID: b.SourceExternalUserID, RequiredConfs: b.RequiredConfirmations, Finality: b.Finality, Chain: b.Chain, FromAddress: b.SourceAddress, ToAddress: b.Recipient, Amount: b.AmountRaw, Asset: b.Asset, TokenContract: b.TokenContract, TxHash: signed.Hash().Hex(), RawPayload: fmt.Sprintf(`{"recovery_attempt_id":%q,"recovery_operation_id":%q,"recovery_predecessor_transaction_id":%q,"sender_nonce":%d,"recovery_binding_hash":%q,"broadcast_outcome":"pending","signed_tx":"enc:v1:synthetic"}`, attemptID.String(), op, parent.String(), *b.Nonce, digest(b))}
	f.withdrawal.TransactionID = &id
	f.withdrawal.Status = "broadcasting"
	proposal := recovery.Proposal{Binding: b, Fees: recovery.FeeEnvelope{MaxGasLimit: 100, MaxGasPriceWei: "2", MaxNativeFeeWei: "207"}, CodeRevision: f.deps.CodeRevision, PreparedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(20 * time.Minute)}
	proposalJSON, _ := recovery.Canonical(proposal)
	reviewJSON, _ := recovery.Canonical(recovery.Review{ProposalHash: digest(proposal), EvidenceHash: strings.Repeat("a", 64), ReviewedBy: "synthetic-independent-reviewer", ReviewedAt: proposal.PreparedAt, ExpiresAt: proposal.ExpiresAt, Approved: true})
	callJSON, _ := recovery.Canonical(map[string]any{"nonce": *b.Nonce, "to": b.TokenContract, "value": "0", "data": hex.EncodeToString(signed.Data()), "gas_limit": signed.Gas(), "gas_price": signed.GasPrice().String(), "l1_fee": "7"})
	f.deps.Attempts.(attemptRows)[b.OperationID] = &models.WithdrawalRecoveryAttempt{ID: attemptID, OperationID: b.OperationID, OriginalTransactionID: b.OriginalTransactionID, AccountID: b.AccountID, WalletID: b.WalletID, SourceAddressID: b.SourceAddressID, NetworkID: b.NetworkID, Sender: b.SourceAddress, SenderNonce: fmt.Sprint(*b.Nonce), BindingHash: digest(b), ProposalJSON: proposalJSON, ProposalHash: digest(proposal), ReviewJSON: reviewJSON, CallJSON: callJSON, State: models.RecoverySendClaimed, CandidateTransactionID: &id, CandidateHash: signed.Hash().Hex(), SealedSignedTx: "enc:v1:synthetic"}
}

func TestObserver_DerivesBindingAndQuotesActualChildWithoutWrites(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	require.Equal(t, "6100000", b.AmountRaw)
	require.NotNil(t, b.Nonce)
	require.Zero(t, *b.Nonce)
	o, err := f.runtime.Observe(context.Background(), b, "")
	require.NoError(t, err)
	require.True(t, o.RPCsAgree)
	require.False(t, o.ReceiptObserved)
	require.False(t, o.OriginalKnown)
	require.Equal(t, "11000000", o.SourceTokenBalanceRaw)
	require.Equal(t, uint64(100), *f.adapter.lastL1.GasLimit)
	require.Equal(t, b.SourceAddress, f.adapter.lastL1.From)
	require.Equal(t, b.Recipient, f.adapter.lastL1.To)
	require.Equal(t, "2", f.adapter.lastL1.GasPrice.String())
	require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
}
func TestObserver_DoesNotTrustCallerEconomicHash(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	b.RequestHash = strings.Repeat("a", 64)
	_, err := f.runtime.Observe(context.Background(), b, "")
	require.Error(t, err)
	require.Zero(t, f.rpcs[0].calls)
}
func TestObserver_RejectsForeignOrChangedPublicBindings(t *testing.T) {
	for _, name := range []string{"account", "production", "wallet", "frozen", "inactive child", "derivation", "child index", "original amount", "withdrawal amount", "contract", "decimals", "inactive token", "mainnet", "chain type", "zero network", "finality", "missing nonce", "recipient"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			b := f.binding(t)
			switch name {
			case "account":
				id := uuid.New()
				f.wallet.AccountID = &id
			case "production":
				f.account.Environment = "prod"
			case "wallet":
				f.wallet.Status = "frozen"
			case "frozen":
				now := f.runtime.deps.Now()
				f.wallet.FrozenUntil = &now
			case "inactive child":
				f.source.IsActive = false
			case "derivation":
				f.source.Address = "0x" + strings.Repeat("8", 40)
				f.original.FromAddress = f.source.Address
			case "child index":
				f.source.DerivationIndex = 2
			case "original amount":
				f.original.Amount = "6100001"
			case "withdrawal amount":
				f.withdrawal.Amount = "6.2"
			case "contract":
				f.token.ContractAddress = "0x" + strings.Repeat("7", 40)
			case "decimals":
				f.token.Decimals = 18
			case "inactive token":
				f.token.Status = "inactive"
			case "mainnet":
				f.network.IsTestnet = false
			case "chain type":
				f.network.AdapterType = "tron"
			case "zero network":
				n := int64(0)
				f.network.NetworkID = &n
			case "finality":
				f.network.RequiredConfirmations = 1
			case "missing nonce":
				f.original.RawPayload = `{"broadcast_outcome":"refused"}`
			case "recipient":
				f.withdrawal.DestinationAddress = "0x" + strings.Repeat("9", 40)
			}
			_, err := f.runtime.Observe(context.Background(), b, "")
			require.Error(t, err)
			require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
		})
	}
}
func TestObserver_RefusesRPCDriftAndNeverClaimsAbsenceFinality(t *testing.T) {
	for _, name := range []string{"network", "nonce", "token precision", "native balance", "token balance", "known", "receipt", "outage"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			b := f.binding(t)
			switch name {
			case "network":
				f.rpcs[1].network = 1
			case "nonce":
				f.rpcs[1].pending = 1
			case "token precision":
				f.rpcs[1].decimals = 18
			case "native balance":
				f.rpcs[1].native = "0x0"
			case "token balance":
				f.rpcs[1].tokens = "0x1"
			case "known":
				f.rpcs[1].known[b.OriginalTxHash] = true
			case "receipt":
				f.rpcs[1].receipts[b.OriginalTxHash] = true
			case "outage":
				f.rpcs[1].bad = errors.New("private endpoint credential")
			}
			o, err := f.runtime.Observe(context.Background(), b, "")
			if err == nil {
				require.False(t, o.RPCsAgree)
			} else {
				require.NotContains(t, err.Error(), "private endpoint credential")
			}
			require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
		})
	}
}
func TestObserver_PreservesOriginalBindingAfterCandidateLink(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	raw := f.signed(t, b, nil)
	f.link(t, b, raw)
	o, err := f.runtime.Observe(context.Background(), b, "")
	require.NoError(t, err)
	require.Equal(t, b, o.Binding)
	var tx evmtypes.Transaction
	require.NoError(t, tx.UnmarshalBinary(raw))
	f.rpcs[0].known[tx.Hash().Hex()] = true
	o, err = f.runtime.Observe(context.Background(), b, "")
	require.NoError(t, err)
	require.True(t, o.CandidateKnown)
	require.Equal(t, b.OriginalTransactionID, o.Binding.OriginalTransactionID)
}
func TestSender_OnlyTransmitsDurablyLinkedExactBytesOnce(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	raw := f.signed(t, b, nil)
	_, err := f.runtime.SendExact(context.Background(), b, raw)
	require.Error(t, err)
	require.Zero(t, f.rpcs[0].sends)
	f.link(t, b, raw)
	hash, err := f.runtime.SendExact(context.Background(), b, raw)
	require.NoError(t, err)
	var tx evmtypes.Transaction
	require.NoError(t, tx.UnmarshalBinary(raw))
	require.Equal(t, tx.Hash().Hex(), hash)
	require.Equal(t, "0x"+hex.EncodeToString(raw), f.rpcs[0].raw)
	require.Equal(t, 1, f.rpcs[0].sends)
	require.Zero(t, f.rpcs[1].sends)
}
func TestSender_RejectsChangedEconomicPayloadBeforeAnySend(t *testing.T) {
	for _, name := range []string{"nonce", "recipient", "amount", "contract", "native value", "sender"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			b := f.binding(t)
			if name == "sender" {
				key, err := crypto.GenerateKey()
				require.NoError(t, err)
				f.key = key
			}
			raw := f.signed(t, b, func(tx *evmtypes.LegacyTx) {
				switch name {
				case "nonce":
					tx.Nonce = 1
				case "recipient":
					tx.Data[35] ^= 1
				case "amount":
					tx.Data[67] ^= 1
				case "contract":
					a := common.HexToAddress("0x" + strings.Repeat("9", 40))
					tx.To = &a
				case "native value":
					tx.Value = big.NewInt(1)
				}
			})
			f.link(t, b, raw)
			_, err := f.runtime.SendExact(context.Background(), b, raw)
			require.Error(t, err)
			require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
		})
	}
}
func TestSender_FreshNonceDriftAndKnownReceiptBlockTransmission(t *testing.T) {
	for _, name := range []string{"latest", "pending", "known original", "known candidate", "receipt", "no native gas", "final nonce race"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			b := f.binding(t)
			raw := f.signed(t, b, nil)
			f.link(t, b, raw)
			var tx evmtypes.Transaction
			require.NoError(t, tx.UnmarshalBinary(raw))
			switch name {
			case "latest":
				f.rpcs[0].latest = 1
				f.rpcs[1].latest = 1
			case "pending":
				f.rpcs[0].pending = 1
				f.rpcs[1].pending = 1
			case "known original":
				f.rpcs[0].known[b.OriginalTxHash] = true
				f.rpcs[1].known[b.OriginalTxHash] = true
			case "known candidate":
				f.rpcs[0].known[tx.Hash().Hex()] = true
				f.rpcs[1].known[tx.Hash().Hex()] = true
			case "receipt":
				f.rpcs[0].receipts[b.OriginalTxHash] = true
				f.rpcs[1].receipts[b.OriginalTxHash] = true
			case "no native gas":
				f.rpcs[0].native = "0x0"
				f.rpcs[1].native = "0x0"
			case "final nonce race":
				reads := 0
				f.rpcs[1].before = func(method string, _ []interface{}) {
					if method == "eth_getTransactionCount" {
						reads++
						if reads >= 3 {
							f.rpcs[1].latest = 1
						}
					}
				}
			}
			_, err := f.runtime.SendExact(context.Background(), b, raw)
			require.Error(t, err)
			require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
		})
	}
}
func TestConstructor_RejectsDuplicateOrSecretEndpointOrigins(t *testing.T) {
	for _, origin := range []string{"https://rpc-a.test", "https://rpc-a.test:443", "https://rpc-a.test:8443", "https://user:password@rpc-c.test", "https://rpc-c.test/v2/private-key", "https://rpc-c.test?key=secret", "http://rpc-c.test"} {
		f := newFixture(t)
		f.deps.Endpoints[1].Origin = origin
		_, err := New(f.deps)
		require.Error(t, err)
	}
}

func TestObserver_RefusesRegistryQuoteAndLocalDriftDuringRPCReads(t *testing.T) {
	for _, name := range []string{"runtime contract", "runtime token chain", "quote failure", "local drift during reads", "duplicate token", "same RPC client"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			b := f.binding(t)
			switch name {
			case "runtime contract":
				f.registry.token.Contract = "0x" + strings.Repeat("9", 40)
			case "runtime token chain":
				f.registry.token.ChainID = "base"
			case "quote failure":
				f.adapter.quoteError = errors.New("credential-bearing URL must not leak")
			case "duplicate token":
				f.runtime.deps.Tokens = tokenRows{f.token, f.token}
			case "local drift during reads":
				f.rpcs[1].before = func(method string, _ []interface{}) {
					if method == "eth_call" {
						f.wallet.Label = "changed-after-inspection"
					}
				}
			case "same RPC client":
				f.deps.Endpoints[1].Client = f.rpcs[0]
				_, err := New(f.deps)
				require.Error(t, err)
				return
			}
			_, err := f.runtime.Observe(context.Background(), b, "")
			require.Error(t, err)
			require.NotContains(t, err.Error(), "credential-bearing")
			require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
		})
	}
}
func TestObserver_ZeroNativeGasIsObservedWithoutAnyFinancialAction(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	for _, rpc := range f.rpcs {
		rpc.native = "0x0"
	}
	o, err := f.runtime.Observe(context.Background(), b, "")
	require.NoError(t, err)
	require.Equal(t, "0", o.SourceNativeBalanceWei)
	require.False(t, o.OriginalKnown)
	require.False(t, o.ReceiptObserved)
	require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
}
func TestSender_UnknownReplyIsNotRetriedOrRebuilt(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	raw := f.signed(t, b, nil)
	f.link(t, b, raw)
	f.rpcs[0].responseHash = "0x" + strings.Repeat("a", 64)
	_, err := f.runtime.SendExact(context.Background(), b, raw)
	require.ErrorIs(t, err, recovery.ErrNeedsReconciliation)
	require.Equal(t, 1, f.rpcs[0].sends)
	require.Zero(t, f.rpcs[1].sends)
}
func TestObserver_DoesNotFollowAnUnrelatedCurrentCandidatePointer(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	raw := f.signed(t, b, nil)
	f.link(t, b, raw)
	current := f.transactions[*f.withdrawal.TransactionID]
	current.ParentTransactionID = nil
	_, err := f.runtime.Observe(context.Background(), b, "")
	require.Error(t, err)
	require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
}

func TestObserver_KnownTransferIsReconciliationEvenWhenItCannotBeQuotedAgain(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	f.adapter.quoteError = errors.New("source no longer holds requested amount")
	for _, rpc := range f.rpcs {
		rpc.known[b.OriginalTxHash] = true
		rpc.latest = 1
		rpc.pending = 1
	}
	o, err := f.runtime.Observe(context.Background(), b, "")
	require.NoError(t, err)
	require.True(t, o.OriginalKnown)
	require.Equal(t, uint64(1), *o.LatestNonce)
	require.Zero(t, o.Quote.GasLimit)
	require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
}
func TestSender_CancelledOrAlreadyFailedCandidateIsNeverTransmitted(t *testing.T) {
	for _, state := range []string{"cancelled", "failed", "confirmed"} {
		t.Run(state, func(t *testing.T) {
			f := newFixture(t)
			b := f.binding(t)
			raw := f.signed(t, b, nil)
			f.link(t, b, raw)
			f.withdrawal.Status = state
			_, err := f.runtime.SendExact(context.Background(), b, raw)
			require.ErrorIs(t, err, recovery.ErrNeedsReconciliation)
			require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
		})
	}
}

func TestSender_VerifiesAndSendsAnOwnedImmutableCopyOfTheBytes(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	raw := f.signed(t, b, nil)
	f.link(t, b, raw)
	expected := "0x" + hex.EncodeToString(raw)
	mutated := false
	f.rpcs[0].before = func(method string, _ []interface{}) {
		if method == "eth_chainId" && !mutated {
			raw[len(raw)-1] ^= 1
			mutated = true
		}
	}
	_, err := f.runtime.SendExact(context.Background(), b, raw)
	require.NoError(t, err)
	require.True(t, mutated)
	require.Equal(t, expected, f.rpcs[0].raw)
}

func TestSender_ResumesUnknownOnlyWithDurableTransmissionClaim(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*models.WithdrawalRecoveryAttempt)
		allowed bool
	}{
		{"committed claim", func(*models.WithdrawalRecoveryAttempt) {}, true},
		{"unknown without claim", func(a *models.WithdrawalRecoveryAttempt) { a.State = models.RecoverySendUnknown }, false},
		{"different candidate", func(a *models.WithdrawalRecoveryAttempt) { id := uuid.New(); a.CandidateTransactionID = &id }, false},
		{"missing sealed bytes", func(a *models.WithdrawalRecoveryAttempt) { a.SealedSignedTx = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			b := f.binding(t)
			raw := f.signed(t, b, nil)
			f.link(t, b, raw)
			f.withdrawal.Status = "broadcast"
			candidate := f.transactions[*f.withdrawal.TransactionID]
			candidate.RawPayload = strings.Replace(candidate.RawPayload, `"broadcast_outcome":"pending"`, `"broadcast_outcome":"unknown"`, 1)
			tc.mutate(f.deps.Attempts.(attemptRows)[b.OperationID])
			_, err := f.runtime.SendExact(context.Background(), b, raw)
			if tc.allowed {
				require.NoError(t, err)
				require.Equal(t, 1, f.rpcs[0].sends)
			} else {
				require.Error(t, err)
				require.Zero(t, f.rpcs[0].sends)
			}
		})
	}
}

func TestSender_UsesOriginalNonzeroNonceWithoutSelectingANewOne(t *testing.T) {
	f := newFixture(t)
	f.original.RawPayload = `{"broadcast_outcome":"refused","sender_nonce":7}`
	for _, rpc := range f.rpcs {
		rpc.latest = 7
		rpc.pending = 7
	}
	b := f.binding(t)
	require.Equal(t, uint64(7), *b.Nonce)
	raw := f.signed(t, b, nil)
	f.link(t, b, raw)
	_, err := f.runtime.SendExact(context.Background(), b, raw)
	require.NoError(t, err)
	claim := f.deps.Attempts.(attemptRows)[b.OperationID]
	claim.SenderNonce = "0"
	_, err = f.runtime.SendExact(context.Background(), b, raw)
	require.Error(t, err)
	require.Equal(t, 1, f.rpcs[0].sends)
}

func TestSender_BoundsFreshL1ForEncodedOldGasLimitByImmutableReview(t *testing.T) {
	for _, tc := range []struct {
		name    string
		l1      int64
		allowed bool
	}{{"exact cap", 7, true}, {"one wei above cap", 8, false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			b := f.binding(t)
			raw := f.signed(t, b, nil)
			f.link(t, b, raw)
			// A fresh quote now uses fifty gas, while the persisted signed call uses one
			// hundred. Passing the new quote alone cannot approve the encoded old fee.
			f.adapter.gasLimit = 50
			f.adapter.l1Hook = func(req types.TransferRequest) *big.Int {
				if req.GasLimit != nil && *req.GasLimit == 100 {
					return big.NewInt(tc.l1)
				}
				return big.NewInt(7)
			}
			_, err := f.runtime.SendExact(context.Background(), b, raw)
			if tc.allowed {
				require.NoError(t, err)
				require.Equal(t, 1, f.rpcs[0].sends)
			} else {
				require.ErrorIs(t, err, recovery.ErrRefused)
				require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
			}
		})
	}
}
func TestSender_RejectsChangedImmutableProposalOrFrozenCallBeforeSending(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*models.WithdrawalRecoveryAttempt)
	}{
		{"missing proposal", func(a *models.WithdrawalRecoveryAttempt) { a.ProposalJSON = "" }},
		{"proposal hash mismatch", func(a *models.WithdrawalRecoveryAttempt) { a.ProposalHash = strings.Repeat("f", 64) }},
		{"changed exact gas limit", func(a *models.WithdrawalRecoveryAttempt) {
			a.CallJSON = strings.Replace(a.CallJSON, `"gas_limit":100`, `"gas_limit":99`, 1)
		}},
		{"missing frozen nonce", func(a *models.WithdrawalRecoveryAttempt) {
			a.CallJSON = strings.Replace(a.CallJSON, `"nonce":0,`, "", 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			b := f.binding(t)
			raw := f.signed(t, b, nil)
			f.link(t, b, raw)
			tc.mutate(f.deps.Attempts.(attemptRows)[b.OperationID])
			_, err := f.runtime.SendExact(context.Background(), b, raw)
			require.Error(t, err)
			require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
		})
	}
}

func TestSender_RefusesReviewThatExpiresDuringFinalRPCReads(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	raw := f.signed(t, b, nil)
	f.link(t, b, raw)
	current := time.Now().UTC()
	f.runtime.deps.Now = func() time.Time { return current }
	claim := f.deps.Attempts.(attemptRows)[b.OperationID]
	var proposal recovery.Proposal
	require.NoError(t, json.Unmarshal([]byte(claim.ProposalJSON), &proposal))
	proposal.ExpiresAt = current.Add(time.Second)
	claim.ProposalJSON, _ = recovery.Canonical(proposal)
	claim.ProposalHash = digest(proposal)
	var review recovery.Review
	require.NoError(t, json.Unmarshal([]byte(claim.ReviewJSON), &review))
	review.ProposalHash = claim.ProposalHash
	review.ExpiresAt = proposal.ExpiresAt
	claim.ReviewJSON, _ = recovery.Canonical(review)
	f.rpcs[1].before = func(method string, args []interface{}) {
		if method == "eth_getTransactionCount" && args[1] == "pending" && f.rpcs[1].calls > 10 {
			current = current.Add(2 * time.Second)
		}
	}
	_, err := f.runtime.SendExact(context.Background(), b, raw)
	require.ErrorIs(t, err, recovery.ErrRefused)
	require.Contains(t, err.Error(), "review expired")
	require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
}

func TestSender_RefusesShorterIndependentReviewExpiryDuringFinalRPCReads(t *testing.T) {
	f := newFixture(t)
	b := f.binding(t)
	raw := f.signed(t, b, nil)
	f.link(t, b, raw)
	claim := f.deps.Attempts.(attemptRows)[b.OperationID]
	var proposal recovery.Proposal
	require.NoError(t, json.Unmarshal([]byte(claim.ProposalJSON), &proposal))
	current := proposal.PreparedAt
	f.runtime.deps.Now = func() time.Time { return current }
	var review recovery.Review
	require.NoError(t, json.Unmarshal([]byte(claim.ReviewJSON), &review))
	review.ExpiresAt = current.Add(time.Second)
	claim.ReviewJSON, _ = recovery.Canonical(review)
	f.rpcs[1].before = func(method string, args []interface{}) {
		if method == "eth_getTransactionCount" && args[1] == "pending" && f.rpcs[1].calls > 10 {
			current = current.Add(2 * time.Second)
		}
	}
	_, err := f.runtime.SendExact(context.Background(), b, raw)
	require.ErrorIs(t, err, recovery.ErrRefused)
	require.Contains(t, err.Error(), "review expired")
	require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
	require.True(t, proposal.ExpiresAt.After(current))
}
func TestSender_RejectsAbsentUnapprovedOrChangedIndependentReview(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*recovery.Review)
	}{
		{"approval removed", func(r *recovery.Review) { r.Approved = false }},
		{"proposal changed", func(r *recovery.Review) { r.ProposalHash = strings.Repeat("f", 64) }},
		{"review extends proposal", func(r *recovery.Review) { r.ExpiresAt = r.ExpiresAt.Add(time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			b := f.binding(t)
			raw := f.signed(t, b, nil)
			f.link(t, b, raw)
			claim := f.deps.Attempts.(attemptRows)[b.OperationID]
			var review recovery.Review
			require.NoError(t, json.Unmarshal([]byte(claim.ReviewJSON), &review))
			tc.mutate(&review)
			claim.ReviewJSON, _ = recovery.Canonical(review)
			_, err := f.runtime.SendExact(context.Background(), b, raw)
			require.ErrorIs(t, err, recovery.ErrRefused)
			require.Zero(t, f.rpcs[0].sends+f.rpcs[1].sends)
		})
	}
}
