package seeds

import (
	"os"

	"github.com/goravel/framework/facades"
)

const placeholderRPCURL = "https://placeholder.invalid"

func encryptRPCFromEnv(envKey string) (string, error) {
	raw := os.Getenv(envKey)
	if raw == "" {
		raw = placeholderRPCURL
	}
	return facades.Crypt().EncryptString(raw)
}

func i64p(v int64) *int64   { return &v }
func strp(s string) *string { return &s }
