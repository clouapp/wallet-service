package account

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"
)

const frontendURLEnv = "APP_FRONTEND_URL"

var (
	ErrInviteTokenRequired = errors.New("invite token is required")
	ErrFrontendURLRequired = errors.New("the front-end URL is required: set platform_urls.frontend_url or APP_FRONTEND_URL")
)

// FrontendURLSource reads the stored front-end base (the platform_urls setting,
// settings.Service); "" means none is stored.
type FrontendURLSource interface {
	FrontendURL(ctx context.Context) (string, error)
}

// FrontendBase is the invite link base: the platform_urls.frontend_url setting
// when source stores one, else APP_FRONTEND_URL. A failed settings read keeps
// APP_FRONTEND_URL. Neither set is ErrFrontendURLRequired.
func FrontendBase(ctx context.Context, source FrontendURLSource) (string, error) {
	if source != nil && ctx != nil {
		if stored, err := source.FrontendURL(ctx); err == nil && strings.TrimSpace(stored) != "" {
			return strings.TrimSpace(stored), nil
		}
	}
	value := strings.TrimSpace(os.Getenv(frontendURLEnv))
	if value == "" {
		return "", ErrFrontendURLRequired
	}
	return value, nil
}

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
