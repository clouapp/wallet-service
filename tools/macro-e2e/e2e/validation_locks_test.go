package e2e

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/macrowallets/waas/pkg/pyjson"
)

var fixedNow = time.Date(2026, 10, 3, 5, 0, 0, 0, time.UTC)

func clock(at time.Time) func() time.Time { return func() time.Time { return at } }

func TestHumanAmountMatchesPythonDecimalScaling(t *testing.T) {
	cases := []struct {
		baseUnits string
		decimals  int
		want      string
	}{
		{"1", 0, "1"},
		{"1", 18, "0.000000000000000001"},
		{"123456", 6, "0.123456"},
		{"100000000000000000", 18, "0.100000000000000000"},
		{"5", 1, "0.5"},
		{"1000", 3, "1.000"},
		{"10", 2, "0.10"},
		{"1", 36, "0.000000000000000000000000000000000001"},
		{"70000", 8, "0.00070000"},
		{"999999999999999999999999999999999999999", 18, "999999999999999999999.999999999999999999"},
	}
	for _, testCase := range cases {
		got, err := HumanAmount(testCase.baseUnits, testCase.decimals)
		if err != nil || got != testCase.want {
			t.Errorf("HumanAmount(%s, %d) = %q, %v; want %q", testCase.baseUnits, testCase.decimals, got, err, testCase.want)
		}
	}
	for _, bad := range []struct {
		baseUnits string
		decimals  int
	}{{"0", 2}, {"01", 2}, {"1.5", 2}, {"-1", 2}, {"1", -1}, {"1", MaxDecimals + 1}} {
		if _, err := HumanAmount(bad.baseUnits, bad.decimals); err == nil {
			t.Errorf("HumanAmount(%q, %d) accepted", bad.baseUnits, bad.decimals)
		}
	}
}

func TestParseDecimalsBounds(t *testing.T) {
	for raw, want := range map[string]int{"0": 0, "18": 18, " 6 ": 6, "36": 36} {
		if got, err := ParseDecimals(raw); err != nil || got != want {
			t.Errorf("ParseDecimals(%q) = %d, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "x", "-1", "37", "1.5"} {
		if _, err := ParseDecimals(raw); err == nil {
			t.Errorf("ParseDecimals(%q) accepted", raw)
		}
	}
}

func TestIdempotencyKeyIsPythonUUID5(t *testing.T) {
	cases := map[string]string{
		"base-sepolia-fund-1": "ddb6b133-6bd9-5e73-9359-4d8721cd1af5",
		"bnb-consolidate-01":  "7962888e-7fd7-5467-afc2-88d9584e763b",
	}
	for tag, want := range cases {
		if got := IdempotencyKey(tag); got != want {
			t.Errorf("IdempotencyKey(%s) = %s, want %s", tag, got, want)
		}
	}
}

func TestRequireAndPythonRepr(t *testing.T) {
	if _, err := Require(TagPattern, "Bad Tag", "tag"); err == nil || err.Error() != "invalid tag: 'Bad Tag'" {
		t.Fatalf("got %v", err)
	}
	cases := map[string]string{"ab'c": `"ab'c"`, `a"b`: `'a"b'`, `a'b"c`: `'a\'b"c'`, "x\ny": `'x\ny'`, "é": "'é'"}
	for input, want := range cases {
		if got := pythonRepr(input); got != want {
			t.Errorf("pythonRepr(%q) = %s, want %s", input, got, want)
		}
	}
}

func newRecordingLock(t *testing.T, now time.Time) RecordingLock {
	t.Helper()
	return RecordingLock{Path: filepath.Join(t.TempDir(), recordingLockName), PID: 4242, Hostname: "test-host", Now: clock(now)}
}

func TestRecordingLockAcquireIsExclusiveAndReleaseChecksTheOwner(t *testing.T) {
	lock := newRecordingLock(t, fixedNow)
	if err := lock.Acquire("send-from-base:tag-one"); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(lock.Path)
	want := `{"owner": "send-from-base:tag-one", "pid": 4242, "host": "test-host", "startedAt": "2026-10-03T05:00:00+00:00", "heartbeatAt": "2026-10-03T05:00:00+00:00"}`
	if string(content) != want {
		t.Fatalf("lock payload %s", content)
	}
	requireMode(t, lock.Path, PrivateFileMode)
	if err := lock.Acquire("consolidate-once:other"); err == nil || !strings.Contains(err.Error(), "recording lock is held, not sending") {
		t.Fatalf("second acquire: %v", err)
	}
	if err := lock.RefuseWhileHeld("restarting"); err == nil {
		t.Fatal("RefuseWhileHeld ignored the lock")
	}
	lock.Release("someone-else")
	if _, err := os.Stat(lock.Path); err != nil {
		t.Fatal("released a lock owned by someone else")
	}
	lock.Release("send-from-base:tag-one")
	if _, err := os.Stat(lock.Path); !os.IsNotExist(err) {
		t.Fatal("owner release kept the lock")
	}
	if err := lock.RefuseWhileHeld("restarting"); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingLockRequireHeldBy(t *testing.T) {
	lock := newRecordingLock(t, fixedNow)
	if err := lock.RequireHeldBy("recorder"); err == nil || !strings.Contains(err.Error(), "nobody holds") {
		t.Fatalf("no lock: %v", err)
	}
	write := func(content string) {
		if err := os.WriteFile(lock.Path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"owner": "recorder", "heartbeatAt": "2026-10-03T04:58:00.123Z"}`)
	if err := lock.RequireHeldBy("recorder"); err != nil {
		t.Fatalf("fresh lock: %v", err)
	}
	if err := lock.RequireHeldBy("another"); err == nil || !strings.Contains(err.Error(), "held by 'recorder', not 'another'") {
		t.Fatalf("other owner: %v", err)
	}
	write(`{"owner": "recorder", "heartbeatAt": "2026-10-03T04:54:59+00:00"}`)
	if err := lock.RequireHeldBy("recorder"); err == nil || !strings.Contains(err.Error(), "301 s old heartbeat") {
		t.Fatalf("stale lock: %v", err)
	}
	write(`{"heartbeatAt": "2026-10-03T05:00:00Z"}`)
	if err := lock.RequireHeldBy("recorder"); err == nil || !strings.Contains(err.Error(), "held by None") {
		t.Fatalf("ownerless lock: %v", err)
	}
	write(`not json`)
	if err := lock.RequireHeldBy("recorder"); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("garbage lock: %v", err)
	}
}

func TestClaimTransferIsExclusiveAndNeverOverwritten(t *testing.T) {
	locks := filepath.Join(t.TempDir(), "locks")
	plan := pyjson.Object{{Key: "tag", Value: "tag-one"}, {Key: "amount", Value: "0.5"}}
	claim, err := ClaimTransfer(locks, "tag-one", plan, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(claim) != "send-tag-one.claim" {
		t.Fatalf("claim path %s", claim)
	}
	requireMode(t, claim, PrivateFileMode)
	requireMode(t, locks, StateDirMode)
	content, _ := os.ReadFile(claim)
	want := "{\n  \"tag\": \"tag-one\",\n  \"amount\": \"0.5\",\n  \"claimedAt\": \"2026-10-03T05:00:00+00:00\"\n}"
	if string(content) != want {
		t.Fatalf("claim content %q", content)
	}
	if _, err := ClaimTransfer(locks, "tag-one", pyjson.Object{}, fixedNow); err == nil || !strings.Contains(err.Error(), "already attempted") {
		t.Fatalf("second claim: %v", err)
	}
	if again, _ := os.ReadFile(claim); string(again) != want {
		t.Fatal("claim was overwritten")
	}
	if len(plan) != 2 {
		t.Fatal("ClaimTransfer mutated the plan")
	}
}

func TestFileLockWaitsThenGivesUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locks", restartLockName)
	first, err := AcquireFileLock(context.Background(), path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireFileLock(context.Background(), path, 300*time.Millisecond); err == nil || !strings.Contains(err.Error(), "do not wrap it in flock") {
		t.Fatalf("contended lock: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AcquireFileLock(cancelled, path, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
	first.Release()
	second, err := AcquireFileLock(context.Background(), path, time.Second)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	second.Release()
	second.Release()
}

func TestLedgerUpsertKeepsPythonLayoutAndOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "funding", fundingLedgerName)
	source := "{\n  \"recordedAt\": \"2026-10-01T00:00:00+00:00\",\n  \"owner\": \"e2e\",\n  \"entries\": [\n    {\n      \"chain\": \"eth\",\n      \"tag\": \"keep-me\",\n      \"amount\": 1.5,\n      \"note\": \"caf\\u00e9\"\n    },\n    {\n      \"tag\": \"base-sepolia-fund-1\",\n      \"status\": \"old\"\n    }\n  ],\n  \"notes\": []\n}\n"
	if err := WritePrivateFile(path, []byte(source)); err != nil {
		t.Fatal(err)
	}
	ledger := FundingLedger{Path: path, Now: clock(fixedNow)}
	if has, err := ledger.HasTag("keep-me"); err != nil || !has {
		t.Fatalf("HasTag keep-me = %v %v", has, err)
	}
	if has, _ := ledger.HasTag("absent"); has {
		t.Fatal("HasTag absent")
	}
	entry := pyjson.Object{
		{Key: "chain", Value: "base"}, {Key: "tag", Value: "base-sepolia-fund-1"},
		{Key: "purpose", Value: sendPurpose}, {Key: "source", Value: nil},
		{Key: "destination", Value: "0xabc"}, {Key: "http_status", Value: 201},
		{Key: "hash", Value: ""}, {Key: "status", Value: statusSubmitted},
		{Key: "recordedAt", Value: "2026-10-03T05:00:00+00:00"},
	}
	if err := ledger.Upsert("base-sepolia-fund-1", entry); err != nil {
		t.Fatal(err)
	}
	pythonOutput := "{\n  \"recordedAt\": \"2026-10-03T05:00:00+00:00\",\n  \"owner\": \"e2e\",\n  \"entries\": [\n    {\n      \"chain\": \"eth\",\n      \"tag\": \"keep-me\",\n      \"amount\": 1.5,\n      \"note\": \"caf\\u00e9\"\n    },\n    {\n      \"chain\": \"base\",\n      \"tag\": \"base-sepolia-fund-1\",\n      \"purpose\": \"e2e funding transfer from base via Wallets API\",\n      \"source\": null,\n      \"destination\": \"0xabc\",\n      \"http_status\": 201,\n      \"hash\": \"\",\n      \"status\": \"submitted\",\n      \"recordedAt\": \"2026-10-03T05:00:00+00:00\"\n    }\n  ],\n  \"notes\": []\n}\n"
	if content, _ := os.ReadFile(path); string(content) != pythonOutput {
		t.Fatalf("ledger differs from Python:\n%s", content)
	}
	requireMode(t, path, PrivateFileMode)

	if err := os.WriteFile(path, []byte(`{"entries": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.HasTag("x"); err == nil || !strings.Contains(err.Error(), "no entries list") {
		t.Fatalf("bad ledger: %v", err)
	}
	if _, err := (FundingLedger{Path: filepath.Join(t.TempDir(), "missing.json")}).HasTag("x"); err == nil {
		t.Fatal("missing ledger accepted")
	}
}

func TestParseArgsAcceptsFlagsAnywhere(t *testing.T) {
	parsed, err := ParseArgs([]string{"--apply", "a", "--chain=base", "b", "--recording-lock-held-by", "owner-1", "c"}, []string{"apply"}, []string{"chain", "recording-lock-held-by"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(parsed.Positional, ",") != "a,b,c" || !parsed.Switches["apply"] || parsed.Values["chain"] != "base" || parsed.Values["recording-lock-held-by"] != "owner-1" {
		t.Fatalf("parsed %+v", parsed)
	}
	if parsed, err := ParseArgs([]string{"--", "--not-a-flag"}, nil, nil, 1); err != nil || parsed.Positional[0] != "--not-a-flag" {
		t.Fatalf("end of flags: %+v %v", parsed, err)
	}
	failures := [][]string{{"--unknown"}, {"--chain"}, {"--apply=yes"}, {"one", "two"}}
	for _, args := range failures {
		if _, err := ParseArgs(args, []string{"apply"}, []string{"chain"}, 0); !errors.Is(err, ErrUsage) {
			t.Errorf("ParseArgs(%v) = %v", args, err)
		}
	}
}
