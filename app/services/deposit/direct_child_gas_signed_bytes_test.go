package deposit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/chainfixtures"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

type gasAuditSealer struct{}

func (gasAuditSealer) Seal(text string) (string, error) { return "enc:v1:test:" + text, nil }
func (gasAuditSealer) Open(text string) (string, error) {
	if value, ok := strings.CutPrefix(text, "enc:v1:test:"); ok {
		return value, nil
	}
	return "", errors.New("invalid test seal")
}

func TestDirectChildGas_ConfirmationTrackerRetainsSealedBytesForRealObserveAndResume(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	node := mocks.NewFakeEVMNode(t)
	node.BlockNumberHex = "0x66"
	node.NativeBalanceHex = "0xde0b6b3a7640000"
	node.TokenBalanceHex = "0xa7d8c0"
	node.EstimateGasHex = "0xc350"
	registry := chain.NewRegistry()
	registry.RegisterChain(chainfixtures.NewEVMSepolia(node.URL()))
	token := types.Token{Symbol: "USDC", Contract: strings.ToLower(models.USDCContractSepolia), Decimals: 6, ChainID: models.ChainTETH}
	registry.RegisterToken(token)
	network := models.EVMNetworkIDEthereumSepolia
	require.NoError(t, facades.Orm().Query().Create(&models.Chain{ID: models.ChainTETH, Name: "Test Sepolia", AdapterType: models.AdapterTypeEVM, NativeSymbol: models.NativeETH, NativeDecimals: 18, NetworkID: &network, RpcURL: node.URL(), Status: "active", IsTestnet: true}))
	account := fixtures.InsertAccount(t, "gas-observer-qa")
	wallet := fixtures.InsertWalletWithAccount(t, models.ChainTETH, &account.ID)
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	base := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	_, err = facades.Orm().Query().Model(&models.Address{}).Where("id = ?", wallet.DepositAddress.ID).Update("address", base)
	require.NoError(t, err)
	child := fixtures.InsertAddress(t, wallet.ID, models.ChainTETH, "0x00000000000000000000000000000000000000aa", "qa-child", 1)
	op := uuid.New()
	nonce := uint64(0)
	b := sweep.DirectChildGasBinding{OperationID: op, ClaimID: op, AccountID: account.ID, WalletID: wallet.ID, SourceAddressID: child.ID, SourceAddress: child.Address, SourceDerivationIndex: 1, SourceExternalUserID: child.ExternalUserID,
		Chain: models.ChainTETH, NetworkID: network, Asset: "USDC", TokenContract: token.Contract, TokenDecimals: 6, AmountRaw: "6100000", Recipient: "0x00000000000000000000000000000000000000bb"}
	p := sweep.DirectChildGasPlan{BaseAddressID: wallet.DepositAddress.ID, BaseAddress: base, FunderNonce: &nonce, AmountWei: "156000000000000", MaxFeeWei: "42000000000000", MinFunderBalanceWei: "0", TokenFeeWei: "130000000000000", ExpiresAt: time.Now().Add(time.Minute)}
	amount, _ := new(big.Int).SetString(p.AmountWei, 10)
	signed, err := gethtypes.SignTx(gethtypes.NewTransaction(0, common.HexToAddress(child.Address), amount, 21_000, big.NewInt(2_000_000_000), nil), gethtypes.LatestSignerForChainID(big.NewInt(network)), key)
	require.NoError(t, err)
	bytes, err := signed.MarshalBinary()
	require.NoError(t, err)
	bindingRaw, err := json.Marshal(b)
	require.NoError(t, err)
	digest := sha256.Sum256(bindingRaw)
	sealed, _ := (gasAuditSealer{}).Seal(hex.EncodeToString(bytes))
	payload, err := json.Marshal(map[string]any{chain.SignedTxPayloadKey: sealed, chain.BroadcastOutcomePayloadKey: chain.BroadcastOutcomePending, chain.SenderNoncePayloadKey: 0,
		"direct_child_gas": map[string]any{"binding": b, "plan": p, "binding_hash": hex.EncodeToString(digest[:]), "signed_fee_wei": p.MaxFeeWei}})
	require.NoError(t, err)
	step := sweep.DirectChildGasStepID(op)
	stepKey := step.String()
	baseID := wallet.DepositAddress.ID
	row := &models.Transaction{ID: step, WalletID: wallet.ID, AddressID: &baseID, ExternalUserID: "system", Chain: models.ChainTETH, TxType: models.TxTypeGasSeed, TxHash: signed.Hash().Hex(), FromAddress: base, ToAddress: child.Address,
		Amount: p.AmountWei, Asset: models.NativeETH, Status: string(types.TxStatusConfirming), RequiredConfs: 3, LogIndex: models.TransactionLogIndexNone, Origin: models.TxOriginGasSeed, Direction: models.TxDirectionSelf, Source: models.TxSourceWithdrawalFlow, IdempotencyKey: &stepKey, RawPayload: string(payload)}
	txs := repositoryOf(t)
	require.NoError(t, txs.Create(ctx, row))
	node.Receipts[strings.ToLower(row.TxHash)] = mocks.FakeEVMReceipt{Block: 100, Status: "0x1", Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: 2_000_000_000}
	tracker := newDepositSvc(registry, newWebhookSvc())
	require.NoError(t, tracker.RunConfirmationCheck(ctx))
	stored := reloadTransaction(t, step)
	require.Equal(t, string(types.TxStatusConfirmed), stored.Status)
	require.Contains(t, stored.RawPayload, chain.SignedTxPayloadKey)
	service := newGasPreparationObserver(registry, gasAuditSealer{})
	observed, err := service.ObserveDirectChildGas(ctx, b)
	require.NoError(t, err)
	require.True(t, observed.Confirmed)
	resumed, err := service.ResumeDirectChildGas(ctx, b)
	require.NoError(t, err)
	require.True(t, resumed.Confirmed)
	require.Empty(t, node.CallsTo("eth_sendRawTransaction"), "settled preparation must never transmit again")
}
