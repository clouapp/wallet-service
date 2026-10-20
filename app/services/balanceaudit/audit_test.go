package balanceaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	auditChain = "teth"
	usdc       = "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"
)

var (
	baseID   = uuid.MustParse("10000000-0000-0000-0000-000000000001")
	childID  = uuid.MustParse("20000000-0000-0000-0000-000000000002")
	missedID = uuid.MustParse("30000000-0000-0000-0000-000000000003")
	walletID = uuid.MustParse("90000000-0000-0000-0000-000000000009")
)

// listedAddresses serves addresses by id, as the repository does.
type listedAddresses struct {
	rows      []models.Address
	accountID *uuid.UUID
	after     []uuid.UUID
	limits    []int
}

func (l *listedAddresses) ListForBalanceAudit(_ context.Context, chainID string, accountID *uuid.UUID, afterID uuid.UUID, limit int) ([]models.Address, error) {
	l.accountID = accountID
	l.after = append(l.after, afterID)
	l.limits = append(l.limits, limit)
	var page []models.Address
	for _, row := range l.rows {
		if row.Chain == chainID && (afterID == uuid.Nil || row.ID.String() > afterID.String()) && len(page) < limit {
			page = append(page, row)
		}
	}
	return page, nil
}

// recordedLedger answers the sums the repository would.
type recordedLedger struct {
	entries  []models.AddressLedgerEntry
	fees     []models.AddressFeeEntry
	asked    [][]string
	withRent []bool
}

func (r *recordedLedger) AddressLedger(_ context.Context, _ string, addresses []string) ([]models.AddressLedgerEntry, error) {
	r.asked = append(r.asked, append([]string(nil), addresses...))
	return r.entries, nil
}

func (r *recordedLedger) AddressFees(_ context.Context, _ string, _ []string, withAccountRent bool) ([]models.AddressFeeEntry, error) {
	r.withRent = append(r.withRent, withAccountRent)
	return r.fees, nil
}

// chainBalances answers balance reads from maps keyed by address (native) and
// address/contract (tokens), counts them, and fails a read listed in failing.
type chainBalances struct {
	mu      sync.Mutex
	native  map[string]int64
	tokens  map[string]int64
	failing map[string]error
	reads   []string
	readAt  []time.Time
}

func (c *chainBalances) read(key string) (*big.Int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads = append(c.reads, key)
	c.readAt = append(c.readAt, time.Now())
	if err := c.failing[key]; err != nil {
		return nil, err
	}
	if value, ok := c.native[key]; ok {
		return big.NewInt(value), nil
	}
	if value, ok := c.tokens[key]; ok {
		return big.NewInt(value), nil
	}
	return big.NewInt(0), nil
}

type auditFixture struct {
	svc       *Service
	addresses *listedAddresses
	ledger    *recordedLedger
	balances  *chainBalances
	adapter   *mocks.MockChain
}

func address(id uuid.UUID, value string) models.Address {
	return models.Address{ID: id, WalletID: walletID, Chain: auditChain, Address: value}
}

func newAuditFixture(t *testing.T) auditFixture {
	t.Helper()
	balances := &chainBalances{native: map[string]int64{}, tokens: map[string]int64{}, failing: map[string]error{}}
	adapter := mocks.NewMockChain(auditChain)
	adapter.NativeAssetVal = "ETH"
	adapter.GetBalanceFn = func(_ context.Context, addr string) (*types.Balance, error) {
		amount, err := balances.read(addr)
		if err != nil {
			return nil, err
		}
		return &types.Balance{Address: addr, Asset: "ETH", Amount: amount}, nil
	}
	adapter.GetTokenBalanceFn = func(_ context.Context, addr string, token types.Token) (*types.Balance, error) {
		amount, err := balances.read(addr + "/" + token.Contract)
		if err != nil {
			return nil, err
		}
		return &types.Balance{Address: addr, Asset: token.Symbol, Amount: amount}, nil
	}
	adapter.DustThresholdFn = func(asset string) *big.Int {
		if asset == "ETH" {
			return big.NewInt(10)
		}
		return nil
	}
	refuseWrites := func(string) { t.Fatal("the audit must not build, sign or broadcast anything") }
	adapter.BuildTransferFn = func(context.Context, types.TransferRequest) (*types.UnsignedTx, error) {
		refuseWrites("build")
		return nil, nil
	}
	adapter.BroadcastTransactionFn = func(context.Context, *types.SignedTx) (string, error) {
		refuseWrites("broadcast")
		return "", nil
	}
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	registry.RegisterToken(types.Token{Symbol: "USDC", Contract: usdc, Decimals: 6, ChainID: auditChain})

	addresses := &listedAddresses{rows: []models.Address{
		address(baseID, "0xbase"), address(childID, "0xchild"), address(missedID, "0xmissed"),
	}}
	ledger := &recordedLedger{
		entries: []models.AddressLedgerEntry{
			{Address: "0xbase", Asset: "ETH", Credited: "10000", Debited: "440"},
			{Address: "0xbase", Asset: "USDC", TokenContract: usdc, Credited: "2500000", Debited: "0"},
			{Address: "0xchild", Asset: "ETH", Credited: "1300", Debited: "0"},
			{Address: "0xchild", Asset: "USDC", TokenContract: strings.ToLower(usdc), Credited: "2500000", Debited: "2500000"},
		},
		fees: []models.AddressFeeEntry{
			{Address: "0xbase", Fees: "63"},
			{Address: "0xchild", Fees: "250"},
		},
	}
	balances.native["0xbase"] = 9490
	balances.tokens["0xbase/"+usdc] = 2500000
	balances.native["0xchild"] = 1050
	balances.native["0xmissed"] = 5000
	balances.tokens["0xmissed/"+usdc] = 1000000

	svc := NewService(Deps{Chains: registry, Addresses: addresses, Ledger: ledger})
	return auditFixture{svc: svc, addresses: addresses, ledger: ledger, balances: balances, adapter: adapter}
}

// row returns the report row of address id and asset.
func row(t *testing.T, report Report, id uuid.UUID, asset string) Row {
	t.Helper()
	for _, row := range report.Rows {
		if row.AddressID == id && row.Asset == asset {
			return row
		}
	}
	t.Fatalf("no row for %s %s in %+v", id, asset, report.Rows)
	return Row{}
}

func amountIs(value *big.Int, want int64) bool {
	return value != nil && value.Cmp(big.NewInt(want)) == 0
}

func TestAudit_Balances_ComparesEveryAssetOfEachAddressWithWhatItsTransactionsExplain(t *testing.T) {
	f := newAuditFixture(t)

	report, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain})
	if err != nil {
		t.Fatal(err)
	}

	if report.Chain != auditChain || report.Addresses != 3 || report.Reads != 6 || report.Next != uuid.Nil {
		t.Fatalf("report %+v, want 3 addresses, 6 reads and nothing left", report)
	}
	base := row(t, report, baseID, "ETH")
	if base.Status != StatusOK || !amountIs(base.Expected, 10000-440-63) || !amountIs(base.Difference, -7) || !amountIs(base.Dust, 10) {
		t.Fatalf("base ETH %+v: 7 below the 9497 its rows explain is within the chain's 10-wei dust", base)
	}
	if got := row(t, report, baseID, "USDC"); got.Status != StatusOK || !amountIs(got.Expected, 2500000) {
		t.Fatalf("base USDC %+v", got)
	}
	if got := row(t, report, childID, "ETH"); got.Status != StatusOK || !amountIs(got.Expected, 1300-250) {
		t.Fatalf("child ETH %+v: the seed and the deposit minus the sweep's gas", got)
	}
	if got := row(t, report, childID, "USDC"); got.Status != StatusOK || !amountIs(got.Expected, 0) || got.TokenContract != usdc {
		t.Fatalf("child USDC %+v: a ledger contract in another case is the registered token", got)
	}
	missedNative := row(t, report, missedID, "ETH")
	if missedNative.Status != StatusMismatch || !amountIs(missedNative.Difference, 5000) || !amountIs(missedNative.OnChain, 5000) || !amountIs(missedNative.Expected, 0) {
		t.Fatalf("missed ETH %+v: a deposit no row records", missedNative)
	}
	missedToken := row(t, report, missedID, "USDC")
	if missedToken.Status != StatusMismatch || !amountIs(missedToken.Difference, 1000000) || !amountIs(missedToken.Dust, 0) {
		t.Fatalf("missed USDC %+v: a token the chain lists is read even without a row", missedToken)
	}
	if report.OK != 4 || report.Mismatched != 2 || report.InFlight != 0 || report.Unverified != 0 {
		t.Fatalf("counts ok %d mismatched %d in flight %d unverified %d", report.OK, report.Mismatched, report.InFlight, report.Unverified)
	}
	if len(f.ledger.asked) != 1 || len(f.ledger.asked[0]) != 3 {
		t.Fatalf("ledger asked %v, want one query for the page", f.ledger.asked)
	}
}

func TestAudit_Balances_ADustFlagReplacesTheChainsDustForEveryAsset(t *testing.T) {
	f := newAuditFixture(t)
	report, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain, Dust: big.NewInt(5)})
	if err != nil {
		t.Fatal(err)
	}
	if got := row(t, report, baseID, "ETH"); got.Status != StatusMismatch || !amountIs(got.Dust, 5) {
		t.Fatalf("base ETH %+v: 7 off is beyond a 5-wei dust", got)
	}
}

func TestAudit_Balances_DoesNotJudgeInFlightFeelessOrUnreadBalances(t *testing.T) {
	f := newAuditFixture(t)
	f.ledger.entries[3].InFlight = 1 // child USDC: a sweep still confirming
	f.ledger.fees[1].InFlight = 1    // child: its native gas is in flight too
	f.ledger.fees[0].MissingFees = 2 // base: two confirmed withdrawals without a fee
	f.balances.failing["0xmissed"] = errors.New("rpc 429 from https://eth-sepolia.g.alchemy.com/v2/secret-key")

	report, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain})
	if err != nil {
		t.Fatal(err)
	}
	if got := row(t, report, childID, "USDC"); got.Status != StatusInFlight {
		t.Fatalf("child USDC %+v", got)
	}
	if got := row(t, report, childID, "ETH"); got.Status != StatusInFlight {
		t.Fatalf("child ETH %+v: a token sweep in flight still moves the native fee", got)
	}
	base := row(t, report, baseID, "ETH")
	if base.Status != StatusUnverified || !strings.Contains(base.Reason, "transactions:backfill-fees") {
		t.Fatalf("base ETH %+v", base)
	}
	if got := row(t, report, baseID, "USDC"); got.Status != StatusOK {
		t.Fatalf("base USDC %+v: a missing native fee says nothing of a token", got)
	}
	unread := row(t, report, missedID, "ETH")
	if unread.Status != StatusUnverified || unread.OnChain != nil || !strings.Contains(unread.Reason, "balance not read") {
		t.Fatalf("missed ETH %+v", unread)
	}
	if report.Unverified != 2 || report.InFlight != 2 || report.Mismatched != 1 {
		t.Fatalf("counts %+v", report)
	}
}

func TestAudit_Balances_StopsAtItsLimitsAndSaysWhereToResume(t *testing.T) {
	f := newAuditFixture(t)
	byLimit, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if byLimit.Addresses != 2 || byLimit.Next != childID {
		t.Fatalf("limit 2: %d addresses, next %s, want 2 and the child", byLimit.Addresses, byLimit.Next)
	}

	f = newAuditFixture(t)
	byReads, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain, MaxReads: 5})
	if err != nil {
		t.Fatal(err)
	}
	if byReads.Addresses != 2 || byReads.Reads != 4 || byReads.Next != childID {
		t.Fatalf("5 reads: %d addresses, %d reads, next %s; an address is read whole or not at all", byReads.Addresses, byReads.Reads, byReads.Next)
	}

	f = newAuditFixture(t)
	resumed, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain, After: childID})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Addresses != 1 || resumed.Next != uuid.Nil || resumed.Rows[0].AddressID != missedID {
		t.Fatalf("resumed after the child: %+v", resumed)
	}

	f = newAuditFixture(t)
	exact, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if exact.Next != uuid.Nil {
		t.Fatalf("every address audited, next %s", exact.Next)
	}
}

func TestAudit_Balances_SpacesItsBalanceReads(t *testing.T) {
	f := newAuditFixture(t)
	spacing := 25 * time.Millisecond
	if _, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain, Spacing: spacing}); err != nil {
		t.Fatal(err)
	}
	if len(f.balances.readAt) != 6 {
		t.Fatalf("%d reads", len(f.balances.readAt))
	}
	for i := 1; i < len(f.balances.readAt); i++ {
		if gap := f.balances.readAt[i].Sub(f.balances.readAt[i-1]); gap < spacing {
			t.Fatalf("read %d followed the previous one after %s, want at least %s", i, gap, spacing)
		}
	}

	f = newAuditFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.svc.AuditBalances(ctx, Audit{ChainID: auditChain, Spacing: time.Hour}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancelled context", err)
	}
}

func TestAudit_Balances_ReadsOnlyTheSelectedAssetsOfTheAccount(t *testing.T) {
	f := newAuditFixture(t)
	account := uuid.MustParse("a0000000-0000-0000-0000-00000000000a")
	report, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain, AccountID: &account, Assets: []string{"usdc"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.addresses.accountID == nil || *f.addresses.accountID != account {
		t.Fatalf("addresses listed for account %v", f.addresses.accountID)
	}
	if report.Reads != 3 || len(report.Rows) != 3 {
		t.Fatalf("report %+v, want the USDC balance of each address only", report)
	}
	for _, read := range f.balances.reads {
		if !strings.HasSuffix(read, "/"+usdc) {
			t.Fatalf("read %s, want token reads only", read)
		}
	}
}

func TestAudit_Balances_ReportsATokenTheChainDoesNotListWithoutReadingIt(t *testing.T) {
	f := newAuditFixture(t)
	f.ledger.entries = append(f.ledger.entries, models.AddressLedgerEntry{Address: "0xchild", Asset: "OLD", TokenContract: "0xdead", Credited: "5", Debited: "0"})
	report, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain})
	if err != nil {
		t.Fatal(err)
	}
	old := row(t, report, childID, "OLD")
	if old.Status != StatusUnverified || old.TokenContract != "0xdead" || !strings.Contains(old.Reason, "not listed") {
		t.Fatalf("unlisted token %+v", old)
	}
	if report.Reads != 6 {
		t.Fatalf("%d reads, want the unlisted token left unread", report.Reads)
	}
}

func TestAudit_Balances_LogsAWarningPerMismatchWithoutTheAddress(t *testing.T) {
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buffer, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	f := newAuditFixture(t)
	if _, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain}); err != nil {
		t.Fatal(err)
	}
	var warned []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == mismatchMessage {
			warned = append(warned, record)
		}
	}
	if len(warned) != 2 {
		t.Fatalf("warnings %v, want one per mismatch", warned)
	}
	first := warned[0]
	if first["level"] != "WARN" || first["chain"] != auditChain || first["address_id"] != missedID.String() || first["difference"] != "5000" {
		t.Fatalf("warning %v", first)
	}
	if strings.Contains(buffer.String(), "0xmissed") {
		t.Fatal("a log line carries the address itself")
	}
}

func TestAudit_Balances_RefusesAnUnknownChainOrAssetAndBadBounds(t *testing.T) {
	f := newAuditFixture(t)
	for name, audit := range map[string]Audit{
		"unknown chain":    {ChainID: "nochain"},
		"blank chain":      {ChainID: " "},
		"unknown asset":    {ChainID: auditChain, Assets: []string{"DOGE"}},
		"limit too large":  {ChainID: auditChain, Limit: MaxLimit + 1},
		"negative limit":   {ChainID: auditChain, Limit: -1},
		"negative reads":   {ChainID: auditChain, MaxReads: -1},
		"negative spacing": {ChainID: auditChain, Spacing: -time.Second},
		"negative dust":    {ChainID: auditChain, Dust: big.NewInt(-1)},
	} {
		if _, err := f.svc.AuditBalances(context.Background(), audit); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(f.balances.reads) != 0 {
		t.Fatalf("a refused audit read %v", f.balances.reads)
	}
	if _, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain, Assets: []string{"DOGE"}}); !errors.Is(err, ErrInvalidAudit) {
		t.Fatalf("err = %v, want ErrInvalidAudit", err)
	}
}

func TestAudit_Balances_SaysWhichWayABalanceDiffers(t *testing.T) {
	f := newAuditFixture(t)
	f.balances.native["0xchild"] = 900 // 150 below the 1050 its rows explain
	report, err := f.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain})
	if err != nil {
		t.Fatal(err)
	}
	less := row(t, report, childID, "ETH")
	if less.Status != StatusMismatch || !amountIs(less.Difference, -150) || !strings.Contains(less.Reason, "less") || !strings.Contains(less.Reason, "outflow") {
		t.Fatalf("child ETH %+v, want a shortfall named as an unrecorded outflow", less)
	}
	more := row(t, report, missedID, "ETH")
	if !strings.Contains(more.Reason, "more") || !strings.Contains(more.Reason, "deposit") {
		t.Fatalf("missed ETH %+v, want a surplus named as an unrecorded deposit", more)
	}
}

// rentChain is the audit's adapter on a chain whose transfers pay account rent.
type rentChain struct{ *mocks.MockChain }

func (rentChain) TransactionAccountRent(context.Context, string) (*big.Int, error) {
	return big.NewInt(0), nil
}

func TestAudit_Balances_OnAChainThatPaysAccountRentAsksForItAndLeavesARowWithoutOneUnverified(t *testing.T) {
	f := newAuditFixture(t)
	registry := chain.NewRegistry()
	registry.RegisterChain(rentChain{f.adapter})
	registry.RegisterToken(types.Token{Symbol: "USDC", Contract: usdc, Decimals: 6, ChainID: auditChain})
	f.ledger.fees[1].MissingRent = 1
	svc := NewService(Deps{Chains: registry, Addresses: f.addresses, Ledger: f.ledger})

	report, err := svc.AuditBalances(context.Background(), Audit{ChainID: auditChain})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.ledger.withRent) != 1 || !f.ledger.withRent[0] {
		t.Fatalf("fees asked with account rent %v, want true on a chain that records it", f.ledger.withRent)
	}
	child := row(t, report, childID, "ETH")
	if child.Status != StatusUnverified || !strings.Contains(child.Reason, "account rent") {
		t.Fatalf("child ETH %+v, want unverified until its rent is backfilled", child)
	}

	plain := newAuditFixture(t)
	if _, err := plain.svc.AuditBalances(context.Background(), Audit{ChainID: auditChain}); err != nil {
		t.Fatal(err)
	}
	if len(plain.ledger.withRent) != 1 || plain.ledger.withRent[0] {
		t.Fatalf("fees asked with account rent %v, want false elsewhere", plain.ledger.withRent)
	}
}
