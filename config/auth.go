package config

import (
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

func registerAuth() {
	facades.Config().Add("auth", map[string]any{
		"defaults": map[string]any{"guard": "web"},
		"guards": map[string]any{
			"web": map[string]any{
				"driver":   "jwt",
				"provider": "users",
			},
		},
		"providers": map[string]any{
			"users": map[string]any{
				"driver": "orm",
				"model":  models.User{},
			},
		},
	})
}
