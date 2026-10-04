package settings

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/policies"
)

// AccountGroup reads one account settings group for the dashboard.
// S1.4.6: GET /v1/accounts/{accountId}/settings/{group} settings.view
// (platform-managed groups readable). An unknown name and a platform-only
// group are ErrGroupNotFound before the role check. Owner, admin, and
// auditor may read. A user may not. A secret is omitted (is_set only).
// The read writes no activity and lists only this account's rows.
func (s *Service) AccountGroup(ctx context.Context, accountID uuid.UUID, role, groupName string) (GroupView, error) {
	if err := requireAccount(ctx, accountID); err != nil {
		return GroupView{}, err
	}
	group, ok := accountScopedGroup(groupName)
	if !ok {
		return GroupView{}, ErrGroupNotFound
	}
	if !policies.MayViewSettings(role) {
		return GroupView{}, ErrViewForbidden
	}
	if s == nil || s.store == nil {
		return GroupView{}, errServiceRequired
	}
	rows, err := s.store.ListGroup(ctx, accountID, group.Name)
	if err != nil {
		return GroupView{}, err
	}
	canUpdate := policies.MayUpdateSettings(role) && group.ManagedBy == ManagedByAccount
	return renderStoredGroup(group, rowsOwnedBy(accountID, group.Name, rows), canUpdate), nil
}

func accountScopedGroup(name string) (Group, bool) {
	group, ok := FindGroup(strings.TrimSpace(name))
	if !ok || group.Scope != ScopeAccount {
		return Group{}, false
	}
	return group, true
}
