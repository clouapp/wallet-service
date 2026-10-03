package snapshot

import (
	"context"
	"os"
	"path/filepath"
)

// ExportOutcome tells the caller what an export did.
type ExportOutcome string

const (
	ExportWritten   ExportOutcome = "written"
	ExportUnchanged ExportOutcome = "unchanged"
	ExportRefused   ExportOutcome = "refused"

	carriedExampleCount = 3
)

// CollectLive reads every live (not deleted) secret, sorted by name.
func (t *Tool) CollectLive(ctx context.Context) ([]Entry, error) {
	names, err := t.secrets.ListSecretNames(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		entry, err := t.secrets.ReadSecret(ctx, name)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Export snapshots every live secret into a new encrypted file when anything
// changed. It refuses until a restore completed in this container run, so an empty
// LocalStack can never overwrite the newest snapshot. Secrets missing live are
// carried over from the previous snapshot unless SECRETS_SNAPSHOT_FORCE=1.
func (t *Tool) Export(ctx context.Context) (ExportOutcome, error) {
	if err := RequireSeedKey(t.cfg.KeyFile); err != nil {
		return "", err
	}
	if !t.restoredInThisRun() {
		t.log.Printf("export skipped: restore has not completed in this container run")
		return ExportRefused, nil
	}
	if err := os.MkdirAll(t.cfg.Dir, privateDirMode); err != nil {
		return "", safeErrorf("create %s: %v", t.cfg.Dir, err)
	}
	lock, _, err := lockFile(filepath.Join(t.cfg.Dir, exportLockName), true)
	if err != nil {
		return "", err
	}
	defer lock.Close()

	live, err := t.CollectLive(ctx)
	if err != nil {
		return "", err
	}
	liveDigest, err := Digest(live)
	if err != nil {
		return "", err
	}
	metas, err := t.snapshotMetas()
	if err != nil {
		return "", err
	}
	if len(metas) > 0 && metas[0].Meta.Digest == liveDigest {
		return ExportUnchanged, nil
	}

	var previous *loadedSnapshot
	if len(metas) > 0 {
		if previous, err = t.newestReadable(ctx); err != nil {
			return "", err
		}
	}
	var previousSecrets []Entry
	if previous != nil {
		previousSecrets = previous.Secrets
	}
	if err := checkVersionInvariant(live, previousSecrets); err != nil {
		return "", err
	}
	secrets := t.mergeWithPrevious(live, previousSecrets)
	digest, err := Digest(secrets)
	if err != nil {
		return "", err
	}
	if previous != nil && previous.Meta.Digest == digest {
		return ExportUnchanged, nil
	}
	if err := t.writeSnapshot(ctx, secrets, len(live), digest); err != nil {
		return "", err
	}
	return ExportWritten, nil
}

func (t *Tool) writeSnapshot(ctx context.Context, secrets []Entry, liveCount int, digest string) error {
	stamp := Stamp(t.now())
	snapshotPath := t.snapshotPathFor(stamp)
	plaintext, err := encodeRecord(t.cfg.AccountID, t.cfg.Region, secrets)
	if err != nil {
		return err
	}
	ciphertext, err := t.crypter.Encrypt(ctx, plaintext)
	zero(plaintext)
	if err != nil {
		return err
	}
	if err := writeAtomic(snapshotPath, ciphertext); err != nil {
		return err
	}
	meta, err := encodeMeta(Meta{Count: len(secrets), LiveCount: liveCount, Digest: digest, CreatedAt: stamp})
	if err != nil {
		return err
	}
	if err := writeAtomic(metaPathOf(snapshotPath), meta); err != nil {
		return err
	}
	t.log.Printf("exported %d secret(s) (%d live) to %s", len(secrets), liveCount, filepath.Base(snapshotPath))
	t.rotate()
	return nil
}

type versionKey struct {
	arn, versionID string
	hasVersion     bool
}

func keyOf(entry Entry) versionKey {
	if entry.VersionID == nil {
		return versionKey{arn: entry.ARN}
	}
	return versionKey{arn: entry.ARN, versionID: *entry.VersionID, hasVersion: true}
}

// checkVersionInvariant: a Secrets Manager version is immutable, so the same ARN
// and VersionId must hold the same value as in the previous snapshot.
func checkVersionInvariant(live, previous []Entry) error {
	known := make(map[versionKey]Entry, len(previous))
	for _, entry := range previous {
		known[keyOf(entry)] = entry
	}
	var mismatched []string
	for _, entry := range live {
		if before, ok := known[keyOf(entry)]; ok && !before.SameMaterial(entry) {
			mismatched = append(mismatched, entry.Name)
		}
	}
	if len(mismatched) == 0 {
		return nil
	}
	return safeErrorf("refusing export: %d secret(s) changed value under the same VersionId (collector mismatch?), e.g. %s",
		len(mismatched), pythonList(firstN(mismatched, carriedExampleCount)))
}

func (t *Tool) mergeWithPrevious(live, previous []Entry) []Entry {
	liveNames := make(map[string]struct{}, len(live))
	for _, entry := range live {
		liveNames[entry.Name] = struct{}{}
	}
	var carried []Entry
	for _, entry := range previous {
		if _, ok := liveNames[entry.Name]; !ok {
			carried = append(carried, entry)
		}
	}
	if len(carried) == 0 || t.cfg.Force {
		return live
	}
	names := make([]string, 0, carriedExampleCount)
	for _, entry := range firstNEntries(carried, carriedExampleCount) {
		names = append(names, entry.Name)
	}
	t.log.Printf("%d secret(s) missing live are kept from the previous snapshot, e.g. %s", len(carried), pythonList(names))
	return sortedByName(append(append([]Entry(nil), live...), carried...))
}

func firstN(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func firstNEntries(values []Entry, limit int) []Entry {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

// pythonList renders names like Python's repr of a list of str, as the old log did.
func pythonList(names []string) string {
	quoted := "["
	for index, name := range names {
		if index > 0 {
			quoted += ", "
		}
		quoted += "'" + name + "'"
	}
	return quoted + "]"
}
