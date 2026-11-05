// Package audit is the admin audit trail: who did what to which end user, and
// who read their decrypted identity data.
//
// An act is recorded by the service that performs it, never by route
// middleware: a middleware runs after the reply is written, so it cannot fail
// closed, and what the entry says (the fields an edit changed, the customer a
// relink replaced) only exists inside the service. Three rules decide how:
//
//   - a state change that is only database writes hands Entry to its
//     repository, which lands the change and the entry in one transaction;
//   - an act that called a provider uses RecordIrreversible once the call and
//     the local save succeeded: the provider side cannot be rolled back, so a
//     failed insert is logged and the act's answer stands;
//   - a read of decrypted data uses Record before it answers, and answers
//     nothing when that fails.
package audit

import (
	"errors"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/audit"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/support/jsonb"
)

// The subject types an act is about: one end-user account, one customer —
// the holder of the money, a person or a company — or one admin. An act on a
// holder's compliance, provider standing or deposit accounts is about the
// customer (KYB contract §12.5); an act on the login stays about the account.
const (
	SubjectUsers     = "users"
	SubjectCustomers = "customers"
	SubjectAdmins    = "admins"
)

// The audit vocabulary. An action names the act, not the route, and is
// written once here so a filter and a test compare the same string.
const (
	ActionUserCreated         = "user.created"
	ActionUserUpdated         = "user.updated"
	ActionUserDeleted         = "user.deleted"
	ActionUserSelfDeleted     = "user.self_deleted"
	ActionUserSuspended       = "user.suspended"
	ActionUserReactivated     = "user.reactivated"
	ActionUserSessionsRevoked = "user.sessions_revoked"
	ActionUserMfaReset        = "user.mfa_reset"
	ActionUserRestored        = "user.restored"
	ActionUserFeaturesUpdated = "user.features_updated"

	ActionKycProfileViewed        = "kyc.profile_viewed"
	ActionKycProfileUpdated       = "kyc.profile_updated"
	ActionKycProfileUpdateRefused = "kyc.profile_update_refused"
	ActionKycIdentityReleased     = "kyc.identity_released"
	ActionKycIdentityLookedUp     = "kyc.identity_looked_up"
	ActionKycReset                = "kyc.reset"
	ActionKycHostedLinkIssued     = "kyc.hosted_link_issued"
	ActionKycRefreshed            = "kyc.refreshed"
	ActionKycApprovalSimulated    = "kyc.approval_simulated"

	ActionIntegrationStarted  = "integration.started"
	ActionIntegrationRelinked = "integration.relinked"
	ActionIntegrationUnlinked = "integration.unlinked"

	// The acts on what the ramp attention queue holds, on the account holder.
	ActionRampsKycIntentResolved     = "ramps.kyc_intent_resolved"
	ActionRampsFinalizationResolved  = "ramps.finalization_resolved"
	ActionRampsSupportTicketRecorded = "ramps.support_ticket_recorded"

	ActionOfframpDepositSimulated   = "offramp.deposit_simulated"
	ActionVirtualAccountDeactivated = "virtual_account.deactivated"

	// The acts on a company as such (KYB contract §12.4–§12.6): its profile
	// read decrypted, its closing, its document released, a wallet
	// registration released.
	ActionCustomerBusinessProfileViewed = "customer.business_profile.viewed"
	ActionCustomerClosed                = "customer.closed"
	ActionCustomerTaxIDReleased         = "customer.tax_id.released"
	// ActionCustomerWalletReleased is a wallet registration let go, the
	// address free for its rightful holder (round-2 review).
	ActionCustomerWalletReleased = "customer.wallet.released"

	// ActionAdminFeaturesUpdated is an admin's own flags written, its second
	// factor among them; the subject is that admin (SubjectAdmins).
	ActionAdminFeaturesUpdated = "admin.features_updated"
)

// ErrUserNotFound answers a history asked for an id no account, live or
// deleted, ever held.
var ErrUserNotFound = errors.New("user not found")

// ErrCustomerNotFound answers a history asked for an id no live customer
// holds.
var ErrCustomerNotFound = errors.New("customer not found")

// Service records and lists the audit trail.
type Service struct {
	logs      repositories.AdminAuditLogRepository
	users     repositories.UserRepository
	customers repositories.CustomerRepository
}

// NewService wires the service.
func NewService(logs repositories.AdminAuditLogRepository, users repositories.UserRepository,
	customers repositories.CustomerRepository) *Service {
	return &Service{logs: logs, users: users, customers: customers}
}

// Entry is the row that records entry as done by actor. A repository that
// writes a state change in a transaction takes it and lands both together.
func Entry(actor dto.ActorDTO, entry dto.EntryDTO) *models.AdminAuditLog {
	row := &models.AdminAuditLog{
		Action:   entry.Action,
		Metadata: jsonb.Object(entry.Metadata),
		IP:       actor.IP,
	}
	if actor.Admin != nil {
		row.AdminID = &actor.Admin.ID
	}
	if entry.SubjectType != "" {
		row.SubjectType = &entry.SubjectType
		row.SubjectID = &entry.SubjectID
	}
	if entry.Provider != "" {
		row.Provider = &entry.Provider
	}
	return row
}

// UserEntry is Entry for an act on one end-user account.
func UserEntry(actor dto.ActorDTO, action string, userID uint, metadata map[string]any) *models.AdminAuditLog {
	return Entry(actor, dto.EntryDTO{Action: action, SubjectType: SubjectUsers, SubjectID: userID, Metadata: metadata})
}

// CustomerEntry is Entry for an act on one customer: its compliance, its
// provider standing, its deposit accounts.
func CustomerEntry(actor dto.ActorDTO, action string, customerID uint, metadata map[string]any) *models.AdminAuditLog {
	return Entry(actor, dto.EntryDTO{Action: action, SubjectType: SubjectCustomers, SubjectID: customerID, Metadata: metadata})
}

// Record appends the entry now and returns a failure: the caller is a read of
// decrypted data, which answers only once it is on the record.
func (s *Service) Record(actor dto.ActorDTO, entry dto.EntryDTO) error {
	return s.logs.Create(Entry(actor, entry))
}

// RecordIrreversible appends the entry of an act that already happened and
// cannot be rolled back, because a provider was called. A failed insert is
// logged, not returned: the operator's answer must say what really happened.
func (s *Service) RecordIrreversible(actor dto.ActorDTO, entry dto.EntryDTO) {
	if err := s.logs.Create(Entry(actor, entry)); err != nil {
		facades.Log().Errorf("admin audit: recording %s on %s %d: %v",
			entry.Action, entry.SubjectType, entry.SubjectID, err)
	}
}

// List returns a page of the whole trail, newest first.
func (s *Service) List(d dto.AdminListAuditLogsDTO) ([]models.AdminAuditLog, int64, error) {
	return s.logs.Paginate(repositories.AdminAuditLogListFilters{
		AdminID:     d.AdminID,
		SubjectType: d.SubjectType,
		SubjectID:   d.SubjectID,
		Action:      d.Action,
		From:        d.From,
		To:          d.To,
		Page:        d.Page,
		PerPage:     d.PerPage,
	})
}

// ListForUser returns a page of one account's history, newest first: the acts
// on the account and on its individual customer, whose id is the account's —
// the compliance acts recorded on the customer since F4 among them. A deleted
// account keeps its history.
func (s *Service) ListForUser(userID uint, page, perPage int) ([]models.AdminAuditLog, int64, error) {
	user, err := s.users.FindByIDWithTrashed(userID)
	if err != nil {
		return nil, 0, err
	}
	if user == nil {
		return nil, 0, ErrUserNotFound
	}
	return s.logs.Paginate(repositories.AdminAuditLogListFilters{
		SubjectTypes: []string{SubjectUsers, SubjectCustomers}, SubjectID: userID, Page: page, PerPage: perPage,
	})
}

// ListForCustomer returns a page of one customer's history, newest first. A
// person's is its account's too, whose id it took: what was recorded on the
// account before F4 stays in the one history.
func (s *Service) ListForCustomer(customerID uint, page, perPage int) ([]models.AdminAuditLog, int64, error) {
	customer, err := s.customers.FindByID(customerID)
	if err != nil {
		return nil, 0, err
	}
	if customer == nil {
		return nil, 0, ErrCustomerNotFound
	}
	subjects := []string{SubjectCustomers}
	if customer.Type == models.CustomerTypeIndividual {
		subjects = append(subjects, SubjectUsers)
	}
	return s.logs.Paginate(repositories.AdminAuditLogListFilters{
		SubjectTypes: subjects, SubjectID: customerID, Page: page, PerPage: perPage,
	})
}
