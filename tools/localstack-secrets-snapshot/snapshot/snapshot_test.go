package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/macrowallets/waas/pkg/pyjson"
)

// Vectors produced once with CPython's json module (the format the existing
// snapshots and their meta digests were written in).
const (
	pythonCanonicalVector = `[["app/config","arn:aws:secretsmanager:us-east-1:000000000000:secret:app/config-XyZabc",null,"line1\nline2\t\u00e9\ud83d\ude00\u007f\\/\b\f\r\u0001"],["vault/wallet/b/share-b","arn:aws:secretsmanager:us-east-1:000000000000:secret:vault/wallet/b/share-b-AbCdEf","AAEC/w==",null]]`
	pythonDigestVector    = "56e5e50875eb3c78c56a1c13ddc7dc46124712ce33b45744a4c9fb99c95de4d1"
	pythonRecordVector    = `{"schema_version": 1, "account_id": "000000000000", "region": "us-east-1", "secrets": [{"name": "app/config", "arn": "arn:aws:secretsmanager:us-east-1:000000000000:secret:app/config-XyZabc", "version_id": null, "description": "caf\u00e9 <&> \"q\"", "tags": [{"Key": "env", "Value": "dev"}], "secret_binary_b64": null, "secret_string": "line1\nline2\t\u00e9\ud83d\ude00\u007f\\/\b\f\r\u0001"}, {"name": "vault/wallet/b/share-b", "arn": "arn:aws:secretsmanager:us-east-1:000000000000:secret:vault/wallet/b/share-b-AbCdEf", "version_id": "11111111-2222-3333-4444-555555555555", "description": null, "tags": [], "secret_binary_b64": "AAEC/w==", "secret_string": null}]}`
	legacyFixtureDir      = "testdata/legacy"
	legacyFixtureKey      = "seed-test-passphrase.txt"
	legacyFixtureName     = "secrets-20261003T041500123456Z.json.gpg"
	testBootID            = "424242"
	otherBootID           = "777"
	fakeCiphertextPrefix  = "FAKE-ENCRYPTED:"
)

func ptr(value string) *string { return &value }

func vectorEntries() []Entry {
	return []Entry{
		{
			Name: "vault/wallet/b/share-b", ARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:vault/wallet/b/share-b-AbCdEf",
			VersionID: ptr("11111111-2222-3333-4444-555555555555"), Tags: []Tag{}, SecretBinaryB64: ptr("AAEC/w=="),
		},
		{
			Name: "app/config", ARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:app/config-XyZabc",
			Description: ptr("caf\u00e9 <&> \"q\""), Tags: []Tag{{Key: "env", Value: "dev"}},
			SecretString: ptr("line1\nline2\t\u00e9\U0001F600\u007f\\/\b\f\r\u0001"),
		},
	}
}

func TestDigest_Matches_PythonJSONDumps(t *testing.T) {
	sorted := sortedByName(vectorEntries())
	rows := make([]any, len(sorted))
	for index, entry := range sorted {
		rows[index] = []any{entry.Name, entry.ARN, entry.SecretBinaryB64, entry.SecretString}
	}
	canonical, err := pyjson.Dumps(rows, pyjson.Compact)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != pythonCanonicalVector {
		t.Fatalf("canonical JSON differs from Python:\n got %s\nwant %s", canonical, pythonCanonicalVector)
	}
	digest, err := Digest(vectorEntries())
	if err != nil || digest != pythonDigestVector {
		t.Fatalf("digest = %s, %v; want %s", digest, err, pythonDigestVector)
	}
}

func TestEncode_Record_MatchesPythonJSONDumps(t *testing.T) {
	encoded, err := encodeRecord(DefaultAccountID, DefaultRegion, sortedByName(vectorEntries()))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != pythonRecordVector {
		t.Fatalf("record differs from Python:\n got %s\nwant %s", encoded, pythonRecordVector)
	}
}

func TestStamp_Uses_PythonMicrosecondLayout(t *testing.T) {
	moment := time.Date(2026, 10, 3, 4, 15, 0, 123456789, time.UTC)
	if got := Stamp(moment); got != "20261003T041500123456Z" {
		t.Fatalf("Stamp = %s", got)
	}
}

// ---------------------------------------------------------------- fakes

type fakeSecrets struct {
	mu       sync.Mutex
	live     map[string]Entry
	created  []string
	failRead map[string]error
	nextARN  func(Entry) string
}

func newFakeSecrets(entries ...Entry) *fakeSecrets {
	fake := &fakeSecrets{live: map[string]Entry{}, failRead: map[string]error{}}
	for _, entry := range entries {
		fake.live[entry.Name] = entry
	}
	return fake
}

func (f *fakeSecrets) ListSecretNames(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.live))
	for name := range f.live {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (f *fakeSecrets) ReadSecret(_ context.Context, name string) (Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failRead[name]; err != nil {
		return Entry{}, err
	}
	entry, ok := f.live[name]
	if !ok {
		return Entry{}, &smtypes.ResourceNotFoundException{Message: ptr("not found")}
	}
	return entry, nil
}

func (f *fakeSecrets) CreateWithOriginalARN(_ context.Context, entry Entry) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.live[entry.Name]; exists {
		return "", errors.New("fake: create over a live secret")
	}
	created := entry
	if f.nextARN != nil {
		created.ARN = f.nextARN(entry)
	}
	f.live[entry.Name] = created
	f.created = append(f.created, entry.Name)
	return created.ARN, nil
}

type fakeCrypter struct{}

func (fakeCrypter) Encrypt(_ context.Context, plaintext []byte) ([]byte, error) {
	return append([]byte(fakeCiphertextPrefix), plaintext...), nil
}

func (fakeCrypter) Decrypt(_ context.Context, path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(raw, []byte(fakeCiphertextPrefix)) {
		return nil, safeErrorf("gpg exited with 2")
	}
	return raw[len(fakeCiphertextPrefix):], nil
}

type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordingLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *recordingLogger) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

type fixture struct {
	cfg     Config
	secrets *fakeSecrets
	log     *recordingLogger
	tool    *Tool
	clock   time.Time
}

func newFixture(t *testing.T, crypter Crypter, live ...Entry) *fixture {
	t.Helper()
	root := t.TempDir()
	keyFile := filepath.Join(root, "seed.key")
	writeFile(t, keyFile, strings.Repeat("k", minSeedKeyBytes))
	procStat := filepath.Join(root, "stat")
	writeProcStat(t, procStat, testBootID)
	f := &fixture{
		cfg: Config{
			Region: DefaultRegion, AccountID: DefaultAccountID, Dir: filepath.Join(root, "snapshots"), KeyFile: keyFile,
			Interval: 10 * time.Millisecond, Keep: DefaultKeep, LoopLock: filepath.Join(root, "loop.lock"),
			RestoredMarker: filepath.Join(root, "restored"), ProcStat: procStat,
		},
		secrets: newFakeSecrets(live...),
		log:     &recordingLogger{},
		clock:   time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC),
	}
	f.rebuild(t, crypter)
	return f
}

func (f *fixture) rebuild(t *testing.T, crypter Crypter) {
	t.Helper()
	tool, err := NewTool(f.cfg, Dependencies{Secrets: f.secrets, Crypter: crypter, Log: f.log, Now: func() time.Time {
		f.clock = f.clock.Add(time.Second)
		return f.clock
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.tool = tool
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeProcStat writes a /proc/<pid>/stat line whose 22nd field (start time) is bootID.
func writeProcStat(t *testing.T, path, bootID string) {
	t.Helper()
	fields := make([]string, procStatFieldAfter+3)
	for index := range fields {
		fields[index] = "0"
	}
	fields[procStatFieldAfter] = bootID
	writeFile(t, path, "1 (python (x)) "+strings.Join(fields, " "))
}

func (f *fixture) markRestored(t *testing.T) {
	t.Helper()
	writeFile(t, f.cfg.RestoredMarker, testBootID+"\n")
}

func (f *fixture) snapshotNames(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(f.cfg.Dir, SnapshotPrefix+"*"+SnapshotSuffix))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(matches)
	return matches
}

func shareEntry(name, suffix, value string) Entry {
	return Entry{
		Name: name, ARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:" + name + "-" + suffix,
		VersionID: ptr("aaaaaaaa-bbbb-cccc-dddd-" + strings.Repeat("e", 12)), Tags: []Tag{}, SecretBinaryB64: ptr(value),
	}
}

// ---------------------------------------------------------------- export

func TestExport_Refuses_UntilRestoreRanInThisContainer(t *testing.T) {
	f := newFixture(t, fakeCrypter{}, shareEntry("vault/wallet/a/share-b", "AAAAAA", "AQ=="))
	outcome, err := f.tool.Export(context.Background())
	if err != nil || outcome != ExportRefused {
		t.Fatalf("Export = %s, %v; want refused", outcome, err)
	}
	writeFile(t, f.cfg.RestoredMarker, otherBootID)
	if outcome, _ := f.tool.Export(context.Background()); outcome != ExportRefused {
		t.Fatalf("a marker of an earlier container run must not unlock export, got %s", outcome)
	}
	if len(f.snapshotNames(t)) != 0 {
		t.Fatal("a refused export wrote a snapshot")
	}
}

func TestExport_Refuses_WithoutSeedKey(t *testing.T) {
	f := newFixture(t, fakeCrypter{})
	f.markRestored(t)
	writeFile(t, f.cfg.KeyFile, "short")
	if _, err := f.tool.Export(context.Background()); err == nil || !strings.Contains(err.Error(), "too short") {
		t.Fatalf("Export with a short seed key = %v", err)
	}
	if err := os.Remove(f.cfg.KeyFile); err != nil {
		t.Fatal(err)
	}
	if _, err := f.tool.Export(context.Background()); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("Export without seed key = %v", err)
	}
}

func TestExport_Writes_PrivateSnapshotThenSkipsUnchanged(t *testing.T) {
	f := newFixture(t, fakeCrypter{}, vectorEntries()...)
	f.markRestored(t)
	outcome, err := f.tool.Export(context.Background())
	if err != nil || outcome != ExportWritten {
		t.Fatalf("first Export = %s, %v", outcome, err)
	}
	names := f.snapshotNames(t)
	if len(names) != 1 {
		t.Fatalf("snapshots = %v", names)
	}
	assertMode(t, f.cfg.Dir, privateDirMode)
	assertMode(t, names[0], privateFileMode)
	assertMode(t, metaPathOf(names[0]), privateFileMode)

	plaintext, err := fakeCrypter{}.Decrypt(context.Background(), names[0])
	if err != nil || string(plaintext) != pythonRecordVector {
		t.Fatalf("snapshot payload differs from the Python layout: %v\n%s", err, plaintext)
	}
	meta, err := os.ReadFile(metaPathOf(names[0]))
	if err != nil || !strings.Contains(string(meta), `"digest": "`+pythonDigestVector+`"`) || !strings.HasPrefix(string(meta), `{"count": 2, "live_count": 2, `) {
		t.Fatalf("meta = %s, %v", meta, err)
	}

	outcome, err = f.tool.Export(context.Background())
	if err != nil || outcome != ExportUnchanged || len(f.snapshotNames(t)) != 1 {
		t.Fatalf("second Export = %s, %v, %d snapshots", outcome, err, len(f.snapshotNames(t)))
	}
}

func TestExport_Carries_SecretsMissingLiveUnlessForced(t *testing.T) {
	kept := shareEntry("vault/wallet/a/share-b", "AAAAAA", "AQ==")
	vanished := shareEntry("vault/wallet/z/share-b", "ZZZZZZ", "Ag==")
	f := newFixture(t, fakeCrypter{}, kept, vanished)
	f.markRestored(t)
	if _, err := f.tool.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	delete(f.secrets.live, vanished.Name)
	added := shareEntry("vault/wallet/b/share-b", "BBBBBB", "Aw==")
	f.secrets.live[added.Name] = added

	if _, err := f.tool.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	newest := f.loadNewest(t)
	if len(newest.Secrets) != 3 || newest.Meta.LiveCount != 2 {
		t.Fatalf("carried snapshot holds %d secrets, live_count %d", len(newest.Secrets), newest.Meta.LiveCount)
	}
	if !strings.Contains(f.log.joined(), "1 secret(s) missing live are kept from the previous snapshot, e.g. ['vault/wallet/z/share-b']") {
		t.Fatalf("log = %s", f.log.joined())
	}

	f.cfg.Force = true
	f.rebuild(t, fakeCrypter{})
	if _, err := f.tool.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	if forced := f.loadNewest(t); len(forced.Secrets) != 2 {
		t.Fatalf("forced export kept %d secrets", len(forced.Secrets))
	}
}

func TestExport_Refuses_ValueChangeUnderSameVersion(t *testing.T) {
	entry := shareEntry("vault/wallet/a/share-b", "AAAAAA", "AQ==")
	f := newFixture(t, fakeCrypter{}, entry)
	f.markRestored(t)
	if _, err := f.tool.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	changed := entry
	changed.SecretBinaryB64 = ptr("Ag==")
	f.secrets.live[entry.Name] = changed
	_, err := f.tool.Export(context.Background())
	if err == nil || !strings.Contains(err.Error(), "changed value under the same VersionId") {
		t.Fatalf("Export = %v", err)
	}
	if len(f.snapshotNames(t)) != 1 {
		t.Fatal("a refused export wrote a snapshot")
	}
}

func TestExport_Rotates_OldSnapshots(t *testing.T) {
	f := newFixture(t, fakeCrypter{})
	f.cfg.Keep = 2
	f.rebuild(t, fakeCrypter{})
	f.markRestored(t)
	for index := 0; index < 4; index++ {
		entry := shareEntry(fmt.Sprintf("vault/wallet/%d/share-b", index), "AAAAAA", "AQ==")
		f.secrets.live[entry.Name] = entry
		if _, err := f.tool.Export(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	names := f.snapshotNames(t)
	metas, _ := filepath.Glob(filepath.Join(f.cfg.Dir, "*"+MetaSuffix))
	if len(names) != 2 || len(metas) != 2 {
		t.Fatalf("after rotation: %d snapshots, %d metas", len(names), len(metas))
	}
}

func (f *fixture) loadNewest(t *testing.T) *loadedSnapshot {
	t.Helper()
	found, err := f.tool.newestReadable(context.Background())
	if err != nil || found == nil {
		t.Fatalf("newestReadable = %v, %v", found, err)
	}
	return found
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %o, want %o", filepath.Base(path), info.Mode().Perm(), want)
	}
}

// ---------------------------------------------------------------- restore / verify

func TestRestore_Recreates_MissingSecretsWithOriginalARN(t *testing.T) {
	present := shareEntry("vault/wallet/a/share-b", "AAAAAA", "AQ==")
	lost := shareEntry("vault/wallet/b/share-b", "BBBBBB", "Ag==")
	edited := shareEntry("vault/wallet/c/share-b", "CCCCCC", "Aw==")
	f := newFixture(t, fakeCrypter{}, present, lost, edited)
	f.markRestored(t)
	if _, err := f.tool.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	delete(f.secrets.live, lost.Name)
	liveEdit := edited
	liveEdit.SecretBinaryB64 = ptr("BA==")
	f.secrets.live[edited.Name] = liveEdit

	if err := f.tool.Restore(context.Background()); err != nil {
		t.Fatalf("Restore = %v", err)
	}
	if got := f.secrets.live[lost.Name]; got.ARN != lost.ARN || !got.SameMaterial(lost) {
		t.Fatalf("lost secret restored as %+v", got)
	}
	if !f.secrets.live[edited.Name].SameMaterial(liveEdit) {
		t.Fatal("restore overwrote a live secret")
	}
	if len(f.secrets.created) != 1 {
		t.Fatalf("created = %v", f.secrets.created)
	}
	if !f.tool.restoredInThisRun() {
		t.Fatal("restore marker missing after a clean restore")
	}
	log := f.log.joined()
	for _, want := range []string{"different: vault/wallet/c/share-b (live value kept)", "different=1, present=1, restored=1"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "AQ==") || strings.Contains(log, "BA==") {
		t.Fatal("secret material reached the log")
	}
}

func TestRestore_Leaves_NoMarkerWhenASecretFails(t *testing.T) {
	entry := shareEntry("vault/wallet/a/share-b", "AAAAAA", "AQ==")
	f := newFixture(t, fakeCrypter{}, entry)
	f.markRestored(t)
	if _, err := f.tool.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.secrets.failRead[entry.Name] = &smtypes.InternalServiceError{Message: ptr("boom")}
	err := f.tool.Restore(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed:InternalServiceError=1") {
		t.Fatalf("Restore = %v", err)
	}
	if f.tool.restoredInThisRun() {
		t.Fatal("a failed restore must not unlock export")
	}
}

func TestRestore_Without_SnapshotMarksRestored(t *testing.T) {
	f := newFixture(t, fakeCrypter{})
	if err := f.tool.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.tool.restoredInThisRun() || !strings.Contains(f.log.joined(), "no snapshot yet; nothing to restore") {
		t.Fatalf("marker/log after an empty restore: %s", f.log.joined())
	}
}

func TestRestore_Fails_WhenNoSnapshotIsReadable(t *testing.T) {
	f := newFixture(t, fakeCrypter{}, shareEntry("vault/wallet/a/share-b", "AAAAAA", "AQ=="))
	f.markRestored(t)
	if _, err := f.tool.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.snapshotNames(t)[0], "corrupted")
	err := f.tool.Restore(context.Background())
	if err == nil || !strings.Contains(err.Error(), "none of the 1 snapshot(s) could be read") {
		t.Fatalf("Restore = %v", err)
	}
}

func TestVerify_Is_ReadOnly(t *testing.T) {
	kept := shareEntry("vault/wallet/a/share-b", "AAAAAA", "AQ==")
	lost := shareEntry("vault/wallet/b/share-b", "BBBBBB", "Ag==")
	f := newFixture(t, fakeCrypter{}, kept, lost)
	f.markRestored(t)
	if _, err := f.tool.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	delete(f.secrets.live, lost.Name)
	if err := os.Remove(f.cfg.RestoredMarker); err != nil {
		t.Fatal(err)
	}
	if err := f.tool.Verify(context.Background(), true); err != nil {
		t.Fatalf("Verify = %v", err)
	}
	if len(f.secrets.created) != 0 || f.tool.restoredInThisRun() {
		t.Fatal("verify changed LocalStack or the restore marker")
	}
	if !strings.Contains(f.log.joined(), "missing=1, present=1") {
		t.Fatalf("log = %s", f.log.joined())
	}
}

func TestVerify_All_FailsOnAnUnreadableSnapshot(t *testing.T) {
	f := newFixture(t, fakeCrypter{}, shareEntry("vault/wallet/a/share-b", "AAAAAA", "AQ=="))
	f.markRestored(t)
	if _, err := f.tool.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.snapshotNames(t)[0], "corrupted")
	if err := f.tool.Verify(context.Background(), true); err == nil {
		t.Fatal("verify --all accepted an unreadable snapshot")
	}
}

// ---------------------------------------------------------------- loop

func TestLoop_Runs_OncePerContainerAndStopsOnCancel(t *testing.T) {
	f := newFixture(t, fakeCrypter{}, shareEntry("vault/wallet/a/share-b", "AAAAAA", "AQ=="))
	f.markRestored(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.tool.Loop(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for len(f.snapshotNames(t)) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(f.snapshotNames(t)) == 0 {
		t.Fatal("loop never exported")
	}
	if err := f.tool.Loop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.log.joined(), "loop already running") {
		t.Fatal("a second loop did not notice the first")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loop ignored cancellation")
	}
}

// ---------------------------------------------------------------- real gpg

func requireGPG(t *testing.T) {
	t.Helper()
	if _, err := lookPathGPG(); err != nil {
		t.Skip("gpg not installed")
	}
}

func copyLegacyFixture(t *testing.T, f *fixture) GPGCrypter {
	t.Helper()
	if err := os.MkdirAll(f.cfg.Dir, privateDirMode); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{legacyFixtureName, strings.TrimSuffix(legacyFixtureName, SnapshotSuffix) + MetaSuffix} {
		raw, err := os.ReadFile(filepath.Join(legacyFixtureDir, name))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(f.cfg.Dir, name), string(raw))
	}
	key, err := os.ReadFile(filepath.Join(legacyFixtureDir, legacyFixtureKey))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.cfg.KeyFile, string(key))
	return GPGCrypter{KeyFile: f.cfg.KeyFile}
}

func TestGPG_Reads_SnapshotWrittenByThePythonTool(t *testing.T) {
	requireGPG(t)
	f := newFixture(t, fakeCrypter{})
	crypter := copyLegacyFixture(t, f)
	f.rebuild(t, crypter)
	found := f.loadNewest(t)
	if found.Name != legacyFixtureName || len(found.Secrets) != 2 {
		t.Fatalf("legacy snapshot = %s with %d secrets", found.Name, len(found.Secrets))
	}
	if err := f.tool.Restore(context.Background()); err != nil {
		t.Fatalf("Restore from the legacy snapshot = %v", err)
	}
	for _, want := range vectorEntries() {
		got := f.secrets.live[want.Name]
		if got.ARN != want.ARN || !got.SameMaterial(want) {
			t.Fatalf("restored %s = %+v", want.Name, got)
		}
	}
}

func TestGPG_Snapshot_WrittenByGoReadsBackAndMatchesPythonLayout(t *testing.T) {
	requireGPG(t)
	f := newFixture(t, fakeCrypter{}, vectorEntries()...)
	crypter := copyLegacyFixture(t, f)
	f.clock = time.Date(2026, 10, 3, 5, 0, 0, 0, time.UTC)
	f.rebuild(t, crypter)
	f.markRestored(t)
	// The live set equals the legacy snapshot, so nothing new is written...
	if outcome, err := f.tool.Export(context.Background()); err != nil || outcome != ExportUnchanged {
		t.Fatalf("Export over an identical legacy snapshot = %s, %v", outcome, err)
	}
	// ...until something changes.
	added := shareEntry("vault/wallet/c/share-b", "CCCCCC", "Aw==")
	f.secrets.live[added.Name] = added
	if outcome, err := f.tool.Export(context.Background()); err != nil || outcome != ExportWritten {
		t.Fatalf("Export = %s, %v", outcome, err)
	}
	newest := f.loadNewest(t)
	if newest.Name == legacyFixtureName || len(newest.Secrets) != 3 {
		t.Fatalf("newest = %s with %d secrets", newest.Name, len(newest.Secrets))
	}
	plaintext, err := crypter.Decrypt(context.Background(), filepath.Join(f.cfg.Dir, newest.Name))
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := encodeRecord(DefaultAccountID, DefaultRegion, sortedByName(append(vectorEntries(), added)))
	if !bytes.Equal(plaintext, expected) {
		t.Fatal("gpg payload is not the Python record layout")
	}
	if err := f.tool.Verify(context.Background(), true); err != nil {
		t.Fatalf("verify --all over legacy + Go snapshots = %v", err)
	}
}

func TestGPG_Fails_WithWrongKey(t *testing.T) {
	requireGPG(t)
	f := newFixture(t, fakeCrypter{})
	copyLegacyFixture(t, f)
	writeFile(t, f.cfg.KeyFile, strings.Repeat("w", minSeedKeyBytes))
	f.rebuild(t, GPGCrypter{KeyFile: f.cfg.KeyFile})
	if _, err := f.tool.newestReadable(context.Background()); err == nil {
		t.Fatal("a wrong seed key decrypted the snapshot")
	}
}

// ---------------------------------------------------------------- config / safety

func TestConfig_From_EnvRejectsBadNumbers(t *testing.T) {
	t.Setenv(EnvInterval, "0")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("interval 0 accepted")
	}
	t.Setenv(EnvInterval, "60")
	t.Setenv(EnvKeep, "abc")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("keep abc accepted")
	}
	t.Setenv(EnvKeep, "")
	t.Setenv(EnvForce, "1")
	cfg, err := ConfigFromEnv()
	if err != nil || !cfg.Force || cfg.Keep != DefaultKeep || cfg.Interval != time.Minute || cfg.EndpointURL != DefaultEndpointURL {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
}

func TestSafe_Message_NeverLeaksMoreThanAWSCodes(t *testing.T) {
	apiError := &smtypes.ResourceNotFoundException{Message: ptr("secret value would never be here")}
	if got := safeMessage(apiError); got != "AWS error ResourceNotFoundException" {
		t.Fatalf("safeMessage = %q", got)
	}
	long := errors.New(strings.Repeat("x", maxErrorLogRunes*2))
	if got := safeMessage(long); len(got) != maxErrorLogRunes {
		t.Fatalf("safeMessage did not truncate: %d", len(got))
	}
}

func TestRun_Command_MapsExitCodes(t *testing.T) {
	f := newFixture(t, fakeCrypter{}, shareEntry("vault/wallet/a/share-b", "AAAAAA", "AQ=="))
	if code := RunCommand(context.Background(), f.tool, f.log, CommandExport, false); code != ExitRefused {
		t.Fatalf("export before restore = %d", code)
	}
	if code := RunCommand(context.Background(), f.tool, f.log, CommandRestore, false); code != ExitOK {
		t.Fatalf("restore = %d", code)
	}
	if code := RunCommand(context.Background(), f.tool, f.log, CommandExport, false); code != ExitOK {
		t.Fatalf("export = %d", code)
	}
	if code := RunCommand(context.Background(), f.tool, f.log, "bogus", false); code != ExitUsage {
		t.Fatalf("bogus = %d", code)
	}
}
