package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	ormmodel "github.com/goravel/framework/database/orm"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	enginefacades "github.com/macrowallets/waas/packages/rampengine/app/facades"
	enginemodels "github.com/macrowallets/waas/packages/rampengine/app/models"
	enginerepo "github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

var ErrRampCustomerNotFound = models.ErrRampCustomerNotFound
var ErrRampCustomerConflict = models.ErrRampCustomerConflict

type RampCustomers struct{ db.Base }

func NewRampCustomers() *RampCustomers { return &RampCustomers{Base: db.NewBase(nil)} }

func (r *RampCustomers) Find(ctx context.Context, accountID, id uuid.UUID) (*models.RampCustomer, error) {
	var row models.RampCustomer
	if err := r.Query(ctx).Where("account_id", accountID).Where("id", id).First(&row); err != nil {
		return nil, err
	}
	if row.ID == uuid.Nil {
		return nil, ErrRampCustomerNotFound
	}
	return &row, nil
}

func (r *RampCustomers) List(ctx context.Context, accountID uuid.UUID, page, limit int, search string) ([]models.RampCustomer, int64, error) {
	q := r.Query(ctx).Model(&models.RampCustomer{}).Where("account_id", accountID)
	if search != "" {
		q = q.Where("name ILIKE ? OR external_id ILIKE ?", "%"+search+"%", "%"+search+"%")
	}
	var rows []models.RampCustomer
	var total int64
	err := q.OrderBy("created_at", "desc").Paginate(page, limit, &rows, &total)
	return rows, total, err
}

func (r *RampCustomers) AccountHolder(ctx context.Context, accountID uuid.UUID) (*models.RampCustomer, error) {
	var row models.RampCustomer
	if err := r.Query(ctx).Where("account_id", accountID).Where("relationship", models.RampAccountHolder).First(&row); err != nil {
		return nil, err
	}
	if row.ID == uuid.Nil {
		return nil, ErrRampCustomerNotFound
	}
	return &row, nil
}

func (r *RampCustomers) FindByEngine(ctx context.Context, engineID uint) (*models.RampCustomer, error) {
	var row models.RampCustomer
	if err := r.Query(ctx).Where("engine_customer_id", engineID).First(&row); err != nil {
		return nil, err
	}
	if row.ID == uuid.Nil {
		return nil, ErrRampCustomerNotFound
	}
	return &row, nil
}

func (r *RampCustomers) ContactAndHolder(ctx context.Context, row *models.RampCustomer) (*enginemodels.User, *enginemodels.Customer, *enginemodels.CustomerMember, error) {
	q := enginefacades.Orm().WithContext(ctx)
	var contact enginemodels.User
	var holder enginemodels.Customer
	var member enginemodels.CustomerMember
	if err := q.Query().Where("id", row.EngineContactID).First(&contact); err != nil {
		return nil, nil, nil, err
	}
	if err := q.Query().Where("id", row.EngineCustomerID).Where("tenant_key", row.AccountID.String()).First(&holder); err != nil {
		return nil, nil, nil, err
	}
	if contact.ID == 0 || holder.ID == 0 {
		return nil, nil, nil, ErrRampCustomerNotFound
	}
	if err := q.Query().Where("customer_id", holder.ID).Where("user_id", contact.ID).First(&member); err != nil {
		return nil, nil, nil, err
	}
	if member.ID == 0 {
		return nil, nil, nil, ErrRampCustomerNotFound
	}
	return &contact, &holder, &member, nil
}

func (r *RampCustomers) Create(ctx context.Context, account *models.Account, draft models.RampCustomerDraft) (*models.RampCustomer, bool, error) {
	if account == nil || account.ID == uuid.Nil {
		return nil, false, fmt.Errorf("ramp customer: account required")
	}
	mode := "production"
	if account.Environment == "test" {
		mode = "sandbox"
	} else if account.Environment != "prod" {
		return nil, false, fmt.Errorf("ramp customer: invalid account environment")
	}
	id := uuid.New()
	row := &models.RampCustomer{ID: id, AccountID: account.ID, ExternalID: strings.TrimSpace(draft.ExternalID), Relationship: draft.Relationship, EntityType: draft.EntityType, Name: strings.TrimSpace(draft.Name), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if row.Relationship == models.RampAccountHolder {
		row.ExternalID = "account-holder"
	}
	var created bool
	err := enginefacades.Orm().WithContext(ctx).Transaction(func(tx contractsorm.Query) error {
		// One account lock covers the unique external reference and company-holder
		// slot; it is a database lock shared by all API processes.
		if _, err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "macro:ramp-customers:"+account.ID.String()); err != nil {
			return err
		}
		var existing models.RampCustomer
		if err := tx.Table("public.ramp_customers").Where("account_id", account.ID).Where("external_id", row.ExternalID).First(&existing); err != nil {
			return err
		}
		if existing.ID != uuid.Nil {
			if existing.Relationship != row.Relationship || existing.EntityType != row.EntityType {
				return ErrRampCustomerConflict
			}
			var contact enginemodels.User
			if err := tx.Where("id", existing.EngineContactID).First(&contact); err != nil {
				return err
			}
			wantEmail := draft.Email
			if row.EntityType == enginemodels.CustomerTypeBusiness {
				wantEmail = draft.RepresentativeEmail
			}
			if !strings.EqualFold(contact.Email, strings.TrimSpace(wantEmail)) {
				return ErrRampCustomerConflict
			}
			*row = existing
			return nil
		}
		email := strings.TrimSpace(draft.Email)
		if row.EntityType == enginemodels.CustomerTypeBusiness {
			email = strings.TrimSpace(draft.RepresentativeEmail)
		}
		// These contacts have no exposed authentication surface. The password
		// marker is deliberately not a valid password hash.
		contact := &enginemodels.User{Handle: strings.ReplaceAll(id.String(), "-", "")[:20], Email: email, Password: "!macro-ramp-contact"}
		if err := tx.Create(contact); err != nil {
			return err
		}
		individual := &enginemodels.Customer{Model: ormmodel.Model{ID: contact.ID}, Type: enginemodels.CustomerTypeIndividual, Status: enginemodels.CustomerStatusActive, TenantKey: account.ID.String(), Environment: mode}
		if err := tx.Create(individual); err != nil {
			return err
		}
		holder := individual
		if row.EntityType == enginemodels.CustomerTypeBusiness {
			var sequence struct{ ID uint }
			if err := tx.Raw("SELECT nextval('customers_business_id_seq') AS id").Scan(&sequence); err != nil {
				return err
			}
			digest := enginerepo.BusinessTaxIDDigest(enginemodels.TaxIdentity{Type: strings.ToLower(draft.TaxIDType), Country: strings.ToUpper(draft.IncorporationCountry)}, draft.TaxID)
			holder = &enginemodels.Customer{TaxIDHash: &digest, Model: ormmodel.Model{ID: sequence.ID}, Type: enginemodels.CustomerTypeBusiness, Status: enginemodels.CustomerStatusDraft, TenantKey: account.ID.String(), Environment: mode}
			if err := tx.Create(holder); err != nil {
				return err
			}
			profile := enginemodels.BusinessProfile{TaxIdentity: enginemodels.TaxIdentity{Type: strings.ToLower(draft.TaxIDType), Country: strings.ToUpper(draft.IncorporationCountry)}, TaxID: enginemodels.CanonicalBusinessTaxID(draft.TaxIDType, draft.TaxID), LegalName: row.Name, Email: strings.TrimSpace(draft.Email)}
			if !profile.TaxIdentity.FitsIncorporation(profile.TaxIdentity.Country) {
				return fmt.Errorf("invalid business tax identity")
			}
			encoded, err := marshalRampProfile(profile)
			if err != nil {
				return err
			}
			sealed, err := facades.Crypt().EncryptString(encoded)
			if err != nil {
				return err
			}
			if err := tx.Create(&enginemodels.CustomerBusinessProfile{CustomerID: holder.ID, Profile: sealed}); err != nil {
				return err
			}
		}
		for _, customerID := range distinctIDs(individual.ID, holder.ID) {
			member := &enginemodels.CustomerMember{CustomerID: customerID, UserID: contact.ID}
			if err := tx.Create(member); err != nil {
				return err
			}
			if _, err := tx.Exec("INSERT INTO model_has_roles (role_id, subject_type, subject_id) SELECT id, ?, ? FROM roles WHERE name = ? AND guard = ?", enginemodels.CustomerMemberMorphType, member.ID, "owner", enginemodels.CustomerGuard); err != nil {
				return err
			}
		}
		row.EngineContactID = contact.ID
		row.EngineCustomerID = holder.ID
		if err := tx.Table("public.ramp_customers").Create(row); err != nil {
			return err
		}
		created = true
		return nil
	})
	return row, created, err
}

func distinctIDs(a, b uint) []uint {
	if a == b {
		return []uint{a}
	}
	return []uint{a, b}
}

func marshalRampProfile(value any) (string, error) {
	b, err := json.Marshal(value)
	return string(b), err
}
