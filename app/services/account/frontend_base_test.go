package account

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFrontend_Base_URLUsesTheEnvWithoutASetting(t *testing.T) {
	ctx := context.Background()
	t.Setenv(frontendURLEnv, "")
	base, err := FrontendBase(ctx, nil)
	if err == nil || base != "" {
		t.Fatal("missing APP_FRONTEND_URL was accepted")
	}
	if strings.Contains(base, "localhost") || strings.Contains(base, "vault.app") || strings.Contains(err.Error(), "http") {
		t.Fatal("missing APP_FRONTEND_URL produced a host")
	}

	t.Setenv(frontendURLEnv, "   ")
	if _, err := FrontendBase(ctx, nil); err == nil {
		t.Fatal("blank APP_FRONTEND_URL was accepted")
	}

	t.Setenv(frontendURLEnv, "  https://wallet.example/app  ")
	base, err = FrontendBase(ctx, nil)
	if err != nil || base != "https://wallet.example/app" {
		t.Fatal("APP_FRONTEND_URL was not the invite link base")
	}
}

type stubFrontendURLs struct {
	value string
	err   error
}

func (s stubFrontendURLs) FrontendURL(context.Context) (string, error) { return s.value, s.err }

func TestFrontend_Base_TheStoredSettingWinsAndAFailedReadKeepsTheEnv(t *testing.T) {
	ctx := context.Background()
	t.Setenv(frontendURLEnv, "https://env.example")

	base, err := FrontendBase(ctx, stubFrontendURLs{value: "https://panel.example/app"})
	if err != nil || base != "https://panel.example/app" {
		t.Fatalf("stored front-end URL was not used: %q %v", base, err)
	}
	base, err = FrontendBase(ctx, stubFrontendURLs{})
	if err != nil || base != "https://env.example" {
		t.Fatalf("no stored URL did not keep APP_FRONTEND_URL: %q %v", base, err)
	}
	base, err = FrontendBase(ctx, stubFrontendURLs{value: "https://panel.example", err: errors.New("db down")})
	if err != nil || base != "https://env.example" {
		t.Fatalf("a failed read did not keep APP_FRONTEND_URL: %q %v", base, err)
	}

	t.Setenv(frontendURLEnv, "")
	base, err = FrontendBase(ctx, stubFrontendURLs{value: "https://panel.example"})
	if err != nil || base != "https://panel.example" {
		t.Fatalf("the setting alone was not enough: %q %v", base, err)
	}
}
