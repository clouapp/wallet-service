package repositories

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// PermissionRepository is the persistence boundary for Permission records
// and their effective resolution against polymorphic subjects.
type PermissionRepository interface {
	FindByName(name, guard string) (*models.Permission, error)
	FindByIDs(ids []uint, guard string) ([]models.Permission, error)
	UpsertByName(name, guard, description string) (*models.Permission, error)
	AttachPermissionToSubject(permissionID uint, subject RBACSubject) error
	// SubjectHasPermission resolves a permission for the subject, scoped to the
	// guard its subject type maps to. The guard is derived rather than passed
	// so a check can never be resolved against another audience's catalog.
	SubjectHasPermission(subject RBACSubject, permission string) (bool, error)
	PermissionsForSubject(subject RBACSubject) ([]string, error)
	AllNames(guard string) ([]string, error)
	List(filters PermissionListFilters) ([]models.Permission, error)
}

// PermissionListFilters carries the parameters of a permission listing.
// Permissions are typically a small fixed catalog so listing returns the
// full set without pagination — only optional search and sort.
type PermissionListFilters struct {
	Search string
	Sort   string // "name" | "-name"
}

type permissionRepository struct{}

func NewPermissionRepository() PermissionRepository {
	return &permissionRepository{}
}

func (r *permissionRepository) FindByName(name, guard string) (*models.Permission, error) {
	return first[models.Permission](facades.Orm().Query().Where("name", name).Where("guard", guard), "permission repo: find")
}

func (r *permissionRepository) UpsertByName(name, guard, description string) (*models.Permission, error) {
	existing, err := r.FindByName(name, guard)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Description != description {
			existing.Description = description
			if err := facades.Orm().Query().Save(existing); err != nil {
				return nil, fmt.Errorf("permission repo: upsert update: %w", err)
			}
		}
		return existing, nil
	}
	p := &models.Permission{Name: name, Guard: guard, Description: description}
	if err := facades.Orm().Query().Create(p); err != nil {
		return nil, fmt.Errorf("permission repo: create: %w", err)
	}
	return p, nil
}

func (r *permissionRepository) AttachPermissionToSubject(permissionID uint, subject RBACSubject) error {
	_, err := facades.Orm().Query().Exec(
		"INSERT INTO model_has_permissions (permission_id, subject_type, subject_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING",
		permissionID, subject.SubjectType(), subject.SubjectID(),
	)
	if err != nil {
		return fmt.Errorf("permission repo: attach: %w", err)
	}
	return nil
}

func (r *permissionRepository) SubjectHasPermission(subject RBACSubject, permission string) (bool, error) {
	guard := rbacGuard(subject)
	var count int64
	err := facades.Orm().Query().Raw(
		`SELECT COUNT(1) FROM (
		   SELECT 1 FROM permissions p
		     INNER JOIN model_has_permissions mhp ON mhp.permission_id = p.id
		    WHERE mhp.subject_type = ? AND mhp.subject_id = ? AND p.name = ? AND p.guard = ?
		   UNION
		   SELECT 1 FROM permissions p
		     INNER JOIN role_has_permissions rhp ON rhp.permission_id = p.id
		     INNER JOIN model_has_roles mhr ON mhr.role_id = rhp.role_id
		    WHERE mhr.subject_type = ? AND mhr.subject_id = ? AND p.name = ? AND p.guard = ?
		 ) x`,
		subject.SubjectType(), subject.SubjectID(), permission, guard,
		subject.SubjectType(), subject.SubjectID(), permission, guard,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("permission repo: subject has: %w", err)
	}
	return count > 0, nil
}

func (r *permissionRepository) PermissionsForSubject(subject RBACSubject) ([]string, error) {
	var names []string
	err := facades.Orm().Query().Raw(
		`SELECT DISTINCT p.name FROM permissions p
		   LEFT JOIN model_has_permissions mhp ON mhp.permission_id = p.id AND mhp.subject_type = ? AND mhp.subject_id = ?
		   LEFT JOIN role_has_permissions rhp ON rhp.permission_id = p.id
		   LEFT JOIN model_has_roles mhr ON mhr.role_id = rhp.role_id AND mhr.subject_type = ? AND mhr.subject_id = ?
		  WHERE (mhp.permission_id IS NOT NULL OR mhr.role_id IS NOT NULL) AND p.guard = ?
		  ORDER BY p.name`,
		subject.SubjectType(), subject.SubjectID(),
		subject.SubjectType(), subject.SubjectID(),
		rbacGuard(subject),
	).Scan(&names)
	if err != nil {
		return nil, fmt.Errorf("permission repo: for subject: %w", err)
	}
	return names, nil
}

func (r *permissionRepository) AllNames(guard string) ([]string, error) {
	var names []string
	err := facades.Orm().Query().Model(&models.Permission{}).
		Where("guard", guard).
		OrderBy("name").
		Pluck("name", &names)
	if err != nil {
		return nil, fmt.Errorf("permission repo: all names: %w", err)
	}
	return names, nil
}

func (r *permissionRepository) FindByIDs(ids []uint, guard string) ([]models.Permission, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var perms []models.Permission
	err := facades.Orm().Query().Model(&models.Permission{}).
		Where("guard", guard).
		Where("id IN ?", ids).
		Find(&perms)
	if err != nil {
		return nil, fmt.Errorf("permission repo: find by ids: %w", err)
	}
	return perms, nil
}

func (r *permissionRepository) List(filters PermissionListFilters) ([]models.Permission, error) {
	q := facades.Orm().Query().Model(&models.Permission{}).Where("guard", "admin")
	if filters.Search != "" {
		like := likeContains(filters.Search)
		q = q.Where("name ILIKE ? OR description ILIKE ?", like, like)
	}
	column, dir := permissionSort.parse(filters.Sort)
	q = q.OrderBy(column, dir)

	var perms []models.Permission
	if err := q.Find(&perms); err != nil {
		return nil, fmt.Errorf("permission repo: list: %w", err)
	}
	return perms, nil
}

var permissionSort = sortable{
	columns: map[string]string{"name": "name", "created_at": "created_at"},
	column:  "name",
	dir:     "asc",
}
