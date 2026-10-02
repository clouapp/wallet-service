package sweep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// ---------------------------------------------------------------------------
// EVM: native withdrawals reserve gas
// ---------------------------------------------------------------------------

// evmNativeFee is the fee of a native transfer on FakeEVMNode: 1 gwei suggested,
// doubled by the adapter's buffer, × 21000.
var evmNativeFee = big.NewInt(2 * 1_000_000_000 * 21_000)

const evmNativeAmount = 1_000_000_000_000_000 // 0.001

func evmNativePlanner(t *testing.T, balance *big.Int, withChild bool) (*service, *evmGasFixture, models.Address) {
	t.Helper()
	fixture := newEVMGasFixture(t)
	fixture.node.NativeBalanceHex = "0x" + balance.Text(16)
	svc := fixture.plannerService()
	child := models.Address{ID: uuid.New(), WalletID: fixture.wallet.ID, Address: gasPlanDestination}
	if withChild {
		svc.addressRepo = &fakeAddressRepo{children: []models.Address{fixture.base, child}}
	}
	return svc, fixture, child
}

func planEVMNative(t *testing.T, svc *service, fixture *evmGasFixture, amount *big.Int) *Plan {
	t.Helper()
	plan, err := svc.PlanForWithdrawal(context.Background(), fixture.wallet.ID, gasPlanNative, amount, "0x00000000000000000000000000000000000000aa", uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPlanEVMNative_BaseHoldingOnlyTheAmountCannotPayGas(t *testing.T) {
	svc, fixture, _ := evmNativePlanner(t, big.NewInt(evmNativeAmount), false)

	if plan := planEVMNative(t, svc, fixture, big.NewInt(evmNativeAmount)); plan.Strategy != StrategyInsufficient {
		t.Fatalf("strategy %s, want insufficient: value + gas exceeds the balance", plan.Strategy)
	}
}

func TestPlanEVMNative_BaseCoveringAmountPlusGasIsDirect(t *testing.T) {
	balance := new(big.Int).Add(big.NewInt(evmNativeAmount), evmNativeFee)
	svc, fixture, _ := evmNativePlanner(t, balance, false)

	plan := planEVMNative(t, svc, fixture, big.NewInt(evmNativeAmount))

	if plan.Strategy != StrategyDirectFromBase {
		t.Fatalf("strategy %s", plan.Strategy)
	}
	if plan.EstimatedGas == nil || plan.EstimatedGas.Cmp(evmNativeFee) != 0 {
		t.Fatalf("EstimatedGas %v, want the reserved %s", plan.EstimatedGas, evmNativeFee)
	}
	if over := planEVMNative(t, svc, fixture, big.NewInt(evmNativeAmount+1)); over.Strategy != StrategyInsufficient {
		t.Fatalf("one wei more must not fit, got %s", over.Strategy)
	}
}

func TestPlanEVMNative_ChildSweepMovesBalanceMinusGas(t *testing.T) {
	// Base and child each hold B; the child sweep nets B − fee, the final transfer
	// needs amount + fee at base: amount = 2B − 2·fee fits exactly.
	balance := big.NewInt(evmNativeAmount)
	exact := new(big.Int).Sub(new(big.Int).Mul(balance, big.NewInt(2)), new(big.Int).Mul(evmNativeFee, big.NewInt(2)))
	svc, fixture, child := evmNativePlanner(t, balance, true)

	plan := planEVMNative(t, svc, fixture, exact)

	if plan.Strategy != StrategyMultiSweep || len(plan.Sweeps) != 1 || plan.Sweeps[0].From.Address != child.Address {
		t.Fatalf("plan %+v", plan)
	}
	if want := new(big.Int).Sub(balance, evmNativeFee); plan.Sweeps[0].Amount.Cmp(want) != 0 {
		t.Fatalf("sweep amount %s, want balance − fee %s", plan.Sweeps[0].Amount, want)
	}
	if short := planEVMNative(t, svc, fixture, new(big.Int).Add(exact, big.NewInt(1))); short.Strategy != StrategyInsufficient {
		t.Fatalf("one wei more must be insufficient, got %s", short.Strategy)
	}
}

func TestPlanEVMToken_IsNotChargedTheNativeReserve(t *testing.T) {
	fixture := newEVMGasFixture(t) // 0 native, 20 USDC on base
	svc := fixture.plannerService()

	plan, err := svc.PlanForWithdrawal(context.Background(), fixture.wallet.ID, gasPlanAsset, big.NewInt(20_000_000), gasPlanDestination, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != StrategyDirectFromBase {
		t.Fatalf("a token withdrawal of the whole token balance must stay direct, got %s", plan.Strategy)
	}
}

// ---------------------------------------------------------------------------
// Bitcoin: confirmed UTXOs only, fee counted per source
// ---------------------------------------------------------------------------

type fakeUTXO struct {
	sats          int64
	confirmations int
}

const btcPlannerSatPerVByte = 2

// btcPlannerFee is the fee of spending inputs to one output at 2 sat/vB:
// ceil(10.5 + 68·inputs + 31) × 2.
func btcPlannerFee(inputs int) int64 {
	halves := int64(21 + 136*inputs + 62)
	return (halves + 1) / 2 * btcPlannerSatPerVByte
}

// fakeBitcoindChain is a real BitcoinLive over a fake bitcoind that answers
// listunspent by address and minconf, and estimatesmartfee at 2 sat/vB.
func fakeBitcoindChain(t *testing.T, utxos map[string][]fakeUTXO) *chain.BitcoinLive {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     uint64            `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode rpc: %v", err)
			return
		}
		var result interface{}
		switch req.Method {
		case "estimatesmartfee":
			result = map[string]interface{}{"feerate": float64(btcPlannerSatPerVByte) / 100_000, "blocks": 3}
		case "listunspent":
			var minconf int
			var addresses []string
			_ = json.Unmarshal(req.Params[0], &minconf)
			_ = json.Unmarshal(req.Params[2], &addresses)
			unspent := make([]map[string]interface{}, 0)
			for i, u := range utxos[addresses[0]] {
				if u.confirmations < minconf {
					continue
				}
				txid := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", addresses[0], i)))
				unspent = append(unspent, map[string]interface{}{
					"txid": hex.EncodeToString(txid[:]),
					"vout": i, "amount": float64(u.sats) / 1e8, "confirmations": u.confirmations,
				})
			}
			result = unspent
		default:
			t.Errorf("unexpected rpc %s", req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return chain.NewBitcoinLive(chain.BitcoinConfig{ChainIDStr: models.ChainBTC, NativeSymbol: models.NativeBTC, RPCURL: srv.URL, IsTestnet: true})
}

func btcPlanner(t *testing.T, utxos map[string][]fakeUTXO, children ...string) (*service, uuid.UUID, *chain.BitcoinLive) {
	t.Helper()
	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: "tb1qbase"}
	addresses := []models.Address{base}
	for _, child := range children {
		addresses = append(addresses, models.Address{ID: uuid.New(), WalletID: walletID, Address: child})
	}
	adapter := fakeBitcoindChain(t, utxos)
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	return &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: &models.Wallet{ID: walletID, Chain: models.ChainBTC, DepositAddress: &base}},
		addressRepo: &fakeAddressRepo{children: addresses},
		chainRepo:   &fakeChainRepo{chain: &models.Chain{ID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin}},
	}, walletID, adapter
}

func planBTC(t *testing.T, svc *service, walletID uuid.UUID, amount int64) *Plan {
	t.Helper()
	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, models.NativeBTC, big.NewInt(amount), "tb1qdest", uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPlanBitcoin_IgnoresUnconfirmedUTXOsAndCountsTheFee(t *testing.T) {
	svc, walletID, adapter := btcPlanner(t, map[string][]fakeUTXO{
		"tb1qbase": {{sats: 100_000, confirmations: 3}, {sats: 500_000, confirmations: 0}},
	})
	maxSendable := int64(100_000) - btcPlannerFee(1)

	displayed, err := adapter.GetBalance(context.Background(), "tb1qbase")
	if err != nil || displayed.Amount.Int64() != 600_000 {
		t.Fatalf("displayed balance must still include unconfirmed utxos: %v %v", displayed, err)
	}

	plan := planBTC(t, svc, walletID, maxSendable)
	if plan.Strategy != StrategyDirectFromBase || plan.BaseBalance.Int64() != 100_000 {
		t.Fatalf("strategy %s base balance %s", plan.Strategy, plan.BaseBalance)
	}
	if _, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{From: "tb1qbase", To: "tb1qdest", Amount: big.NewInt(maxSendable)}); err != nil {
		t.Fatalf("a planned amount must build: %v", err)
	}

	for _, amount := range []int64{maxSendable + 1, 100_000, 300_000} {
		if plan := planBTC(t, svc, walletID, amount); plan.Strategy != StrategyInsufficient {
			t.Fatalf("amount %d: strategy %s, want insufficient (confirmed 100000 minus fee)", amount, plan.Strategy)
		}
	}
}

func TestPlanBitcoin_FeeGrowsWithTheSourceInputs(t *testing.T) {
	svc, walletID, adapter := btcPlanner(t, map[string][]fakeUTXO{
		"tb1qbase": {{sats: 40_000, confirmations: 1}, {sats: 40_000, confirmations: 1}, {sats: 40_000, confirmations: 1}},
	})
	maxSendable := int64(120_000) - btcPlannerFee(3)

	if plan := planBTC(t, svc, walletID, maxSendable); plan.Strategy != StrategyDirectFromBase {
		t.Fatalf("strategy %s", plan.Strategy)
	}
	if _, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{From: "tb1qbase", To: "tb1qdest", Amount: big.NewInt(maxSendable)}); err != nil {
		t.Fatalf("build: %v", err)
	}
	if plan := planBTC(t, svc, walletID, maxSendable+1); plan.Strategy != StrategyInsufficient {
		t.Fatalf("strategy %s", plan.Strategy)
	}
}

func TestPlanBitcoin_DirectFromChildUsesTheChildsConfirmedFunds(t *testing.T) {
	svc, walletID, _ := btcPlanner(t, map[string][]fakeUTXO{
		"tb1qbase":    {{sats: 10_000, confirmations: 1}},
		"tb1qpending": {{sats: 900_000, confirmations: 0}},
		"tb1qready":   {{sats: 200_000, confirmations: 6}},
	}, "tb1qpending", "tb1qready")

	plan := planBTC(t, svc, walletID, 150_000)

	if plan.Strategy != StrategyDirectFromChild || plan.SourceAddress == nil || plan.SourceAddress.Address != "tb1qready" {
		t.Fatalf("plan %+v", plan)
	}
}

func TestPlanBitcoin_MultiSweepLegsAreNetOfTheirFee(t *testing.T) {
	svc, walletID, adapter := btcPlanner(t, map[string][]fakeUTXO{
		"tb1qbase":  {{sats: 50_000, confirmations: 1}},
		"tb1qchild": {{sats: 40_000, confirmations: 1}, {sats: 40_000, confirmations: 1}, {sats: 70_000, confirmations: 0}},
	}, "tb1qchild")
	childSweepable := int64(80_000) - btcPlannerFee(2)
	exact := int64(50_000) + childSweepable - btcPlannerFee(1)

	plan := planBTC(t, svc, walletID, exact)

	if plan.Strategy != StrategyMultiSweep || len(plan.Sweeps) != 1 || plan.Sweeps[0].Amount.Int64() != childSweepable {
		t.Fatalf("plan %+v", plan)
	}
	if _, err := adapter.BuildSweep(context.Background(), types.SweepRequest{From: "tb1qchild", To: "tb1qbase", Amount: plan.Sweeps[0].Amount}); err != nil {
		t.Fatalf("the planned sweep leg must build: %v", err)
	}
	if plan := planBTC(t, svc, walletID, exact+1); plan.Strategy != StrategyInsufficient {
		t.Fatalf("strategy %s", plan.Strategy)
	}
}

type spendableMockChain struct {
	*mocks.MockChain
	funds chain.SpendableFunds
	err   error
}

func (c *spendableMockChain) SpendableFunds(context.Context, string) (chain.SpendableFunds, error) {
	return c.funds, c.err
}

func spendableMockPlanner(t *testing.T, adapter *spendableMockChain) (*service, uuid.UUID) {
	t.Helper()
	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	return &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: &models.Wallet{ID: walletID, Chain: models.ChainBTC, DepositAddress: &base}},
		addressRepo: &fakeAddressRepo{children: []models.Address{base}},
		chainRepo:   &fakeChainRepo{chain: &models.Chain{ID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin}},
	}, walletID
}

func newSpendableMock(funds chain.SpendableFunds, err error) *spendableMockChain {
	mockChain := mocks.NewMockChain(models.ChainBTC)
	mockChain.NativeAssetVal = models.NativeBTC
	return &spendableMockChain{MockChain: mockChain, funds: funds, err: err}
}

func TestPlanSpendableFunds_ErrorsAndInvalidValuesFailThePlan(t *testing.T) {
	utxoDown := errors.New("utxo api down")
	cases := map[string]*spendableMockChain{
		"reader error":      newSpendableMock(chain.SpendableFunds{}, utxoDown),
		"nil balance":       newSpendableMock(chain.SpendableFunds{MaxTransferFee: big.NewInt(1)}, nil),
		"nil fee":           newSpendableMock(chain.SpendableFunds{Balance: big.NewInt(1)}, nil),
		"negative balance":  newSpendableMock(chain.SpendableFunds{Balance: big.NewInt(-1), MaxTransferFee: big.NewInt(0)}, nil),
		"fee above balance": newSpendableMock(chain.SpendableFunds{Balance: big.NewInt(10), MaxTransferFee: big.NewInt(11)}, nil),
	}
	for name, adapter := range cases {
		t.Run(name, func(t *testing.T) {
			svc, walletID := spendableMockPlanner(t, adapter)
			_, err := svc.PlanForWithdrawal(context.Background(), walletID, models.NativeBTC, big.NewInt(5), "", uuid.Nil)
			if err == nil {
				t.Fatal("expected an error")
			}
			if name == "reader error" && !errors.Is(err, utxoDown) {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func TestPlanSpendableFunds_OnlyAppliesToTheNativeAsset(t *testing.T) {
	adapter := newSpendableMock(chain.SpendableFunds{}, errors.New("must not be called for tokens"))
	adapter.GetTokenBalanceFn = func(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
		return &types.Balance{Amount: big.NewInt(1_000)}, nil
	}
	svc, walletID := spendableMockPlanner(t, adapter)
	svc.registry.RegisterToken(types.Token{Symbol: "TOKEN", ChainID: models.ChainBTC})

	plan, err := svc.PlanForWithdrawal(context.Background(), walletID, "TOKEN", big.NewInt(500), "", uuid.Nil)
	if err != nil || plan.Strategy != StrategyDirectFromBase {
		t.Fatalf("plan %+v err %v", plan, err)
	}
}
