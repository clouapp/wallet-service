package repositories_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	swapproviders "github.com/macrowallets/waas/app/adapters/swap"
	swapresources "github.com/macrowallets/waas/app/http/resources/swaps"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/providers"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/stretchr/testify/require"
)

// This opt-in server exercises real services, adapters and SQL persistence.
// Provider HTTP responses, custody signing and chain deposits are controlled fixtures.
// It never contacts an external provider, RPC, signing key store or funded wallet.
func TestSwapControlledHTTPDemo(t *testing.T) {
	if os.Getenv("SWAP_DEMO_SERVE") != "1" {
		t.Skip("opt-in controlled integration server")
	}
	fixtures.TestDB(t)
	account := fixtures.InsertAccount(t, "Controlled swap demonstration")
	actor := fixtures.InsertUser(t)
	source := fixtures.InsertWalletWithAccount(t, "eth", &account.ID)
	destination := fixtures.InsertWalletWithAccount(t, "btc", &account.ID)
	source.Status, destination.Status = "active", "active"
	repo := repositories.NewSwapRepository(nil)
	execution := &controlledSwapExecution{repo: repo}
	write := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	ethToken := map[string]any{"address": "0x0000000000000000000000000000000000000000", "symbol": "ETH", "decimals": 18, "chainId": 1}
	btcToken := map[string]any{"address": "bitcoin", "symbol": "BTC", "decimals": 8, "chainId": 20000000000001}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/quote":
			write(w, 404, map[string]string{"error": "controlled no route"})
		case "/chains":
			write(w, 200, map[string]any{"chains": []any{map[string]any{"id": 1, "key": "eth", "nativeToken": ethToken}, map[string]any{"id": 20000000000001, "key": "btc", "nativeToken": btcToken}}})
		case "/quote":
			q := r.URL.Query()
			write(w, 200, map[string]any{"id": "controlled-route", "tool": "controlled-bridge", "action": map[string]any{"fromChainId": 1, "toChainId": 20000000000001, "fromToken": ethToken, "toToken": btcToken, "fromAmount": q.Get("fromAmount"), "fromAddress": q.Get("fromAddress"), "toAddress": q.Get("toAddress")}, "estimate": map[string]any{"toAmount": "345000", "toAmountMin": "340000", "executionDuration": 120}, "transactionRequest": map[string]any{"to": "0x4444444444444444444444444444444444444444", "value": q.Get("fromAmount"), "data": "0x1234", "chainId": 1}})
		case "/status":
			write(w, 200, map[string]any{"status": "DONE", "substatus": "COMPLETED", "receiving": map[string]any{"txHash": controlledReceiveHash, "amount": "343210", "toAddress": destination.DepositAddress.Address, "token": btcToken}})
		default:
			write(w, 404, map[string]string{"error": "unknown fixture route"})
		}
	}))
	defer provider.Close()
	factory, err := swap.NewFactory(swapproviders.NewSwapKit(swapproviders.Deps{BaseURL: provider.URL}), swapproviders.NewLiFi(swapproviders.Deps{BaseURL: provider.URL}))
	require.NoError(t, err)
	policy := controlledSwapPolicy{}
	service := swap.NewService(swap.Deps{Factory: factory, Router: swap.NewRouter(factory, policy), Policy: policy, Credentials: policy,
		Store: providers.NewSwapStore(repo), Wallets: repositories.NewWalletRepository(nil), Catalog: controlledSwapCatalog{}, Execution: execution,
		Verifier: swap.NewDepositVerifier(repositories.NewTransactionRepository(nil), repo)})
	mux := http.NewServeMux()
	var server *http.Server
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer controlled-swap-only" {
			write(w, 401, map[string]string{"error": "demo authorization required"})
			return
		}
		ctx := r.Context()
		if r.URL.Path == "/__demo/context" {
			write(w, 200, map[string]any{"source": source.ID, "destination": destination.ID, "funding_count": execution.sends.Load()})
			return
		}
		if r.URL.Path == "/__demo/stop" {
			write(w, 200, map[string]bool{"stopped": true})
			go server.Shutdown(context.Background())
			return
		}
		if r.URL.Path == "/__demo/confirm" && r.Method == http.MethodPost {
			var existing models.Transaction
			if facades.Orm().Query().Where("tx_hash = ?", controlledReceiveHash).FirstOrFail(&existing) != nil {
				tx := &models.Transaction{ID: uuid.New(), WalletID: destination.ID, AddressID: destination.DepositAddressID, ExternalUserID: "system", Chain: "btc", TxType: models.TxTypeDeposit, TxHash: controlledReceiveHash, LogIndex: 0, FromAddress: "controlled-provider", ToAddress: destination.DepositAddress.Address, Amount: "343210", Asset: "BTC", Status: string(types.TxStatusConfirmed), Direction: models.TxDirectionInbound, Source: models.TxSourceChain, RawPayload: "{}", Fee: "0", Confirmations: 12, RequiredConfs: 12}
				if err := repositories.NewTransactionRepository(nil).Create(ctx, tx); err != nil {
					write(w, 500, map[string]string{"error": err.Error()})
					return
				}
			}
			write(w, 200, map[string]bool{"confirmed": true})
			return
		}
		base := "/api/v1/wallets/" + source.ID.String() + "/swaps"
		var body any
		status := 200
		var requestErr error
		switch {
		case r.Method == http.MethodPost && r.URL.Path == base+"/quotes/to/"+destination.ID.String():
			var input struct {
				From   string `json:"from_asset"`
				To     string `json:"to_asset"`
				Amount string `json:"amount"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
				write(w, 422, map[string]string{"error": "invalid quote"})
				return
			}
			row, err := service.CreateQuote(ctx, &account, &source, destination.ID, swap.QuoteInput{FromAsset: input.From, ToAsset: input.To, Amount: input.Amount})
			requestErr = err
			if err == nil {
				body = swapresources.NewQuote(row)
				status = 201
			}
		case r.Method == http.MethodPost && r.URL.Path == base:
			var input struct {
				Quote      uuid.UUID `json:"quote_id"`
				Key        uuid.UUID `json:"idempotency_key"`
				Passphrase string    `json:"passphrase"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
				write(w, 422, map[string]string{"error": "invalid acceptance"})
				return
			}
			row, err := service.Execute(ctx, &account, &source, swap.ExecuteInput{QuoteID: input.Quote, IdempotencyKey: input.Key, Passphrase: input.Passphrase, UserID: actor.ID})
			requestErr = err
			if err == nil {
				body = swapresources.NewOrder(row)
				status = 202
			}
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, base+"/by-key/"):
			id, err := uuid.Parse(strings.TrimPrefix(r.URL.Path, base+"/by-key/"))
			requestErr = err
			if err == nil {
				row, err := service.Recover(ctx, &account, &source, id)
				requestErr = err
				if err == nil {
					body = swapresources.NewOrder(row)
				}
			}
		default:
			write(w, 404, map[string]string{"error": "not found"})
			return
		}
		if requestErr != nil {
			status = 422
			code := "invalid_swap_request"
			if errors.Is(requestErr, swap.ErrNotFound) {
				status = 404
				code = "not_found"
			}
			if errors.Is(requestErr, swap.ErrExpired) {
				status = 409
				code = "swap_quote_expired"
			}
			write(w, status, map[string]any{"error": map[string]string{"code": code}})
			return
		}
		write(w, status, body)
	})
	address := os.Getenv("SWAP_DEMO_ADDRESS")
	if address == "" {
		address = "127.0.0.1:2396"
	}
	server = &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	t.Log("Controlled swap HTTP server ready; synthetic provider and chain data, no real funds")
	err = server.ListenAndServe()
	require.ErrorIs(t, err, http.ErrServerClosed)
}

const controlledReceiveHash = "2222222222222222222222222222222222222222222222222222222222222222"

type controlledSwapPolicy struct{}

func (controlledSwapPolicy) Resolve(context.Context, string, string) (swap.RoutingConfig, error) {
	return swap.RoutingConfig{Enabled: true, Preferred: swap.ProviderSwapKit, Fallback: true, SlippageBPS: 100, QuoteTTL: time.Minute, Providers: map[string]swap.ProviderConfig{swap.ProviderSwapKit: {Enabled: true}, swap.ProviderLiFi: {Enabled: true}}}, nil
}
func (controlledSwapPolicy) Provider(context.Context, string) (swap.ProviderConfig, error) {
	return swap.ProviderConfig{Enabled: true}, nil
}

type controlledSwapCatalog struct{}

func (controlledSwapCatalog) FindByID(_ context.Context, id string) (*models.Chain, error) {
	decimals, symbol := 8, "BTC"
	var network *int64
	if id == "eth" {
		decimals, symbol = 18, "ETH"
		n := int64(1)
		network = &n
	}
	return &models.Chain{ID: id, Status: "active", NativeSymbol: symbol, NativeDecimals: decimals, NetworkID: network}, nil
}
func (controlledSwapCatalog) TokensForChain(string) []types.Token { return nil }
func (controlledSwapCatalog) ValidateAddress(string, string) bool { return true }

type controlledSwapExecution struct {
	repo  *repositories.SwapRepository
	sends atomic.Int32
}

func (*controlledSwapExecution) Authorize(_ context.Context, _ *models.Wallet, in swap.ExecuteInput) error {
	if in.Passphrase != "controlled-passphrase" {
		return swap.ErrInvalidPassphrase
	}
	return nil
}
func (e *controlledSwapExecution) Send(ctx context.Context, order *models.SwapOrder, source *models.Wallet, quote *swap.Quote, _ *swap.Funding, _ swap.ExecuteInput) (*swap.SendResult, error) {
	hash := "0x1111111111111111111111111111111111111111111111111111111111111111"
	tx := &models.Transaction{ID: uuid.New(), WalletID: source.ID, AddressID: source.DepositAddressID, ExternalUserID: "system", Chain: "eth", TxType: models.TxTypeWithdrawal, TxHash: hash, LogIndex: models.TransactionLogIndexNone, ToAddress: "0x4444444444444444444444444444444444444444", FromAddress: source.DepositAddress.Address, Amount: quote.Request.AmountRaw, Asset: "ETH", Status: string(types.TxStatusPending), Direction: models.TxDirectionOutbound, Source: models.TxSourceWithdrawalFlow, RawPayload: "{}", Fee: "0"}
	step := &models.SwapExecutionStep{ID: uuid.New(), OrderID: order.ID, TransactionID: tx.ID, Position: 0, Purpose: "funding", TxHash: hash}
	if err := e.repo.RecordSigned(ctx, step, tx); err != nil {
		return nil, err
	}
	e.sends.Add(1)
	return &swap.SendResult{Hash: hash, Uncertain: true}, nil
}
