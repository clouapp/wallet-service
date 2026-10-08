package config

import (
	"strings"
	"testing"
)

const goodKey = "0123456789abcdef0123456789abcdef"
const goodJWT = "a-jwt-signing-secret-of-32-chars!"

func TestValidate_Secrets(t *testing.T) {
	tests := []struct {
		name         string
		appKey       string
		jwtSecret    string
		wantErr      string
		wantAlso     string
		wantWarnings int
	}{
		{name: "both set", appKey: goodKey, jwtSecret: goodJWT},
		{name: "empty JWT secret", appKey: goodKey, jwtSecret: "", wantErr: "JWT_SECRET is empty"},
		{name: "blank JWT secret", appKey: goodKey, jwtSecret: "   ", wantErr: "JWT_SECRET is empty"},
		{name: "empty app key", appKey: "", jwtSecret: goodJWT, wantErr: "APP_KEY must be exactly 32 bytes (got 0)"},
		{name: "short app key", appKey: "0123456789abcdef", jwtSecret: goodJWT, wantErr: "APP_KEY must be exactly 32 bytes (got 16)"},
		{name: "long app key", appKey: goodKey + "0", jwtSecret: goodJWT, wantErr: "APP_KEY must be exactly 32 bytes (got 33)"},
		{name: "short JWT secret only warns", appKey: goodKey, jwtSecret: "short-secret", wantWarnings: 1},
		{name: "both bad reports both", appKey: "", jwtSecret: "", wantErr: "APP_KEY must be exactly 32 bytes (got 0)", wantAlso: "JWT_SECRET is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := ValidateSecrets(tt.appKey, tt.jwtSecret)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
			}
			if tt.wantAlso != "" && !strings.Contains(err.Error(), tt.wantAlso) {
				t.Fatalf("error = %v, want it to contain %q too", err, tt.wantAlso)
			}
			if len(warnings) != tt.wantWarnings {
				t.Fatalf("warnings = %v, want %d", warnings, tt.wantWarnings)
			}
		})
	}
}

func TestValidate_Secrets_NeverEchoesTheSecrets(t *testing.T) {
	_, err := ValidateSecrets("short-app-key-value", "")
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "short-app-key-value") {
		t.Fatalf("the error repeats the key: %v", err)
	}
}

func TestIs_KeyGenerationCommand(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"artisan", "key:generate"}, true},
		{[]string{"--env=.env.dev", "artisan", "key:generate"}, true},
		{[]string{"--env", ".env.dev", "artisan", "key:generate", "--show"}, true},
		{[]string{"--env=.env.dev", "artisan", "jwt:secret"}, true},
		{[]string{"artisan", "migrate"}, false},
		{[]string{"artisan", "list"}, false},
		{[]string{"artisan"}, false},
		{nil, false},
		{[]string{"--env=.env.dev"}, false},
		{[]string{"key:generate"}, false},
	}
	for _, tt := range tests {
		if got := IsKeyGenerationCommand(tt.args); got != tt.want {
			t.Errorf("IsKeyGenerationCommand(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}
