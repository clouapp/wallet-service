package models

import (
	"errors"
	"strings"
)

const (
	WalletRoleAdmin    = "admin"
	WalletRoleSpender  = "spender"
	WalletRoleApprover = "approver"
	WalletRoleViewer   = "viewer"
	WalletRoleView     = "view"
)

var ErrInvalidWalletRoles = errors.New("roles must be a set of admin, spender, approver, viewer, view")

// ParseWalletRoles accepts a comma-separated set from the closed wallet-role vocabulary.
// Duplicates and account roles (owner, auditor, user) are rejected.
func ParseWalletRoles(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	if strings.TrimSpace(raw) == "" {
		return nil, ErrInvalidWalletRoles
	}
	seen := make(map[string]struct{}, len(parts))
	roles := make([]string, 0, len(parts))
	for _, part := range parts {
		role := strings.TrimSpace(part)
		if !isWalletRole(role) {
			return nil, ErrInvalidWalletRoles
		}
		if _, ok := seen[role]; ok {
			return nil, ErrInvalidWalletRoles
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	if len(roles) == 0 {
		return nil, ErrInvalidWalletRoles
	}
	return roles, nil
}

func isWalletRole(role string) bool {
	switch role {
	case WalletRoleAdmin, WalletRoleSpender, WalletRoleApprover, WalletRoleViewer, WalletRoleView:
		return true
	default:
		return false
	}
}

// FormatWalletRoles stores the validated set as a comma-separated string.
func FormatWalletRoles(roles []string) string {
	return strings.Join(roles, ",")
}
