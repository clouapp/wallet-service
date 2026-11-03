package kyc

import (
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/rules"
)

// The codes a business application is refused with field by field, in the
// validation shape (API contract §5.2, §5.3).
const (
	RefusalValidationFailed  = "validation failed"
	RefusalProfileIncomplete = "profile_incomplete"
)

// maxAssociatedPersons keeps the one documents call of an application under
// nginx's 20 MiB (contract §6.3).
const maxAssociatedPersons = 8

// FieldRefusal is an application refused field by field: Code is
// RefusalValidationFailed or RefusalProfileIncomplete, Fields the errors map
// by dotted key (associated_persons.1.birth_date) and rule. The rule is the
// contract; the message is English and only for logs.
type FieldRefusal struct {
	Code   string
	Fields map[string]map[string]string
}

func (r *FieldRefusal) Error() string { return "kyc: application refused: " + r.Code }

func (r *FieldRefusal) add(field, rule, message string) {
	if r.Fields[field] == nil {
		r.Fields[field] = map[string]string{}
	}
	r.Fields[field][rule] = message
}

// soleProprietorships are the company types that have one owner, which Bridge
// refuses with more (sole_proprietorship_too_many_ubos).
var soleProprietorships = []string{models.CompanyTypeMEI, models.CompanyTypeEI, models.CompanyTypeSoleProp}

// personRoles is every role a person may hold in a company.
var personRoles = []string{
	ramp.AssociatedPersonOwner, ramp.AssociatedPersonControl, ramp.AssociatedPersonSigner, ramp.AssociatedPersonDirector,
}

// checkBusinessApplication is what the form request cannot see of a business
// application for a company incorporated in incorporation whose members'
// account e-mails are memberEmails: the rules across fields, against the
// draft and against the members (validation failed), then a person's
// required field left out (profile_incomplete). nil when it passes.
func checkBusinessApplication(in dto.BusinessApplicationDTO, incorporation string, memberEmails []string) *FieldRefusal {
	refusal := &FieldRefusal{Code: RefusalValidationFailed, Fields: map[string]map[string]string{}}
	checkCompany(refusal, in, incorporation, memberEmails)
	checkActivity(refusal, in)
	checkPeople(refusal, in)
	if len(refusal.Fields) > 0 {
		return refusal
	}
	incomplete := &FieldRefusal{Code: RefusalProfileIncomplete, Fields: map[string]map[string]string{}}
	for i, person := range in.AssociatedPersons {
		missingPersonFields(incomplete, "associated_persons."+strconv.Itoa(i)+".", person)
	}
	if len(incomplete.Fields) > 0 {
		return incomplete
	}
	return nil
}

func checkCompany(refusal *FieldRefusal, in dto.BusinessApplicationDTO, incorporation string, memberEmails []string) {
	if slices.ContainsFunc(memberEmails, func(email string) bool { return strings.EqualFold(email, strings.TrimSpace(in.Email)) }) {
		refusal.add("email", "distinct_from_members", "the company e-mail must not be a member's")
	}
	if models.ResidenceCode(in.RegisteredAddress.Country) != incorporation {
		refusal.add("registered_address.country", "incorporation_country", "the registered address is in the country of incorporation")
	}
	if !slices.Contains(models.CompanyTypesFor(incorporation), in.CompanyType) {
		refusal.add("company_type", "in", "the company type is not one of the country of incorporation")
	}
	if in.IncorporationDate > carbon.Now().ToDateString() {
		refusal.add("incorporation_date", "before_or_equal", "the company cannot be incorporated in the future")
	}
}

func checkActivity(refusal *FieldRefusal, in dto.BusinessApplicationDTO) {
	if in.AccountPurpose == "other" && strings.TrimSpace(in.AccountPurposeOther) == "" {
		refusal.add("account_purpose_other", "required", "another purpose is named")
	}
	if in.RegulatedActivity == nil {
		refusal.add("regulated_activity", "required", "whether the activity is regulated is answered")
	}
	if in.ConductsMoneyServices == nil {
		refusal.add("conducts_money_services", "required", "whether the company moves third parties' money is answered")
	}
	if in.ConductsMoneyServices != nil && *in.ConductsMoneyServices && strings.TrimSpace(in.MoneyServicesDescription) == "" {
		refusal.add("money_services_description", "required", "how the company moves third parties' money is described")
	}
	if !in.OwnershipAttested {
		refusal.add("ownership_attested", "accepted", "the representative declares the owner list complete")
	}
}

func checkPeople(refusal *FieldRefusal, in dto.BusinessApplicationDTO) {
	people := in.AssociatedPersons
	if len(people) > maxAssociatedPersons {
		refusal.add("associated_persons", "max_len", "an application names at most 8 people")
	}
	total := new(big.Rat)
	owners, representatives := 0, 0
	control, signer := false, false
	refs := map[string]bool{}
	for i, person := range people {
		key := "associated_persons." + strconv.Itoa(i) + "."
		if refs[person.LocalRef] {
			refusal.add(key+"local_ref", "distinct", "each person has their own reference")
		}
		refs[person.LocalRef] = true
		checkRoles(refusal, key, person)
		if share := checkShare(refusal, key, person); share != nil {
			total.Add(total, share)
		}
		owners += countIf(slices.Contains(person.Roles, ramp.AssociatedPersonOwner))
		control = control || slices.Contains(person.Roles, ramp.AssociatedPersonControl)
		signer = signer || slices.Contains(person.Roles, ramp.AssociatedPersonSigner)
		representatives += countIf(person.FromMember)
	}
	if total.Cmp(big.NewRat(100, 1)) > 0 {
		refusal.add("associated_persons", "ownership_total", "the owners hold at most 100%")
	}
	if !control || !signer {
		refusal.add("associated_persons", "control_and_signer", "someone has control and someone signs")
	}
	if representatives != 1 {
		refusal.add("associated_persons", "representative", "exactly one person is the member registering the company")
	}
	if slices.Contains(soleProprietorships, in.CompanyType) && owners > 1 {
		refusal.add("associated_persons", "sole_proprietorship", "a sole proprietorship has one owner")
	}
}

// checkRoles holds a person's roles to the vocabulary, once each, and the
// representative to control and signing: Bridge's ownership attestation goes
// on a controller, and the representative signs for the company.
func checkRoles(refusal *FieldRefusal, key string, person dto.AssociatedPersonDTO) {
	seen := map[string]bool{}
	for _, role := range person.Roles {
		if !slices.Contains(personRoles, role) {
			refusal.add(key+"roles", "in", "the roles are owner, control, signer and director")
		}
		if seen[role] {
			refusal.add(key+"roles", "distinct", "each role once")
		}
		seen[role] = true
	}
	if person.FromMember && (!seen[ramp.AssociatedPersonControl] || !seen[ramp.AssociatedPersonSigner]) {
		refusal.add(key+"roles", "representative", "the representative has control and signs")
	}
	if seen[ramp.AssociatedPersonControl] && strings.TrimSpace(person.Title) == "" {
		refusal.add(key+"title", "required", "a person with control has a title")
	}
}

// checkShare is an owner's share, above 0 and at most 100 with at most two
// decimals, or nil: a share is owed by an owner and by nobody else.
func checkShare(refusal *FieldRefusal, key string, person dto.AssociatedPersonDTO) *big.Rat {
	share := strings.TrimSpace(person.OwnershipPercent)
	owner := slices.Contains(person.Roles, ramp.AssociatedPersonOwner)
	switch {
	case share == "" && owner:
		refusal.add(key+"ownership_percent", "required", "an owner's share is declared")
		return nil
	case share == "":
		return nil
	case !owner:
		refusal.add(key+"ownership_percent", "prohibited", "only an owner declares a share")
		return nil
	}
	value, ok := new(big.Rat).SetString(share)
	if !ok || value.Sign() <= 0 || value.Cmp(big.NewRat(100, 1)) > 0 || !twoDecimals(value) {
		refusal.add(key+"ownership_percent", "ownership_percent", "a share above 0 and at most 100, two decimals at most")
		return nil
	}
	return value
}

// twoDecimals reports whether value has at most two decimal places.
func twoDecimals(value *big.Rat) bool {
	return new(big.Rat).Mul(value, big.NewRat(100, 1)).IsInt()
}

func countIf(b bool) int {
	if b {
		return 1
	}
	return 0
}

// personFields are the fields every person declares, with the message their
// absence answers.
var personFields = []struct {
	field, message string
	value          func(dto.AssociatedPersonDTO) string
}{
	{"first_name", "the first name is required", func(p dto.AssociatedPersonDTO) string { return p.FirstName }},
	{"last_name", "the last name is required", func(p dto.AssociatedPersonDTO) string { return p.LastName }},
	{"email", "the e-mail is required", func(p dto.AssociatedPersonDTO) string { return p.Email }},
	{"phone", "the phone is required", func(p dto.AssociatedPersonDTO) string { return p.Phone }},
	{"birth_date", "the birth date is required", func(p dto.AssociatedPersonDTO) string { return p.BirthDate }},
	{"tax_id", "the tax id is required", func(p dto.AssociatedPersonDTO) string { return p.TaxID }},
	{"street_line_1", "the street is required", func(p dto.AssociatedPersonDTO) string { return p.Address.StreetLine1 }},
	{"city", "the city is required", func(p dto.AssociatedPersonDTO) string { return p.Address.City }},
	{"country", "the country is required", func(p dto.AssociatedPersonDTO) string { return p.Address.Country }},
}

// missingPersonFields adds every required field person left out, under key:
// the fields every person declares, and the subdivision and postal code where
// the person's country of residence uses them (as the PF form requires them).
func missingPersonFields(refusal *FieldRefusal, key string, person dto.AssociatedPersonDTO) {
	for _, field := range personFields {
		if strings.TrimSpace(field.value(person)) == "" {
			refusal.add(key+field.field, "required", field.message)
		}
	}
	country := person.Address.Country
	if strings.TrimSpace(country) == "" {
		return
	}
	if rules.SubdivisionRequired(country) && strings.TrimSpace(person.Address.Subdivision) == "" {
		refusal.add(key+"subdivision", "required", "the subdivision is required")
	}
	if rules.PostalCodeRequired(country) && strings.TrimSpace(person.Address.PostalCode) == "" {
		refusal.add(key+"postal_code", "required", "the postal code is required")
	}
}
