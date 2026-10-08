package evmcall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	claimDirMode  = 0o700
	claimFileMode = 0o600
	claimPrefix   = "evm-call-"
	claimSuffix   = ".claim"
	resultSuffix  = ".result.json"
)

// ErrAlreadyClaimed means a broadcast with this tag was already attempted.
var ErrAlreadyClaimed = errors.New("this tag was already claimed for a broadcast; review it before anything else")

// Claimer guarantees at most one broadcast per tag.
type Claimer interface {
	// Claim records details under tag, failing with ErrAlreadyClaimed when the tag exists.
	Claim(tag string, details []byte) (string, error)
	// Record stores the outcome of the claimed broadcast next to its claim.
	Record(tag string, outcome []byte) (string, error)
}

// FileClaimer creates <dir>/evm-call-<tag>.claim exclusively and never removes it,
// so a second broadcast with the same tag refuses until a human reviews it.
type FileClaimer struct {
	Dir string
}

func (c FileClaimer) Claim(tag string, details []byte) (string, error) {
	if err := c.ensureDir(); err != nil {
		return "", err
	}
	path := filepath.Join(c.Dir, claimPrefix+tag+claimSuffix)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, claimFileMode)
	if errors.Is(err, os.ErrExist) {
		return path, fmt.Errorf("%w: %s", ErrAlreadyClaimed, path)
	}
	if err != nil {
		return "", fmt.Errorf("create claim %s: %w", path, err)
	}
	defer file.Close()
	if _, err := file.Write(details); err != nil {
		return path, fmt.Errorf("write claim %s: %w", path, err)
	}
	return path, nil
}

func (c FileClaimer) Record(tag string, outcome []byte) (string, error) {
	if err := c.ensureDir(); err != nil {
		return "", err
	}
	path := filepath.Join(c.Dir, claimPrefix+tag+resultSuffix)
	partial := path + ".partial"
	if err := os.WriteFile(partial, outcome, claimFileMode); err != nil {
		return "", fmt.Errorf("write result %s: %w", path, err)
	}
	if err := os.Rename(partial, path); err != nil {
		return "", fmt.Errorf("install result %s: %w", path, err)
	}
	return path, nil
}

func (c FileClaimer) ensureDir() error {
	if c.Dir == "" {
		return fmt.Errorf("claim directory is required")
	}
	if err := os.MkdirAll(c.Dir, claimDirMode); err != nil {
		return fmt.Errorf("create claim directory: %w", err)
	}
	return nil
}
