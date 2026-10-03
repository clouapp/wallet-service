package pending

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const (
	fileOpPut    = "put"
	fileOpDelete = "delete"

	// compactAfterRecords rewrites a chain's log with only its live entries once it
	// holds this many records, so resolved entries do not pile up forever.
	compactAfterRecords = 1000

	dirPermissions  = 0o700
	filePermissions = 0o600
)

type fileRecord struct {
	Op    string `json:"op"`
	Block uint64 `json:"block"`
	Entry *Entry `json:"entry,omitempty"`
}

// FileStore keeps one append-only JSON-lines log per chain (<dir>/<chain>.jsonl).
// Every write is fsynced before it returns, and an flock on <chain>.lock serializes
// processes sharing the directory (the API and an artisan command).
type FileStore struct {
	dir          string
	compactAfter int
	mu           sync.Mutex
}

func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, errors.New("pending file store: directory is required")
	}
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return nil, fmt.Errorf("pending file store: create %s: %w", dir, err)
	}
	return &FileStore{dir: dir, compactAfter: compactAfterRecords}, nil
}

func (s *FileStore) logPath(chain string) string  { return filepath.Join(s.dir, chain+".jsonl") }
func (s *FileStore) lockPath(chain string) string { return filepath.Join(s.dir, chain+".lock") }

func (s *FileStore) Put(_ context.Context, entry Entry) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	return s.withLock(entry.Chain, func() error {
		return s.appendAndMaybeCompact(entry.Chain, fileRecord{Op: fileOpPut, Block: entry.Block, Entry: &entry})
	})
}

func (s *FileStore) Delete(_ context.Context, chain string, block uint64) error {
	return s.withLock(chain, func() error {
		return s.appendAndMaybeCompact(chain, fileRecord{Op: fileOpDelete, Block: block})
	})
}

func (s *FileStore) List(_ context.Context, chain string) ([]Entry, error) {
	var entries []Entry
	err := s.withLock(chain, func() error {
		live, _, err := s.replay(chain)
		if err != nil {
			return err
		}
		entries = liveEntries(live)
		return nil
	})
	return entries, err
}

func (s *FileStore) withLock(chain string, run func() error) error {
	if chain == "" {
		return fmt.Errorf("%w: chain is required", ErrInvalidEntry)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := os.OpenFile(s.lockPath(chain), os.O_CREATE|os.O_RDWR, filePermissions)
	if err != nil {
		return fmt.Errorf("pending file store: open lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("pending file store: lock %s: %w", chain, err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return run()
}

func (s *FileStore) appendAndMaybeCompact(chain string, record fileRecord) error {
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("pending file store: encode: %w", err)
	}
	if err := dropTornTail(s.logPath(chain)); err != nil {
		return err
	}
	file, err := os.OpenFile(s.logPath(chain), os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePermissions)
	if err != nil {
		return fmt.Errorf("pending file store: open log: %w", err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("pending file store: append: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("pending file store: fsync: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("pending file store: close: %w", err)
	}

	live, records, err := s.replay(chain)
	if err != nil || records < s.compactAfter {
		return err
	}
	return s.compact(chain, liveEntries(live))
}

// dropTornTail truncates an unterminated last line, left by a crash in the middle of
// an append whose caller never saw it succeed, so the next record starts on its own line.
func dropTornTail(path string) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("pending file store: read log: %w", err)
	}
	if len(content) == 0 || bytes.HasSuffix(content, []byte("\n")) {
		return nil
	}
	keep := bytes.LastIndexByte(content, '\n') + 1
	if err := os.Truncate(path, int64(keep)); err != nil {
		return fmt.Errorf("pending file store: drop torn record: %w", err)
	}
	return nil
}

// replay rebuilds the live entries from the log. A torn last line (a crash in the
// middle of an append) is ignored; any other unreadable line fails the read.
func (s *FileStore) replay(chain string) (map[uint64]Entry, int, error) {
	content, err := os.ReadFile(s.logPath(chain))
	if errors.Is(err, os.ErrNotExist) {
		return map[uint64]Entry{}, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("pending file store: read log: %w", err)
	}
	live := map[uint64]Entry{}
	records := 0
	lines := bytes.Split(content, []byte("\n"))
	for i, raw := range lines {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var record fileRecord
		if err := json.Unmarshal(line, &record); err != nil {
			isUnterminatedLastLine := i == len(lines)-1
			if isUnterminatedLastLine {
				break
			}
			return nil, 0, fmt.Errorf("pending file store: corrupt record in %s: %w", s.logPath(chain), err)
		}
		records++
		switch record.Op {
		case fileOpPut:
			if record.Entry == nil {
				return nil, 0, fmt.Errorf("pending file store: put without entry in %s", s.logPath(chain))
			}
			live[record.Block] = *record.Entry
		case fileOpDelete:
			delete(live, record.Block)
		default:
			return nil, 0, fmt.Errorf("pending file store: unknown op %q in %s", record.Op, s.logPath(chain))
		}
	}
	return live, records, nil
}

// compact atomically replaces the log with one put per live entry.
func (s *FileStore) compact(chain string, entries []Entry) error {
	var buffer bytes.Buffer
	for i := range entries {
		line, err := json.Marshal(fileRecord{Op: fileOpPut, Block: entries[i].Block, Entry: &entries[i]})
		if err != nil {
			return fmt.Errorf("pending file store: encode: %w", err)
		}
		buffer.Write(line)
		buffer.WriteByte('\n')
	}
	temp, err := os.CreateTemp(s.dir, chain+".jsonl.compact-*")
	if err != nil {
		return fmt.Errorf("pending file store: compact: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(buffer.Bytes()); err != nil {
		_ = temp.Close()
		return fmt.Errorf("pending file store: compact write: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("pending file store: compact fsync: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("pending file store: compact close: %w", err)
	}
	if err := os.Chmod(tempPath, filePermissions); err != nil {
		return fmt.Errorf("pending file store: compact chmod: %w", err)
	}
	return os.Rename(tempPath, s.logPath(chain))
}

func liveEntries(live map[uint64]Entry) []Entry {
	entries := make([]Entry, 0, len(live))
	for _, entry := range live {
		entries = append(entries, entry)
	}
	sortByBlock(entries)
	return entries
}
