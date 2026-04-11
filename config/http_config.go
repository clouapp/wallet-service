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
		"drivers": map[string]any{
			"gin": map[string]any{
				"route": func() (route.Route, error) {
					return ginfacades.Route("gin"), nil
				},
			},
		},
	})
}
