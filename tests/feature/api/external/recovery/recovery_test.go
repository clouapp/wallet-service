package recovery_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	rpcadapter "github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/hdkey"
	"github.com/macrowallets/waas/app/services/settings"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	testutil.BootApp()
	os.Exit(m.Run())
}

type readOnlyRecoveryRPC struct{ calls, sends int }

func (r *readOnlyRecoveryRPC) Call(_ context.Context, method string, out interface{}, params ...interface{}) error {
	r.calls++
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
		return fmt.Errorf("read-only inspect must never send")
	default:
		return fmt.Errorf("unexpected synthetic RPC")
	}
	data, _ := json.Marshal(value)
	return json.Unmarshal(data, out)
}

type readOnlyRecoveryAdapter struct{ types.Chain }

func (readOnlyRecoveryAdapter) EstimateGasPrice(context.Context) (*big.Int, error) {
	return big.NewInt(2), nil
}
func (readOnlyRecoveryAdapter) EstimateTransferGasLimit(context.Context, types.TransferRequest) (uint64, error) {
	return 100, nil
}
func (readOnlyRecoveryAdapter) EstimateL1DataFee(context.Context, types.TransferRequest) (*big.Int, error) {
	return big.NewInt(7), nil
}

// The real router, middleware, dependency container, request factory, repositories,
// observer and proposal service execute together. Only RPC/chain fee reads are
// synthetic. Every write endpoint is deliberately paused before body parsing.
func TestRecoveryActualRoutesAndDIInspectReadOnlyWithWriteGates(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	q := facades.Orm().Query()
	accountID, bearer, _ := support.SetupAPIAuth(t, false)
	_, err := q.Model(&models.Account{}).Where("id = ?", accountID).Update("environment", "test")
	require.NoError(t, err)
	primary, err := settings.Seal(facades.Crypt(), "https://rpc-a.test/private-key")
	require.NoError(t, err)
	secondary, err := settings.Seal(facades.Crypt(), "https://rpc-b.test/private-key")
	require.NoError(t, err)
	networkID := int64(421614)
	require.NoError(t, q.Create(&models.Chain{ID: "arbitrum", Name: "Synthetic test", AdapterType: "evm", NativeSymbol: "ETH", NativeDecimals: 18, NetworkID: &networkID, RpcURL: primary, IsTestnet: true, RequiredConfirmations: 120, Status: "active", Finality: models.FinalityConfirmations}))
	require.NoError(t, q.Create(&models.ChainRPCEndpoint{ChainID: "arbitrum", URL: secondary, Position: 2, Enabled: true}))
	require.NoError(t, q.Create(&models.Token{ID: uuid.New(), ChainID: "arbitrum", Symbol: "USDC", ContractAddress: models.USDCContractArbSepolia, Decimals: 6, Status: models.TokenStatusActive}))
	wallet := fixtures.InsertWalletWithAccount(t, "arbitrum", &accountID)
	parent, err := crypto.HexToECDSA(strings.Repeat("0", 63) + "1")
	require.NoError(t, err)
	public := crypto.CompressPubkey(&parent.PublicKey)
	code := strings.Repeat("11", 32)
	cc, _ := hex.DecodeString(code)
	child, err := hdkey.DeriveSecp256k1Child(public, cc, 1)
	require.NoError(t, err)
	childPublic, err := crypto.DecompressPubkey(child.PublicKey)
	require.NoError(t, err)
	address := crypto.PubkeyToAddress(*childPublic).Hex()
	_, err = q.Model(&models.Wallet{}).Where("id = ?", wallet.ID).Update(map[string]any{"mpc_public_key": hex.EncodeToString(public), "mpc_chain_code": code, "status": "active"})
	require.NoError(t, err)
	source := fixtures.InsertAddress(t, wallet.ID, "arbitrum", address, "2", 1)
	_, err = q.Model(&models.Address{}).Where("id = ?", source.ID).Update("derivation_type", "bip32")
	require.NoError(t, err)
	op, original := uuid.New(), uuid.New()
	recipient := "0x" + strings.Repeat("2", 40)
	require.NoError(t, q.Create(&models.Transaction{ID: original, WalletID: wallet.ID, AddressID: &source.ID, ExternalUserID: "2", Chain: "arbitrum", TxType: "withdrawal", TxHash: "0x" + strings.Repeat("a", 64), LogIndex: -1, FromAddress: address, ToAddress: recipient, Amount: "6100000", Asset: "USDC", TokenContract: models.USDCContractArbSepolia, RequiredConfs: 120, Status: "failed", ErrorMessage: "broadcast_refused", Direction: models.TxDirectionOutbound, Source: models.TxSourceWithdrawalFlow, Origin: models.TxOriginUserRequest, RawPayload: `{"broadcast_outcome":"refused","sender_nonce":0}`, Finality: models.FinalityConfirmations}))
	require.NoError(t, q.Create(&models.Withdrawal{ID: op, WalletID: wallet.ID, AccountID: &accountID, TransactionID: &original, Status: "failed", Amount: "6.1", FeeEstimate: "0", DestinationAddress: recipient}))
	registry := container.MustMake[*chain.Registry]()
	registry.ResetCatalog()
	registry.RegisterChain(readOnlyRecoveryAdapter{mocks.NewMockChain("arbitrum")})
	registry.RegisterToken(types.Token{ChainID: "arbitrum", Symbol: "USDC", Contract: models.USDCContractArbSepolia, Decimals: 6})
	var clients []*readOnlyRecoveryRPC
	chain.SetJSONRPCCaller(func(chain.JSONRPCDeps) chain.JSONRPCCaller {
		client := &readOnlyRecoveryRPC{}
		clients = append(clients, client)
		return client
	})
	t.Cleanup(func() {
		chain.SetJSONRPCCaller(func(d chain.JSONRPCDeps) chain.JSONRPCCaller {
			return rpcadapter.NewRPCClient(rpcadapter.RPCClientDeps{URL: d.URL, User: d.User, Password: d.Password})
		})
	})
	facades.Config().Add("app.url", "https://provider.test")
	executable, err := os.Executable()
	require.NoError(t, err)
	revisionDir, err := os.OpenRoot(filepath.Dir(executable))
	require.NoError(t, err)
	t.Cleanup(func() { _ = revisionDir.Close() })
	file, err := revisionDir.OpenFile("REVISION", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	_, err = file.WriteString(strings.Repeat("a", 40) + "\n")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	t.Cleanup(func() { _ = revisionDir.Remove("REVISION") })
	flags := repositories.NewFeatureRepository(nil)
	require.NoError(t, flags.UpsertGlobal(ctx, features.FlagWithdrawalsEnabled, false))
	require.NoError(t, flags.UpsertGlobal(ctx, features.FlagSweepEnabled, false))
	call := func(method, path, token, body string) (int, map[string]json.RawMessage) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, requestErr := facades.Route().Test(req)
		require.NoError(t, requestErr)
		defer resp.Body.Close()
		var out map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return resp.StatusCode, out
	}
	base := "/api/v1/wallets/" + wallet.ID.String() + "/withdrawals/" + op.String() + "/recovery"
	inspectBody := fmt.Sprintf(`{"original_transaction_id":%q,"fees":{"max_gas_limit":100,"max_gas_price_wei":"2","max_native_fee_wei":"207"},"prepared_by":"synthetic-preparer"}`, original.String())
	status, body := call(http.MethodPost, base+"/inspect", bearer, inspectBody)
	require.Equal(t, http.StatusOK, status, string(body["error"]))
	var proposal recovery.Proposal
	require.NoError(t, json.Unmarshal(body["proposal"], &proposal))
	var hash string
	require.NoError(t, json.Unmarshal(body["proposal_hash"], &hash))
	actualHash, _ := recovery.Hash(proposal)
	require.Equal(t, actualHash, hash)
	require.Equal(t, op, proposal.Binding.OperationID)
	require.Equal(t, original, proposal.Binding.OriginalTransactionID)
	require.Equal(t, wallet.ID, proposal.Binding.WalletID)
	require.Equal(t, accountID, proposal.Binding.AccountID)
	require.Equal(t, "6100000", proposal.Binding.AmountRaw)
	require.Nil(t, proposal.GasSeed)
	require.Equal(t, strings.Repeat("a", 40), proposal.CodeRevision)
	for _, suffix := range []string{"/apply", "/resume"} {
		status, _ = call(http.MethodPost, base+suffix, bearer, `{}`)
		require.Equal(t, http.StatusConflict, status)
	}
	require.NoError(t, flags.UpsertGlobal(ctx, features.FlagWithdrawalsEnabled, true))
	status, body = call(http.MethodPost, base+"/apply", bearer, `{}`)
	require.Equal(t, http.StatusConflict, status)
	require.Contains(t, string(body["error"]), features.CodeSweepPaused)

	// Permission denial precedes the paused-money gates and body inspection.
	_, err = q.Model(&models.AccessToken{}).Where("account_id = ?", accountID).Update("permissions", "[]")
	require.NoError(t, err)
	status, _ = call(http.MethodPost, base+"/inspect", bearer, inspectBody)
	require.Equal(t, http.StatusForbidden, status)
	_, err = q.Model(&models.AccessToken{}).Where("account_id = ?", accountID).Update("permissions", models.AllAPIPermissionGrants())
	require.NoError(t, err)
	// Authentication environment comes from the actual database account.
	_, err = q.Model(&models.Account{}).Where("id = ?", accountID).Update("environment", "prod")
	require.NoError(t, err)
	status, _ = call(http.MethodPost, base+"/inspect", bearer, inspectBody)
	require.Equal(t, http.StatusForbidden, status)
	_, err = q.Model(&models.Account{}).Where("id = ?", accountID).Update("environment", "test")
	require.NoError(t, err)
	// A limited token cannot bypass the ordinary spending reservation even
	// after both money switches open. Refusal happens before the scoped factory.
	require.NoError(t, flags.UpsertGlobal(ctx, features.FlagSweepEnabled, true))
	_, err = q.Model(&models.AccessToken{}).Where("account_id = ?", accountID).Update("spending_limit", `{"USDC":"10"}`)
	require.NoError(t, err)
	for _, suffix := range []string{"/apply", "/resume"} {
		status, _ = call(http.MethodPost, base+suffix, bearer, `{}`)
		require.Equal(t, http.StatusForbidden, status)
	}
	_, foreignBearer, _ := support.SetupAPIAuth(t, false)
	status, _ = call(http.MethodPost, base+"/inspect", foreignBearer, inspectBody)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = call(http.MethodPost, base+"/inspect", "", inspectBody)
	require.Equal(t, http.StatusUnauthorized, status)
	count, err := q.Model(&models.WithdrawalRecoveryAttempt{}).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	count, err = q.Model(&models.Transaction{}).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.Len(t, clients, 2)
	for _, client := range clients {
		require.Positive(t, client.calls)
		require.Zero(t, client.sends)
	}
	saved, err := repositories.NewWithdrawalRepository(nil).FindByID(ctx, op)
	require.NoError(t, err)
	require.Equal(t, "failed", saved.Status)
	require.Equal(t, original, *saved.TransactionID)
}
