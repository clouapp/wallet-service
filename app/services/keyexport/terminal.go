package keyexport

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"golang.org/x/term"
)

const (
	controllingTerminalPath = "/dev/tty"
	maxTerminalLineBytes    = 4096
)

// ErrNoTerminal: secrets are only ever typed on the controlling terminal.
var ErrNoTerminal = errors.New("no controlling terminal: this command must be run interactively (passwords are never read from flags, environment or pipes)")

// Terminal is where the operator types secrets (no echo) and confirmations.
type Terminal interface {
	ReadSecret(prompt string) ([]byte, error)
	ReadLine(prompt string) (string, error)
	Notify(message string)
}

// TTY is the controlling terminal (/dev/tty), independent of stdin/stdout, so a
// redirected stdin can never feed a password.
type TTY struct {
	mu    sync.Mutex
	file  *os.File
	fd    int
	state *term.State
}

// OpenTTY opens the controlling terminal and records its state so Restore can undo
// a no-echo read cut short by a signal.
func OpenTTY() (*TTY, error) {
	return openTTYAt(controllingTerminalPath)
}

func openTTYAt(path string) (*TTY, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, ErrNoTerminal
	}
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		_ = file.Close()
		return nil, ErrNoTerminal
	}
	state, err := term.GetState(fd)
	if err != nil {
		_ = file.Close()
		return nil, ErrNoTerminal
	}
	return &TTY{file: file, fd: fd, state: state}, nil
}

func (t *TTY) ReadSecret(prompt string) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := fmt.Fprint(t.file, prompt); err != nil {
		return nil, fmt.Errorf("%w: write prompt: %v", ErrAborted, err)
	}
	secret, err := term.ReadPassword(t.fd)
	_, _ = fmt.Fprint(t.file, "\r\n")
	if err != nil {
		zeroBytes(secret)
		return nil, fmt.Errorf("%w: read from terminal: %v", ErrAborted, err)
	}
	return secret, nil
}

func (t *TTY) ReadLine(prompt string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := fmt.Fprint(t.file, prompt); err != nil {
		return "", fmt.Errorf("%w: write prompt: %v", ErrAborted, err)
	}
	line := make([]byte, 0, 64)
	one := make([]byte, 1)
	for len(line) < maxTerminalLineBytes {
		n, err := t.file.Read(one)
		if err != nil {
			return "", fmt.Errorf("%w: read from terminal: %v", ErrAborted, err)
		}
		if n == 0 {
			continue
		}
		if one[0] == '\n' {
			return string(trimCarriageReturn(line)), nil
		}
		line = append(line, one[0])
	}
	return "", fmt.Errorf("%w: terminal line longer than %d bytes", ErrAborted, maxTerminalLineBytes)
}

func (t *TTY) Notify(message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = fmt.Fprintln(t.file, message)
}

// Restore puts the terminal back as it was opened (echo on). Safe to call from a
// signal handler while a read is blocked.
func (t *TTY) Restore() {
	if t == nil || t.state == nil {
		return
	}
	_ = term.Restore(t.fd, t.state)
}

func (t *TTY) Close() error {
	if t == nil {
		return nil
	}
	t.Restore()
	return t.file.Close()
}

func trimCarriageReturn(line []byte) []byte {
	if n := len(line); n > 0 && line[n-1] == '\r' {
		return line[:n-1]
	}
	return line
}
