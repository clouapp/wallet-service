package config

import "github.com/goravel/framework/facades"

func registerJWT() {
	facades.Config().Add("jwt", map[string]any{
		"secret":      envString("JWT_SECRET", ""),
		"ttl":         envInt("JWT_TTL", 15),
		"refresh_ttl": envInt("JWT_REFRESH_TTL", 43200),
	})
}
