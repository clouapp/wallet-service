package keyexport

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"
)

const (
	// MinArchivePasswordLength counts characters (runes). WinZip AES derives its key
	// with only 1000 PBKDF2-SHA1 rounds, so the password itself must be strong.
	MinArchivePasswordLength        = 16
	MaxArchivePasswordLength        = 1024
	MinArchivePasswordDistinctBytes = 8
	maxArchivePasswordAttempts      = 3

	archivePasswordPrompt        = "Senha do zip (mín. %d caracteres, não aparece na tela): "
	archivePasswordConfirmPrompt = "Repita a senha do zip: "
)

var (
	ErrArchivePasswordMismatch = errors.New("the two passwords differ")
	ErrArchivePasswordShort    = fmt.Errorf("the archive password must have at least %d characters", MinArchivePasswordLength)
	ErrArchivePasswordLong     = fmt.Errorf("the archive password must have at most %d characters", MaxArchivePasswordLength)
	ErrArchivePasswordWeak     = fmt.Errorf("the archive password is weak: use at least %d different characters", MinArchivePasswordDistinctBytes)
	ErrArchivePasswordEncoding = errors.New("the archive password must be valid UTF-8 without control characters")
)

// ValidateArchivePassword accepts a password typed twice identically that is long
// enough, not a run of a few repeated characters, and printable.
func ValidateArchivePassword(first, second []byte) error {
	if len(first) != len(second) || subtle.ConstantTimeCompare(first, second) != 1 {
		return ErrArchivePasswordMismatch
	}
	if !utf8.Valid(first) {
		return ErrArchivePasswordEncoding
	}
	for _, r := range string(first) {
		if unicode.IsControl(r) {
			return ErrArchivePasswordEncoding
		}
	}
	length := utf8.RuneCount(first)
	if length < MinArchivePasswordLength {
		return ErrArchivePasswordShort
	}
	if length > MaxArchivePasswordLength {
		return ErrArchivePasswordLong
	}
	if distinctBytes(first) < MinArchivePasswordDistinctBytes {
		return ErrArchivePasswordWeak
	}
	return nil
}

func distinctBytes(value []byte) int {
	var seen [256]bool
	count := 0
	for _, b := range value {
		if !seen[b] {
			seen[b] = true
			count++
		}
	}
	for i := range seen {
		seen[i] = false
	}
	return count
}

// ReadArchivePassword asks twice on the terminal, without echo, until a valid
// password is typed or the attempts run out. The caller zeroes the result.
func ReadArchivePassword(terminal Terminal) ([]byte, error) {
	if terminal == nil {
		return nil, ErrNoTerminal
	}
	var lastErr error
	for attempt := 1; attempt <= maxArchivePasswordAttempts; attempt++ {
		first, err := terminal.ReadSecret(fmt.Sprintf(archivePasswordPrompt, MinArchivePasswordLength))
		if err != nil {
			return nil, err
		}
		second, err := terminal.ReadSecret(archivePasswordConfirmPrompt)
		if err != nil {
			zeroBytes(first)
			return nil, err
		}
		lastErr = ValidateArchivePassword(first, second)
		zeroBytes(second)
		if lastErr == nil {
			return first, nil
		}
		zeroBytes(first)
		terminal.Notify(fmt.Sprintf("Senha recusada: %v (tentativa %d de %d)", lastErr, attempt, maxArchivePasswordAttempts))
	}
	return nil, fmt.Errorf("archive password refused: %w", lastErr)
}
