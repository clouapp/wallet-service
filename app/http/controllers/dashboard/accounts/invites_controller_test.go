package accounts

import (
	"strings"
	"testing"
)

func TestFrontendBaseURLUsesOnlyTheEnv(t *testing.T) {
	t.Setenv(frontendURLEnv, "")
	base, err := frontendBaseURL()
	if err == nil || base != "" {
		t.Fatal("missing APP_FRONTEND_URL was accepted")
	}
	if strings.Contains(base, "localhost") || strings.Contains(base, "vault.app") || strings.Contains(err.Error(), "http") {
		t.Fatal("missing APP_FRONTEND_URL produced a host")
	}

	t.Setenv(frontendURLEnv, "   ")
	if _, err := frontendBaseURL(); err == nil {
		t.Fatal("blank APP_FRONTEND_URL was accepted")
	}

	t.Setenv(frontendURLEnv, "  https://wallet.example/app  ")
	base, err = frontendBaseURL()
	if err != nil || base != "https://wallet.example/app" {
		t.Fatal("APP_FRONTEND_URL was not the invite link base")
	}
}
