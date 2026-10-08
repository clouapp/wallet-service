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
		// Rate limits, attempts per minute; 0 turns a limit off. The keys use
		// middleware.ClientIP, so TRUSTED_PROXIES must match the deployment.
		"throttle": map[string]any{
			// POST /v1/auth/login: per client IP + email, and per client IP.
			"login_per_ip_email": envInt("THROTTLE_LOGIN_PER_IP_EMAIL", 10),
			"login_per_ip":       envInt("THROTTLE_LOGIN_PER_IP", 60),
			// POST /v1/auth/recover: per client IP + email.
			"recover_per_ip_email": envInt("THROTTLE_RECOVER_PER_IP_EMAIL", 5),
			// Per client IP and path: recover (IP part), recover/confirm, 2fa/verify,
			// refresh, register and invites/accept.
			"auth_per_ip": envInt("THROTTLE_AUTH_PER_IP", 60),
			// /api/v1: per API token (client IP when no token is sent).
			"api_per_minute": envInt("THROTTLE_API_PER_MINUTE", 600),
		},
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
