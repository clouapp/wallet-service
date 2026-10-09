package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/http/limit"
	frameworkmiddleware "github.com/goravel/framework/http/middleware"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/responses"
)

// Names of the registered rate limiters. A route picks one with Throttle.
const (
	// ThrottleLogin is POST /v1/auth/login: per client IP and email, and per client IP.
	ThrottleLogin = "auth-login"
	// ThrottleRecover is POST /v1/auth/recover: per client IP and email, and per client IP.
	ThrottleRecover = "auth-recover"
	// ThrottleAuth covers the other unauthenticated /v1/auth routes that take a
	// secret (2fa verify, recover confirm, refresh, register, invite accept):
	// per client IP and path.
	ThrottleAuth = "auth"
	// ThrottleAPI is the /api/v1 group: per API token, or per client IP when the
	// request carries no bearer token.
	ThrottleAPI = "api"
	// ThrottleGasCheck is POST .../gas-check: one on-chain read per wallet per minute.
	ThrottleGasCheck = "gas-check"
)

// Config keys under http.throttle, all attempts per minute. Zero turns that
// limit off. config/http_config.go reads the environment into them.
const (
	throttleLoginPerIPEmail   = "http.throttle.login_per_ip_email"
	throttleLoginPerIP        = "http.throttle.login_per_ip"
	throttleRecoverPerIPEmail = "http.throttle.recover_per_ip_email"
	throttleAuthPerIP         = "http.throttle.auth_per_ip"
	throttleAPIPerMinute      = "http.throttle.api_per_minute"
)

// gasCheckPerMinute is fixed: the chain adapter caches for less than a minute.
const gasCheckPerMinute = 1

// gasCheckRetryAfterSeconds is the window ThrottleGasCheck lasts, as the 429 body reports it.
const gasCheckRetryAfterSeconds = 60

// Throttle applies the rate limiter registered under name (RegisterThrottles).
// A store failure lets the request through: the limit is not worth an outage.
func Throttle(name string) http.Middleware {
	return frameworkmiddleware.Throttle(name)
}

// RegisterThrottles registers every limiter Throttle can name. The limits are
// read from config on each request.
func RegisterThrottles(limiter http.RateLimiter) {
	limiter.ForWithLimits(ThrottleLogin, func(ctx http.Context) []http.Limit {
		ip := ClientIP(ctx)
		return limits(
			perMinute(facades.Config().GetInt(throttleLoginPerIPEmail), "ip-email:"+ip+":"+emailKey(ctx)),
			perMinute(facades.Config().GetInt(throttleLoginPerIP), "ip:"+ip),
		)
	})
	limiter.ForWithLimits(ThrottleRecover, func(ctx http.Context) []http.Limit {
		ip := ClientIP(ctx)
		return limits(
			perMinute(facades.Config().GetInt(throttleRecoverPerIPEmail), "ip-email:"+ip+":"+emailKey(ctx)),
			perMinute(facades.Config().GetInt(throttleAuthPerIP), "ip:"+ip),
		)
	})
	limiter.ForWithLimits(ThrottleAuth, func(ctx http.Context) []http.Limit {
		return limits(
			perMinute(facades.Config().GetInt(throttleAuthPerIP), "ip:"+ClientIP(ctx)+":"+ctx.Request().Path()),
		)
	})
	limiter.ForWithLimits(ThrottleAPI, func(ctx http.Context) []http.Limit {
		return limits(
			perMinute(facades.Config().GetInt(throttleAPIPerMinute), apiKey(ctx)),
		)
	})
	limiter.ForWithLimits(ThrottleGasCheck, func(ctx http.Context) []http.Limit {
		return []http.Limit{
			limit.PerMinute(gasCheckPerMinute).
				By("wallet:" + ctx.Request().Route("walletId")).
				Response(gasCheckLimited),
		}
	})
}

// perMinute builds one limit, or nil when attempts is not positive. The
// framework store turns a zero into one attempt, which is not "off".
func perMinute(attempts int, key string) http.Limit {
	if attempts <= 0 {
		return nil
	}
	return limit.PerMinute(attempts).By(key).Response(tooManyRequests)
}

func limits(all ...http.Limit) []http.Limit {
	kept := make([]http.Limit, 0, len(all))
	for _, l := range all {
		if l != nil {
			kept = append(kept, l)
		}
	}
	return kept
}

// tooManyRequests ends the chain with the error envelope. The framework
// already set Retry-After and the X-RateLimit-* headers.
func tooManyRequests(ctx http.Context) {
	_ = responses.Error(ctx, http.StatusTooManyRequests, responses.CodeTooManyRequests, "too many requests").Abort()
}

// gasCheckLimited keeps the body ForceGasCheck answered before the limit moved
// into this middleware.
func gasCheckLimited(ctx http.Context) {
	_ = responses.FailWith(ctx, http.StatusTooManyRequests, "rate_limited", "rate_limited", map[string]any{
		"limit_type":          "gas_check",
		"retry_after_seconds": gasCheckRetryAfterSeconds,
	}).Abort()
}

// emailKey is the request's email, lower-cased and hashed so the cache key
// holds neither the address nor an unbounded string.
func emailKey(ctx http.Context) string {
	email := strings.ToLower(strings.TrimSpace(ctx.Request().Input("email")))
	return digest(email)
}

// apiKey identifies the caller of /api/v1: the bearer token, hashed, or the
// client IP when none was sent. The token is not verified here; APITokenAuth
// runs after and refuses a forged one.
func apiKey(ctx http.Context) string {
	bearer := strings.TrimSpace(strings.TrimPrefix(ctx.Request().Header("Authorization", ""), "Bearer "))
	if bearer == "" {
		return "ip:" + ClientIP(ctx)
	}
	return "token:" + digest(bearer)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
