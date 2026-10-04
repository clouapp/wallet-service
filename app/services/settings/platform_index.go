package settings

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/policies"
)

// PlatformIndex lists every platform group a platform admin may view.
// S1.4.6 names settings.view and filters the index by each group's
// ViewPermission. This branch has no platform permission catalog, so a
// platform_admins row stands in for settings.view and for a ViewPermission
// that is not in that catalog. Account groups stay out. A secret value is
// omitted (is_set only). The read writes no activity.
func (s *Service) PlatformIndex(ctx context.Context, actorID uuid.UUID) (RegistryView, error) {
	if ctx == nil {
		return RegistryView{}, fmt.Errorf("platform settings: context is required")
	}
	if s == nil || s.store == nil {
		return RegistryView{}, errServiceRequired
	}
	if actorID == uuid.Nil {
		return RegistryView{}, fmt.Errorf("platform settings: actor is required")
	}
	if s.admins == nil {
		return RegistryView{}, fmt.Errorf("platform settings: platform admins are required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return RegistryView{}, err
	}
	if !admin {
		return RegistryView{}, ErrPlatformViewForbidden
	}
	return s.platformIndexView(ctx)
}

// PlatformGroup reads one platform group.
// S1.4.6: GET /v1/platform/settings/{group} — settings.view + group ViewPermission (404 before 403).
// An unknown name, including an account-only group, is ErrGroupNotFound before
// the platform_admins check and before the store is read. A missing stored
// row is still the registry defaults: 404 is the unknown name, not a missing
// row. This branch has no platform permission catalog, so the platform_admins
// row stands in for settings.view and for a ViewPermission that is not in
// that catalog. A secret is omitted (is_set only). The read writes no activity.
func (s *Service) PlatformGroup(ctx context.Context, actorID uuid.UUID, groupName string) (GroupView, error) {
	if ctx == nil {
		return GroupView{}, fmt.Errorf("platform settings: context is required")
	}
	if s == nil {
		return GroupView{}, errServiceRequired
	}
	groupName = strings.TrimSpace(groupName)
	group, ok := FindGroup(groupName)
	if !ok || group.Scope != ScopePlatform {
		return GroupView{}, ErrGroupNotFound
	}
	if s.store == nil {
		return GroupView{}, errServiceRequired
	}
	if actorID == uuid.Nil {
		return GroupView{}, fmt.Errorf("platform settings: actor is required")
	}
	if s.admins == nil {
		return GroupView{}, fmt.Errorf("platform settings: platform admins are required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return GroupView{}, err
	}
	if !admin {
		return GroupView{}, ErrPlatformViewForbidden
	}
	if !visibleOnPlatformIndex(group) {
		return GroupView{}, ErrPlatformViewForbidden
	}
	return s.platformGroupView(ctx, group)
}

func (s *Service) platformIndexView(ctx context.Context) (RegistryView, error) {
	view := RegistryView{
		Permissions: Permissions{
			View:   policies.PermSettingsView,
			Update: policies.PermSettingsUpdate,
		},
		Sections: []SectionView{},
	}
	sectionAt := map[string]int{}
	blockAt := map[string]map[string]int{}
	for _, group := range Registry() {
		if !visibleOnPlatformIndex(group) {
			continue
		}
		rendered, err := s.platformGroupView(ctx, group)
		if err != nil {
			return RegistryView{}, err
		}
		sectionName := group.SectionName()
		sectionIndex, ok := sectionAt[sectionName]
		if !ok {
			sectionIndex = len(view.Sections)
			sectionAt[sectionName] = sectionIndex
			view.Sections = append(view.Sections, SectionView{Name: sectionName, Blocks: []BlockView{}})
			blockAt[sectionName] = map[string]int{}
		}
		blockIndex, ok := blockAt[sectionName][group.Block]
		if !ok {
			blockIndex = len(view.Sections[sectionIndex].Blocks)
			blockAt[sectionName][group.Block] = blockIndex
			view.Sections[sectionIndex].Blocks = append(view.Sections[sectionIndex].Blocks, BlockView{
				Title:  group.Block,
				Groups: []GroupView{},
			})
		}
		view.Sections[sectionIndex].Blocks[blockIndex].Groups = append(
			view.Sections[sectionIndex].Blocks[blockIndex].Groups,
			rendered,
		)
	}
	return view, nil
}

// visibleOnPlatformIndex is the S1.4.6 index filter. Account groups are
// omitted. A platform group with no ViewPermission needs only the route
// gate. A named ViewPermission is an extra filter, and a name that is not
// in a platform catalog stays listed because the platform_admins row stands
// in for it.
func visibleOnPlatformIndex(group Group) bool {
	if group.Scope != ScopePlatform {
		return false
	}
	if strings.TrimSpace(group.ViewPermission) == "" {
		return true
	}
	return platformAdminCoversViewPermission(group.ViewPermission)
}

// platformAdminCoversViewPermission reports whether a platform_admins row
// covers a group ViewPermission. There is no platform permission catalog,
// so every named permission is covered and the group stays in the index.
func platformAdminCoversViewPermission(permission string) bool {
	return strings.TrimSpace(permission) != ""
}
