package e2e

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/macrowallets/waas/tools/internal/pyjson"
)

const (
	testSeedHash  = "b98f2c81477602477f8fd8b98b3127bf4428d8deba8395421294421800ae5dd3"
	testSweepHash = "ffd7949e78c65ac0c8cbb62984bed1a4c5de8504dbc0ccf5ff7bcd8389b7c6ed"
	testDecoyHash = "d6763d758e2716022e6c852da83fe7c0f9ee9df3e9e3e6fd3cbcf61d44b2731a"
	testBase      = "TMv7xqHnL87cL2gTAMN1Hc7ZsZ5x1maWDg"
	testChild     = "TQsMjgZtnxd1rAC8AmNLmmV1ZrToFUk1hW"
	testSweepTag  = "tron-usdt-child-sweep-01"
)

// fakeClock advances only when the code under test sleeps.
type fakeClock struct {
	now    time.Time
	sleeps int
}

func (clock *fakeClock) Now() time.Time { return clock.now }

func (clock *fakeClock) Sleep(_ context.Context, duration time.Duration) error {
	clock.sleeps++
	clock.now = clock.now.Add(duration)
	return nil
}

func TestConsolidateApplyPollsVaultTestUntilEveryLegHasAHash(t *testing.T) {
	fixture := newFundingFixture(t)
	fixture.api.postCode = http.StatusOK
	fixture.api.postBody = `{"transactions": [{"from": "` + testFrom + `", "status": "confirming", "tx_hash": "` + testTxHash + `"}]}`
	fixture.sweepRows[0].Status = vaultStatusConfirmed
	fixture.sweepRows[0].Amount = "4"
	fixture.sweepRowsAfter = 3
	fake := &fakeClock{now: fixedNow}
	fixture.funding.Now, fixture.funding.Sleep = fake.Now, fake.Sleep
	fixture.funding.SweepPollEvery, fixture.funding.SweepPollFor = SweepPollInterval, SweepPollWait

	request := ConsolidateRequest{Tag: "bnb-consolidate-02", WalletID: testWalletID, Asset: "BNB", Apply: true}
	if code, err := fixture.funding.Consolidate(context.Background(), request); err != nil || code != ExitOK {
		t.Fatalf("apply %d %v (stderr %s)", code, err, fixture.err.String())
	}
	if len(fixture.api.posts) != 1 || fixture.api.gets != 0 {
		t.Fatalf("posts %d gets %d: the poll must only read vault_test", len(fixture.api.posts), fixture.api.gets)
	}
	if len(fixture.sweepQueries) != 3 || fake.sleeps != 2 {
		t.Fatalf("lookups %d sleeps %d", len(fixture.sweepQueries), fake.sleeps)
	}
	query := fixture.sweepQueries[0]
	if query.WalletID != testWalletID || !query.CreatedFrom.Equal(fixedNow.Add(-SweepWindowSlack)) || !query.CreatedUntil.IsZero() {
		t.Fatalf("query %+v", query)
	}
	ledger, _ := os.ReadFile(fixture.paths.FundingLedger)
	for _, fragment := range []string{`"tx_hash": "` + testTxHash + `"`, `"vault_status": "confirmed"`, `"hash": "` + testTxHash + `"`, `"status": "confirmed"`} {
		if !strings.Contains(string(ledger), fragment) {
			t.Errorf("ledger misses %s:\n%s", fragment, ledger)
		}
	}
	if !strings.HasSuffix(fixture.out.String(), "tx hash: "+testTxHash+"; confirmed in vault_test\n") {
		t.Fatalf("output %s", fixture.out.String())
	}
}

func TestConsolidateApplyKeepsSubmittedUntilVaultTestConfirms(t *testing.T) {
	fixture := newFundingFixture(t)
	request := ConsolidateRequest{Tag: "bnb-consolidate-03", WalletID: testWalletID, Asset: "BNB", Apply: true}
	if code, err := fixture.funding.Consolidate(context.Background(), request); err != nil || code != ExitOK {
		t.Fatalf("apply %d %v", code, err)
	}
	ledger, _ := os.ReadFile(fixture.paths.FundingLedger)
	if !strings.Contains(string(ledger), `"status": "submitted"`) || !strings.Contains(string(ledger), `"hash": "`+testTxHash+`"`) || !strings.Contains(string(ledger), `"vault_status": "confirming"`) {
		t.Fatalf("ledger %s", ledger)
	}
	if !strings.Contains(fixture.out.String(), `ledger status stays "submitted" (run ledger-reconcile bnb-consolidate-03 later)`) {
		t.Fatalf("output %s", fixture.out.String())
	}
}

func TestConsolidateApplyStopsPollingAtTheDeadline(t *testing.T) {
	fixture := newFundingFixture(t)
	fixture.sweepRowsAfter = 1 << 30
	fake := &fakeClock{now: fixedNow}
	fixture.funding.Now, fixture.funding.Sleep = fake.Now, fake.Sleep
	fixture.funding.SweepPollEvery, fixture.funding.SweepPollFor = 5*time.Second, 30*time.Second

	request := ConsolidateRequest{Tag: "bnb-consolidate-04", WalletID: testWalletID, Asset: "BNB", Apply: true}
	code, err := fixture.funding.Consolidate(context.Background(), request)
	if err != nil || code != ExitNoTxHashYet {
		t.Fatalf("code %d err %v", code, err)
	}
	if len(fixture.sweepQueries) != 7 || len(fixture.api.posts) != 1 {
		t.Fatalf("lookups %d posts %d", len(fixture.sweepQueries), len(fixture.api.posts))
	}
	ledger, _ := os.ReadFile(fixture.paths.FundingLedger)
	if !strings.Contains(string(ledger), `"status": "submitted, no tx hash yet"`) || strings.Contains(string(ledger), `"hash"`) {
		t.Fatalf("ledger %s", ledger)
	}
}

func TestMatchSweepLegsPrefersTheResponseHashOverAmounts(t *testing.T) {
	legs := []pyjson.Object{
		{{Key: "from", Value: testBase}, {Key: "to", Value: testChild}, {Key: "amount", Value: nil}},
		{{Key: "from", Value: testChild}, {Key: "to", Value: testBase}, {Key: "amount", Value: "20000001"}},
	}
	rows := []SweepRow{
		{ID: "decoy", TxType: "sweep", Status: "confirmed", TxHash: testDecoyHash, From: testChild, To: testBase, Amount: "20000001"},
		{ID: "seed", TxType: "gas_seed", Status: "confirmed", TxHash: testSeedHash, From: testBase, To: testChild, Amount: "3578400"},
		{ID: "sweep", TxType: "sweep", Status: "confirming", TxHash: testSweepHash, From: strings.ToLower(testChild), To: testBase, Amount: "19999999"},
	}
	matches := MatchSweepLegs(legs, rows, map[string]string{strings.ToLower(testChild): testSweepHash})
	if matches[0].Row == nil || matches[0].Row.ID != "seed" || matches[1].Row == nil || matches[1].Row.ID != "sweep" {
		t.Fatalf("matches %+v", matches)
	}
	if primarySweepHash(matches) != testSweepHash || allRowsConfirmed(matches) || !allHashesKnown(matches) {
		t.Fatal("primary hash or status summary")
	}
	none := MatchSweepLegs(legs[:1], nil, map[string]string{strings.ToLower(testBase): testSeedHash})
	if none[0].Row != nil || none[0].Hash() != testSeedHash || allHashesKnown(none) {
		t.Fatalf("without rows %+v", none)
	}
}

func TestSweepRowsQueriesVaultTestReadOnly(t *testing.T) {
	var calls []recordedProcess
	stdout := strings.Join([]string{"row-1", "tron", "sweep", "manual_consolidation", "confirmed", testSweepHash, testChild, testBase, "20000001", "USDT", "2026-10-05 01:02:57"}, vaultFieldSeparator) + "\n"
	services := outboundServices(stdout, &calls)
	query := SweepQuery{WalletID: testWalletID, CreatedFrom: time.Date(2026, 10, 5, 1, 2, 43, 0, time.UTC), CreatedUntil: time.Date(2026, 10, 5, 1, 3, 1, 0, time.UTC)}
	rows, err := services.SweepRows(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].TxHash != testSweepHash || rows[0].Chain != "tron" || rows[0].From != testChild || rows[0].CreatedAt != "2026-10-05 01:02:57" {
		t.Fatalf("rows %+v", rows)
	}
	args := strings.Join(calls[0].args, " ")
	if !strings.Contains(args, "-e "+vaultReadOnlyOption) || !strings.Contains(args, "-d "+E2EDatabase) {
		t.Fatalf("args %v", calls[0].args)
	}
	sql := calls[0].args[len(calls[0].args)-1]
	if !strings.HasPrefix(sql, "select ") {
		t.Fatalf("not a SELECT: %s", sql)
	}
	for _, fragment := range []string{"wallet_id = '" + testWalletID + "'", "tx_type in ('sweep','gas_seed')", "created_at >= '2026-10-05 01:02:43'", "created_at <= '2026-10-05 01:03:01'"} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("query misses %q: %s", fragment, sql)
		}
	}

	calls = nil
	if _, err := services.SweepRows(context.Background(), SweepQuery{WalletID: "x'; delete from transactions; --", CreatedFrom: fixedNow}); err == nil || len(calls) != 0 {
		t.Fatalf("unsafe wallet id: %v %v", err, calls)
	}
	if _, err := services.SweepRows(context.Background(), SweepQuery{WalletID: testWalletID}); err == nil || len(calls) != 0 {
		t.Fatal("open window accepted")
	}
	broken := outboundServices("only|two\n", &calls)
	if _, err := broken.SweepRows(context.Background(), query); err == nil {
		t.Fatal("malformed row accepted")
	}
}

func chainServer(t *testing.T, handle func(path string, body string) (int, string)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		status, reply := handle(request.URL.Path, string(raw))
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestChainVerifierTron(t *testing.T) {
	replies := map[string]string{}
	server := chainServer(t, func(path, body string) (int, string) {
		if body != `{"value": "`+testSweepHash+`"}` {
			t.Errorf("TRON body %s", body)
		}
		return http.StatusOK, replies[path]
	})
	verifier := ChainVerifier{TronURL: server.URL, HTTP: server.Client()}
	check := func(info, transaction string, wantConfirmed bool, wantNote string) {
		t.Helper()
		replies[tronInfoPath], replies[tronTransactionPath] = info, transaction
		got, err := verifier.Verify(context.Background(), "tron", testSweepHash)
		if err != nil || got.Confirmed != wantConfirmed || got.Note != wantNote {
			t.Fatalf("got %+v %v, want %v %q", got, err, wantConfirmed, wantNote)
		}
	}
	check(`{"id": "x", "blockNumber": 61234567, "receipt": {"result": "SUCCESS"}}`, `{}`, true, "TRON Nile block 61234567, receipt.result SUCCESS")
	check(`{"id": "x", "blockNumber": 61234568, "receipt": {"net_usage": 268}}`, `{"ret": [{"contractRet": "SUCCESS"}]}`, true, "TRON Nile block 61234568, contractRet SUCCESS")
	check(`{"id": "x", "blockNumber": 61234569, "receipt": {"result": "OUT_OF_ENERGY"}}`, `{}`, false, "TRON Nile block 61234569, receipt.result OUT_OF_ENERGY")
	check(`{}`, `{}`, false, "TRON Nile: transaction not found yet")
	if _, err := verifier.Verify(context.Background(), "tron", "not-a-hash"); err == nil {
		t.Fatal("invalid hash accepted")
	}
	if _, err := verifier.Verify(context.Background(), "sol", testSweepHash); err == nil {
		t.Fatal("unknown chain accepted")
	}
}

func TestChainVerifierLitecoin(t *testing.T) {
	reply := `{"confirmed": true, "block_height": 4512345}`
	server := chainServer(t, func(path, _ string) (int, string) {
		if path != "/tx/"+testSweepHash+"/status" {
			return http.StatusNotFound, "not found"
		}
		return http.StatusOK, reply
	})
	verifier := ChainVerifier{LitecoinURL: server.URL, HTTP: server.Client()}
	got, err := verifier.Verify(context.Background(), "ltc", testSweepHash)
	if err != nil || !got.Confirmed || got.Note != "litecoinspace testnet block 4512345, confirmed" {
		t.Fatalf("got %+v %v", got, err)
	}
	reply = `{"confirmed": false}`
	if got, err := verifier.Verify(context.Background(), "ltc", testSweepHash); err != nil || got.Confirmed {
		t.Fatalf("unconfirmed %+v %v", got, err)
	}
}

func TestChainVerifierEVMNeverPrintsTheRPCURL(t *testing.T) {
	reply := `{"jsonrpc": "2.0", "id": 1, "result": {"status": "0x1", "blockNumber": "0x1f4"}}`
	server := chainServer(t, func(path, body string) (int, string) {
		if path != "/v2/secret-key" || !strings.Contains(body, `"method": "eth_getTransactionReceipt", "params": ["`+testTxHash+`"]`) {
			t.Errorf("EVM call %s %s", path, body)
		}
		return http.StatusOK, reply
	})
	environ := map[string]string{networkProfileKey: testnetProfile, "BSC_RPC_URL": server.URL + "/v2/secret-key"}
	verifier, err := ChainVerifierFromEnviron(environ)
	if err != nil {
		t.Fatal(err)
	}
	got, err := verifier.Verify(context.Background(), "bsc", testTxHash)
	if err != nil || !got.Confirmed || got.Note != "bsc RPC block 500, receipt status 0x1" {
		t.Fatalf("got %+v %v", got, err)
	}
	reply = `{"jsonrpc": "2.0", "id": 1, "result": {"status": "0x0", "blockNumber": "0x1f5"}}`
	if got, err := verifier.Verify(context.Background(), "bsc", testTxHash); err != nil || got.Confirmed {
		t.Fatalf("reverted %+v %v", got, err)
	}
	reply = `{"jsonrpc": "2.0", "id": 1, "result": null}`
	if got, err := verifier.Verify(context.Background(), "bsc", testTxHash); err != nil || got.Confirmed || got.Note != "bsc RPC: no receipt yet" {
		t.Fatalf("pending %+v %v", got, err)
	}
	if _, err := verifier.Verify(context.Background(), "base", testTxHash); err == nil || !strings.Contains(err.Error(), "BASE_RPC_URL is not set") {
		t.Fatalf("missing RPC: %v", err)
	}

	server.Close()
	_, err = verifier.Verify(context.Background(), "bsc", testTxHash)
	host := strings.TrimPrefix(server.URL, "http://")
	if err == nil || strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), host) {
		t.Fatalf("transport error leaks the URL: %v", err)
	}
	if _, err := ChainVerifierFromEnviron(map[string]string{networkProfileKey: "mainnet"}); err == nil {
		t.Fatal("mainnet profile accepted")
	}
}

type reconcileFixture struct {
	reconcile Reconcile
	paths     Paths
	out       *bytes.Buffer
	rows      []SweepRow
	queries   []SweepQuery
	verified  map[string]Verification
	checked   []string
}

func newReconcileFixture(t *testing.T) *reconcileFixture {
	t.Helper()
	paths := newTestPaths(t)
	responseFile := filepath.Join(paths.FundingDir, testSweepTag+".json")
	sweeps := []any{
		pyjson.Object{{Key: "from", Value: testBase}, {Key: "to", Value: testChild}, {Key: "amount", Value: nil}},
		pyjson.Object{{Key: "from", Value: testChild}, {Key: "to", Value: testBase}, {Key: "amount", Value: "20000001"}},
	}
	ledger := pyjson.Object{
		{Key: "recordedAt", Value: "2026-10-05T01:02:56+00:00"},
		{Key: "entries", Value: []any{
			pyjson.Object{{Key: "chain", Value: "tron"}, {Key: "tag", Value: testSweepTag}, {Key: "sweeps", Value: sweeps}, {Key: "response_file", Value: responseFile}, {Key: "status", Value: statusSubmitted}, {Key: "recordedAt", Value: "2026-10-05T01:02:56+00:00"}},
			pyjson.Object{{Key: "chain", Value: "ltc"}, {Key: "tag", Value: "keep-me"}, {Key: "status", Value: statusSubmitted}},
		}},
	}
	if err := writeIndentedJSON(paths.FundingLedger, ledger); err != nil {
		t.Fatal(err)
	}
	plan := pyjson.Object{{Key: "tag", Value: testSweepTag}, {Key: "wallet_id", Value: testWalletID}}
	if _, err := ClaimTransfer(paths.LocksDir, testSweepTag, plan, time.Date(2026, 10, 5, 1, 2, 48, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	response, _ := pyjson.Decode([]byte(`{"transactions": [{"from": "` + testChild + `", "status": "confirming", "tx_hash": "` + testSweepHash + `"}]}`))
	if err := writeIndentedJSON(responseFile, pyjson.Object{{Key: "plan", Value: plan}, {Key: "status", Value: 200}, {Key: "response", Value: response}}); err != nil {
		t.Fatal(err)
	}
	fixture := &reconcileFixture{paths: paths, out: &bytes.Buffer{}, verified: map[string]Verification{
		testSeedHash:  {Confirmed: true, Note: "TRON Nile block 1, contractRet SUCCESS"},
		testSweepHash: {Confirmed: true, Note: "TRON Nile block 2, receipt.result SUCCESS"},
	}}
	fixture.rows = []SweepRow{
		{ID: "seed", Chain: "tron", TxType: "gas_seed", Status: "confirmed", TxHash: testSeedHash, From: testBase, To: testChild, Amount: "3578400"},
		{ID: "sweep", Chain: "tron", TxType: "sweep", Status: "confirmed", TxHash: testSweepHash, From: testChild, To: testBase, Amount: "20000001"},
	}
	fixture.reconcile = Reconcile{
		Paths:     paths,
		Ledger:    FundingLedger{Path: paths.FundingLedger, LockPath: paths.LedgerLock, Now: clock(fixedNow)},
		Recording: RecordingLock{Path: paths.RecordingLock, PID: 7, Hostname: "host", Now: clock(fixedNow)},
		SweepRows: func(_ context.Context, query SweepQuery) ([]SweepRow, error) {
			fixture.queries = append(fixture.queries, query)
			return fixture.rows, nil
		},
		Verify: func(_ context.Context, chain, hash string) (Verification, error) {
			fixture.checked = append(fixture.checked, chain+" "+hash)
			return fixture.verified[hash], nil
		},
		Now: clock(fixedNow),
		Out: fixture.out,
	}
	return fixture
}

func TestLedgerReconcileDryRunWritesNothing(t *testing.T) {
	fixture := newReconcileFixture(t)
	before, _ := os.ReadFile(fixture.paths.FundingLedger)
	code, err := fixture.reconcile.Run(context.Background(), ReconcileRequest{Tags: []string{testSweepTag}})
	if err != nil || code != ExitOK {
		t.Fatalf("code %d err %v", code, err)
	}
	after, _ := os.ReadFile(fixture.paths.FundingLedger)
	if !bytes.Equal(before, after) {
		t.Fatal("dry run changed the ledger")
	}
	if backups, _ := filepath.Glob(fixture.paths.FundingLedger + ledgerBackupInfix + "*"); len(backups) != 0 {
		t.Fatalf("dry run wrote backups %v", backups)
	}
	query := fixture.queries[0]
	if query.WalletID != testWalletID || !query.CreatedFrom.Equal(time.Date(2026, 10, 5, 1, 2, 43, 0, time.UTC)) || !query.CreatedUntil.Equal(time.Date(2026, 10, 5, 1, 3, 1, 0, time.UTC)) {
		t.Fatalf("query %+v", query)
	}
	if strings.Join(fixture.checked, ",") != "tron "+testSeedHash+",tron "+testSweepHash {
		t.Fatalf("checked %v", fixture.checked)
	}
	if !strings.Contains(fixture.out.String(), "=> confirmed, hash "+testSweepHash) || !strings.Contains(fixture.out.String(), "dry run: ledger not written") {
		t.Fatalf("output %s", fixture.out.String())
	}
}

func TestLedgerReconcileApplyBacksUpAndUpdatesInPlace(t *testing.T) {
	fixture := newReconcileFixture(t)
	before, _ := os.ReadFile(fixture.paths.FundingLedger)
	code, err := fixture.reconcile.Run(context.Background(), ReconcileRequest{Tags: []string{testSweepTag}, Apply: true})
	if err != nil || code != ExitOK {
		t.Fatalf("code %d err %v", code, err)
	}
	backup := fixture.paths.FundingLedger + ledgerBackupInfix + "20261003T050000Z"
	requireMode(t, backup, PrivateFileMode)
	if saved, _ := os.ReadFile(backup); !bytes.Equal(saved, before) {
		t.Fatal("backup differs from the original ledger")
	}
	_, entries, err := fixture.reconcile.Ledger.Load()
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := entries[0].(pyjson.Object)
	if updated.String("tag") != testSweepTag || entries[1].(pyjson.Object).String("tag") != "keep-me" {
		t.Fatal("entry order changed")
	}
	if updated.String("status") != statusConfirmed || updated.String("hash") != testSweepHash || updated.String("confirmedAt") != "2026-10-03T05:00:00+00:00" || updated.String("recordedAt") != "2026-10-05T01:02:56+00:00" {
		t.Fatalf("entry %v", updated)
	}
	if !strings.Contains(updated.String("verified"), "TRON Nile block 2, receipt.result SUCCESS") {
		t.Fatalf("verified %q", updated.String("verified"))
	}
	legs := sweepLegs(updated)
	if legs[0].String("tx_hash") != testSeedHash || legs[1].String("tx_hash") != testSweepHash || legs[1].String("onchain") != "TRON Nile block 2, receipt.result SUCCESS" {
		t.Fatalf("legs %v", legs)
	}
	if amount, present := legs[0].Get("amount"); !present || amount != nil {
		t.Fatal("planned leg fields were not kept")
	}
	if _, err := os.Stat(fixture.paths.RecordingLock); !os.IsNotExist(err) {
		t.Fatal("recording lock was not released")
	}
	if !strings.Contains(fixture.out.String(), "ledger backup "+backup) {
		t.Fatalf("output %s", fixture.out.String())
	}
}

func TestLedgerReconcileKeepsSubmittedWhenTheChainHasNotConfirmed(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.verified[testSweepHash] = Verification{Note: "TRON Nile: transaction not found yet"}
	code, err := fixture.reconcile.Run(context.Background(), ReconcileRequest{Tags: []string{testSweepTag}, Apply: true})
	if err != nil || code != ExitNotConfirmedYet {
		t.Fatalf("code %d err %v", code, err)
	}
	_, entries, _ := fixture.reconcile.Ledger.Load()
	updated, _ := entries[0].(pyjson.Object)
	if updated.String("status") != statusSubmitted || updated.String("hash") != testSweepHash || updated.String("confirmedAt") != "" {
		t.Fatalf("entry %v", updated)
	}
}

func TestLedgerReconcileGuards(t *testing.T) {
	t.Run("recording in progress", func(t *testing.T) {
		fixture := newReconcileFixture(t)
		if err := fixture.reconcile.Recording.Acquire("markets-recording"); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.reconcile.Run(context.Background(), ReconcileRequest{Tags: []string{testSweepTag}, Apply: true}); err == nil || !strings.Contains(err.Error(), "recording lock is held") {
			t.Fatalf("got %v", err)
		}
		if len(fixture.queries) != 0 {
			t.Fatal("queried during a recording")
		}
	})
	t.Run("vault_test disagrees with the response", func(t *testing.T) {
		fixture := newReconcileFixture(t)
		fixture.rows[1].TxHash = testDecoyHash
		before, _ := os.ReadFile(fixture.paths.FundingLedger)
		if _, err := fixture.reconcile.Run(context.Background(), ReconcileRequest{Tags: []string{testSweepTag}, Apply: true}); err == nil || !strings.Contains(err.Error(), "differs from the consolidate response") {
			t.Fatalf("got %v", err)
		}
		if after, _ := os.ReadFile(fixture.paths.FundingLedger); !bytes.Equal(before, after) {
			t.Fatal("ledger written after an error")
		}
	})
	t.Run("bad tags", func(t *testing.T) {
		fixture := newReconcileFixture(t)
		for _, tags := range [][]string{{"missing-tag"}, {"keep-me"}, {"X"}, {testSweepTag, testSweepTag}, nil} {
			if code, err := fixture.reconcile.Run(context.Background(), ReconcileRequest{Tags: tags, Apply: true}); err == nil || code != ExitFailure {
				t.Errorf("tags %v accepted", tags)
			}
		}
		if backups, _ := filepath.Glob(fixture.paths.FundingLedger + ledgerBackupInfix + "*"); len(backups) != 0 {
			t.Fatalf("backups after refusals %v", backups)
		}
	})
	t.Run("response file outside the funding dir", func(t *testing.T) {
		fixture := newReconcileFixture(t)
		document, entries, _ := fixture.reconcile.Ledger.Load()
		entries[0] = entries[0].(pyjson.Object).Set("response_file", "/etc/passwd")
		if err := writeIndentedJSON(fixture.paths.FundingLedger, document.Set("entries", entries)); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.reconcile.Run(context.Background(), ReconcileRequest{Tags: []string{testSweepTag}}); err == nil || !strings.Contains(err.Error(), "outside") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestLedgerBackupAndReplaceEntries(t *testing.T) {
	fixture := newReconcileFixture(t)
	ledger := fixture.reconcile.Ledger
	if _, err := ledger.Backup(fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Backup(fixedNow); err == nil {
		t.Fatal("overwrote an existing backup")
	}
	if err := ledger.ReplaceEntries(map[string]pyjson.Object{"absent": {}}); err == nil {
		t.Fatal("replaced a missing tag")
	}
}

func TestParseArgsAtLeast(t *testing.T) {
	parsed, err := ParseArgsAtLeast([]string{"a-tag", "--apply", "b-tag"}, []string{"apply"}, nil, 1)
	if err != nil || strings.Join(parsed.Positional, " ") != "a-tag b-tag" || !parsed.Switches["apply"] {
		t.Fatalf("parsed %+v %v", parsed, err)
	}
	if _, err := ParseArgsAtLeast([]string{"--apply"}, []string{"apply"}, nil, 1); err == nil {
		t.Fatal("no tag accepted")
	}
}
