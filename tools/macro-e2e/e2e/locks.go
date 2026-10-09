package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/macrowallets/waas/tools/internal/pyjson"
)

const (
	RecordingLockStaleAfter = 300 * time.Second
	recordingHolderPreview  = 300
	claimFilePrefix         = "send-"
	claimFileSuffix         = ".claim"
	flockRetryInterval      = 250 * time.Millisecond
	isoSecondsLayout        = "2006-01-02T15:04:05+00:00"
)

// NowISO is Python's datetime.now(timezone.utc).isoformat(timespec="seconds").
func NowISO(now time.Time) string {
	return now.UTC().Format(isoSecondsLayout)
}

// RecordingLock is the custody e2e recording lock (<state dir>/custody-e2e-recording.lock),
// shared with the Markets recordings. Nothing may send or restart while someone else holds it.
type RecordingLock struct {
	Path     string
	PID      int
	Hostname string
	Now      func() time.Time
}

// Acquire creates the lock exclusively as owner.
func (lock RecordingLock) Acquire(owner string) error {
	started := NowISO(lock.Now())
	payload, err := pyjson.Dumps(pyjson.Object{
		{Key: "owner", Value: owner},
		{Key: "pid", Value: lock.PID},
		{Key: "host", Value: lock.Hostname},
		{Key: "startedAt", Value: started},
		{Key: "heartbeatAt", Value: started},
	}, pyjson.Default)
	if err != nil {
		return err
	}
	handle, err := os.OpenFile(lock.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, PrivateFileMode)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("recording lock is held, not sending: %s", lock.holderPreview())
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", lock.Path, err)
	}
	_, writeErr := handle.WriteString(payload)
	if err := errors.Join(writeErr, handle.Close()); err != nil {
		return fmt.Errorf("write %s: %w", lock.Path, err)
	}
	return nil
}

// RequireHeldBy accepts only a lock owned by owner with a heartbeat at most 300 s old
// (a recording that sends while it records).
func (lock RecordingLock) RequireHeldBy(owner string) error {
	raw, err := os.ReadFile(lock.Path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("--recording-lock-held-by %s: nobody holds %s; not sending", owner, lock.Path)
	}
	if err != nil {
		return fmt.Errorf("%s is unreadable; not sending", lock.Path)
	}
	held, err := decodeObject(raw)
	if err != nil {
		return fmt.Errorf("%s is unreadable; not sending", lock.Path)
	}
	heldOwner, _ := held.Get("owner")
	if heldOwnerText, isString := heldOwner.(string); !isString || heldOwnerText != owner {
		return fmt.Errorf("recording lock is held by %s, not %s; not sending", pythonValueRepr(heldOwner), pythonRepr(owner))
	}
	heartbeat, err := parseISOTimestamp(held.String("heartbeatAt"))
	if err != nil {
		return fmt.Errorf("recording lock of %s has an unreadable heartbeat; not sending", owner)
	}
	if age := lock.Now().Sub(heartbeat); age > RecordingLockStaleAfter {
		return fmt.Errorf("recording lock of %s has a %d s old heartbeat; not sending", owner, int(age.Seconds()))
	}
	return nil
}

// Release removes the lock only when owner still holds it.
func (lock RecordingLock) Release(owner string) {
	raw, err := os.ReadFile(lock.Path)
	if err != nil {
		return
	}
	held, err := decodeObject(raw)
	if err != nil || held.String("owner") != owner {
		return
	}
	_ = os.Remove(lock.Path)
}

// RefuseWhileHeld fails when anyone holds the recording lock.
func (lock RecordingLock) RefuseWhileHeld(action string) error {
	if _, err := os.Lstat(lock.Path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("%s holds the recording lock; not %s during a recording: %s", lock.Path, action, lock.holderPreview())
}

func (lock RecordingLock) holderPreview() string {
	raw, err := os.ReadFile(lock.Path)
	if err != nil {
		return ""
	}
	return truncateRunes(strings.ToValidUTF8(string(raw), "\uFFFD"), recordingHolderPreview)
}

func parseISOTimestamp(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	return time.Parse(time.RFC3339Nano, value)
}

// ClaimTransfer creates <locks>/send-<tag>.claim exclusively (0600) with the plan and
// claimedAt. The claim is never removed: a second run with the same tag refuses until
// a human reviews it.
func ClaimTransfer(locksDir, tag string, plan pyjson.Object, now time.Time) (string, error) {
	if err := EnsurePrivateDir(locksDir); err != nil {
		return "", err
	}
	claim := filepath.Join(locksDir, claimFilePrefix+tag+claimFileSuffix)
	content, err := pyjson.DumpsIndent(plan.Set("claimedAt", NowISO(now)), 2)
	if err != nil {
		return "", err
	}
	handle, err := os.OpenFile(claim, os.O_WRONLY|os.O_CREATE|os.O_EXCL, PrivateFileMode)
	if errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("%s exists: this transfer was already attempted; review it before anything else", claim)
	}
	if err != nil {
		return "", fmt.Errorf("create %s: %w", claim, err)
	}
	_, writeErr := handle.WriteString(content)
	if err := errors.Join(writeErr, handle.Close()); err != nil {
		return "", fmt.Errorf("write %s: %w", claim, err)
	}
	return claim, nil
}

// FileLock is an exclusive flock(2) on a lock file (same semantics as flock(1)).
type FileLock struct{ handle *os.File }

// AcquireFileLock waits up to wait for the exclusive lock on path.
func AcquireFileLock(ctx context.Context, path string, wait time.Duration) (*FileLock, error) {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	handle, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, PrivateFileMode)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &FileLock{handle: handle}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = handle.Close()
			return nil, fmt.Errorf("flock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			_ = handle.Close()
			return nil, fmt.Errorf("%s is held by another process after %s (macro-e2e takes this lock itself; do not wrap it in flock)", path, wait)
		}
		select {
		case <-ctx.Done():
			_ = handle.Close()
			return nil, ctx.Err()
		case <-time.After(flockRetryInterval):
		}
	}
}

// Release unlocks and closes the lock file (the file itself stays).
func (lock *FileLock) Release() {
	if lock == nil || lock.handle == nil {
		return
	}
	_ = syscall.Flock(int(lock.handle.Fd()), syscall.LOCK_UN)
	_ = lock.handle.Close()
	lock.handle = nil
}
