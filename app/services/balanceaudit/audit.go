package balanceaudit

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/security"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// DefaultLimit is how many addresses one audit reads when the request names none.
	DefaultLimit = 100
	// MaxLimit is the most addresses one audit reads.
	MaxLimit = 1000
	// DefaultMaxReads is how many balances one audit reads from the chain at most
	// when the request names no budget.
	DefaultMaxReads = 500

	mismatchMessage = "deposit address balance differs from its recorded transactions"
)

// Status is the verdict on one asset of one address.
type Status string

const (
	// StatusOK is a balance within dust of what the recorded transactions explain.
	StatusOK Status = "ok"
	// StatusMismatch is a balance beyond dust of it: a deposit, or an outflow, no row records.
	StatusMismatch Status = "mismatch"
	// StatusInFlight is an asset with a transaction still pending or confirming: not judged.
	StatusInFlight Status = "in_flight"
	// StatusUnverified is an asset whose balance or expected balance could not be known.
	StatusUnverified Status = "unverified"
)

// Audit is one audit request. Zero values take the defaults: DefaultLimit
// addresses, DefaultMaxReads balance reads, no spacing, each asset's dust from the
// chain adapter (none: 0), and the native asset with every token the chain lists.
type Audit struct {
	ChainID   string
	AccountID *uuid.UUID
	// After resumes after this address id (Report.Next of an earlier audit).
	After    uuid.UUID
	Limit    int
	MaxReads int
	// Spacing is the wait between two balance reads.
	Spacing time.Duration
	// Dust, when set, is the tolerance of every asset, in base units.
	Dust *big.Int
	// Assets restricts the audit to these symbols.
	Assets []string
}

// Row is the verdict on one asset of one address. OnChain is nil when the balance
// was not read; Difference is OnChain − Expected.
type Row struct {
	AddressID     uuid.UUID
	WalletID      uuid.UUID
	Address       string
	Asset         string
	TokenContract string
	OnChain       *big.Int
	Expected      *big.Int
	Difference    *big.Int
	Dust          *big.Int
	Status        Status
	Reason        string
}

// Report is the outcome of one audit. Next is the last address audited when a
// limit stopped the audit before the chain's last address (resume with After);
// uuid.Nil when none is left.
type Report struct {
	Chain      string
	Addresses  int
	Reads      int
	Rows       []Row
	Next       uuid.UUID
	OK         int
	Mismatched int
	InFlight   int
	Unverified int
}

// auditedAsset is one asset an audit reads on each address: the native asset
// (token nil) or a token the chain lists.
type auditedAsset struct {
	symbol string
	token  *types.Token
}

// plan is a validated request.
type plan struct {
	Audit
	adapter types.Chain
	native  string
	assets  []auditedAsset
	// onlySelected leaves out the ledger tokens the chain does not list.
	onlySelected bool
}

// AuditBalances audits the addresses of audit.ChainID by id, one page of at most
// Limit addresses, and stops before an address whose reads would pass MaxReads
// (the first address is always read). Each address's assets are read whole or not
// at all. A balance that cannot be read is a row, not a failure; an invalid request,
// a store that fails or a cancelled context fail the audit.
func (s *Service) AuditBalances(ctx context.Context, audit Audit) (Report, error) {
	p, err := s.plan(audit)
	if err != nil {
		return Report{}, err
	}
	addresses, err := s.addresses.ListForBalanceAudit(ctx, p.ChainID, p.AccountID, p.After, p.Limit+1)
	if err != nil {
		return Report{}, err
	}
	report := Report{Chain: p.ChainID}
	more := len(addresses) > p.Limit
	if more {
		addresses = addresses[:p.Limit]
	}
	if len(addresses) == 0 {
		return report, nil
	}
	ledger, err := s.loadLedger(ctx, p, addresses)
	if err != nil {
		return Report{}, err
	}

	reads := len(p.assets)
	for i, address := range addresses {
		if report.Addresses > 0 && report.Reads+reads > p.MaxReads {
			report.Next = addresses[i-1].ID
			return report, nil
		}
		rows, err := s.auditAddress(ctx, p, address, ledger[address.Address], &report.Reads)
		if err != nil {
			return report, err
		}
		report.Addresses++
		for _, row := range rows {
			report.add(row)
		}
	}
	if more {
		report.Next = addresses[len(addresses)-1].ID
	}
	return report, nil
}

// plan validates audit and resolves its chain, assets and defaults.
func (s *Service) plan(audit Audit) (plan, error) {
	audit.ChainID = strings.TrimSpace(audit.ChainID)
	if audit.ChainID == "" {
		return plan{}, fmt.Errorf("%w: a chain is required", ErrInvalidAudit)
	}
	switch {
	case audit.Limit < 0 || audit.Limit > MaxLimit:
		return plan{}, fmt.Errorf("%w: the limit is between 1 and %d addresses", ErrInvalidAudit, MaxLimit)
	case audit.MaxReads < 0:
		return plan{}, fmt.Errorf("%w: the read budget cannot be negative", ErrInvalidAudit)
	case audit.Spacing < 0:
		return plan{}, fmt.Errorf("%w: the spacing cannot be negative", ErrInvalidAudit)
	case audit.Dust != nil && audit.Dust.Sign() < 0:
		return plan{}, fmt.Errorf("%w: the dust cannot be negative", ErrInvalidAudit)
	}
	if audit.Limit == 0 {
		audit.Limit = DefaultLimit
	}
	if audit.MaxReads == 0 {
		audit.MaxReads = DefaultMaxReads
	}
	adapter, err := s.chains.Chain(audit.ChainID)
	if err != nil {
		return plan{}, err
	}
	p := plan{Audit: audit, adapter: adapter, native: adapter.NativeAsset()}
	available := []auditedAsset{{symbol: p.native}}
	for _, token := range s.chains.TokensForChain(audit.ChainID) {
		listed := token
		available = append(available, auditedAsset{symbol: token.Symbol, token: &listed})
	}
	if len(audit.Assets) == 0 {
		p.assets = available
		return p, nil
	}
	p.onlySelected = true
	for _, wanted := range audit.Assets {
		found := false
		for _, asset := range available {
			if types.SameAssetSymbol(asset.symbol, strings.TrimSpace(wanted)) {
				p.assets, found = append(p.assets, asset), true
				break
			}
		}
		if !found {
			return plan{}, fmt.Errorf("%w: %s is neither the native asset nor a token of %s", ErrInvalidAudit, wanted, audit.ChainID)
		}
	}
	return p, nil
}

// addressLedger is what the recorded transactions explain of one address: per
// asset (the native one under ""), and the native fees its outbound rows paid.
type addressLedger struct {
	assets map[string]*assetLedger
	fees   models.AddressFeeEntry
}

type assetLedger struct {
	symbol   string
	contract string
	credited *big.Int
	debited  *big.Int
	inFlight int
	// listed is false for a token the chain does not list.
	listed bool
}

// loadLedger reads the ledger of the page's addresses, keyed by address; a token
// contract is keyed by the registered contract it matches (EVM contracts match in
// any case).
func (s *Service) loadLedger(ctx context.Context, p plan, addresses []models.Address) (map[string]*addressLedger, error) {
	values := make([]string, len(addresses))
	for i, address := range addresses {
		values[i] = address.Address
	}
	entries, err := s.ledger.AddressLedger(ctx, p.ChainID, values)
	if err != nil {
		return nil, err
	}
	fees, err := s.ledger.AddressFees(ctx, p.ChainID, values)
	if err != nil {
		return nil, err
	}
	ledgers := map[string]*addressLedger{}
	of := func(address string) *addressLedger {
		if ledgers[address] == nil {
			ledgers[address] = &addressLedger{assets: map[string]*assetLedger{}}
		}
		return ledgers[address]
	}
	for _, entry := range entries {
		key, symbol, listed := "", p.native, true
		if entry.TokenContract != "" {
			key, symbol = entry.TokenContract, entry.Asset
			if token, err := s.chains.FindTokenByContract(p.ChainID, entry.TokenContract); err == nil && token != nil {
				key, symbol = token.Contract, token.Symbol
			} else {
				listed = false
			}
		}
		credited, err := baseUnits(entry.Credited)
		if err != nil {
			return nil, err
		}
		debited, err := baseUnits(entry.Debited)
		if err != nil {
			return nil, err
		}
		ledger := of(entry.Address)
		asset := ledger.assets[key]
		if asset == nil {
			asset = &assetLedger{symbol: symbol, contract: key, credited: new(big.Int), debited: new(big.Int), listed: listed}
			ledger.assets[key] = asset
		}
		asset.credited.Add(asset.credited, credited)
		asset.debited.Add(asset.debited, debited)
		asset.inFlight += entry.InFlight
	}
	for _, fee := range fees {
		of(fee.Address).fees = fee
	}
	return ledgers, nil
}

// auditAddress reads each planned asset of address, spaced, and judges it; then
// reports the ledger tokens the chain does not list, unread.
func (s *Service) auditAddress(ctx context.Context, p plan, address models.Address, ledger *addressLedger, reads *int) ([]Row, error) {
	if ledger == nil {
		ledger = &addressLedger{assets: map[string]*assetLedger{}}
	}
	fees, err := baseUnits(ledger.fees.Fees)
	if err != nil {
		return nil, err
	}
	var rows []Row
	for _, asset := range p.assets {
		if *reads > 0 {
			if err := wait(ctx, p.Spacing); err != nil {
				return nil, err
			}
		} else if err := ctx.Err(); err != nil {
			return nil, err
		}
		*reads++
		key := ""
		if asset.token != nil {
			key = asset.token.Contract
		}
		recorded := ledger.assets[key]
		row := Row{
			AddressID: address.ID, WalletID: address.WalletID, Address: address.Address,
			Asset: asset.symbol, TokenContract: key, Dust: s.dust(p, asset.symbol),
		}
		expected, inFlight := new(big.Int), 0
		if recorded != nil {
			expected.Sub(recorded.credited, recorded.debited)
			inFlight = recorded.inFlight
		}
		if asset.token == nil {
			expected.Sub(expected, fees)
			inFlight += ledger.fees.InFlight
		}
		row.Expected = expected
		onChain, readErr := s.balance(ctx, p.adapter, address.Address, asset.token)
		switch {
		case readErr != nil:
			row.Status, row.Reason = StatusUnverified, "balance not read: "+security.RedactText(chain.ClientText(readErr))
		default:
			row.OnChain = onChain
			row.Difference = new(big.Int).Sub(onChain, expected)
			row.Status, row.Reason = judge(row, inFlight, asset.token == nil, ledger.fees.MissingFees)
		}
		if row.Status == StatusMismatch {
			slog.Warn(mismatchMessage,
				"chain", p.ChainID, "address_id", row.AddressID.String(), "wallet_id", row.WalletID.String(),
				"asset", row.Asset, "token_contract", row.TokenContract,
				"on_chain", row.OnChain.String(), "expected", row.Expected.String(), "difference", row.Difference.String(), "dust", row.Dust.String())
		}
		rows = append(rows, row)
	}
	if !p.onlySelected {
		rows = append(rows, unlistedTokens(p, address, ledger)...)
	}
	return rows, nil
}

// judge is the verdict on a read balance: an asset with transactions in flight, or
// a native balance whose confirmed outbound rows lack their fee, is not judged.
func judge(row Row, inFlight int, native bool, missingFees int) (Status, string) {
	switch {
	case inFlight > 0:
		return StatusInFlight, fmt.Sprintf("%d transactions still pending or confirming", inFlight)
	case native && missingFees > 0:
		return StatusUnverified, fmt.Sprintf("%d confirmed outbound transfers have no recorded fee (transactions:backfill-fees)", missingFees)
	case new(big.Int).Abs(row.Difference).Cmp(row.Dust) <= 0:
		return StatusOK, ""
	case row.Difference.Sign() > 0:
		return StatusMismatch, "the chain holds more than the recorded transactions explain (a deposit no row records)"
	default:
		return StatusMismatch, "the chain holds less than the recorded transactions explain (an outflow no row records)"
	}
}

// unlistedTokens reports, unread, the tokens the ledger of address records that
// the chain does not list (their balance cannot be read through the registry).
func unlistedTokens(p plan, address models.Address, ledger *addressLedger) []Row {
	contracts := make([]string, 0, len(ledger.assets))
	for contract, asset := range ledger.assets {
		if !asset.listed {
			contracts = append(contracts, contract)
		}
	}
	sort.Strings(contracts)
	var rows []Row
	for _, contract := range contracts {
		asset := ledger.assets[contract]
		rows = append(rows, Row{
			AddressID: address.ID, WalletID: address.WalletID, Address: address.Address,
			Asset: asset.symbol, TokenContract: asset.contract,
			Expected: new(big.Int).Sub(asset.credited, asset.debited),
			Status:   StatusUnverified,
			Reason:   "token not listed for " + p.ChainID + "; its balance is not read",
		})
	}
	return rows
}

// balance reads the native balance, or the token's, of address.
func (s *Service) balance(ctx context.Context, adapter types.Chain, address string, token *types.Token) (*big.Int, error) {
	var balance *types.Balance
	var err error
	if token == nil {
		balance, err = adapter.GetBalance(ctx, address)
	} else {
		balance, err = adapter.GetTokenBalance(ctx, address, *token)
	}
	if err != nil {
		return nil, err
	}
	if balance == nil || balance.Amount == nil {
		return nil, fmt.Errorf("the chain answered no amount")
	}
	return new(big.Int).Set(balance.Amount), nil
}

// dust is the tolerance of symbol: the request's, else the adapter's, else 0.
func (s *Service) dust(p plan, symbol string) *big.Int {
	if p.Dust != nil {
		return new(big.Int).Set(p.Dust)
	}
	if threshold := p.adapter.DustThreshold(symbol); threshold != nil && threshold.Sign() > 0 {
		return new(big.Int).Set(threshold)
	}
	return new(big.Int)
}

func (r *Report) add(row Row) {
	r.Rows = append(r.Rows, row)
	switch row.Status {
	case StatusOK:
		r.OK++
	case StatusMismatch:
		r.Mismatched++
	case StatusInFlight:
		r.InFlight++
	default:
		r.Unverified++
	}
}

// baseUnits parses a numeric text the ledger sums; empty is 0.
func baseUnits(text string) (*big.Int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return new(big.Int), nil
	}
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return nil, fmt.Errorf("ledger amount %q is not an integer of base units", text)
	}
	return value, nil
}

// wait sleeps d unless ctx ends first.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
