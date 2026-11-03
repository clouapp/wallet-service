package repositories

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrDeviceTokenNotFound is returned by DeleteOwned when the user holds no
// registration of the token -- including when another user does, which the
// caller must not be able to tell apart.
var ErrDeviceTokenNotFound = errors.New("device token not found")

// DeviceTokenRepository is the persistence boundary for devices registered to
// receive push notifications.
type DeviceTokenRepository interface {
	// Register stores device, keyed by its token. A token already registered
	// is rewritten in place, moving to device.UserID if another account held
	// it: one device holds one token, and it notifies whoever signed in last.
	Register(device *models.DeviceToken) error
	// KeepNewest forgets all but the user's keep most recently registered
	// devices.
	KeepNewest(userID uint, keep int) error
	// FindByToken returns the registration of token, or (nil, nil).
	FindByToken(token string) (*models.DeviceToken, error)
	ListByUser(userID uint) ([]models.DeviceToken, error)
	// DeleteOwned removes the user's registration of token, and answers
	// ErrDeviceTokenNotFound when they hold none.
	DeleteOwned(userID uint, token string) error
	// DeleteRegistration forgets the registration as it was read: one made
	// again since -- by the same device signing in anew, or by another account
	// on it -- is newer and stays. Deleting one already gone is not an error.
	DeleteRegistration(device *models.DeviceToken) error
	// DeleteByToken forgets token whoever holds it, for a token the vendor
	// declared dead. Deleting one already gone is not an error.
	DeleteByToken(token string) error
}

type deviceTokenRepository struct{}

// NewDeviceTokenRepository returns a DeviceTokenRepository backed by the
// default ORM connection.
func NewDeviceTokenRepository() DeviceTokenRepository {
	return &deviceTokenRepository{}
}

func (r *deviceTokenRepository) Register(device *models.DeviceToken) error {
	err := r.updateOrCreate(device)
	if isUniqueViolation(err) {
		// A concurrent registration of the same token inserted it between this
		// one's lookup and its insert. The row exists now, so a second attempt
		// finds it and rewrites it like any re-registration.
		err = r.updateOrCreate(device)
	}
	if err != nil {
		return fmt.Errorf("device token repo: register: %w", err)
	}
	return nil
}

// updateOrCreate writes every column but the token from a map rather than a
// struct, because a struct's nil APNsEnvironment would be skipped as a zero
// value and leave the previous one in place.
func (r *deviceTokenRepository) updateOrCreate(device *models.DeviceToken) error {
	return facades.Orm().Query().UpdateOrCreate(device,
		map[string]any{"token": device.Token},
		map[string]any{
			"user_id":          device.UserID,
			"platform":         device.Platform,
			"apns_environment": device.APNsEnvironment,
			"locale":           device.Locale,
		},
	)
}

func (r *deviceTokenRepository) KeepNewest(userID uint, keep int) error {
	var newest []uint
	if err := facades.Orm().Query().Model(&models.DeviceToken{}).Where("user_id", userID).
		Order("updated_at desc").Order("id desc").Limit(keep).Pluck("id", &newest); err != nil {
		return fmt.Errorf("device token repo: keep newest: read: %w", err)
	}
	if _, err := facades.Orm().Query().Where("user_id", userID).Where("id NOT IN ?", newest).
		Delete(&models.DeviceToken{}); err != nil {
		return fmt.Errorf("device token repo: keep newest: delete: %w", err)
	}
	return nil
}

func (r *deviceTokenRepository) FindByToken(token string) (*models.DeviceToken, error) {
	return first[models.DeviceToken](facades.Orm().Query().Where("token", token), "device token repo: find by token")
}

func (r *deviceTokenRepository) ListByUser(userID uint) ([]models.DeviceToken, error) {
	var devices []models.DeviceToken
	if err := facades.Orm().Query().Where("user_id", userID).Order("id asc").Find(&devices); err != nil {
		return nil, fmt.Errorf("device token repo: list by user: %w", err)
	}
	return devices, nil
}

func (r *deviceTokenRepository) DeleteOwned(userID uint, token string) error {
	res, err := facades.Orm().Query().Where("user_id", userID).Where("token", token).Delete(&models.DeviceToken{})
	if err != nil {
		return fmt.Errorf("device token repo: delete owned: %w", err)
	}
	if res.RowsAffected == 0 {
		return ErrDeviceTokenNotFound
	}
	return nil
}

func (r *deviceTokenRepository) DeleteRegistration(device *models.DeviceToken) error {
	query := facades.Orm().Query().Where("id", device.ID)
	if device.UpdatedAt != nil {
		query = query.Where("updated_at <= ?", device.UpdatedAt.StdTime())
	}
	if _, err := query.Delete(&models.DeviceToken{}); err != nil {
		return fmt.Errorf("device token repo: delete registration: %w", err)
	}
	return nil
}

func (r *deviceTokenRepository) DeleteByToken(token string) error {
	if _, err := facades.Orm().Query().Where("token", token).Delete(&models.DeviceToken{}); err != nil {
		return fmt.Errorf("device token repo: delete by token: %w", err)
	}
	return nil
}

// DeleteByUserTx forgets every device of the user through q, for the account
// deletion that removes them in its own transaction (userRepository.DeleteAudited).
func (r *deviceTokenRepository) DeleteByUserTx(q orm.Query, userID uint) error {
	if _, err := q.Where("user_id", userID).Delete(&models.DeviceToken{}); err != nil {
		return fmt.Errorf("device token repo: delete by user: %w", err)
	}
	return nil
}
