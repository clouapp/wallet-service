package seeds

import (
	"github.com/goravel/framework/facades"
	"github.com/spf13/cast"
)

const placeholderRPCURL = "https://placeholder.invalid"

func encryptRPCFromEnv(envKey string) (string, error) {
	raw := cast.ToString(facades.Config().Env(envKey, ""))
	if raw == "" {
		raw = placeholderRPCURL
	}
	return facades.Crypt().EncryptString(raw)
}

func i64p(v int64) *int64   { return &v }
func strp(s string) *string { return &s }
