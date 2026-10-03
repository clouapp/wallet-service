package snapshot

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
)

// Exit codes of the CLI, unchanged from the Python tool.
const (
	ExitOK      = 0
	ExitFailed  = 1
	ExitUsage   = 2
	ExitRefused = 3

	logStampLayout     = "2006-01-02T15:04:05Z"
	logPrefix          = "secrets-snapshot"
	maxErrorLogRunes   = 200
	procStatFieldAfter = 19
)

// Logger writes one line per call; only names and counts are ever logged.
type Logger interface {
	Printf(format string, args ...any)
}

// StampedLogger prefixes each line like the Python tool: "<UTC> secrets-snapshot: ...".
type StampedLogger struct {
	Out io.Writer
	Now func() time.Time
}

func (l StampedLogger) Printf(format string, args ...any) {
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	fmt.Fprintf(l.Out, "%s %s: %s\n", now().UTC().Format(logStampLayout), logPrefix, fmt.Sprintf(format, args...))
}

// Tool runs export, restore, verify and loop against one LocalStack.
type Tool struct {
	cfg     Config
	secrets SecretsAPI
	crypter Crypter
	log     Logger
	now     func() time.Time
}

// Dependencies of a Tool; Now defaults to time.Now.
type Dependencies struct {
	Secrets SecretsAPI
	Crypter Crypter
	Log     Logger
	Now     func() time.Time
}

// NewTool validates its dependencies.
func NewTool(cfg Config, deps Dependencies) (*Tool, error) {
	if deps.Secrets == nil || deps.Crypter == nil || deps.Log == nil {
		return nil, errors.New("snapshot tool needs secrets, crypter and logger")
	}
	if cfg.Dir == "" || cfg.KeyFile == "" || cfg.RestoredMarker == "" || cfg.ProcStat == "" || cfg.LoopLock == "" {
		return nil, errors.New("snapshot tool config is incomplete")
	}
	if cfg.Keep < minKeep || cfg.Interval <= 0 {
		return nil, errors.New("snapshot tool needs keep >= 1 and a positive interval")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Tool{cfg: cfg, secrets: deps.Secrets, crypter: deps.Crypter, log: deps.Log, now: now}, nil
}

// bootID is the start time of PID 1, which changes on every container (re)start.
func (t *Tool) bootID() (string, error) {
	raw, err := os.ReadFile(t.cfg.ProcStat)
	if err != nil {
		return "", safeErrorf("read %s: %v", t.cfg.ProcStat, err)
	}
	text := string(raw)
	closing := strings.LastIndex(text, ")")
	if closing < 0 {
		return "", safeErrorf("unexpected %s layout", t.cfg.ProcStat)
	}
	fields := strings.Fields(text[closing+1:])
	if len(fields) <= procStatFieldAfter {
		return "", safeErrorf("unexpected %s layout", t.cfg.ProcStat)
	}
	return fields[procStatFieldAfter], nil
}

func (t *Tool) restoredInThisRun() bool {
	marker, err := os.ReadFile(t.cfg.RestoredMarker)
	if err != nil {
		return false
	}
	id, err := t.bootID()
	return err == nil && strings.TrimSpace(string(marker)) == id
}

func (t *Tool) markRestored() error {
	id, err := t.bootID()
	if err != nil {
		return err
	}
	if err := os.WriteFile(t.cfg.RestoredMarker, []byte(id), privateFileMode); err != nil {
		return safeErrorf("write restore marker: %v", err)
	}
	return nil
}

// lockFile opens path and takes an exclusive flock; blocking selects LOCK_EX vs
// LOCK_EX|LOCK_NB. The lock lives until the returned file is closed.
func lockFile(path string, blocking bool) (*os.File, bool, error) {
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, privateFileMode)
	if err != nil {
		return nil, false, safeErrorf("open lock %s: %v", path, err)
	}
	how := syscall.LOCK_EX
	if !blocking {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(handle.Fd()), how); err != nil {
		handle.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, safeErrorf("lock %s: %v", path, err)
	}
	return handle, true, nil
}

// safeMessage renders err for the log without leaking anything but codes and
// names: AWS API errors become their code, other errors are truncated.
func safeMessage(err error) string {
	if err == nil {
		return ""
	}
	var safe *SafeError
	if errors.As(err, &safe) {
		return safe.Error()
	}
	if code := awsErrorCode(err); code != "" {
		return "AWS error " + code
	}
	return truncateRunes(err.Error(), maxErrorLogRunes)
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
