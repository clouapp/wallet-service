package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/pkg/pyjson"
)

const (
	testWalletID   = "0b9f6a52-3c1d-4e8f-9a7b-1c2d3e4f5a6b"
	testPassphrase = "vault passphrase not for production"
	testToken      = "markets-token-not-real"
	testTo         = "0x1111111111111111111111111111111111111111"
	testFrom       = "0x2222222222222222222222222222222222222222"
	testTag        = "base-sepolia-fund-1"
	testTxHash     = "0x" + "ab" + "cd" + "ef" + "00000000000000000000000000000000000000000000000000000000ab"
)

type fixedPassphrase struct{ reads int }

func (source *fixedPassphrase) Read(_ context.Context, walletID uuid.UUID) (string, error) {
	source.reads++
	if walletID.String() != testWalletID {
		return "", errors.New("unknown wallet")
	}
	return testPassphrase, nil
}

func preparePreflightState(t *testing.T) Paths {
	t.Helper()
	paths := newTestPaths(t)
	if err := WritePrivateFile(paths.APIBinary, []byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(paths.APIEnviron, EncodeEnviron(map[string]string{"DB_DATABASE": E2EDatabase})); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestPreflightSendsThePassphraseOnStdinAndParsesTheLastResultLine(t *testing.T) {
	paths := preparePreflightState(t)
	var captured struct {
		binary string
		args   []string
		dir    string
		env    []string
		stdin  string
	}
	preflight := Preflight{Paths: paths, Passphrases: &fixedPassphrase{}, Run: func(_ context.Context, binary string, args []string, dir string, env []string, stdin []byte) (ProcessResult, error) {
		captured.binary, captured.args, captured.dir, captured.env, captured.stdin = binary, args, dir, env, string(stdin)
		stdout := "booting\nPREFLIGHT {\"broadcast\": true}\nPREFLIGHT {\"mode\":\"withdrawal\",\"signature_verified\":true,\"broadcast\":false,\"transactions\":[{\"to\":\"" + testTo + "\",\"amount\":\"5\"}]}\n"
		return ProcessResult{Stdout: []byte(stdout)}, nil
	}}
	request := PreflightRequest{Mode: PreflightModeWithdrawal, WalletID: testWalletID, Asset: "ETH", Amount: "5", To: testTo}
	result, err := preflight.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPreflight(result); err != nil {
		t.Fatal(err)
	}
	wantStdin := `{"mode": "withdrawal", "wallet_id": "` + testWalletID + `", "asset": "ETH", "amount": "5", "to": "` + testTo + `", "passphrase": "` + testPassphrase + `"}`
	if captured.stdin != wantStdin {
		t.Fatalf("stdin %s", captured.stdin)
	}
	if captured.binary != paths.APIBinary || strings.Join(captured.args, " ") != "artisan withdraw:preflight" || captured.dir != paths.BackDir {
		t.Fatalf("command %+v", captured)
	}
	if strings.Join(captured.env, ",") != "DB_DATABASE="+E2EDatabase {
		t.Fatalf("env %v", captured.env)
	}
	if transactions := PreflightTransactions(result); len(transactions) != 1 || transactions[0].String("to") != testTo {
		t.Fatalf("transactions %v", transactions)
	}
}

func TestPreflightFailuresAndVerification(t *testing.T) {
	paths := preparePreflightState(t)
	failing := Preflight{Paths: paths, Passphrases: &fixedPassphrase{}, Run: func(context.Context, string, []string, string, []string, []byte) (ProcessResult, error) {
		return ProcessResult{Stdout: []byte(strings.Repeat("x", 2000)), Stderr: []byte("boom"), ExitCode: 3}, nil
	}}
	_, err := failing.Execute(context.Background(), PreflightRequest{Mode: PreflightModeConsolidation, WalletID: testWalletID, Asset: "ETH"})
	var failure *PreflightFailure
	if !errors.As(err, &failure) || failure.ExitCode != 3 || len(failure.Tail) != failureTailRunes || !strings.HasSuffix(failure.Tail, "boom") {
		t.Fatalf("failure %v", err)
	}
	if _, err := failing.Execute(context.Background(), PreflightRequest{Mode: "other", WalletID: testWalletID, Asset: "ETH"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if _, err := failing.Execute(context.Background(), PreflightRequest{Mode: PreflightModeWithdrawal, WalletID: testWalletID, Asset: "ETH", Amount: "0", To: testTo}); err == nil {
		t.Fatal("zero amount accepted")
	}
	if err := os.Remove(paths.APIBinary); err != nil {
		t.Fatal(err)
	}
	if _, err := failing.Execute(context.Background(), PreflightRequest{Mode: PreflightModeConsolidation, WalletID: testWalletID, Asset: "ETH"}); err == nil || !strings.Contains(err.Error(), "--build") {
		t.Fatalf("missing binary: %v", err)
	}
	unverified := Preflight{Paths: preparePreflightState(t), Passphrases: &fixedPassphrase{}, Run: func(context.Context, string, []string, string, []string, []byte) (ProcessResult, error) {
		return ProcessResult{Stdout: []byte(`PREFLIGHT {"strategy": "multi_sweep", "signature_verified": false, "broadcast": false, "transactions": []}`)}, nil
	}}
	wantUnverified := "pre-flight failed:\n{\n  \"strategy\": \"multi_sweep\",\n  \"signature_verified\": false,\n  \"broadcast\": false,\n  \"transactions\": []\n}\n" + ErrPreflightUnverified.Error()
	if _, err := unverified.Verified(context.Background(), PreflightRequest{Mode: PreflightModeConsolidation, WalletID: testWalletID, Asset: "ETH"}); err == nil || err.Error() != wantUnverified {
		t.Fatalf("unverified: %v", err)
	}
	for _, document := range []string{`{"signature_verified": true}`, `{"signature_verified": false, "broadcast": false}`, `{"signature_verified": true, "broadcast": 0}`, `{"signature_verified": 1, "broadcast": true}`} {
		decoded, _ := decodeObject([]byte(document))
		if VerifyPreflight(decoded) == nil {
			t.Errorf("VerifyPreflight accepted %s", document)
		}
	}
}

type fakeAPI struct {
	mu        sync.Mutex
	posts     []map[string]any
	gets      int
	postCode  int
	postBody  string
	hashAfter int
}

func (api *fakeAPI) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("authorization header %q", request.Header.Get("Authorization"))
		}
		switch request.Method {
		case http.MethodPost:
			var body map[string]any
			raw, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(raw, &body)
			api.posts = append(api.posts, body)
			writer.WriteHeader(api.postCode)
			_, _ = writer.Write([]byte(api.postBody))
		case http.MethodGet:
			api.gets++
			if api.gets >= api.hashAfter {
				_, _ = writer.Write([]byte(`{"data": {"status": "broadcast", "tx_hash": "` + testTxHash + `"}}`))
				return
			}
			_, _ = writer.Write([]byte(`{"data": {"status": "pending", "tx_hash": null}}`))
		}
	})
}

type fundingFixture struct {
	funding      Funding
	paths        Paths
	api          *fakeAPI
	out, err     *bytes.Buffer
	matches      []string
	matchQueries [][]string
	preflight    pyjson.Object
	preflights   int
}

func newFundingFixture(t *testing.T) *fundingFixture {
	t.Helper()
	paths := newTestPaths(t)
	if err := WritePrivateFile(paths.FundingLedger, []byte("{\n  \"entries\": []\n}\n")); err != nil {
		t.Fatal(err)
	}
	fixture := &fundingFixture{paths: paths, api: &fakeAPI{postCode: http.StatusCreated, postBody: `{"data": {"status": "pending"}}`, hashAfter: 2}, out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	fixture.preflight, _ = decodeObject([]byte(`{"strategy": "direct", "signature_verified": true, "broadcast": false, "transactions": [{"role": "withdrawal", "from": "` + testFrom + `", "to": "` + testTo + `", "amount": "5"}]}`))
	server := httptest.NewServer(fixture.api.handler(t))
	t.Cleanup(server.Close)
	fixture.funding = Funding{
		Paths: paths,
		Preflight: func(context.Context, PreflightRequest) (pyjson.Object, error) {
			fixture.preflights++
			return fixture.preflight, nil
		},
		Passphrases: &fixedPassphrase{},
		Services: Services{
			OutboundMatches: func(_ context.Context, walletID, to, asset, baseUnits string) ([]string, error) {
				fixture.matchQueries = append(fixture.matchQueries, []string{walletID, to, asset, baseUnits})
				return fixture.matches, nil
			},
			MarketsToken: func(context.Context) (string, error) { return testToken, nil },
			API:          APIClient{BaseURL: server.URL, HTTP: server.Client()},
		},
		Ledger:    FundingLedger{Path: paths.FundingLedger, Now: clock(fixedNow)},
		Recording: RecordingLock{Path: paths.RecordingLock, PID: 7, Hostname: "host", Now: clock(fixedNow)},
		Now:       time.Now,
		Sleep:     func(context.Context, time.Duration) error { return nil },
		PollEvery: time.Millisecond,
		PollFor:   5 * time.Second,
		Out:       fixture.out,
		Err:       fixture.err,
	}
	return fixture
}

func sendRequest(apply bool) SendRequest {
	return SendRequest{Tag: testTag, WalletID: testWalletID, Asset: "ETH", BaseUnits: "5", Decimals: "1", To: testTo, ExternalUserID: "42", Chain: "base", Apply: apply}
}

func TestSendFromBaseDryRunPrintsThePlanAndSendsNothing(t *testing.T) {
	fixture := newFundingFixture(t)
	code, err := fixture.funding.SendFromBase(context.Background(), sendRequest(false))
	if err != nil || code != ExitOK {
		t.Fatalf("code %d err %v", code, err)
	}
	wantPlan := `plan {"tag": "` + testTag + `", "wallet_id": "` + testWalletID + `", "asset": "ETH", "amount": "0.5", "base_units": "5", "to": "` + testTo + `", "from": "` + testFrom + `", "idempotency_key": "ddb6b133-6bd9-5e73-9359-4d8721cd1af5"}`
	if fixture.out.String() != wantPlan+"\ndry run: pre-flight verified, nothing sent (add --apply to send once)\n" {
		t.Fatalf("output %s", fixture.out.String())
	}
	if len(fixture.api.posts) != 0 {
		t.Fatal("dry run posted")
	}
	if _, err := os.Stat(filepath.Join(fixture.paths.LocksDir, "send-"+testTag+".claim")); !os.IsNotExist(err) {
		t.Fatal("dry run claimed the tag")
	}
}

func TestSendFromBaseAppliesOnceRecordsTheLedgerAndReleasesTheLock(t *testing.T) {
	fixture := newFundingFixture(t)
	code, err := fixture.funding.SendFromBase(context.Background(), sendRequest(true))
	if err != nil || code != ExitOK {
		t.Fatalf("code %d err %v (stderr %s)", code, err, fixture.err.String())
	}
	if len(fixture.api.posts) != 1 {
		t.Fatalf("posts %d", len(fixture.api.posts))
	}
	post := fixture.api.posts[0]
	if post["passphrase"] != testPassphrase || post["amount"] != "0.5" || post["external_user_id"] != "42" || post["idempotency_key"] != IdempotencyKey(testTag) {
		t.Fatalf("POST body %v", post)
	}
	output := fixture.out.String()
	if strings.Contains(output, testPassphrase) || strings.Contains(output, testToken) {
		t.Fatal("output leaks the passphrase or the token")
	}
	if !strings.Contains(output, "POST status 201; claim ") || !strings.HasSuffix(output, "tx hash: "+testTxHash+"\n") {
		t.Fatalf("output %s", output)
	}
	if _, err := os.Stat(fixture.paths.RecordingLock); !os.IsNotExist(err) {
		t.Fatal("recording lock was not released")
	}
	ledger, _ := os.ReadFile(fixture.paths.FundingLedger)
	for _, fragment := range []string{`"chain": "base"`, `"amount": "5 base units (0.5 ETH)"`, `"hash": "` + testTxHash + `"`, `"status": "broadcast"`, `"lookup_status": "broadcast"`, `"source": "` + testFrom + `"`} {
		if !strings.Contains(string(ledger), fragment) {
			t.Errorf("ledger misses %s:\n%s", fragment, ledger)
		}
	}
	record := filepath.Join(fixture.paths.FundingDir, testTag+".json")
	requireMode(t, record, PrivateFileMode)
	if content, _ := os.ReadFile(record); strings.Contains(string(content), testPassphrase) {
		t.Fatal("funding record stores the passphrase")
	}

	code, err = fixture.funding.SendFromBase(context.Background(), sendRequest(true))
	if code != ExitFailure || err == nil || !strings.Contains(err.Error(), "already has "+testTag) {
		t.Fatalf("second send: %d %v", code, err)
	}
	if len(fixture.api.posts) != 1 {
		t.Fatal("second send posted again")
	}
}

func TestSendFromBaseGuards(t *testing.T) {
	t.Run("vault_test already has the transfer", func(t *testing.T) {
		fixture := newFundingFixture(t)
		fixture.matches = []string{"id-1 completed 0xab"}
		if _, err := fixture.funding.SendFromBase(context.Background(), sendRequest(true)); err == nil || err.Error() != "vault_test already has this outbound transfer: ['id-1 completed 0xab']" {
			t.Fatalf("got %v", err)
		}
		if fixture.preflights != 0 {
			t.Fatal("pre-flight ran after a guard refused")
		}
		want := []string{testWalletID, testTo, "ETH", "5"}
		if len(fixture.matchQueries) != 1 || strings.Join(fixture.matchQueries[0], " ") != strings.Join(want, " ") {
			t.Fatalf("outbound lookup %v, want %v", fixture.matchQueries, want)
		}
	})
	t.Run("pre-flight planned something else", func(t *testing.T) {
		fixture := newFundingFixture(t)
		fixture.preflight = fixture.preflight.Set("transactions", []any{pyjson.Object{{Key: "to", Value: testFrom}, {Key: "amount", Value: "5"}}})
		if _, err := fixture.funding.SendFromBase(context.Background(), sendRequest(true)); err == nil || !strings.Contains(err.Error(), "pre-flight planned something else") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("claim from an earlier attempt", func(t *testing.T) {
		fixture := newFundingFixture(t)
		if _, err := ClaimTransfer(fixture.paths.LocksDir, testTag, pyjson.Object{}, fixedNow); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.funding.SendFromBase(context.Background(), sendRequest(true)); err == nil || !strings.Contains(err.Error(), "already attempted") {
			t.Fatalf("got %v", err)
		}
		if len(fixture.api.posts) != 0 {
			t.Fatal("posted despite the claim")
		}
		if _, err := os.Stat(fixture.paths.RecordingLock); !os.IsNotExist(err) {
			t.Fatal("recording lock leaked after a refused claim")
		}
	})
	t.Run("someone else records", func(t *testing.T) {
		fixture := newFundingFixture(t)
		other := fixture.funding.Recording
		if err := other.Acquire("markets-recording"); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.funding.SendFromBase(context.Background(), sendRequest(true)); err == nil || !strings.Contains(err.Error(), "recording lock is held") {
			t.Fatalf("got %v", err)
		}
		request := sendRequest(true)
		request.RecordingLockHeldBy = "markets-recording"
		fixture.funding.Recording.Now = clock(fixedNow.Add(RecordingLockStaleAfter + time.Second))
		if code, err := fixture.funding.SendFromBase(context.Background(), request); err == nil && code == ExitOK {
			t.Fatal("stale heartbeat accepted")
		}
		fixture.funding.Recording.Now = clock(fixedNow)
		if code, err := fixture.funding.SendFromBase(context.Background(), request); err != nil || code != ExitOK {
			t.Fatalf("held-by send: %d %v", code, err)
		}
		if _, err := os.Stat(fixture.paths.RecordingLock); err != nil {
			t.Fatal("removed the recording lock of the caller")
		}
	})
	t.Run("invalid inputs", func(t *testing.T) {
		fixture := newFundingFixture(t)
		mutations := []func(*SendRequest){
			func(r *SendRequest) { r.Tag = "X" },
			func(r *SendRequest) { r.WalletID = "not-a-uuid" },
			func(r *SendRequest) { r.Asset = "eth" },
			func(r *SendRequest) { r.BaseUnits = "0" },
			func(r *SendRequest) { r.Decimals = "37" },
			func(r *SendRequest) { r.To = "0x'; drop table transactions; --" },
			func(r *SendRequest) { r.ExternalUserID = "abc" },
			func(r *SendRequest) { r.Chain = "Base!" },
			func(r *SendRequest) { r.RecordingLockHeldBy = "X" },
		}
		for index, mutate := range mutations {
			request := sendRequest(true)
			mutate(&request)
			if code, err := fixture.funding.SendFromBase(context.Background(), request); err == nil || code != ExitFailure {
				t.Errorf("mutation %d accepted", index)
			}
		}
		if len(fixture.api.posts) != 0 {
			t.Fatal("an invalid request posted")
		}
	})
}

func TestSendFromBaseReportsErrorsAndMissingHashes(t *testing.T) {
	fixture := newFundingFixture(t)
	fixture.api.postCode = http.StatusUnprocessableEntity
	fixture.api.postBody = `{"error": "insufficient funds"}`
	code, err := fixture.funding.SendFromBase(context.Background(), sendRequest(true))
	if err != nil || code != ExitFailure || !strings.Contains(fixture.err.String(), `{"error": "insufficient funds"}`) {
		t.Fatalf("code %d err %v stderr %s", code, err, fixture.err.String())
	}

	pending := newFundingFixture(t)
	pending.api.hashAfter = 1 << 30
	pending.funding.PollFor = 20 * time.Millisecond
	pending.funding.Sleep = func(_ context.Context, duration time.Duration) error { time.Sleep(5 * time.Millisecond); return nil }
	code, err = pending.funding.SendFromBase(context.Background(), sendRequest(true))
	if err != nil || code != ExitNoTxHashYet || !strings.HasSuffix(pending.out.String(), "tx hash: (none yet)\n") {
		t.Fatalf("code %d err %v out %s", code, err, pending.out.String())
	}
	ledger, _ := os.ReadFile(pending.paths.FundingLedger)
	if !strings.Contains(string(ledger), `"status": "submitted, no tx hash yet"`) {
		t.Fatalf("ledger %s", ledger)
	}
}

func TestConsolidateDryRunAndApplyOnce(t *testing.T) {
	fixture := newFundingFixture(t)
	request := ConsolidateRequest{Tag: "bnb-consolidate-01", WalletID: testWalletID, Asset: "BNB"}
	if code, err := fixture.funding.Consolidate(context.Background(), request); err != nil || code != ExitOK {
		t.Fatalf("dry run %d %v", code, err)
	}
	wantPlan := `plan {"tag": "bnb-consolidate-01", "wallet_id": "` + testWalletID + `", "asset": "BNB", "strategy": "direct", "sweeps": [{"from": "` + testFrom + `", "to": "` + testTo + `", "amount": "5"}], "idempotency_key": "7962888e-7fd7-5467-afc2-88d9584e763b"}`
	if !strings.HasPrefix(fixture.out.String(), wantPlan+"\ndry run: pre-flight verified, nothing sent (add --apply to consolidate once)\n") || len(fixture.api.posts) != 0 {
		t.Fatalf("dry run output %s", fixture.out.String())
	}

	request.Apply = true
	fixture.out.Reset()
	if code, err := fixture.funding.Consolidate(context.Background(), request); err != nil || code != ExitOK {
		t.Fatalf("apply %d %v", code, err)
	}
	if len(fixture.api.posts) != 1 || fixture.api.posts[0]["asset"] != "BNB" || fixture.api.posts[0]["passphrase"] != testPassphrase {
		t.Fatalf("posts %v", fixture.api.posts)
	}
	if !strings.Contains(fixture.out.String(), "{\n \"data\": {\n  \"status\": \"pending\"\n }\n}\n") {
		t.Fatalf("apply output %q", fixture.out.String())
	}
	ledger, _ := os.ReadFile(fixture.paths.FundingLedger)
	if !strings.Contains(string(ledger), `"chain": "bnb"`) || !strings.Contains(string(ledger), `"response_file": "`+filepath.Join(fixture.paths.FundingDir, "bnb-consolidate-01.json")+`"`) {
		t.Fatalf("ledger %s", ledger)
	}
	if _, err := os.Stat(fixture.paths.RecordingLock); !os.IsNotExist(err) {
		t.Fatal("recording lock was not released")
	}
	if _, err := fixture.funding.Consolidate(context.Background(), request); err == nil || !strings.Contains(err.Error(), "not consolidating again") {
		t.Fatalf("second consolidate: %v", err)
	}

	empty := newFundingFixture(t)
	empty.preflight = empty.preflight.Set("transactions", []any{})
	if _, err := empty.funding.Consolidate(context.Background(), request); err == nil || !strings.Contains(err.Error(), "planned no sweep") {
		t.Fatalf("empty sweep: %v", err)
	}
}

func TestResponseHelpers(t *testing.T) {
	decode := func(document string) any {
		value, err := pyjson.Decode([]byte(document))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	cases := map[string]string{
		`{"tx_hash": "0x1"}`:                       "0x1",
		`{"data": {"tx_hash": "0x2"}}`:             "0x2",
		`{"withdrawal": {"tx_hash": "0x3"}}`:       "0x3",
		`{"tx_hash": "", "data": {"tx_hash": ""}}`: "",
		`[1, 2]`:        "",
		`{"data": [1]}`: "",
	}
	for document, want := range cases {
		if got := FindTxHash(decode(document)); got != want {
			t.Errorf("FindTxHash(%s) = %q", document, got)
		}
	}
	if LookupStatus(decode(`{"data": {"status": "broadcast"}}`)) != "broadcast" || LookupStatus(decode(`{"data": null, "status": "top"}`)) != "top" || LookupStatus(decode(`{"data": [1]}`)) != nil {
		t.Fatal("LookupStatus")
	}
	if raw, isObject := decodeResponseBody([]byte("<html>bad gateway</html>")).(pyjson.Object); !isObject || raw.String("raw") != "<html>bad gateway</html>" {
		t.Fatal("non-JSON body")
	}
	if empty, isObject := decodeResponseBody(nil).(pyjson.Object); !isObject || len(empty) != 0 {
		t.Fatal("empty body")
	}
}
