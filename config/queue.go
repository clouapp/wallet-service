package config

import "github.com/goravel/framework/facades"

func registerQueue() {
	facades.Config().Add("queue", map[string]any{
		"default": envString("QUEUE_CONNECTION", "sync"),
		"connections": map[string]any{
			"sync": map[string]any{
				"driver": "sync",
			},
		},
		"failed": map[string]any{
			"database": "postgres",
			"table":    "failed_jobs",
		},
	})
}
