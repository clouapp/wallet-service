package providers

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
	"github.com/macrowallets/waas/app/services/withdrawrecoveryflow"
	runtime "github.com/macrowallets/waas/app/services/withdrawrecoveryruntime"
)

// Resolving the flow at application boot performs no configuration, key or RPC
// reads. Only an authenticated, wallet-scoped request constructs its runtime.
func newWithdrawalRecoveryFlow(app foundation.Application) *withdrawrecoveryflow.Flow {
	return withdrawrecoveryflow.New(func(ctx context.Context, walletID uuid.UUID) (*withdrawrecoveryflow.Scope, error) {
		accountID, ok := requestctx.AccountID(ctx)
		env, _ := requestctx.AccountEnvironment(ctx)
		if !ok || env != models.EnvironmentTest {
			return nil, recovery.ErrNotFound
		}
		wallets, err := resolve[*repositories.WalletRepository](app)
		if err != nil {
			return nil, err
		}
		wallet, err := wallets.FindByID(ctx, walletID)
		if err != nil || wallet == nil || wallet.AccountID == nil || *wallet.AccountID != accountID {
			return nil, recovery.ErrNotFound
		}
		accounts, err := resolve[*repositories.AccountRepository](app)
		if err != nil {
			return nil, err
		}
		account, err := accounts.FindByID(ctx, accountID)
		if err != nil || account == nil || account.Environment != models.EnvironmentTest || account.Status != models.StatusActive {
			return nil, recovery.ErrNotFound
		}
		chains, err := resolve[*repositories.ChainRepository](app)
		if err != nil {
			return nil, err
		}
		network, err := chains.FindByID(ctx, wallet.Chain)
		if err != nil || network == nil || !network.IsTestnet || network.AdapterType != models.AdapterTypeEVM || network.Status != models.StatusActive || network.NetworkID == nil || *network.NetworkID <= 0 {
			return nil, recovery.ErrRefused
		}
		endpoints, err := resolve[*repositories.ChainRPCEndpointRepository](app)
		if err != nil {
			return nil, err
		}
		fallbacks, err := endpoints.ListByChain(ctx, network.ID)
		if err != nil {
			return nil, err
		}
		flags, err := resolve[*features.Service](app)
		if err != nil {
			return nil, err
		}
		guard := func(ctx context.Context) error {
			if deadlineErr := gateRecoveryReviewDeadline(ctx, time.Now()); deadlineErr != nil {
				return deadlineErr
			}
			id, hasAccount := requestctx.AccountID(ctx)
			if !hasAccount || id != accountID {
				return recovery.ErrNotFound
			}
			if gateErr := flags.Gate(ctx, accountID, features.FlagWithdrawalsEnabled, features.CodeWithdrawalsPaused); gateErr != nil {
				return gateErr
			}
			return flags.Gate(ctx, accountID, features.FlagSweepEnabled, features.CodeSweepPaused)
		}
		pair, err := recoveryRPCPair(network, fallbacks, openChainEndpoint, func(endpoint string) runtime.RPC {
			return chainpkg.NewJSONRPCCaller(chainpkg.JSONRPCDeps{URL: endpoint})
		}, guard)
		if err != nil {
			return nil, err
		}
		revision, err := recoveryExecutableRevision()
		if err != nil {
			return nil, err
		}
		withdrawals, err := resolve[*repositories.WithdrawalRepository](app)
		if err != nil {
			return nil, err
		}
		transactions, err := resolve[*repositories.TransactionRepository](app)
		if err != nil {
			return nil, err
		}
		addresses, err := resolve[*repositories.AddressRepository](app)
		if err != nil {
			return nil, err
		}
		tokens, err := resolve[*repositories.TokenRepository](app)
		if err != nil {
			return nil, err
		}
		registry, err := resolve[*chainpkg.Registry](app)
		if err != nil {
			return nil, err
		}
		store, err := resolve[*repositories.WithdrawalRecoveryRepository](app)
		if err != nil {
			return nil, err
		}
		observer, err := runtime.New(runtime.Dependencies{Attempts: store, Withdrawals: withdrawals, Transactions: transactions, Wallets: wallets, Addresses: addresses, Accounts: accounts, Chains: chains, Tokens: tokens, Registry: registry, Endpoints: pair, ProviderOrigin: facades.Config().GetString("app.url"), CodeRevision: revision})
		if err != nil {
			return nil, err
		}
		box, err := resolve[*sweep.Box](app)
		if err != nil {
			return nil, err
		}
		signer, ok := box.Service.(recovery.Signer)
		if !ok {
			return nil, recovery.ErrRefused
		}
		privateSweep, err := newSweepService(app, func(ctx context.Context, id uuid.UUID) error {
			if id != accountID {
				return recovery.ErrNotFound
			}
			return guard(ctx)
		})
		if err != nil {
			return nil, err
		}
		preparer, ok := privateSweep.(sweep.DirectChildGasPreparer)
		if !ok {
			return nil, recovery.ErrRefused
		}
		gas := &recoveryGasBridge{preparer: preparer, observer: observer, endpoints: pair, guard: guard}
		service, err := recovery.NewService(recovery.Dependencies{Store: store, Observer: observer, Signer: signer, Sealer: settings.CryptSealer{}, Sender: observer, Gas: gas, CodeRevision: revision})
		if err != nil {
			return nil, err
		}
		return &withdrawrecoveryflow.Scope{WalletID: walletID, AccountID: accountID, Service: service, Runtime: observer, Store: store, GuardWrite: guard, QuoteGas: gas.quote}, nil
	})
}

func recoveryExecutableRevision() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("recovery executable unavailable")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(executable), "REVISION"))
	if err != nil {
		return "", fmt.Errorf("recovery revision unavailable")
	}
	return recoveryRevision(data)
}
func recoveryRevision(data []byte) (string, error) {
	revision := strings.TrimSpace(string(data))
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(revision) {
		return "", fmt.Errorf("recovery revision invalid")
	}
	return revision, nil
}

type recoveryGatedRPC struct {
	runtime.RPC
	guard func(context.Context) error
}

func (r *recoveryGatedRPC) Call(ctx context.Context, method string, out interface{}, params ...interface{}) error {
	if method == "eth_sendRawTransaction" {
		if r.guard == nil {
			return recovery.ErrRefused
		}
		if err := r.guard(ctx); err != nil {
			return err
		}
	}
	return r.RPC.Call(ctx, method, out, params...)
}

// Each client is built for one configured endpoint, never a failover pool.
// Paths/query credentials remain private and only the HTTPS origin is evidence.
func recoveryRPCPair(network *models.Chain, fallback []models.ChainRPCEndpoint, open func(string) (string, error), dial func(string) runtime.RPC, guard func(context.Context) error) ([2]runtime.Endpoint, error) {
	var result [2]runtime.Endpoint
	if network == nil || open == nil || dial == nil {
		return result, recovery.ErrRefused
	}
	stored := []string{network.RpcURL}
	for _, row := range fallback {
		if row.Enabled && row.ChainID == network.ID {
			stored = append(stored, row.URL)
		}
	}
	seen := map[string]bool{}
	count := 0
	for _, value := range stored {
		plain, err := open(value)
		if err != nil {
			return result, fmt.Errorf("recovery RPC configuration unavailable")
		}
		// Resolve environment-backed URLs privately before constructing either client.
		plain, err = models.ResolveRPCURL(plain)
		if err != nil {
			return result, fmt.Errorf("recovery RPC configuration unavailable")
		}
		u, err := url.Parse(plain)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return result, fmt.Errorf("recovery requires configured HTTPS RPC providers")
		}
		host := strings.ToLower(u.Hostname())
		if seen[host] {
			continue
		}
		seen[host] = true
		client := dial(plain)
		if client == nil {
			return result, recovery.ErrRefused
		}
		result[count] = runtime.Endpoint{Origin: "https://" + strings.ToLower(u.Host), Client: &recoveryGatedRPC{RPC: client, guard: guard}}
		count++
		if count == 2 {
			return result, nil
		}
	}
	return result, fmt.Errorf("recovery requires two independent configured RPC providers")
}

type recoveryReviewDeadlineKey struct{}

func withRecoveryReviewDeadline(ctx context.Context, expiry time.Time) context.Context {
	return context.WithValue(ctx, recoveryReviewDeadlineKey{}, expiry)
}
func gateRecoveryReviewDeadline(ctx context.Context, now time.Time) error {
	expiry, present := ctx.Value(recoveryReviewDeadlineKey{}).(time.Time)
	if present && !expiry.After(now) {
		return recovery.ErrRefused
	}
	return nil
}
