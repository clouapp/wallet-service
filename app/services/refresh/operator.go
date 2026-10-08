package refresh

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// Output is what an operator command prints. A returned error is a failure;
// SoftError lines are misses the command reports and then continues.
type Output struct {
	Info      []string
	Warning   []string
	Line      []string
	SoftError []string
}

// WalletLookup loads one wallet. The command does not query.
type WalletLookup interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
}

// AddressLookup loads one address on a chain.
type AddressLookup interface {
	FindByChainAndAddress(ctx context.Context, chainID, address string) (*models.Address, error)
}

// TransactionLookup loads one transaction by chain and hash.
type TransactionLookup interface {
	FindByChainAndTxHash(ctx context.Context, chainID, txHash string) (*models.Transaction, error)
}

// ChainCatalog resolves a currency symbol to a chain id.
type ChainCatalog interface {
	ChainIDs() []string
	Chain(id string) (types.Chain, error)
}

// OperatorDeps is everything the refresh and reconcile commands share.
type OperatorDeps struct {
	Balances       *BalanceService
	Dispatcher     Dispatcher
	Wallets        WalletLookup
	Addresses      AddressLookup
	Transactions   TransactionLookup
	Chains         ChainCatalog
	RequestRefresh func(walletID, chainID string) error
}

// Operator runs one refresh or reconcile the way the artisan command asks.
type Operator struct {
	balances       *BalanceService
	dispatcher     Dispatcher
	wallets        WalletLookup
	addresses      AddressLookup
	transactions   TransactionLookup
	chains         ChainCatalog
	requestRefresh func(walletID, chainID string) error
}

// NewOperator wires the operator from OperatorDeps. A missing finder is
// reported when that command runs.
func NewOperator(deps OperatorDeps) *Operator {
	return &Operator{
		balances:       deps.Balances,
		dispatcher:     deps.Dispatcher,
		wallets:        deps.Wallets,
		addresses:      deps.Addresses,
		transactions:   deps.Transactions,
		chains:         deps.Chains,
		requestRefresh: deps.RequestRefresh,
	}
}

// WalletCommand is one refresh:wallet invocation.
type WalletCommand struct {
	WalletID string
	Scope    string
	Chain    string
	Queue    bool
	Reason   string
}

// RefreshWallet refreshes one wallet in process or on the queue.
func (o *Operator) RefreshWallet(ctx context.Context, cmd WalletCommand) (Output, error) {
	var out Output
	wallet, err := o.walletByID(ctx, cmd.WalletID)
	if err != nil {
		return out, err
	}
	chainID := cmd.Chain
	if chainID == "" {
		chainID = wallet.Chain
	}
	if cmd.Queue {
		out.Info = append(out.Info, "dispatching wallet refresh to blockchain queue: wallet="+cmd.WalletID+" scope="+cmd.Scope+" reason="+cmd.Reason)
		return out, o.dispatchQueuedRefresh(cmd.Scope, cmd.WalletID, chainID)
	}
	out.Info = append(out.Info, "sync mode: refreshing wallet="+cmd.WalletID+" scope="+cmd.Scope+" reason="+cmd.Reason)
	if err := o.refresh(ctx, "refresh:wallet", wallet); err != nil {
		return out, fmt.Errorf("refresh failed: %w", err)
	}
	out.Info = append(out.Info, "wallet "+cmd.WalletID+" refreshed successfully")
	return out, nil
}

// AddressCommand is one refresh:address invocation.
type AddressCommand struct {
	Chain     string
	Addresses []string
	Queue     bool
	Reason    string
}

// RefreshAddresses refreshes the wallets that own the given addresses.
func (o *Operator) RefreshAddresses(ctx context.Context, cmd AddressCommand) (Output, error) {
	var out Output
	if len(cmd.Addresses) == 0 {
		return out, fmt.Errorf("at least one address is required")
	}
	if o == nil || o.addresses == nil {
		return out, fmt.Errorf("refresh:address: address store is required")
	}
	for _, addr := range cmd.Addresses {
		record, err := o.addresses.FindByChainAndAddress(ctx, cmd.Chain, addr)
		if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
			return out, fmt.Errorf("failed to look up address %s: %w", addr, err)
		}
		if err != nil || record == nil {
			out.SoftError = append(out.SoftError, "address not found: chain="+cmd.Chain+" address="+addr)
			continue
		}
		wallet, err := o.walletForAddress(ctx, record.WalletID, addr)
		if err != nil {
			if errors.Is(err, errWalletMissing) {
				out.SoftError = append(out.SoftError, "wallet not found for address "+addr)
				continue
			}
			return out, err
		}
		if cmd.Queue {
			out.Info = append(out.Info, "dispatching refresh for address "+addr+" wallet="+wallet.ID.String()+" reason="+cmd.Reason)
			if err := o.dispatchNamed("refresh:address", "balances", wallet.ID.String(), wallet.Chain); err != nil {
				return out, fmt.Errorf("dispatch failed for address %s: %w", addr, err)
			}
			continue
		}
		out.Info = append(out.Info, "sync mode: refreshing address "+addr+" wallet="+wallet.ID.String())
		if err := o.refresh(ctx, "refresh:address", wallet); err != nil {
			return out, fmt.Errorf("refresh failed for address %s: %w", addr, err)
		}
		out.Info = append(out.Info, "address "+addr+" refreshed successfully")
	}
	out.Info = append(out.Info, "processed "+fmt.Sprint(len(cmd.Addresses))+" address(es) on chain="+cmd.Chain+" ["+strings.Join(cmd.Addresses, ", ")+"]")
	return out, nil
}

// CurrencyCommand is one refresh:currency invocation.
type CurrencyCommand struct {
	Currency  string
	Addresses []string
	Chain     string
	Queue     bool
	Reason    string
}

// RefreshCurrency refreshes wallets that hold one currency.
func (o *Operator) RefreshCurrency(ctx context.Context, cmd CurrencyCommand) (Output, error) {
	var out Output
	currency := strings.ToLower(cmd.Currency)
	if len(cmd.Addresses) == 0 {
		return out, fmt.Errorf("at least one address is required")
	}
	if AmbiguousCurrency(currency) && cmd.Chain == "" {
		return out, fmt.Errorf("currency %q exists on multiple chains — provide --chain to disambiguate", currency)
	}
	chainID, err := resolveCurrency(o.chains, currency, cmd.Chain)
	if err != nil {
		return out, err
	}
	if o == nil || o.addresses == nil {
		return out, fmt.Errorf("refresh:currency: address store is required")
	}
	for _, addr := range cmd.Addresses {
		record, err := o.addresses.FindByChainAndAddress(ctx, chainID, addr)
		if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
			return out, fmt.Errorf("failed to look up address %s: %w", addr, err)
		}
		if err != nil || record == nil {
			out.SoftError = append(out.SoftError, "address not found: chain="+chainID+" address="+addr)
			continue
		}
		wallet, err := o.walletForAddress(ctx, record.WalletID, addr)
		if err != nil {
			if errors.Is(err, errWalletMissing) {
				out.SoftError = append(out.SoftError, "wallet not found for address "+addr)
				continue
			}
			return out, err
		}
		if cmd.Queue {
			out.Info = append(out.Info, "dispatching refresh for currency="+currency+" address="+addr+" wallet="+wallet.ID.String()+" reason="+cmd.Reason)
			if err := o.dispatchNamed("refresh:currency", "balances", wallet.ID.String(), wallet.Chain); err != nil {
				return out, fmt.Errorf("dispatch failed for address %s: %w", addr, err)
			}
			continue
		}
		out.Info = append(out.Info, "sync mode: refreshing currency="+currency+" address="+addr+" wallet="+wallet.ID.String())
		if err := o.refresh(ctx, "refresh:currency", wallet); err != nil {
			return out, fmt.Errorf("refresh failed for address %s: %w", addr, err)
		}
		out.Info = append(out.Info, "address "+addr+" refreshed successfully")
	}
	out.Info = append(out.Info, "processed "+fmt.Sprint(len(cmd.Addresses))+" address(es) for currency="+currency+" chain="+chainID)
	return out, nil
}

// TxCommand is one refresh:tx invocation.
type TxCommand struct {
	Chain  string
	TxHash string
	Queue  bool
	Reason string
}

// RefreshTransaction refreshes the wallet that owns one transaction.
func (o *Operator) RefreshTransaction(ctx context.Context, cmd TxCommand) (Output, error) {
	var out Output
	if o == nil || o.transactions == nil {
		return out, fmt.Errorf("refresh:tx: transaction store is required")
	}
	tx, err := o.transactions.FindByChainAndTxHash(ctx, cmd.Chain, cmd.TxHash)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return out, fmt.Errorf("failed to look up transaction: %w", err)
	}
	if err != nil || tx == nil {
		return out, fmt.Errorf("transaction not found: chain=%s tx_hash=%s", cmd.Chain, cmd.TxHash)
	}
	wallet, err := o.loadWallet(ctx, tx.WalletID)
	if err != nil {
		if errors.Is(err, errWalletMissing) {
			return out, fmt.Errorf("wallet not found for transaction: wallet_id=%s", tx.WalletID)
		}
		return out, fmt.Errorf("failed to load wallet for transaction: %w", err)
	}
	if cmd.Queue {
		out.Info = append(out.Info, "dispatching tx refresh to blockchain queue: chain="+cmd.Chain+" tx_hash="+cmd.TxHash+" wallet="+wallet.ID.String()+" reason="+cmd.Reason)
		return out, o.dispatchNamed("refresh:tx", "transactions", wallet.ID.String(), wallet.Chain)
	}
	out.Info = append(out.Info, "sync mode: refreshing tx chain="+cmd.Chain+" tx_hash="+cmd.TxHash+" wallet="+wallet.ID.String())
	if err := o.refresh(ctx, "refresh:tx", wallet); err != nil {
		return out, fmt.Errorf("refresh failed: %w", err)
	}
	out.Info = append(out.Info, "transaction "+cmd.TxHash+" wallet refreshed successfully")
	return out, nil
}

// ReconcileCommand is one reconcile:wallet invocation.
type ReconcileCommand struct {
	WalletID string
	Queue    bool
	Reason   string
}

// Reconcile compares one wallet with the chain, in process or on the queue.
func (o *Operator) Reconcile(ctx context.Context, cmd ReconcileCommand) (Output, error) {
	var out Output
	wallet, err := o.walletByID(ctx, cmd.WalletID)
	if err != nil {
		return out, err
	}
	if cmd.Queue {
		out.Info = append(out.Info, "dispatching wallet reconciliation to blockchain queue: wallet="+cmd.WalletID+" reason="+cmd.Reason)
		return out, o.dispatchNamed("reconcile:wallet", "reconcile", cmd.WalletID, wallet.Chain)
	}
	out.Info = append(out.Info, "sync mode: reconciling wallet="+cmd.WalletID+" reason="+cmd.Reason)
	if err := o.refresh(ctx, "reconcile:wallet", wallet); err != nil {
		return out, fmt.Errorf("reconciliation failed: %w", err)
	}
	out.Info = append(out.Info, "wallet "+cmd.WalletID+" reconciled successfully")
	return out, nil
}

var ambiguousCurrencies = map[string]bool{
	"usdt": true,
	"usdc": true,
	"dai":  true,
	"wbtc": true,
	"weth": true,
}

// AmbiguousCurrency reports a symbol that exists on more than one chain.
func AmbiguousCurrency(code string) bool {
	return ambiguousCurrencies[strings.ToLower(code)]
}

var errWalletMissing = errors.New("wallet missing")

func (o *Operator) walletByID(ctx context.Context, walletID string) (*models.Wallet, error) {
	id, err := uuid.Parse(walletID)
	if err != nil {
		return nil, fmt.Errorf("invalid wallet_id: %s", walletID)
	}
	wallet, err := o.loadWallet(ctx, id)
	if err != nil {
		if errors.Is(err, errWalletMissing) {
			return nil, fmt.Errorf("wallet not found: %s", walletID)
		}
		return nil, fmt.Errorf("failed to load wallet: %w", err)
	}
	return wallet, nil
}

func (o *Operator) walletForAddress(ctx context.Context, id uuid.UUID, addr string) (*models.Wallet, error) {
	wallet, err := o.loadWallet(ctx, id)
	if err != nil {
		if errors.Is(err, errWalletMissing) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to load wallet for address %s: %w", addr, err)
	}
	return wallet, nil
}

func (o *Operator) loadWallet(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	if o == nil || o.wallets == nil {
		return nil, fmt.Errorf("wallet store is required")
	}
	wallet, err := o.wallets.FindByID(ctx, id)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, err
	}
	if wallet == nil || errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, errWalletMissing
	}
	return wallet, nil
}

func (o *Operator) refresh(ctx context.Context, label string, wallet *models.Wallet) error {
	if o == nil || o.balances == nil {
		return fmt.Errorf("%s: balance refresh service is not initialized", label)
	}
	return o.balances.RefreshWallet(ctx, wallet)
}

func (o *Operator) dispatchNamed(label, name, walletID, chainID string) error {
	if o == nil || o.dispatcher == nil {
		return fmt.Errorf("%s: refresh dispatcher is not initialized", label)
	}
	switch name {
	case "balances":
		return o.dispatcher.DispatchBalances(walletID, chainID)
	case "transactions":
		return o.dispatcher.DispatchTransactions(walletID, chainID)
	case "reconcile":
		return o.dispatcher.DispatchReconcile(walletID, chainID)
	default:
		return fmt.Errorf("%s: refresh dispatcher is not initialized", label)
	}
}

func (o *Operator) dispatchQueuedRefresh(scope, walletID, chainID string) error {
	switch scope {
	case "balances":
		if err := o.requestWalletRefresh(walletID, chainID); err != nil {
			return err
		}
		return o.dispatchNamed("refresh:wallet", "balances", walletID, chainID)
	case "full":
		if err := o.requestWalletRefresh(walletID, chainID); err != nil {
			return err
		}
		return o.dispatchScopedJobs(scope, walletID, chainID)
	default:
		return o.dispatchScopedJobs(scope, walletID, chainID)
	}
}

func (o *Operator) requestWalletRefresh(walletID, chainID string) error {
	if o == nil || o.requestRefresh == nil {
		return fmt.Errorf("refresh:wallet: event dispatcher is not initialized")
	}
	return o.requestRefresh(walletID, chainID)
}

func (o *Operator) dispatchScopedJobs(scope, walletID, chainID string) error {
	if o == nil || o.dispatcher == nil {
		return fmt.Errorf("refresh:wallet: refresh dispatcher is not initialized")
	}
	switch scope {
	case "balances":
		return o.dispatcher.DispatchBalances(walletID, chainID)
	case "transactions":
		return o.dispatcher.DispatchTransactions(walletID, chainID)
	case "tokens":
		return o.dispatcher.DispatchTokens(walletID, chainID)
	case "utxos":
		return o.dispatcher.DispatchUTXOs(walletID, chainID)
	case "full":
		for _, call := range []func(string, string) error{
			o.dispatcher.DispatchBalances,
			o.dispatcher.DispatchTransactions,
			o.dispatcher.DispatchTokens,
			o.dispatcher.DispatchUTXOs,
		} {
			if err := call(walletID, chainID); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown scope: %s", scope)
	}
}

func resolveCurrency(catalog ChainCatalog, currency, chainFlag string) (string, error) {
	if catalog == nil {
		return "", fmt.Errorf("refresh:currency: chain registry is not initialized")
	}
	if chainFlag != "" {
		return chainFlag, nil
	}
	for _, id := range catalog.ChainIDs() {
		if id == currency {
			return id, nil
		}
	}
	for _, id := range catalog.ChainIDs() {
		adapter, err := catalog.Chain(id)
		if err != nil {
			continue
		}
		if types.SameAssetSymbol(adapter.NativeAsset(), currency) {
			return id, nil
		}
	}
	return "", fmt.Errorf("cannot resolve currency %q to a chain — provide --chain", currency)
}
