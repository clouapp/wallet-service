package customers

import (
	"errors"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/notifications"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	mailsvc "github.com/macrowallets/waas/packages/rampengine/app/services/mail"
	pushsvc "github.com/macrowallets/waas/packages/rampengine/app/services/push"
)

// VerdictPush tells users of a notification on their devices: push.Service.
type VerdictPush interface {
	Notify(userIDs []uint, n notifications.Notification) error
}

// VerdictMail mails a notification to its recipients:
// mail.NotificationChannel.
type VerdictMail interface {
	Notify(recipients []mailsvc.Recipient, n notifications.Notification) error
}

// KycVerdictsDeps are what KycVerdicts is built from.
type KycVerdictsDeps struct {
	// Next is told everything that is not a business's KYC: a person's
	// verdicts and every transaction (push.Service).
	Next      pushsvc.Notifier
	Customers repositories.CustomerRepository
	// Kyc is the business's rows, which a read reconciling its status asks
	// whether a provider approved it.
	Kyc      repositories.CustomerProviderKycRepository
	Profiles repositories.CustomerBusinessProfileRepository
	Members  repositories.CustomerMemberRepository
	Users    repositories.UserRepository
	Devices  repositories.DeviceTokenRepository
	Push     VerdictPush
	Mail     VerdictMail
}

// KycVerdicts is what a provider's verdict on a business does (KYB spec §7,
// §7.4b; API contract §7.1, §11): its first approval turns it from draft to
// active, and every verdict is told to each member in the company's name —
// by push, skipping iOS, and by e-mail in the language of the member's
// newest device. Everything else is Next's to tell.
//
// It is the pushsvc.Notifier the KYC flows and the webhooks are told
// through, so a verdict does the same whichever way it arrived. Like the
// notifier it stands in for, it never fails its caller: the verdict has
// already committed, and what fails here is logged.
type KycVerdicts struct {
	d KycVerdictsDeps
}

// NewKycVerdicts builds the notifier.
func NewKycVerdicts(d KycVerdictsDeps) *KycVerdicts { return &KycVerdicts{d: d} }

// TransactionUpdated is Next's.
func (v *KycVerdicts) TransactionUpdated(tx *models.Transaction) { v.d.Next.TransactionUpdated(tx) }

// KycUpdated activates a business on its first approval and tells its
// members of a verdict; a person's change is Next's.
//
// The approval is told when it activates the company, and only then: a
// company that could not be activated — another business holds its document,
// or the activation failed — is told nothing, since the notice would send
// its members to a wallet they cannot open; a read activates it later
// (ActivateIfApproved) and tells them then. A rejection is told as it
// arrives.
//
// An approval whose Pix endorsement is still under review activates it
// already, though the overview reads it in review until the endorsement
// settles: that settling changes nothing the webhooks and refreshes announce
// (models.KycPosture leaves endorsements out). The wallet waits for the
// overview's approval (kycsvc.RequireApproved), not for the status.
func (v *KycVerdicts) KycUpdated(previous string, row *models.CustomerProviderKyc) {
	if !row.Business {
		v.d.Next.KycUpdated(previous, row)
		return
	}
	if row.KycStatus == models.KycStatusApproved {
		if _, err := v.activate(row); err != nil {
			facades.Log().Errorf("customers: activate business %d: %v", row.CustomerID, err)
		}
		return
	}
	n, ok := notifications.NewKycReviewNotification(previous, row)
	if !ok {
		return
	}
	v.tell(row.CustomerID, n)
}

// ActivateIfApproved reconciles a business's status with its verdicts, for a
// read that finds it a draft: approved at a provider, and no other business
// holding its document, it is activated and its approval told — once, by
// whichever read activates it. It answers whether the business is active.
// A verdict whose activation failed, or a document compliance released from
// a closed rival since, is how a draft comes to need it: no provider change
// may ever be announced again to activate it.
func (v *KycVerdicts) ActivateIfApproved(customerID uint) (bool, error) {
	customer, err := v.d.Customers.FindByID(customerID)
	if err != nil {
		return false, fmt.Errorf("customers: read business %d: %w", customerID, err)
	}
	if customer == nil || customer.Type != models.CustomerTypeBusiness {
		return false, nil
	}
	switch customer.Status {
	case models.CustomerStatusActive:
		return true, nil
	case models.CustomerStatusDraft:
	default:
		return false, nil
	}
	rows, err := v.d.Kyc.ListByCustomer(customerID)
	if err != nil {
		return false, fmt.Errorf("customers: read business %d verdicts: %w", customerID, err)
	}
	for i := range rows {
		if rows[i].KycStatus != models.KycStatusApproved {
			continue
		}
		activated, err := v.activate(&rows[i])
		if err != nil || activated {
			return activated, err
		}
		// Not this call's to activate: another read just did, or its document
		// is held. Which one is the status now.
		now, err := v.d.Customers.FindByID(customerID)
		if err != nil {
			return false, fmt.Errorf("customers: read business %d: %w", customerID, err)
		}
		return now != nil && now.Status == models.CustomerStatusActive, nil
	}
	return false, nil
}

// activate turns the business row is of active and tells its approval when
// it did. ErrTaxIDHeld — another business holds its document — leaves it a
// draft, untold; the overview reads it refused. Answers whether this call
// activated it.
func (v *KycVerdicts) activate(row *models.CustomerProviderKyc) (bool, error) {
	activated, err := v.d.Customers.ActivateBusiness(row.CustomerID)
	if errors.Is(err, repositories.ErrTaxIDHeld) {
		facades.Log().Warningf("customers: business %d approved, but another business holds its document; it stays a draft", row.CustomerID)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !activated {
		return false, nil
	}
	facades.Log().Infof("customers: business %d is active", row.CustomerID)
	if n, ok := notifications.NewKycReviewNotification("", row); ok {
		v.tell(row.CustomerID, n)
	}
	return true, nil
}

// tell sends n, in the company's name, to every member.
func (v *KycVerdicts) tell(customerID uint, n *notifications.KycReviewNotification) {
	profiles, err := v.d.Profiles.FindByCustomers([]uint{customerID})
	if err != nil {
		facades.Log().Errorf("customers: verdict of business %d: read its name: %v", customerID, err)
		return
	}
	profile, ok := profiles[customerID]
	if !ok {
		facades.Log().Errorf("customers: verdict of business %d: it declared no company", customerID)
		return
	}
	business := n.ForBusiness(customerID, profile.DisplayName())
	members, err := v.d.Members.MembersOf(customerID)
	if err != nil {
		facades.Log().Errorf("customers: verdict of business %d: who acts for it: %v", customerID, err)
		return
	}
	if err := v.d.Push.Notify(members, business); err != nil {
		facades.Log().Errorf("customers: push the verdict of business %d: %v", customerID, err)
	}
	if err := v.d.Mail.Notify(v.recipients(members), business); err != nil {
		facades.Log().Errorf("customers: mail the verdict of business %d: %v", customerID, err)
	}
}

// recipients is each member's account e-mail, in the language of their newest
// device; one whose account cannot be read is left out, and logged.
func (v *KycVerdicts) recipients(members []uint) []mailsvc.Recipient {
	out := make([]mailsvc.Recipient, 0, len(members))
	for _, id := range members {
		user, err := v.d.Users.FindByID(id)
		if err != nil || user == nil {
			facades.Log().Errorf("customers: mail recipient %d: %v", id, err)
			continue
		}
		out = append(out, mailsvc.Recipient{UserID: id, Email: user.Email, Locale: v.newestLocale(id)})
	}
	return out
}

// newestLocale is the language of the device userID registered last, empty
// when none says one.
func (v *KycVerdicts) newestLocale(userID uint) string {
	devices, err := v.d.Devices.ListByUser(userID)
	if err != nil {
		facades.Log().Errorf("customers: devices of user %d: %v", userID, err)
		return ""
	}
	var locale string
	var newest time.Time
	for _, device := range devices {
		if device.UpdatedAt == nil || device.Locale == "" {
			continue
		}
		if at := device.UpdatedAt.StdTime(); at.After(newest) {
			newest, locale = at, device.Locale
		}
	}
	return locale
}

var _ pushsvc.Notifier = (*KycVerdicts)(nil)
