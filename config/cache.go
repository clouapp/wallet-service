package config

import (
	contractscache "github.com/goravel/framework/contracts/cache"
	"github.com/goravel/framework/facades"
	redisfacades "github.com/goravel/redis/facades"
)

func registerCache() {
	facades.Config().Add("cache", map[string]any{
		"default": envString("CACHE_DRIVER", "redis"),
		"stores": map[string]any{
			"redis": map[string]any{
				"driver": "custom",
				"via": func() (contractscache.Driver, error) {
					return redisfacades.Cache("redis")
				},
			},
			"memory": map[string]any{
				"driver": "memory",
			},
		},
	})
}
