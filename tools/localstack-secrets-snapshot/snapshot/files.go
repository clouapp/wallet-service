package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/macrowallets/waas/tools/internal/pyjson"
)

const (
	SnapshotPrefix = "secrets-"
	SnapshotSuffix = ".json.gpg"
	MetaSuffix     = ".meta.json"
	exportLockName = ".export.lock"
	tempFilePrefix = ".tmp-"

	privateDirMode  os.FileMode = 0o700
	privateFileMode os.FileMode = 0o600

	recordSchemaVersion = 1
	stampMicroDivisor   = 1000
)

// Meta is the plaintext sidecar of a snapshot: enough to detect changes without
// decrypting anything.
type Meta struct {
	Count     int    `json:"count"`
	LiveCount int    `json:"live_count"`
	Digest    string `json:"digest"`
	CreatedAt string `json:"created_at"`
}

// snapshotFile is an encrypted snapshot whose meta file was readable.
type snapshotFile struct {
	Path string
	Meta Meta
}

func (f snapshotFile) Name() string {
	return filepath.Base(f.Path)
}

// loadedSnapshot is a decrypted snapshot that matched its meta.
type loadedSnapshot struct {
	Name    string
	Meta    Meta
	Secrets []Entry
}

// Stamp formats t like Python's strftime("%Y%m%dT%H%M%S%fZ") in UTC.
func Stamp(t time.Time) string {
	utc := t.UTC()
	return fmt.Sprintf("%s%06dZ", utc.Format("20060102T150405"), utc.Nanosecond()/stampMicroDivisor)
}

func (t *Tool) snapshotPathFor(stamp string) string {
	return filepath.Join(t.cfg.Dir, SnapshotPrefix+stamp+SnapshotSuffix)
}

func metaPathOf(snapshotPath string) string {
	return strings.TrimSuffix(snapshotPath, SnapshotSuffix) + MetaSuffix
}

// snapshotMetas lists snapshots newest first, skipping metas without a snapshot
// and logging (by name only) metas that cannot be parsed.
func (t *Tool) snapshotMetas() ([]snapshotFile, error) {
	metaPaths, err := filepath.Glob(filepath.Join(t.cfg.Dir, SnapshotPrefix+"*"+MetaSuffix))
	if err != nil {
		return nil, safeErrorf("list snapshot metas: %v", err)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(metaPaths)))
	found := make([]snapshotFile, 0, len(metaPaths))
	for _, metaPath := range metaPaths {
		snapshotPath := strings.TrimSuffix(metaPath, MetaSuffix) + SnapshotSuffix
		if _, err := os.Stat(snapshotPath); err != nil {
			continue
		}
		raw, err := os.ReadFile(metaPath)
		var meta Meta
		if err == nil {
			err = json.Unmarshal(raw, &meta)
		}
		if err != nil {
			t.log.Printf("ignoring unreadable meta %s", filepath.Base(metaPath))
			continue
		}
		found = append(found, snapshotFile{Path: snapshotPath, Meta: meta})
	}
	return found, nil
}

func (t *Tool) loadSnapshot(ctx context.Context, file snapshotFile) ([]Entry, error) {
	plaintext, err := t.crypter.Decrypt(ctx, file.Path)
	if err != nil {
		return nil, err
	}
	defer zero(plaintext)
	var decoded struct {
		Secrets *[]Entry `json:"secrets"`
	}
	if err := json.Unmarshal(plaintext, &decoded); err != nil {
		return nil, safeErrorf("%s does not hold JSON", file.Name())
	}
	if decoded.Secrets == nil {
		return nil, safeErrorf("%s has no secrets list", file.Name())
	}
	secrets := *decoded.Secrets
	digest, err := Digest(secrets)
	if err != nil {
		return nil, err
	}
	if len(secrets) != file.Meta.Count || digest != file.Meta.Digest {
		return nil, safeErrorf("%s does not match its meta (count/digest)", file.Name())
	}
	return secrets, nil
}

// newestReadable returns nil when there is no snapshot at all, and an error when
// snapshots exist but none can be decrypted and verified.
func (t *Tool) newestReadable(ctx context.Context) (*loadedSnapshot, error) {
	metas, err := t.snapshotMetas()
	if err != nil {
		return nil, err
	}
	if len(metas) == 0 {
		return nil, nil
	}
	for _, file := range metas {
		secrets, err := t.loadSnapshot(ctx, file)
		if err != nil {
			t.log.Printf("skipping snapshot %s: %s", file.Name(), safeMessage(err))
			continue
		}
		return &loadedSnapshot{Name: file.Name(), Meta: file.Meta, Secrets: secrets}, nil
	}
	return nil, safeErrorf("none of the %d snapshot(s) could be read", len(metas))
}

// writeAtomic writes data to path through a 0600 temp file, fsyncs it, renames it
// over path and fsyncs the directory.
func writeAtomic(path string, data []byte) (err error) {
	temp, err := os.CreateTemp(filepath.Dir(path), tempFilePrefix)
	if err != nil {
		return safeErrorf("create temp file: %v", err)
	}
	tempPath := temp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return safeErrorf("write %s: %v", filepath.Base(path), err)
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return safeErrorf("fsync %s: %v", filepath.Base(path), err)
	}
	if err = temp.Close(); err != nil {
		return safeErrorf("close %s: %v", filepath.Base(path), err)
	}
	if err = os.Chmod(tempPath, privateFileMode); err != nil {
		return safeErrorf("chmod %s: %v", filepath.Base(path), err)
	}
	if err = os.Rename(tempPath, path); err != nil {
		return safeErrorf("rename %s: %v", filepath.Base(path), err)
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return safeErrorf("open dir: %v", err)
	}
	defer handle.Close()
	if err := handle.Sync(); err != nil {
		return safeErrorf("fsync dir: %v", err)
	}
	return nil
}

// rotate keeps the newest Keep snapshots (and their metas).
func (t *Tool) rotate() {
	snapshots, err := filepath.Glob(filepath.Join(t.cfg.Dir, SnapshotPrefix+"*"+SnapshotSuffix))
	if err != nil {
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(snapshots)))
	if len(snapshots) <= t.cfg.Keep {
		return
	}
	for _, snapshotPath := range snapshots[t.cfg.Keep:] {
		_ = os.Remove(snapshotPath)
		_ = os.Remove(metaPathOf(snapshotPath))
	}
}

func encodeRecord(accountID, region string, secrets []Entry) ([]byte, error) {
	items := make([]any, len(secrets))
	for index, entry := range secrets {
		items[index] = entry.pythonObject()
	}
	encoded, err := pyjson.Dumps(pyjson.Object{
		{Key: "schema_version", Value: recordSchemaVersion},
		{Key: "account_id", Value: accountID},
		{Key: "region", Value: region},
		{Key: "secrets", Value: items},
	}, pyjson.Default)
	return []byte(encoded), err
}

func encodeMeta(meta Meta) ([]byte, error) {
	encoded, err := pyjson.Dumps(pyjson.Object{
		{Key: "count", Value: meta.Count},
		{Key: "live_count", Value: meta.LiveCount},
		{Key: "digest", Value: meta.Digest},
		{Key: "created_at", Value: meta.CreatedAt},
	}, pyjson.Default)
	return []byte(encoded), err
}

func zero(buffer []byte) {
	for index := range buffer {
		buffer[index] = 0
	}
}
