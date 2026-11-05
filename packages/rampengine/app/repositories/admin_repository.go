package repositories

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// AdminRepository is the persistence boundary for Admin records.
type AdminRepository interface {
	Create(admin *models.Admin) error
	FindByEmail(email string) (*models.Admin, error)
	FindByID(id uint) (*models.Admin, error)
	// Update writes the named columns of admin, and only those. A request writes
	// what it changed: a full-row save from a login or an edit that loaded the
	// row before a password reset would put the old password back.
	Update(admin *models.Admin, columns ...string) error
	// UpdateWithRoles writes the named columns of admin and replaces its roles
	// in one transaction: a failure between the two would leave the name
	// changed and the roles as they were. With no column it only replaces the
	// roles.
	UpdateWithRoles(admin *models.Admin, roleIDs []uint, columns ...string) error
	// UpdateSpendingCode writes the named columns of admin and spends the
	// one-time code that authorized the change in one transaction, so the
	// change lands only with the code spent. A nil code writes the columns
	// alone. It returns ErrOTPCodeAlreadyUsed when another request spent the
	// code first.
	UpdateSpendingCode(admin *models.Admin, code *models.OTPCode, columns ...string) error
	// Delete soft-deletes the admin and detaches its roles (model_has_roles)
	// in one transaction: the row keeps its id and email, so the existence
	// checks below look through deleted_at on purpose.
	Delete(admin *models.Admin) error
	ExistsByEmail(email string) (bool, error)
	ExistsByEmailExcept(email string, excludeID uint) (bool, error)
	Paginate(filters ListFilters) ([]models.Admin, int64, error)
}

type adminRepository struct{}

// NewAdminRepository returns an AdminRepository backed by the default ORM connection.
func NewAdminRepository() AdminRepository {
	return &adminRepository{}
}

func (r *adminRepository) Create(admin *models.Admin) error {
	if err := facades.Orm().Query().Create(admin); err != nil {
		return fmt.Errorf("admin repo: create: %w", taken(err))
	}
	return nil
}

func (r *adminRepository) FindByEmail(email string) (*models.Admin, error) {
	return first[models.Admin](facades.Orm().Query().Where("email", email), "admin repo: find by email")
}

func (r *adminRepository) FindByID(id uint) (*models.Admin, error) {
	return first[models.Admin](facades.Orm().Query().Where("id", id), "admin repo: find by id")
}

func (r *adminRepository) Update(admin *models.Admin, columns ...string) error {
	if len(columns) == 0 {
		return errors.New("admin repo: update: no column named")
	}
	if err := updateAdminColumns(facades.Orm().Query(), admin, columns); err != nil {
		return fmt.Errorf("admin repo: update: %w", taken(err))
	}
	return nil
}

func (r *adminRepository) UpdateWithRoles(admin *models.Admin, roleIDs []uint, columns ...string) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		if err := updateAdminColumns(tx, admin, columns); err != nil {
			return err
		}
		return syncRolesForSubject(tx, admin, roleIDs)
	})
	if err != nil {
		return fmt.Errorf("admin repo: update with roles: %w", taken(err))
	}
	return nil
}

func (r *adminRepository) UpdateSpendingCode(admin *models.Admin, code *models.OTPCode, columns ...string) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		if code != nil {
			if err := markOTPUsed(tx, code); err != nil {
				return err
			}
		}
		return updateAdminColumns(tx, admin, columns)
	})
	if err != nil {
		return fmt.Errorf("admin repo: update spending code: %w", err)
	}
	return nil
}

func (r *adminRepository) Delete(admin *models.Admin) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		if _, err := tx.Delete(admin); err != nil {
			return err
		}
		return syncRolesForSubject(tx, admin, nil)
	})
	if err != nil {
		return fmt.Errorf("admin repo: delete: %w", err)
	}
	return nil
}

// ExistsByEmail and ExistsByEmailExcept include soft-deleted rows: the unique
// index on admins.email is not partial, so a trashed admin still owns its
// address and a create/update reusing it would fail at the database instead
// of with a domain error.
func (r *adminRepository) ExistsByEmail(email string) (bool, error) {
	exists, err := facades.Orm().Query().WithTrashed().Model(&models.Admin{}).Where("email", email).Exists()
	if err != nil {
		return false, fmt.Errorf("admin repo: exists by email: %w", err)
	}
	return exists, nil
}

func (r *adminRepository) ExistsByEmailExcept(email string, excludeID uint) (bool, error) {
	exists, err := facades.Orm().Query().WithTrashed().Model(&models.Admin{}).
		Where("email", email).
		Where("id != ?", excludeID).Exists()
	if err != nil {
		return false, fmt.Errorf("admin repo: exists by email except: %w", err)
	}
	return exists, nil
}

func (r *adminRepository) Paginate(filters ListFilters) ([]models.Admin, int64, error) {
	q := facades.Orm().Query().Model(&models.Admin{})
	if filters.Search != "" {
		like := likeContains(filters.Search)
		q = q.Where("name ILIKE ? OR email ILIKE ?", like, like)
	}
	column, dir := adminSort.parse(filters.Sort)
	q = q.OrderBy(column, dir)

	var admins []models.Admin
	var total int64
	if err := q.Paginate(filters.Page, filters.PerPage, &admins, &total); err != nil {
		return nil, 0, fmt.Errorf("admin repo: paginate: %w", err)
	}
	return admins, total, nil
}

var adminSort = sortable{
	columns: map[string]string{"created_at": "created_at", "email": "email", "name": "name", "last_login_at": "last_login_at"},
	column:  "created_at",
	dir:     "desc",
}

// updateAdminColumns writes the named columns of admin, and updated_at, to its
// row. No column writes nothing.
func updateAdminColumns(q orm.Query, admin *models.Admin, columns []string) error {
	if len(columns) == 0 {
		return nil
	}
	_, err := q.Model(admin).Select(columns...).Update(admin)
	return err
}
