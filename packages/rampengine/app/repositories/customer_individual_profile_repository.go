package repositories

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/digits"
	"github.com/macrowallets/waas/packages/rampengine/app/support/keyedhash"
)

// ErrTaxIDInUse refuses a CPF another account's retained profile declares.
var ErrTaxIDInUse = errors.New("tax id held by another account")

// ErrPhoneInUse refuses a phone another account's retained profile declares.
var ErrPhoneInUse = errors.New("phone held by another account")

// ErrKycProfilePurged refuses an edit of a profile whose declaration was
// purged with its account: there is nothing left to edit.
var ErrKycProfilePurged = errors.New("kyc profile purged")

// ErrKycProfileNotPurged refuses releasing the reservations of a live
// account: Upsert recomputes both digests from the declaration on every
// submission, so the release would be undone by the next one. A restored
// account is live though its profile stayed purged — its CPF is its own
// again, and a release would hand it to someone else.
var ErrKycProfileNotPurged = errors.New("kyc profile not purged")

// The identity fields a profile reserves, as IdentityHolder names them.
const (
	IdentityFieldTaxID = "tax_id"
	IdentityFieldPhone = "phone"
)

// AdminKycProfile is the retained profile as an operator reads it: what the
// user declared — nothing once purged with the account — and which identities
// it keeps reserved.
type AdminKycProfile struct {
	UserID        uint
	UpdatedAt     *carbon.DateTime
	Purged        bool
	TaxIDReserved bool
	PhoneReserved bool
	// Profile is nil when Purged.
	Profile *models.KycProfile
}

// IdentityHolder is an account whose profile reserves a CPF or phone, and
// which of the two (IdentityField*). User is loaded with trashed: a deleted
// account keeps its reservation.
type IdentityHolder struct {
	User  models.User
	Field string
}

// CustomerIndividualProfileRepository is the persistence boundary for the
// retained KYC profile of an individual customer — the fields its person
// typed, kept so a provider added later asks only for the photographs again.
//
// The submission's methods name the customer. Purge and the admin's name the
// account: they act on its individual customer's profile, whose id is the
// account's.
//
// It takes and returns models.KycProfile, the type app/adapters/ramp aliases
// as ramp.Profile (ports.go: "type Profile = models.KycProfile") — so a
// caller holding a ramp.Profile value passes it here directly, with no
// conversion, and this interface reads exactly like the brief's literal
// signature. It is spelled with models.KycProfile rather than ramp.Profile
// because app/adapters/ramp already imports this package (the router's own
// repository dependencies), so a repository importing ramp back would be a
// build cycle; models never imports ramp, and repositories already imports
// models everywhere. The type alias is what makes this free: it is not a
// second declaration to drift from ramp.Profile, it is the same type under a
// different import path.
//
// The parameter being a real struct, not a JSON string, is the point: an
// image has no field to travel in, so a caller cannot hand this an image even
// by mistake — the boundary is enforced by the compiler, not by convention at
// one call site.
type CustomerIndividualProfileRepository interface {
	// Upsert stores profile, encrypted at rest with facades.Crypt(), and the
	// digests of the CPF and phone it declares. One row per customer — a
	// second submission replaces the first rather than appending, since the
	// row is the customer's current declaration, not a history of it.
	//
	// ErrTaxIDInUse or ErrPhoneInUse when another account's profile declares
	// the same CPF or phone: the unique index settles two accounts that both
	// passed CheckIdentityAvailable, and the loser's row is left as it was.
	Upsert(customerID uint, profile models.KycProfile) error
	// Find returns (nil, nil) when the customer has never submitted a profile,
	// which is always the answer for a business.
	Find(customerID uint) (*models.KycProfile, error)
	// CheckIdentityAvailable answers ErrTaxIDInUse when another account's
	// profile declares the tax id declared does, else ErrPhoneInUse when one
	// declares its phone. Only TaxID with its type and issuing country (and,
	// for a profile older than them, Address.Country) and Phone are read,
	// digested exactly as Upsert digests them; an empty one is not checked.
	// The tax id is asked first: it is the refusal nothing fixes, and naming
	// the phone first would send the user to change it only to be refused
	// again.
	CheckIdentityAvailable(customerID uint, declared models.KycProfile) error
	// Purge erases the declaration and the phone digest, and keeps the CPF
	// digest. Called from the account soft-delete path (see admin
	// users.Service.Delete): the profile "morre com o usuário", and
	// CascadeOnDelete alone never fires because users are soft-deleted, not
	// hard-deleted. The CPF digest outlives it on purpose — decided on
	// 28/09/2026, a deleted account keeps its CPF reserved, because its
	// customers still exist at the providers, which would refuse the same CPF
	// from a new account anyway. Releasing it is a support action. The phone is
	// not kept: a number changes hands, and a recycled one would stay taken
	// with no one to release it. A no-op, not an error, for a user who never
	// submitted a profile; Find answers (nil, nil) afterwards.
	Purge(userID uint) error
	// FindForAdmin reads the profile the way an operator sees it, purged or
	// not. (nil, nil) when the user never saved one.
	FindForAdmin(userID uint) (*AdminKycProfile, error)
	// UpdateForAdmin runs edit on the decrypted declaration with the row
	// locked, then stores what it leaves with its digests recomputed as
	// Upsert computes them (IdentityDigests) — the caller canonicalises the
	// tax id and phone first, as a submission does — and writes the audit row
	// record builds from the declaration before and after, in the same
	// transaction. An edit that changes nothing writes and records nothing.
	// ErrKycProfilePurged for a purged profile, ErrTaxIDInUse/ErrPhoneInUse
	// when another account holds what the edit declares; an error from edit
	// rolls back. (nil, nil) when the user never saved one.
	UpdateForAdmin(userID uint, edit func(*models.KycProfile) error,
		record func(before, after models.KycProfile) *models.AdminAuditLog) (*AdminKycProfile, error)
	// ReleaseIdentity drops the tax id and/or phone reservation of a deleted
	// account's purged profile, writing the audit row record builds from the
	// fields it actually dropped in the same transaction; nothing left to drop
	// writes and records nothing. ErrKycProfileNotPurged for a live account,
	// restored ones included; (nil, nil) when the user never saved one.
	ReleaseIdentity(userID uint, taxID, phone bool,
		record func(released []string) *models.AdminAuditLog) (*AdminKycProfile, error)
	// FindIdentityHolders names the accounts reserving the tax id or the
	// phone declared — what the uniqueness index would refuse. It reads and
	// digests declared exactly as CheckIdentityAvailable does: TaxID by its
	// type and issuing country (with neither, a CPF; with only a country other
	// than Brazil, a tax id retained before its type was) and Phone (E.164, as
	// stored). An empty one is not looked up.
	FindIdentityHolders(declared models.KycProfile) ([]IdentityHolder, error)
}

type customerIndividualProfileRepository struct{}

// NewCustomerIndividualProfileRepository returns a CustomerIndividualProfileRepository backed by
// the default ORM connection.
func NewCustomerIndividualProfileRepository() CustomerIndividualProfileRepository {
	return &customerIndividualProfileRepository{}
}

func (r *customerIndividualProfileRepository) Upsert(customerID uint, profile models.KycProfile) error {
	sealed, err := sealProfile(profile)
	if err != nil {
		return err
	}

	existing, err := r.find(customerID)
	if err != nil {
		return err
	}
	if existing != nil {
		existing.Profile = sealed
		existing.TaxIDHash, existing.PhoneHash = IdentityDigests(profile)
		if err := facades.Orm().Query().Save(existing); err != nil {
			return fmt.Errorf("customer individual profile repo: update: %w", taken(err))
		}
		return nil
	}

	row := &models.CustomerIndividualProfile{CustomerID: customerID, Profile: sealed}
	row.TaxIDHash, row.PhoneHash = IdentityDigests(profile)
	if err := facades.Orm().Query().Create(row); err != nil {
		return fmt.Errorf("customer individual profile repo: create: %w", taken(err))
	}
	return nil
}

// sealProfile encodes and encrypts a declaration for the profile column.
func sealProfile(profile models.KycProfile) (string, error) {
	encoded, err := json.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("customer individual profile repo: encode: %w", err)
	}
	sealed, err := facades.Crypt().EncryptString(string(encoded))
	if err != nil {
		return "", fmt.Errorf("customer individual profile repo: encrypt: %w", err)
	}
	return sealed, nil
}

// IdentityDigests are the two digests a declaration is unique by. Upsert,
// CheckIdentityAvailable and the identity backfill (migration
// 20260928140100) all read them from here, so a profile is digested one way
// whichever of them sees it. nil for what it does not declare, which the
// partial indexes ignore.
func IdentityDigests(profile models.KycProfile) (taxID, phone *string) {
	return taxIDHash(profile), phoneHash(profile.Phone)
}

// taxIDHash is the digest a tax id is unique by, taken by the type it was
// declared as (KycProfile.TaxIdentity), never by the address.
//
// A CPF is its digits, the form every provider's tax_id takes, so
// "390.533.447-05" and "39053344705" are one person — wherever its holder
// lives, and on a profile with no country, Brazilian as every one retained
// before the country was.
//
// A passport is its letters and digits, uppercased, under PASSPORT and the
// country that issued it: the same number from two countries is two people,
// and no passport is ever a CPF, not even a Brazilian one with a CPF's digits.
//
// A tax id retained before its type was, declared under a country other than
// Brazil, keeps the digest it was reserved under: its letters and digits
// under that country (digits alone had made "X1234567Z" and "Y1234567Z" one
// person).
func taxIDHash(profile models.KycProfile) *string {
	identity := profile.TaxIdentity()
	if identity.IsCPF() {
		return identityHash(digits.Only(profile.TaxID))
	}
	document := alphanumericUpper(profile.TaxID)
	if document == "" {
		return nil
	}
	if strings.TrimSpace(profile.TaxIDType) == "" {
		return identityHash(identity.Country + ":" + document)
	}
	return identityHash(strings.ToUpper(identity.Type) + ":" + identity.Country + ":" + document)
}

// alphanumericUpper keeps the letters and digits of value, uppercased.
func alphanumericUpper(value string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(value) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// phoneHash is the digest the phone is unique by: of the E.164 string the
// submission canonicalised it into (kyc.applicationFrom), so the same number
// typed two ways arrives here as one.
func phoneHash(phone string) *string {
	return identityHash(strings.TrimSpace(phone))
}

func identityHash(normalized string) *string {
	if normalized == "" {
		return nil
	}
	digest := keyedhash.Hex(normalized)
	return &digest
}

func (r *customerIndividualProfileRepository) CheckIdentityAvailable(customerID uint, declared models.KycProfile) error {
	taxDigest, phoneDigest := IdentityDigests(declared)
	for _, claim := range []struct {
		column string
		digest *string
		taken  error
	}{
		{"tax_id_hash", taxDigest, ErrTaxIDInUse},
		{"phone_hash", phoneDigest, ErrPhoneInUse},
	} {
		if claim.digest == nil {
			continue
		}
		held, err := facades.Orm().Query().Model(&models.CustomerIndividualProfile{}).
			Where(claim.column, *claim.digest).Where("customer_id <> ?", customerID).
			Where("customer_id IN (SELECT id FROM customers WHERE tenant_key = (SELECT tenant_key FROM customers WHERE id = ?))", customerID).Exists()
		if err != nil {
			return fmt.Errorf("customer individual profile repo: check %s: %w", claim.column, err)
		}
		if held {
			return claim.taken
		}
	}
	return nil
}

func (r *customerIndividualProfileRepository) find(customerID uint) (*models.CustomerIndividualProfile, error) {
	return first[models.CustomerIndividualProfile](facades.Orm().Query().Where("customer_id", customerID),
		"customer individual profile repo: find")
}

func (r *customerIndividualProfileRepository) Find(customerID uint) (*models.KycProfile, error) {
	row, err := r.find(customerID)
	if err != nil || row == nil || row.Profile == "" {
		return nil, err
	}
	return openProfile(row)
}

// openProfile decrypts and decodes the declaration a row holds.
func openProfile(row *models.CustomerIndividualProfile) (*models.KycProfile, error) {
	plaintext, err := facades.Crypt().DecryptString(row.Profile)
	if err != nil {
		return nil, fmt.Errorf("customer individual profile repo: decrypt: %w", err)
	}
	var profile models.KycProfile
	if err := json.Unmarshal([]byte(plaintext), &profile); err != nil {
		return nil, fmt.Errorf("customer individual profile repo: decode: %w", err)
	}
	return &profile, nil
}

func (r *customerIndividualProfileRepository) Purge(userID uint) error {
	return r.PurgeTx(facades.Orm().Query(), userID)
}

// PurgeTx is Purge through q, for the account deletion that purges the
// profile in its own transaction (userRepository.DeleteAudited).
func (r *customerIndividualProfileRepository) PurgeTx(q orm.Query, userID uint) error {
	if _, err := q.Model(&models.CustomerIndividualProfile{}).
		Where("customer_id", userID).Update(map[string]any{"profile": "", "phone_hash": nil}); err != nil {
		return fmt.Errorf("customer individual profile repo: purge: %w", err)
	}
	return nil
}

func (r *customerIndividualProfileRepository) FindForAdmin(userID uint) (*AdminKycProfile, error) {
	row, err := r.find(userID)
	if err != nil || row == nil {
		return nil, err
	}
	return adminKycProfile(row)
}

// adminKycProfile opens the row's declaration unless it was purged.
func adminKycProfile(row *models.CustomerIndividualProfile) (*AdminKycProfile, error) {
	result := &AdminKycProfile{
		UserID: row.CustomerID, UpdatedAt: row.UpdatedAt, Purged: row.Profile == "",
		TaxIDReserved: row.TaxIDHash != nil, PhoneReserved: row.PhoneHash != nil,
	}
	if result.Purged {
		return result, nil
	}
	profile, err := openProfile(row)
	if err != nil {
		return nil, err
	}
	result.Profile = profile
	return result, nil
}

func (r *customerIndividualProfileRepository) UpdateForAdmin(
	userID uint, edit func(*models.KycProfile) error,
	record func(before, after models.KycProfile) *models.AdminAuditLog,
) (*AdminKycProfile, error) {
	var result *AdminKycProfile
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		row, err := findForUpdate(tx, userID)
		if err != nil || row == nil {
			return err
		}
		if row.Profile == "" {
			return ErrKycProfilePurged
		}
		profile, err := openProfile(row)
		if err != nil {
			return err
		}
		before := *profile
		if err := edit(profile); err != nil {
			return err
		}
		// An edit that leaves the declaration as it was writes and records
		// nothing.
		if reflect.DeepEqual(before, *profile) {
			result, err = adminKycProfile(row)
			return err
		}
		if row.Profile, err = sealProfile(*profile); err != nil {
			return err
		}
		row.TaxIDHash, row.PhoneHash = IdentityDigests(*profile)
		if err := tx.Save(row); err != nil {
			return fmt.Errorf("customer individual profile repo: admin update: %w", taken(err))
		}
		if err := createAuditLog(tx, record(before, *profile)); err != nil {
			return fmt.Errorf("customer individual profile repo: admin update: audit: %w", err)
		}
		result, err = adminKycProfile(row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *customerIndividualProfileRepository) ReleaseIdentity(
	userID uint, taxID, phone bool, record func(released []string) *models.AdminAuditLog,
) (*AdminKycProfile, error) {
	var result *AdminKycProfile
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		// The account first, locked, in the order a delete or a restore takes
		// the account and then the profile: a restore committing under the
		// release waits for it, or is seen by it.
		user, err := first[models.User](tx.WithTrashed().LockForUpdate().Where("id", userID),
			"customer individual profile repo: release identity: account")
		if err != nil || user == nil {
			return err
		}
		row, err := findForUpdate(tx, userID)
		if err != nil || row == nil {
			return err
		}
		if !user.DeletedAt.Valid || row.Profile != "" {
			return ErrKycProfileNotPurged
		}
		var released []string
		if taxID && row.TaxIDHash != nil {
			row.TaxIDHash = nil
			released = append(released, IdentityFieldTaxID)
		}
		if phone && row.PhoneHash != nil {
			row.PhoneHash = nil
			released = append(released, IdentityFieldPhone)
		}
		// Nothing reserved of what was asked: nothing to write or record.
		if len(released) == 0 {
			result, err = adminKycProfile(row)
			return err
		}
		if err := tx.Save(row); err != nil {
			return fmt.Errorf("customer individual profile repo: release identity: %w", err)
		}
		if err := createAuditLog(tx, record(released)); err != nil {
			return fmt.Errorf("customer individual profile repo: release identity: audit: %w", err)
		}
		result, err = adminKycProfile(row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func findForUpdate(tx orm.Query, customerID uint) (*models.CustomerIndividualProfile, error) {
	return first[models.CustomerIndividualProfile](tx.LockForUpdate().Where("customer_id", customerID),
		"customer individual profile repo: find for update")
}

func (r *customerIndividualProfileRepository) FindIdentityHolders(declared models.KycProfile) ([]IdentityHolder, error) {
	taxDigest, phoneDigest := IdentityDigests(declared)
	var holders []IdentityHolder
	for _, claim := range []struct {
		field, column string
		digest        *string
	}{
		{IdentityFieldTaxID, "tax_id_hash", taxDigest},
		{IdentityFieldPhone, "phone_hash", phoneDigest},
	} {
		if claim.digest == nil {
			continue
		}
		row, err := first[models.CustomerIndividualProfile](facades.Orm().Query().Where(claim.column, *claim.digest),
			"customer individual profile repo: find identity holder")
		if err != nil {
			return nil, err
		}
		if row == nil {
			continue
		}
		user, err := first[models.User](facades.Orm().Query().WithTrashed().Where("id", row.CustomerID),
			"customer individual profile repo: load identity holder")
		if err != nil {
			return nil, err
		}
		if user != nil {
			holders = append(holders, IdentityHolder{User: *user, Field: claim.field})
		}
	}
	return holders, nil
}
