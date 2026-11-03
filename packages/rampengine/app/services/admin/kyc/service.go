// Package kyc is the admin's hands on a user's retained KYC profile and the
// identity reservations it holds: reading it decrypted, editing it, releasing
// a deleted account's CPF or phone, and finding which account holds one.
//
// Every read of the decrypted profile, and every lookup, is on the audit trail
// before it answers; every write lands its audit row in the same transaction.
package kyc

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	auditdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/audit"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/kyc"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/admin/audit"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// ErrProfileNotFound is a user who never saved a profile, or no user at all.
var ErrProfileNotFound = errors.New("admin kyc: profile not found")

// ErrProfilePurged refuses an edit of a profile purged with its account.
var ErrProfilePurged = repositories.ErrKycProfilePurged

// ErrProfileNotPurged refuses releasing a live profile's reservations: its
// next submission would recompute them. Edit the profile instead.
var ErrProfileNotPurged = repositories.ErrKycProfileNotPurged

// ErrTaxIDInUse and ErrPhoneInUse refuse an edit declaring what another
// account holds.
var (
	ErrTaxIDInUse = repositories.ErrTaxIDInUse
	ErrPhoneInUse = repositories.ErrPhoneInUse
)

// ErrInvalidPhone is a looked-up phone that does not canonicalise into E.164.
var ErrInvalidPhone = errors.New("admin kyc: phone does not canonicalise")

// lookupCountry is the country a looked-up phone without a calling code is
// read in, as the client's kyc_phone rule reads one.
const lookupCountry = "BRA"

// identityFieldBusinessTaxID is the field a company document's lookup
// matches.
const identityFieldBusinessTaxID = "business_tax_id"

// Service drives the admin actions on the retained KYC profile.
type Service struct {
	profiles repositories.CustomerIndividualProfileRepository
	// members finds who acts for a company holding a looked-up document.
	members repositories.CustomerMemberRepository
	audit   *audit.Service
}

// NewService wires the service.
func NewService(profiles repositories.CustomerIndividualProfileRepository,
	members repositories.CustomerMemberRepository, trail *audit.Service) *Service {
	return &Service{profiles: profiles, members: members, audit: trail}
}

// Profile reads the profile decrypted. The read is recorded before it
// answers, and a failure to record answers nothing.
func (s *Service) Profile(actor auditdto.ActorDTO, userID uint) (*dto.KycProfileDTO, error) {
	found, err := s.profiles.FindForAdmin(userID)
	if err != nil {
		return nil, fmt.Errorf("admin kyc: read profile: %w", err)
	}
	if found == nil {
		return nil, ErrProfileNotFound
	}
	if err := s.audit.Record(actor, auditdto.EntryDTO{
		Action: audit.ActionKycProfileViewed, SubjectType: audit.SubjectCustomers, SubjectID: userID,
	}); err != nil {
		return nil, fmt.Errorf("admin kyc: record the profile read: %w", err)
	}
	return profileDTO(found), nil
}

// UpdateProfile applies an operator's edit to the retained profile, the CPF
// and phone canonicalised as a submission would, and records the names of the
// fields it wrote in the same transaction.
//
// Every answer but a refusal carries the profile decrypted, and a refusal as
// in use says another account holds the value. So an edit that changed
// nothing is recorded as the read it is, and an in-use refusal as the
// question it answered — each before it answers, and a failure to record
// answers nothing.
func (s *Service) UpdateProfile(actor auditdto.ActorDTO, userID uint, d dto.UpdateKycProfileDTO) (*dto.KycProfileDTO, error) {
	written := false
	found, err := s.profiles.UpdateForAdmin(userID, func(profile *models.KycProfile) error {
		return applyEdit(profile, d)
	}, func(before, after models.KycProfile) *models.AdminAuditLog {
		written = true
		return audit.CustomerEntry(actor, audit.ActionKycProfileUpdated, userID,
			map[string]any{"fields": changedFields(before, after)})
	})
	if code := inUseCode(err); code != "" {
		// The transaction rolled back, so the entry is written on its own.
		if recordErr := s.audit.Record(actor, auditdto.EntryDTO{
			Action: audit.ActionKycProfileUpdateRefused, SubjectType: audit.SubjectCustomers, SubjectID: userID,
			Metadata: map[string]any{"error": code},
		}); recordErr != nil {
			return nil, fmt.Errorf("admin kyc: record the refused edit: %w", recordErr)
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrProfileNotFound
	}
	if !written {
		if err := s.audit.Record(actor, auditdto.EntryDTO{
			Action: audit.ActionKycProfileViewed, SubjectType: audit.SubjectCustomers, SubjectID: userID,
		}); err != nil {
			return nil, fmt.Errorf("admin kyc: record the profile read: %w", err)
		}
	}
	return profileDTO(found), nil
}

// inUseCode is the contract's code for an edit refused because another
// account holds its tax id or phone, and "" for anything else.
func inUseCode(err error) string {
	switch {
	case errors.Is(err, ErrTaxIDInUse):
		return "tax_id_in_use"
	case errors.Is(err, ErrPhoneInUse):
		return "phone_in_use"
	default:
		return ""
	}
}

// Release drops a deleted account's CPF and/or phone reservation ("tax_id",
// "phone"), recording the fields in the same transaction.
func (s *Service) Release(actor auditdto.ActorDTO, userID uint, d dto.ReleaseIdentityDTO) (*dto.KycProfileDTO, error) {
	found, err := s.profiles.ReleaseIdentity(userID,
		slices.Contains(d.Fields, repositories.IdentityFieldTaxID),
		slices.Contains(d.Fields, repositories.IdentityFieldPhone),
		func(released []string) *models.AdminAuditLog {
			fields := slices.Clone(released)
			slices.Sort(fields)
			return audit.CustomerEntry(actor, audit.ActionKycIdentityReleased, userID, map[string]any{"fields": fields})
		})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrProfileNotFound
	}
	return profileDTO(found), nil
}

// LookupIdentity names the accounts holding a tax id — a CPF, a passport under
// the country that issued it, or one retained before its type was under its
// country — or a phone: what the uniqueness index would refuse. The lookup is
// recorded with the field asked and how many matched, never the value, before
// it answers.
func (s *Service) LookupIdentity(actor auditdto.ActorDTO, d dto.IdentityLookupDTO) ([]dto.IdentityHolderDTO, error) {
	if strings.TrimSpace(d.BusinessTaxID) != "" {
		return s.lookUpBusiness(actor, d)
	}
	field, phone := repositories.IdentityFieldTaxID, ""
	if strings.TrimSpace(d.Phone) != "" {
		normalized, err := ramp.NormalizePhone(d.Phone, lookupCountry)
		if err != nil || normalized == "" {
			return nil, ErrInvalidPhone
		}
		field, phone = repositories.IdentityFieldPhone, normalized
	}
	holders, err := s.profiles.FindIdentityHolders(models.KycProfile{
		TaxID: d.TaxID, TaxIDType: d.TaxIDType, TaxIDCountry: d.TaxIDCountry, Phone: phone,
	})
	if err != nil {
		return nil, fmt.Errorf("admin kyc: look up identity: %w", err)
	}
	if err := s.audit.Record(actor, auditdto.EntryDTO{
		Action:   audit.ActionKycIdentityLookedUp,
		Metadata: map[string]any{"field": field, "matches": len(holders)},
	}); err != nil {
		return nil, fmt.Errorf("admin kyc: record the identity lookup: %w", err)
	}
	out := make([]dto.IdentityHolderDTO, 0, len(holders))
	for _, holder := range holders {
		// A profile is its individual customer's, whose id is the account's.
		out = append(out, dto.IdentityHolderDTO{User: holder.User, CustomerID: holder.User.ID, Field: holder.Field})
	}
	return out, nil
}

// lookUpBusiness names every member of each company holding the document d
// declares, by the digest it is held under (contract §12.5), recorded like a
// person's lookup.
func (s *Service) lookUpBusiness(actor auditdto.ActorDTO, d dto.IdentityLookupDTO) ([]dto.IdentityHolderDTO, error) {
	country := strings.ToUpper(strings.TrimSpace(d.IncorporationCountry))
	if code, ok := iso3166.Alpha3(country); ok {
		country = code
	}
	identity := models.TaxIdentity{Type: strings.ToLower(strings.TrimSpace(d.TaxIDType)), Country: country}
	members, err := s.members.ListByBusinessTaxIDDigest(repositories.BusinessTaxIDDigest(identity, d.BusinessTaxID))
	if err != nil {
		return nil, fmt.Errorf("admin kyc: look up business: %w", err)
	}
	out := make([]dto.IdentityHolderDTO, 0, len(members))
	for _, member := range members {
		if member.User != nil {
			out = append(out, dto.IdentityHolderDTO{User: *member.User, CustomerID: member.CustomerID, Field: identityFieldBusinessTaxID})
		}
	}
	if err := s.audit.Record(actor, auditdto.EntryDTO{
		Action:   audit.ActionKycIdentityLookedUp,
		Metadata: map[string]any{"field": identityFieldBusinessTaxID, "matches": len(out)},
	}); err != nil {
		return nil, fmt.Errorf("admin kyc: record the identity lookup: %w", err)
	}
	return out, nil
}

// changedFields names the profile fields an edit changed, in the API's
// spelling, sorted — the audit keeps names, never values.
func changedFields(before, after models.KycProfile) []string {
	fields := []string{}
	for name, changed := range map[string]bool{
		"tax_id":      before.TaxID != after.TaxID,
		"tax_id_type": before.TaxIDType != after.TaxIDType, "tax_id_country": before.TaxIDCountry != after.TaxIDCountry,
		"first_name": before.FirstName != after.FirstName, "last_name": before.LastName != after.LastName,
		"legal_name": before.LegalName != after.LegalName, "mother_name": before.MotherName != after.MotherName,
		"email": before.Email != after.Email, "phone": before.Phone != after.Phone,
		"birth_date": before.BirthDate != after.BirthDate, "residential_address": before.Address != after.Address,
		"nationality": before.Nationality != after.Nationality, "occupation": before.Occupation != after.Occupation,
		"source_of_funds":             before.SourceOfFunds != after.SourceOfFunds,
		"account_purpose":             before.AccountPurpose != after.AccountPurpose,
		"monthly_income_usd":          !reflect.DeepEqual(before.MonthlyIncomeUSD, after.MonthlyIncomeUSD),
		"expected_monthly_volume_usd": !reflect.DeepEqual(before.ExpectedMonthlyVolumeUSD, after.ExpectedMonthlyVolumeUSD),
		"is_pep":                      !reflect.DeepEqual(before.IsPEP, after.IsPEP),
	} {
		if changed {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	return fields
}

func profileDTO(found *repositories.AdminKycProfile) *dto.KycProfileDTO {
	out := &dto.KycProfileDTO{
		UserID: found.UserID, Purged: found.Purged,
		TaxIDReserved: found.TaxIDReserved, PhoneReserved: found.PhoneReserved,
	}
	if found.UpdatedAt != nil {
		updated := found.UpdatedAt.StdTime().UTC()
		out.UpdatedAt = &updated
	}
	if found.Profile != nil {
		out.Profile = *found.Profile
	}
	return out
}
