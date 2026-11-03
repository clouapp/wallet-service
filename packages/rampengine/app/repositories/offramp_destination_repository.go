package repositories

import (
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// OfframpDestinationRepository is the persistence boundary for
// OfframpDestination records. A destination is identified by its customer,
// provider, key hash and document hash; DestinationRef is what it holds.
type OfframpDestinationRepository interface {
	// FindRef returns the provider's id for the destination, or "" when the
	// provider never registered it. A row still being registered (any state
	// but ready) has no ref to hand out.
	FindRef(destination models.OfframpDestination) (string, error)
	// Upsert records the destination, replacing the ref of one already
	// recorded under the same identity.
	Upsert(destination models.OfframpDestination) error
	// FindForRegistration returns the row of the identity in any state, or
	// (nil, nil) when there is none.
	FindForRegistration(destination models.OfframpDestination) (*models.OfframpDestination, error)
	// CreatePending writes the row of a registration about to be sent, and
	// says whether it wrote it: two callers racing for one identity write one
	// row between them.
	CreatePending(row *models.OfframpDestination) (bool, error)
	// SaveRegistration writes the registration fields of the row (ref, status,
	// attempt, key, hash, deadline, failure code) in one statement.
	SaveRegistration(row *models.OfframpDestination) error
	// DeleteByCustomerAndProvider forgets every destination this customer has
	// with the provider. A ref belongs to the provider-side customer it was
	// registered against, so a relink to another one invalidates all of them
	// at once.
	DeleteByCustomerAndProvider(customerID uint, provider string) error
}

type offrampDestinationRepository struct{}

func NewOfframpDestinationRepository() OfframpDestinationRepository {
	return &offrampDestinationRepository{}
}

func (r *offrampDestinationRepository) FindRef(destination models.OfframpDestination) (string, error) {
	var found models.OfframpDestination
	// Column by column: a struct condition skips zero values, and an empty
	// document hash is a value here.
	err := facades.Orm().Query().
		Where("customer_id", destination.CustomerID).
		Where("provider", destination.Provider).
		Where("pix_key_hash", destination.PixKeyHash).
		Where("document_hash", destination.DocumentHash).
		Where("status", models.DestinationReady).
		First(&found)
	if err != nil {
		return "", fmt.Errorf("offramp destination repo: find ref: %w", err)
	}
	return found.DestinationRef, nil
}

// Upsert is one statement so two withdrawals recording the same destination
// at once cannot both insert. The later ref wins, as the later registration
// does upstream.
func (r *offrampDestinationRepository) Upsert(destination models.OfframpDestination) error {
	_, err := facades.Orm().Query().Exec(`
		INSERT INTO offramp_destinations
			(customer_id, provider, pix_key_hash, document_hash, destination_ref, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NOW(), NOW())
		ON CONFLICT (customer_id, provider, pix_key_hash, document_hash)
		DO UPDATE SET destination_ref = EXCLUDED.destination_ref, status = 'ready', updated_at = NOW()`,
		destination.CustomerID, destination.Provider, destination.PixKeyHash,
		destination.DocumentHash, destination.DestinationRef)
	if err != nil {
		return fmt.Errorf("offramp destination repo: upsert: %w", err)
	}
	return nil
}

func (r *offrampDestinationRepository) FindForRegistration(destination models.OfframpDestination) (*models.OfframpDestination, error) {
	return first[models.OfframpDestination](facades.Orm().Query().
		Where("customer_id", destination.CustomerID).
		Where("provider", destination.Provider).
		Where("pix_key_hash", destination.PixKeyHash).
		Where("document_hash", destination.DocumentHash),
		"offramp destination repo: find for registration")
}

// CreatePending is one statement: the unique identity decides which of two
// concurrent callers writes the row, and the loser reads the winner's.
func (r *offrampDestinationRepository) CreatePending(row *models.OfframpDestination) (bool, error) {
	var id uint
	err := facades.Orm().Query().Raw(`
		INSERT INTO offramp_destinations
			(customer_id, provider, pix_key_hash, document_hash, destination_ref, status, name,
			 registration_idempotency_key, registration_request_hash, registration_attempt,
			 retry_deadline, provider_customer_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', 'pending', ?, ?, ?, ?, ?, ?, NOW(), NOW())
		ON CONFLICT (customer_id, provider, pix_key_hash, document_hash) DO NOTHING
		RETURNING id`,
		row.CustomerID, row.Provider, row.PixKeyHash, row.DocumentHash, row.Name,
		row.RegistrationIdempotencyKey, row.RegistrationRequestHash, row.RegistrationAttempt,
		row.RetryDeadline, row.ProviderCustomerID).Scan(&id)
	if err != nil {
		return false, fmt.Errorf("offramp destination repo: create pending: %w", err)
	}
	if id == 0 {
		return false, nil
	}
	row.ID = id
	row.Status = models.DestinationPending
	return true, nil
}

func (r *offrampDestinationRepository) SaveRegistration(row *models.OfframpDestination) error {
	_, err := facades.Orm().Query().Model(&models.OfframpDestination{}).Where("id", row.ID).Update(map[string]any{
		"destination_ref":              row.DestinationRef,
		"status":                       row.Status,
		"name":                         row.Name,
		"registration_idempotency_key": row.RegistrationIdempotencyKey,
		"registration_request_hash":    row.RegistrationRequestHash,
		"registration_attempt":         row.RegistrationAttempt,
		"retry_deadline":               row.RetryDeadline,
		"failure_code":                 row.FailureCode,
		"provider_customer_id":         row.ProviderCustomerID,
	})
	if err != nil {
		return fmt.Errorf("offramp destination repo: save registration: %w", err)
	}
	return nil
}

func (r *offrampDestinationRepository) DeleteByCustomerAndProvider(customerID uint, provider string) error {
	if err := deleteOfframpDestinations(facades.Orm().Query(), customerID, provider); err != nil {
		return fmt.Errorf("offramp destination repo: delete by customer and provider: %w", err)
	}
	return nil
}

// deleteOfframpDestinations forgets every destination the customer has with
// the provider on q: the one delete a relink, an unlink and a detach share,
// since each leaves the provider-side customer those refs were registered
// against.
func deleteOfframpDestinations(q orm.Query, customerID uint, provider string) error {
	_, err := q.Where("customer_id", customerID).Where("provider", provider).Delete(&models.OfframpDestination{})
	return err
}
