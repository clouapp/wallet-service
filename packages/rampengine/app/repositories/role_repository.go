package repositories

import (
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// RoleRepository is the persistence boundary for Role records and their
// polymorphic assignments via model_has_roles.
type RoleRepository interface {
	Create(role *models.Role) error
	FindByID(id uint) (*models.Role, error)
	FindByName(name, guard string) (*models.Role, error)
	FindByIDs(ids []uint, guard string) ([]models.Role, error)
	UpsertByName(name, guard, description string) (*models.Role, error)
	Update(role *models.Role) error
	Delete(id uint) error
	AttachRoleToSubject(roleID uint, subject RBACSubject) error
	SyncRolePermissions(roleID uint, permissionIDs []uint) error
	SyncRolesForSubject(subject RBACSubject, roleIDs []uint) error
	RolesForSubject(subject RBACSubject) ([]models.Role, error)
	// RolesForSubjectIDs and PermissionCountsForRoles are the page-at-a-time
	// forms, for listings that would otherwise query once per row.
	RolesForSubjectIDs(subjectType string, subjectIDs []uint) (map[uint][]models.Role, error)
	PermissionCountsForRoles(roleIDs []uint) (map[uint]int, error)
	SubjectHasRole(subject RBACSubject, name, guard string) (bool, error)
	ListByGuard(guard string) ([]models.Role, error)
	Paginate(filters RoleListFilters) ([]models.Role, int64, error)
	PermissionsForRole(roleID uint) ([]models.Permission, error)
	ExistsByNameExcept(name, guard string, excludeID uint) (bool, error)
	CountAdminsWithRole(roleID uint) (int64, error)
	// UpdateWithPermissions saves the role and replaces its permissions in one
	// transaction: a failure between the two would leave a rename applied and
	// the grant that came with it not.
	UpdateWithPermissions(role *models.Role, permissionIDs []uint) error
}

// RoleListFilters carries the parameters of a paginated role listing.
type RoleListFilters struct {
	Page    int
	PerPage int
	Search  string
	Sort    string // "name" | "-name" | "created_at" | "-created_at"
}

type roleRepository struct{}

func NewRoleRepository() RoleRepository {
	return &roleRepository{}
}

func (r *roleRepository) Create(role *models.Role) error {
	if err := facades.Orm().Query().Create(role); err != nil {
		return fmt.Errorf("role repo: create: %w", err)
	}
	return nil
}

func (r *roleRepository) FindByName(name, guard string) (*models.Role, error) {
	return first[models.Role](facades.Orm().Query().Where("name", name).Where("guard", guard), "role repo: find by name")
}

func (r *roleRepository) UpsertByName(name, guard, description string) (*models.Role, error) {
	existing, err := r.FindByName(name, guard)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Description != description {
			existing.Description = description
			if err := facades.Orm().Query().Save(existing); err != nil {
				return nil, fmt.Errorf("role repo: upsert update: %w", err)
			}
		}
		return existing, nil
	}
	role := &models.Role{Name: name, Guard: guard, Description: description}
	if err := r.Create(role); err != nil {
		return nil, err
	}
	return role, nil
}

func (r *roleRepository) AttachRoleToSubject(roleID uint, subject RBACSubject) error {
	return attachRole(facades.Orm().Query(), roleID, subject)
}

// attachRole is AttachRoleToSubject through q, for a write that assigns a role
// in its own transaction.
func attachRole(q orm.Query, roleID uint, subject RBACSubject) error {
	_, err := q.Exec(
		"INSERT INTO model_has_roles (role_id, subject_type, subject_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING",
		roleID, subject.SubjectType(), subject.SubjectID(),
	)
	if err != nil {
		return fmt.Errorf("role repo: attach: %w", err)
	}
	return nil
}

func (r *roleRepository) SyncRolePermissions(roleID uint, permissionIDs []uint) error {
	return facades.Orm().Transaction(func(tx orm.Query) error {
		return syncRolePermissions(tx, roleID, permissionIDs)
	})
}

func syncRolePermissions(tx orm.Query, roleID uint, permissionIDs []uint) error {
	if _, err := tx.Exec("DELETE FROM role_has_permissions WHERE role_id = ?", roleID); err != nil {
		return fmt.Errorf("role repo: sync clear: %w", err)
	}
	for _, pid := range permissionIDs {
		if _, err := tx.Exec(
			"INSERT INTO role_has_permissions (role_id, permission_id) VALUES (?, ?) ON CONFLICT DO NOTHING",
			roleID, pid,
		); err != nil {
			return fmt.Errorf("role repo: sync insert: %w", err)
		}
	}
	return nil
}

func (r *roleRepository) FindByIDs(ids []uint, guard string) ([]models.Role, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var roles []models.Role
	err := facades.Orm().Query().Model(&models.Role{}).
		Where("guard", guard).
		Where("id IN ?", ids).
		Find(&roles)
	if err != nil {
		return nil, fmt.Errorf("role repo: find by ids: %w", err)
	}
	return roles, nil
}

func (r *roleRepository) SyncRolesForSubject(subject RBACSubject, roleIDs []uint) error {
	return facades.Orm().Transaction(func(tx orm.Query) error {
		return syncRolesForSubject(tx, subject, roleIDs)
	})
}

// syncRolesForSubject replaces a subject's roles through tx. The admin
// repository calls it too, to change an account and its roles in one unit.
func syncRolesForSubject(tx orm.Query, subject RBACSubject, roleIDs []uint) error {
	if _, err := tx.Exec(
		"DELETE FROM model_has_roles WHERE subject_type = ? AND subject_id = ?",
		subject.SubjectType(), subject.SubjectID(),
	); err != nil {
		return fmt.Errorf("role repo: sync roles clear: %w", err)
	}
	for _, rid := range roleIDs {
		if _, err := tx.Exec(
			"INSERT INTO model_has_roles (role_id, subject_type, subject_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING",
			rid, subject.SubjectType(), subject.SubjectID(),
		); err != nil {
			return fmt.Errorf("role repo: sync roles insert: %w", err)
		}
	}
	return nil
}

func (r *roleRepository) ListByGuard(guard string) ([]models.Role, error) {
	var roles []models.Role
	err := facades.Orm().Query().Model(&models.Role{}).
		Where("guard", guard).
		OrderBy("name").
		Find(&roles)
	if err != nil {
		return nil, fmt.Errorf("role repo: list by guard: %w", err)
	}
	return roles, nil
}

// RolesForSubjectIDs answers the same question as RolesForSubject for a whole
// page of subjects at once. A listing that called the single-subject version in
// a loop paid one query per row.
func (r *roleRepository) RolesForSubjectIDs(subjectType string, subjectIDs []uint) (map[uint][]models.Role, error) {
	if len(subjectIDs) == 0 {
		return map[uint][]models.Role{}, nil
	}
	var rows []struct {
		models.Role
		SubjectID uint
	}
	err := facades.Orm().Query().Raw(
		`SELECT r.*, mhr.subject_id FROM roles r
		 INNER JOIN model_has_roles mhr ON mhr.role_id = r.id
		 WHERE mhr.subject_type = ? AND mhr.subject_id IN ?`,
		subjectType, subjectIDs,
	).Scan(&rows)
	if err != nil {
		return nil, fmt.Errorf("role repo: roles for subject ids: %w", err)
	}
	bySubject := make(map[uint][]models.Role, len(subjectIDs))
	for _, row := range rows {
		bySubject[row.SubjectID] = append(bySubject[row.SubjectID], row.Role)
	}
	return bySubject, nil
}

// PermissionCountsForRoles counts the permissions of several roles in one
// query. The roles listing renders a badge, not the rows, so loading the
// permissions themselves would be reading a page to count its lines.
func (r *roleRepository) PermissionCountsForRoles(roleIDs []uint) (map[uint]int, error) {
	if len(roleIDs) == 0 {
		return map[uint]int{}, nil
	}
	var rows []struct {
		RoleID uint
		Total  int
	}
	err := facades.Orm().Query().Raw(
		`SELECT rhp.role_id, COUNT(1) AS total FROM role_has_permissions rhp
		 WHERE rhp.role_id IN ?
		 GROUP BY rhp.role_id`,
		roleIDs,
	).Scan(&rows)
	if err != nil {
		return nil, fmt.Errorf("role repo: permission counts for roles: %w", err)
	}
	counts := make(map[uint]int, len(roleIDs))
	for _, row := range rows {
		counts[row.RoleID] = row.Total
	}
	return counts, nil
}

func (r *roleRepository) RolesForSubject(subject RBACSubject) ([]models.Role, error) {
	var roles []models.Role
	err := facades.Orm().Query().Raw(
		`SELECT r.* FROM roles r
		 INNER JOIN model_has_roles mhr ON mhr.role_id = r.id
		 WHERE mhr.subject_type = ? AND mhr.subject_id = ?`,
		subject.SubjectType(), subject.SubjectID(),
	).Scan(&roles)
	if err != nil {
		return nil, fmt.Errorf("role repo: roles for model: %w", err)
	}
	return roles, nil
}

func (r *roleRepository) FindByID(id uint) (*models.Role, error) {
	return first[models.Role](facades.Orm().Query().Where("id", id), "role repo: find by id")
}

func (r *roleRepository) Update(role *models.Role) error {
	if err := facades.Orm().Query().Save(role); err != nil {
		return fmt.Errorf("role repo: update: %w", err)
	}
	return nil
}

func (r *roleRepository) UpdateWithPermissions(role *models.Role, permissionIDs []uint) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		if err := tx.Save(role); err != nil {
			return err
		}
		return syncRolePermissions(tx, role.ID, permissionIDs)
	})
	if err != nil {
		return fmt.Errorf("role repo: update with permissions: %w", err)
	}
	return nil
}

func (r *roleRepository) Delete(id uint) error {
	_, err := facades.Orm().Query().Exec("DELETE FROM roles WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("role repo: delete: %w", err)
	}
	return nil
}

func (r *roleRepository) ExistsByNameExcept(name, guard string, excludeID uint) (bool, error) {
	exists, err := facades.Orm().Query().Model(&models.Role{}).
		Where("name", name).
		Where("guard", guard).
		Where("id != ?", excludeID).Exists()
	if err != nil {
		return false, fmt.Errorf("role repo: exists by name except: %w", err)
	}
	return exists, nil
}

// SubjectHasRole reports whether the subject is assigned the named role in the
// given guard. The guard is part of the role's identity, so the same name in
// another audience's catalog never matches.
func (r *roleRepository) SubjectHasRole(subject RBACSubject, name, guard string) (bool, error) {
	var count int64
	err := facades.Orm().Query().Raw(
		`SELECT COUNT(1) FROM model_has_roles mhr
		 INNER JOIN roles r ON r.id = mhr.role_id
		 WHERE mhr.subject_type = ? AND mhr.subject_id = ?
		   AND r.name = ? AND r.guard = ?`,
		subject.SubjectType(), subject.SubjectID(), name, guard,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("role repo: subject has role: %w", err)
	}
	return count > 0, nil
}

func (r *roleRepository) CountAdminsWithRole(roleID uint) (int64, error) {
	var count int64
	err := facades.Orm().Query().Raw(
		"SELECT COUNT(1) FROM model_has_roles WHERE role_id = ? AND subject_type = ?",
		roleID, models.AdminMorphType,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("role repo: count admins with role: %w", err)
	}
	return count, nil
}

func (r *roleRepository) Paginate(filters RoleListFilters) ([]models.Role, int64, error) {
	q := facades.Orm().Query().Model(&models.Role{}).Where("guard", "admin")
	if filters.Search != "" {
		like := likeContains(filters.Search)
		q = q.Where("name ILIKE ? OR description ILIKE ?", like, like)
	}
	column, dir := roleSort.parse(filters.Sort)
	q = q.OrderBy(column, dir)

	var roles []models.Role
	var total int64
	if err := q.Paginate(filters.Page, filters.PerPage, &roles, &total); err != nil {
		return nil, 0, fmt.Errorf("role repo: paginate: %w", err)
	}
	return roles, total, nil
}

func (r *roleRepository) PermissionsForRole(roleID uint) ([]models.Permission, error) {
	var perms []models.Permission
	err := facades.Orm().Query().Raw(
		`SELECT p.* FROM permissions p
		 INNER JOIN role_has_permissions rhp ON rhp.permission_id = p.id
		 WHERE rhp.role_id = ?
		 ORDER BY p.name`,
		roleID,
	).Scan(&perms)
	if err != nil {
		return nil, fmt.Errorf("role repo: permissions for role: %w", err)
	}
	return perms, nil
}

var roleSort = sortable{
	columns: map[string]string{"name": "name", "created_at": "created_at", "updated_at": "updated_at"},
	column:  "name",
	dir:     "asc",
}
