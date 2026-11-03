package repositories

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrCustomerLastOwner refuses deleting the only live owner of a business that
// is neither closed nor deleted (spec §18.5): the company — a draft too, which
// may already exist at a provider — would be left with nobody to answer for
// it. An admin closes it first.
var ErrCustomerLastOwner = errors.New("the only owner of a live business")

// CustomerMemberRepository is the persistence boundary for customer_members,
// the users who act for a customer.
type CustomerMemberRepository interface {
	// FindByCustomerAndUser is the user's membership of the customer, with the
	// customer loaded; nil when the user is not a member, which is also the
	// answer for a customer that does not exist — a deleted one included.
	FindByCustomerAndUser(customerID, userID uint) (*models.CustomerMember, error)
	// MembersOf is the ids of the users who act for the customer: the user
	// alone for an individual. What happens to a customer is told to each of
	// them (spec §2, SSE and push).
	MembersOf(customerID uint) ([]uint, error)
	// ListByCustomers is the memberships of customerIDs, oldest first, each
	// with its user — a deleted account included, who still is who held the
	// money.
	ListByCustomers(customerIDs []uint) ([]models.CustomerMember, error)
	// ListByUser is the user's memberships of live customers, each with its
	// customer, in CustomerRepository.ListByMember's order.
	ListByUser(userID uint) ([]models.CustomerMember, error)
	// ListByBusinessTaxIDDigest is the memberships of every business holding
	// the document digest names (BusinessTaxIDDigest) — a draft, a closed or a
	// deleted one included — each with its user, deleted accounts included.
	ListByBusinessTaxIDDigest(digest string) ([]models.CustomerMember, error)
}

type customerMemberRepository struct{}

// NewCustomerMemberRepository returns a CustomerMemberRepository backed by the
// default ORM connection.
func NewCustomerMemberRepository() CustomerMemberRepository {
	return &customerMemberRepository{}
}

func (r *customerMemberRepository) FindByCustomerAndUser(customerID, userID uint) (*models.CustomerMember, error) {
	// The customer's own scope hides a deleted customer from With, not the
	// membership pointing at it: without the EXISTS the membership came back
	// holding no customer.
	return first[models.CustomerMember](facades.Orm().Query().With("Customer").
		Where("customer_id", customerID).Where("user_id", userID).
		Where("EXISTS (SELECT 1 FROM customers WHERE customers.id = customer_members.customer_id AND customers.deleted_at IS NULL)"),
		"customer member repo: find by customer and user")
}

func (r *customerMemberRepository) MembersOf(customerID uint) ([]uint, error) {
	var userIDs []uint
	if err := facades.Orm().Query().Model(&models.CustomerMember{}).
		Where("customer_id", customerID).Order("user_id").Pluck("user_id", &userIDs); err != nil {
		return nil, fmt.Errorf("customer member repo: members of: %w", err)
	}
	return userIDs, nil
}

func (r *customerMemberRepository) ListByCustomers(customerIDs []uint) ([]models.CustomerMember, error) {
	members := []models.CustomerMember{}
	if len(customerIDs) == 0 {
		return members, nil
	}
	if err := facades.Orm().Query().
		With("User", func(users orm.Query) orm.Query { return users.WithTrashed() }).
		Where("customer_id IN ?", customerIDs).Order("id").Get(&members); err != nil {
		return nil, fmt.Errorf("customer member repo: list by customers: %w", err)
	}
	return members, nil
}

func (r *customerMemberRepository) ListByUser(userID uint) ([]models.CustomerMember, error) {
	var members []models.CustomerMember
	if err := facades.Orm().Query().With("Customer").
		Join("JOIN customers ON customers.id = customer_members.customer_id AND customers.deleted_at IS NULL").
		Where("customer_members.user_id", userID).
		Order("customers.type = 'business'").Order("customers.created_at").Order("customers.id").
		Select("customer_members.*").
		Get(&members); err != nil {
		return nil, fmt.Errorf("customer member repo: list by user: %w", err)
	}
	return members, nil
}

// refuseLastOwner answers ErrCustomerLastOwner when userID is the only live
// owner of a business that is neither closed nor deleted, read through tx: the
// deletion's own transaction, after it took the user's row, so a business
// opened concurrently (CreateBusinessDraft reads the row FOR SHARE) is either
// seen here or refused for the deleted account.
func refuseLastOwner(tx orm.Query, userID uint) error {
	var held []uint
	if err := tx.Raw(`SELECT c.id FROM customers c
		JOIN customer_members m ON m.customer_id = c.id AND m.user_id = ?
		WHERE c.type = ? AND c.status <> ? AND c.deleted_at IS NULL
		AND `+ownerHeld("m")+`
		AND NOT EXISTS (SELECT 1 FROM customer_members o JOIN users u ON u.id = o.user_id AND u.deleted_at IS NULL
			WHERE o.customer_id = c.id AND o.user_id <> m.user_id AND `+ownerHeld("o")+`)
		LIMIT 1`,
		userID, models.CustomerTypeBusiness, models.CustomerStatusClosed,
		models.CustomerMemberMorphType, models.CustomerOwnerRoleName, models.CustomerGuard,
		models.CustomerMemberMorphType, models.CustomerOwnerRoleName, models.CustomerGuard,
	).Scan(&held); err != nil {
		return fmt.Errorf("read businesses the user owns alone: %w", err)
	}
	if len(held) > 0 {
		return ErrCustomerLastOwner
	}
	return nil
}

// ownerHeld is the condition that the membership aliased member holds the
// owner role; it takes the morph type, the role name and its guard.
func ownerHeld(member string) string {
	return `EXISTS (SELECT 1 FROM model_has_roles mhr JOIN roles r ON r.id = mhr.role_id
		WHERE mhr.subject_type = ? AND mhr.subject_id = ` + member + `.id AND r.name = ? AND r.guard = ?)`
}

func (r *customerMemberRepository) ListByBusinessTaxIDDigest(digest string) ([]models.CustomerMember, error) {
	members := []models.CustomerMember{}
	if err := facades.Orm().Query().
		With("User", func(users orm.Query) orm.Query { return users.WithTrashed() }).
		Join("JOIN customers ON customers.id = customer_members.customer_id").
		Where("customers.type", models.CustomerTypeBusiness).Where("customers.tax_id_hash", digest).
		Order("customers.id").Order("customer_members.id").
		Select("customer_members.*").
		Get(&members); err != nil {
		return nil, fmt.Errorf("customer member repo: list by business tax id: %w", err)
	}
	return members, nil
}
