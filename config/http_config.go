package config

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"
	ginfacades "github.com/goravel/gin/facades"
)

func registerHTTP() {
	facades.Config().Add("http", map[string]any{
		"default":         "gin",
		"request_timeout": envInt("HTTP_REQUEST_TIMEOUT", 30),
		// Comma-separated CIDRs of the reverse proxies whose X-Forwarded-For is
		// believed (middleware.ClientIP). Anything else is attributed to the
		// connection's own address.
		"trusted_proxies": envString("TRUSTED_PROXIES", "127.0.0.1/32,::1/128"),
		"drivers": map[string]any{
			"gin": map[string]any{
				"route": func() (route.Route, error) {
					return ginfacades.Route("gin"), nil
				},
			},
		},
	})
}
