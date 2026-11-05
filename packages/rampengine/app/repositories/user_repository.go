package repositories

import (
	"errors"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrUserDeleted refuses a write on an account deleted since it was read: an
// admin's, whose audit entry is rolled back with it so the entry never records
// an act that did not land, and a client's that would put money in flight for
// the account (see lockLiveUser).
var ErrUserDeleted = errors.New("user deleted")

// ErrUserNotDeleted refuses to restore an account restored since it was read.
var ErrUserNotDeleted = errors.New("user not deleted")

// UserRepository is the persistence boundary for User records.
type UserRepository interface {
	// Create and CreateAudited are the only two ways an account is born, and
	// both write its individual customer (id = users.id), the membership and
	// the owner role in the account's own transaction: no account is ever
	// without the customer that holds its money.
	Create(user *models.User) error
	FindByEmail(email string) (*models.User, error)
	FindByID(id uint) (*models.User, error)
	// FindByIDWithTrashed is FindByID that also finds a soft-deleted account.
	// Only the admin reads one: auth, features and relink rely on FindByID
	// leaving it out.
	FindByIDWithTrashed(id uint) (*models.User, error)
	// Update writes the named columns of user, and only those. A request writes
	// what it changed: a full-row save from a request that loaded the row
	// earlier would put back whatever changed since, a password reset's new
	// password and the stamp that ends the sessions opened before it included.
	Update(user *models.User, columns ...string) error
	// UpdateSpendingCode writes the named columns of user and spends the
	// one-time code that authorized the change in one transaction, so a
	// redemption that fails to persist leaves the code unspent. With no column
	// it only spends the code. It returns ErrOTPCodeAlreadyUsed when another
	// request spent the code first.
	UpdateSpendingCode(user *models.User, code *models.OTPCode, columns ...string) error
	Delete(user *models.User) error
	// CreateAudited, UpdateAudited and DeleteAudited are the admin's writes:
	// each lands the change and its audit entry in one transaction, so neither
	// exists without the other. CreateAudited sets the entry's subject to the
	// new account, whose id exists only once it is inserted. UpdateAudited and
	// DeleteAudited write only a live account and answer ErrUserDeleted for one
	// deleted since it was read. DeleteAudited refuses while money can still
	// move for the account — ErrVirtualAccountOpen for a standing account
	// active or being provisioned, ErrTransactionOpen for an order not yet
	// terminal, at any provider — and while the account is the only owner of a
	// business neither closed nor deleted (ErrCustomerLastOwner), all read
	// inside its transaction, and purges what
	// the account retained — the KYC profile's declaration and phone digest,
	// the saved Pix keys, the devices registered for push — in that same
	// transaction: a failure anywhere leaves the account live, nothing purged
	// and nothing recorded.
	CreateAudited(user *models.User, entry *models.AdminAuditLog) error
	UpdateAudited(user *models.User, entry *models.AdminAuditLog, columns ...string) error
	DeleteAudited(user *models.User, entry *models.AdminAuditLog) error
	// RestoreAudited brings a soft-deleted account back and writes its
	// sessions_revoked_at, so nothing issued before the deletion comes back to
	// life with it, together with entry. It answers ErrUserNotDeleted for an
	// account restored since it was read.
	RestoreAudited(user *models.User, entry *models.AdminAuditLog) error
	ExistsByEmail(email string) (bool, error)
	ExistsByEmailExcept(email string, excludeID uint) (bool, error)
	ExistsByHandle(handle string) (bool, error)
	ExistsByHandleExcept(handle string, excludeID uint) (bool, error)
	Paginate(filters ListFilters) ([]models.User, int64, error)
}

// ListFilters carries the parameters of a paginated user listing for the admin namespace.
type ListFilters struct {
	Page    int
	PerPage int
	Search  string
	Sort    string // "created_at" | "-created_at" | "email" | "-email" | "handle" | "-handle"
	// Status is one of models.UserStatus*; empty lists every live account,
	// suspended ones included.
	Status string
}

type userRepository struct{}

// NewUserRepository returns a UserRepository backed by the default ORM connection.
func NewUserRepository() UserRepository {
	return &userRepository{}
}

func (r *userRepository) Create(user *models.User) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		if err := tx.Create(user); err != nil {
			return taken(err)
		}
		return createIndividualCustomer(tx, user)
	})
	if err != nil {
		return fmt.Errorf("user repo: create: %w", err)
	}
	return nil
}

func (r *userRepository) FindByEmail(email string) (*models.User, error) {
	return first[models.User](facades.Orm().Query().Where("email", email), "user repo: find by email")
}

func (r *userRepository) FindByID(id uint) (*models.User, error) {
	return first[models.User](facades.Orm().Query().Where("id", id), "user repo: find by id")
}

func (r *userRepository) FindByIDWithTrashed(id uint) (*models.User, error) {
	return first[models.User](facades.Orm().Query().WithTrashed().Where("id", id), "user repo: find by id with trashed")
}

func (r *userRepository) Update(user *models.User, columns ...string) error {
	if len(columns) == 0 {
		return errors.New("user repo: update: no column named")
	}
	if err := updateUserColumns(facades.Orm().Query(), user, columns); err != nil {
		return fmt.Errorf("user repo: update: %w", taken(err))
	}
	return nil
}

func (r *userRepository) UpdateSpendingCode(user *models.User, code *models.OTPCode, columns ...string) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		if err := markOTPUsed(tx, code); err != nil {
			return err
		}
		if len(columns) == 0 {
			return nil
		}
		return updateUserColumns(tx, user, columns)
	})
	if err != nil {
		return fmt.Errorf("user repo: update spending code: %w", err)
	}
	return nil
}

func (r *userRepository) Delete(user *models.User) error {
	if _, err := facades.Orm().Query().Delete(user); err != nil {
		return fmt.Errorf("user repo: delete: %w", err)
	}
	return nil
}

func (r *userRepository) CreateAudited(user *models.User, entry *models.AdminAuditLog) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		if err := tx.Create(user); err != nil {
			return taken(err)
		}
		if err := createIndividualCustomer(tx, user); err != nil {
			return err
		}
		entry.SubjectID = &user.ID
		return createAuditLog(tx, entry)
	})
	if err != nil {
		return fmt.Errorf("user repo: create audited: %w", err)
	}
	return nil
}

func (r *userRepository) UpdateAudited(user *models.User, entry *models.AdminAuditLog, columns ...string) error {
	if len(columns) == 0 {
		return errors.New("user repo: update audited: no column named")
	}
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		// The soft-delete scope makes this UPDATE … WHERE deleted_at IS NULL,
		// and the row lock it takes makes a concurrent deletion wait for, or
		// be seen by, this write.
		res, err := tx.Model(user).Select(columns...).Update(user)
		if err != nil {
			return taken(err)
		}
		if res.RowsAffected == 0 {
			return ErrUserDeleted
		}
		return createAuditLog(tx, entry)
	})
	if err != nil {
		return fmt.Errorf("user repo: update audited: %w", err)
	}
	return nil
}

func (r *userRepository) DeleteAudited(user *models.User, entry *models.AdminAuditLog) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		res, err := tx.Delete(user)
		if err != nil {
			return err
		}
		if res.RowsAffected == 0 {
			return ErrUserDeleted
		}
		// After the row is taken, not before: a writer of money in flight
		// reads the user row FOR SHARE, so one that got there first has
		// committed by now and is seen here, and one that comes later waits
		// for this transaction and finds the account gone.
		if err := refuseMoneyInFlightAnywhere(tx, user.ID); err != nil {
			return err
		}
		if err := refuseLastOwner(tx, user.ID); err != nil {
			return err
		}
		if err := (&customerIndividualProfileRepository{}).PurgeTx(tx, user.ID); err != nil {
			return err
		}
		if err := (&userPixKeyRepository{}).DeleteByUserTx(tx, user.ID); err != nil {
			return err
		}
		if err := (&userBankAccountRepository{}).DeleteByUserTx(tx, user.ID); err != nil {
			return err
		}
		if err := (&userLiquidationAddressRepository{}).DeactivateByUserTx(tx, user.ID); err != nil {
			return err
		}
		// The individual customer's id is the user's (KYB spec §3.2).
		if err := (&customerBankBeneficiaryRepository{}).PurgeByCustomerTx(tx, user.ID); err != nil {
			return err
		}
		if err := (&deviceTokenRepository{}).DeleteByUserTx(tx, user.ID); err != nil {
			return err
		}
		return createAuditLog(tx, entry)
	})
	if err != nil {
		return fmt.Errorf("user repo: delete audited: %w", err)
	}
	return nil
}

func (r *userRepository) RestoreAudited(user *models.User, entry *models.AdminAuditLog) error {
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		res, err := tx.WithTrashed().Where("deleted_at IS NOT NULL").Restore(user)
		if err != nil {
			return err
		}
		if res.RowsAffected == 0 {
			return ErrUserNotDeleted
		}
		if err := updateUserColumns(tx, user, []string{"sessions_revoked_at"}); err != nil {
			return err
		}
		return createAuditLog(tx, entry)
	})
	if err != nil {
		return fmt.Errorf("user repo: restore audited: %w", err)
	}
	user.DeletedAt.Valid, user.DeletedAt.Time = false, time.Time{}
	return nil
}

// The existence checks include soft-deleted rows: users.email and
// users.handle are plain unique indexes, so a trashed user still owns both and
// reusing either would fail at the database instead of with a domain error.
func (r *userRepository) ExistsByEmail(email string) (bool, error) {
	exists, err := facades.Orm().Query().WithTrashed().Model(&models.User{}).Where("email", email).Exists()
	if err != nil {
		return false, fmt.Errorf("user repo: exists by email: %w", err)
	}
	return exists, nil
}

func (r *userRepository) ExistsByEmailExcept(email string, excludeID uint) (bool, error) {
	exists, err := facades.Orm().Query().WithTrashed().Model(&models.User{}).
		Where("email", email).
		Where("id != ?", excludeID).Exists()
	if err != nil {
		return false, fmt.Errorf("user repo: exists by email except: %w", err)
	}
	return exists, nil
}

func (r *userRepository) ExistsByHandle(handle string) (bool, error) {
	exists, err := facades.Orm().Query().WithTrashed().Model(&models.User{}).Where("handle", handle).Exists()
	if err != nil {
		return false, fmt.Errorf("user repo: exists by handle: %w", err)
	}
	return exists, nil
}

func (r *userRepository) ExistsByHandleExcept(handle string, excludeID uint) (bool, error) {
	exists, err := facades.Orm().Query().WithTrashed().Model(&models.User{}).
		Where("handle", handle).
		Where("id != ?", excludeID).Exists()
	if err != nil {
		return false, fmt.Errorf("user repo: exists by handle except: %w", err)
	}
	return exists, nil
}

func (r *userRepository) Paginate(filters ListFilters) ([]models.User, int64, error) {
	q := facades.Orm().Query().Model(&models.User{})
	switch filters.Status {
	case models.UserStatusActive:
		q = q.Where("suspended_at IS NULL")
	case models.UserStatusSuspended:
		q = q.Where("suspended_at IS NOT NULL")
	case models.UserStatusDeleted:
		q = q.WithTrashed().Where("deleted_at IS NOT NULL")
	}
	if filters.Search != "" {
		like := likeContains(filters.Search)
		q = q.Where("email ILIKE ? OR handle ILIKE ?", like, like)
	}
	column, dir := userSort.parse(filters.Sort)
	q = q.OrderBy(column, dir)

	var users []models.User
	var total int64
	if err := q.Paginate(filters.Page, filters.PerPage, &users, &total); err != nil {
		return nil, 0, fmt.Errorf("user repo: paginate: %w", err)
	}
	return users, total, nil
}

var userSort = sortable{
	columns: map[string]string{"created_at": "created_at", "email": "email", "handle": "handle"},
	column:  "created_at",
	dir:     "desc",
}

// lockLiveUser takes the user's row FOR SHARE in q's transaction, and answers
// ErrUserDeleted for an account that is gone. It is how a write that puts
// money in flight for an account serializes with the account's deletion:
// FOR SHARE conflicts with the row lock DeleteAudited's soft delete takes, so
// whichever commits first is seen by the other — the deletion finds the money
// and refuses, or the writer waits for the deletion and finds the account gone.
// Two writers share the lock and never wait for each other.
func lockLiveUser(q orm.Query, userID uint) error {
	user, err := first[models.User](q.SharedLock().Where("id", userID), "lock live user")
	if err != nil {
		return err
	}
	if user == nil {
		return ErrUserDeleted
	}
	return nil
}

// updateUserColumns writes the named columns of user, and updated_at, to its
// row.
func updateUserColumns(q orm.Query, user *models.User, columns []string) error {
	_, err := q.Model(user).Select(columns...).Update(user)
	return err
}
