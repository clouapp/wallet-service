package requests

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
)

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

// UpdateAccountUserRequest changes a member's role, status, or both.
// At least one field is required. viewer is not a role on this branch.
type UpdateAccountUserRequest struct {
	Role   string `form:"role" json:"role"`
	Status string `form:"status" json:"status"`
}

func (r *UpdateAccountUserRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *UpdateAccountUserRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"role":   "trim",
		"status": "trim",
	}
}

func (r *UpdateAccountUserRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"role":   "required_without:status|in:owner,admin,auditor,user",
		"status": "required_without:role|in:active,suspended",
	}
}

func (r *UpdateAccountUserRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"role.required_without":   "role or status is required",
		"role.in":                 "role must be owner, admin, auditor or user",
		"status.required_without": "role or status is required",
		"status.in":               "status must be active or suspended",
	}
}
