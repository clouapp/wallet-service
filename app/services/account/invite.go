package account

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
)

var ErrInviteTokenRequired = errors.New("invite token is required")

// InviteLink builds the accept URL. The raw token is a query parameter on the
// caller's frontend, never the hard-coded vault.app placeholder.
func InviteLink(base, rawToken string) (string, error) {
	base = strings.TrimSpace(base)
	rawToken = strings.TrimSpace(rawToken)
	if base == "" || rawToken == "" {
		return "", ErrInviteTokenRequired
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", ErrInviteTokenRequired
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/accept-invite"
	query := parsed.Query()
	query.Set("token", rawToken)
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String(), nil
}

func HashInviteToken(rawToken string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(rawToken)))
	return hex.EncodeToString(sum[:])
}

func InviteTokenMatches(storedHash, rawToken string) bool {
	want, err := hex.DecodeString(storedHash)
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := hex.DecodeString(HashInviteToken(rawToken))
	if err != nil || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
