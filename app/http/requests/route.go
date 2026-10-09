package requests

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
)

// RouteString reads one path parameter. An empty value is an error.
func RouteString(ctx http.Context, name string) (string, error) {
	if ctx == nil || ctx.Request() == nil {
		return "", fmt.Errorf("missing request")
	}
	if name == "" {
		return "", fmt.Errorf("missing path parameter name")
	}
	value := strings.TrimSpace(ctx.Request().Route(name))
	if value == "" {
		return "", fmt.Errorf("missing %s", name)
	}
	return value, nil
}

// RouteUUID reads one path parameter as a UUID.
func RouteUUID(ctx http.Context, name string) (uuid.UUID, error) {
	if ctx == nil || ctx.Request() == nil {
		return uuid.Nil, fmt.Errorf("missing request")
	}
	if name == "" {
		return uuid.Nil, fmt.Errorf("missing path parameter name")
	}
	id, err := uuid.Parse(strings.TrimSpace(ctx.Request().Route(name)))
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid %s", name)
	}
	return id, nil
}
