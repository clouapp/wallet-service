package middleware

import (
	"strings"

	"github.com/goravel/framework/contracts/http"
)

// ClientIP is the address S3.4.6 compares with an API token's ip_cidr.
func ClientIP(ctx http.Context) string {
	if ctx == nil || ctx.Request() == nil {
		return ""
	}
	return strings.TrimSpace(ctx.Request().Ip())
}
