// Package push sends push notifications. Notify renders a notification for each
// device a user registered and queues one delivery per device; the queue
// worker hands each delivery to its platform's transport through Deliver.
//
// It is to app/adapters/pusher what app/services/mail is to app/adapters/mailer:
// nothing outside the two knows which vendor reaches a device.
package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	contractsqueue "github.com/goravel/framework/contracts/queue"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/pusher"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/notifications"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/support/queuejob"
)

// sendTimeout bounds one delivery, for the reason mail's does: config/queue.go
// runs a single concurrent job, and one hung vendor would otherwise stop every
// job in the system, mail included.
const sendTimeout = 10 * time.Second

// sentFor is how long a notification's ID is remembered as sent. It outlasts
// the webhook redeliveries and reconcile sweeps that can report one status
// again, which is what it exists to absorb.
const sentFor = 7 * 24 * time.Hour

// Delivery is one rendered message on its way to one device: what a
// SendPushJob carries. The device is named by its token and the account it was
// meant for, the message by the customer whose affairs it tells, and both are
// read again when the delivery runs. A delivery queued before the customer
// was named carries none: the account's own.
type Delivery struct {
	UserID     uint           `json:"user_id"`
	CustomerID uint           `json:"customer_id,omitempty"`
	Token      string         `json:"token"`
	Message    pusher.Message `json:"message"`
}

// Deps are the service's collaborators.
type Deps struct {
	Config  *pusher.Config
	Factory *pusher.Factory
	Devices repositories.DeviceTokenRepository
	Users   repositories.UserRepository
	// Members is who acts for each customer: who is told of its affairs, and
	// whether a delivery's user still does when it runs.
	Members repositories.CustomerMemberRepository
	// Customers says what a customer is: a company's pushes skip iOS.
	Customers repositories.CustomerRepository
	// Job resolves the SendPushJob at dispatch time; see queuejob.Deferred
	// for why it is not held.
	Job queuejob.Deferred
}

// Service sends push notifications.
type Service struct {
	config    *pusher.Config
	factory   *pusher.Factory
	devices   repositories.DeviceTokenRepository
	users     repositories.UserRepository
	members   repositories.CustomerMemberRepository
	customers repositories.CustomerRepository
	job       queuejob.Deferred
}

// NewService wires the service.
func NewService(d Deps) *Service {
	return &Service{
		config: d.Config, factory: d.Factory, devices: d.Devices, users: d.Users,
		members: d.Members, customers: d.Customers, job: d.Job,
	}
}

// Notify queues n for every device each of userIDs registered, once per
// n.ID().
//
// The ID is claimed once, for every recipient, before anything is queued, so
// two reports of one change racing each other send it once — and a claim per
// recipient would reach only the first of them. A claim that failed to queue
// anything is released, letting the next report of the same change try again.
func (s *Service) Notify(userIDs []uint, n notifications.Notification) error {
	key := "push:sent:" + n.ID()
	if !facades.Cache().Add(key, true, sentFor) {
		return nil
	}
	queued := 0
	var errs []error
	for _, userID := range userIDs {
		sent, err := s.queue(userID, n)
		queued += sent
		if err != nil {
			errs = append(errs, err)
		}
	}
	if queued == 0 && len(errs) > 0 {
		facades.Cache().Forget(key)
	}
	return errors.Join(errs...)
}

// queue renders n once per locale and queues a delivery per device it reaches
// (notifications.ReachesPlatform). One
// device that cannot be queued does not hold back the others; their errors are
// joined, and the count says whether anything went out.
//
// Whether a device may still receive the user's notifications is not decided
// here but when the delivery runs, which on a queue shared with mail can be a
// while later.
func (s *Service) queue(userID uint, n notifications.Notification) (int, error) {
	devices, err := s.devices.ListByUser(userID)
	if err != nil {
		return 0, fmt.Errorf("push: list devices: %w", err)
	}

	rendered := make(map[string]pusher.Message, 1)
	queued := 0
	var errs []error
	for _, device := range devices {
		if !notifications.ReachesPlatform(n, device.Platform) {
			continue
		}
		msg, ok := rendered[device.Locale]
		if !ok {
			msg = render(n, device.Locale)
			rendered[device.Locale] = msg
		}
		if err := s.dispatch(Delivery{UserID: userID, CustomerID: n.CustomerID(), Token: device.Token, Message: msg}); err != nil {
			errs = append(errs, err)
			continue
		}
		queued++
	}
	return queued, errors.Join(errs...)
}

func (s *Service) dispatch(d Delivery) error {
	payload, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("push: marshal delivery: %w", err)
	}
	job, err := s.job.Stage(contractsqueue.Arg{Type: "string", Value: string(payload)})
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	if err := job.Dispatch(); err != nil {
		return fmt.Errorf("push: dispatch delivery: %w", err)
	}
	return nil
}

// Deliver sends one queued delivery, if the device it was queued for may
// still receive it.
//
// Everything that decides that is read here, in the worker, because the queue
// is shared and the delivery may have waited: the registration (the user
// signed out on the device, or another account signed in on it since), the
// account (deleted, suspended, or its sessions revoked after the device
// registered), whether the user still acts for the customer the message is
// about, and the platform's switch in the panel. A registration that no
// longer speaks for the account is forgotten; signing in again registers it
// anew. A token the vendor declared dead is forgotten rather than failed: no
// retry can reach it. A device of an environment the panel holds no key for
// is kept and skipped, for the same reason.
func (s *Service) Deliver(ctx context.Context, d Delivery) error {
	device, err := s.devices.FindByToken(d.Token)
	if err != nil {
		return fmt.Errorf("push: find device: %w", err)
	}
	if device == nil || device.UserID != d.UserID {
		return nil
	}
	user, err := s.users.FindByID(d.UserID)
	if err != nil {
		return fmt.Errorf("push: find user: %w", err)
	}
	if user == nil || !signedIn(user, device) {
		if err := s.devices.DeleteRegistration(device); err != nil {
			return fmt.Errorf("push: forget a signed-out device: %w", err)
		}
		return nil
	}
	acting, err := s.stillActsFor(d)
	if err != nil || !acting {
		return err
	}

	enabled, err := s.config.Enabled(device.Platform)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	transport, err := s.factory.Get(device.Platform)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	err = transport.Send(ctx, deviceOf(device), d.Message)
	if errors.Is(err, pusher.ErrNoCredentials) {
		// A build of an environment this installation holds no key for: a
		// development build signed in against a backend that only serves the
		// stores. Configuring the key is what changes it, not a retry.
		facades.Log().Infof("push: not delivered: %v", err)
		return nil
	}
	if !errors.Is(err, pusher.ErrUnregistered) {
		return err
	}
	facades.Log().Infof("push: forgetting a %s device: %v", device.Platform, err)
	if err := s.devices.DeleteByToken(device.Token); err != nil {
		return fmt.Errorf("push: forget an unregistered device: %w", err)
	}
	return nil
}

// stillActsFor reports whether the delivery's user still acts for the
// customer it tells of. An account always acts for its own customer, whose id
// is its own — as for a delivery that names none — so only another customer's
// membership is read; one the user left while the delivery waited is not
// theirs to hear of, and the device, still theirs, is kept.
func (s *Service) stillActsFor(d Delivery) (bool, error) {
	if d.CustomerID == 0 || d.CustomerID == d.UserID {
		return true, nil
	}
	member, err := s.members.FindByCustomerAndUser(d.CustomerID, d.UserID)
	if err != nil {
		return false, fmt.Errorf("push: read the membership: %w", err)
	}
	return member != nil, nil
}

// signedIn reports whether the registration still speaks for the account: it
// was made after the account's sessions were last revoked, and the account is
// not suspended. It is the rule a session token is held to, applied to the
// moment the device registered.
func signedIn(user *models.User, device *models.DeviceToken) bool {
	return device.UpdatedAt != nil && user.AcceptsTokenIssuedAt(device.UpdatedAt.StdTime())
}

// render writes n in locale, one render at a time with every other channel's
// (notifications.Translated says why).
func render(n notifications.Notification, locale string) pusher.Message {
	return notifications.Translated(locale, n.ToPush)
}

func deviceOf(device *models.DeviceToken) pusher.Device {
	out := pusher.Device{Platform: device.Platform, Token: device.Token}
	if device.APNsEnvironment != nil {
		out.Environment = *device.APNsEnvironment
	}
	return out
}
