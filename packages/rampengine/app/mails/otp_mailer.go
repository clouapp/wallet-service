package mails

import (
	"fmt"

	"github.com/goravel/framework/contracts/mail"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
)

// AdminOTPMailer and ClientOTPMailer adapt the shared OTP service to each
// audience's copy. The mechanics of a one-time code are identical for an admin
// and a customer; the words are not, and the service has no business knowing
// which is which.
type AdminOTPMailer struct{}

func NewAdminOTPMailer() *AdminOTPMailer { return &AdminOTPMailer{} }

func (m *AdminOTPMailer) Send(subject identity.Subject, purpose identity.OTPPurpose, code string) error {
	mailable, err := adminMailable(purpose, subject.SubjectEmail(), code)
	if err != nil {
		return err
	}
	if err := facades.Mail().Queue(mailable); err != nil {
		return fmt.Errorf("admin otp mail: queue %s: %w", purpose, err)
	}
	return nil
}

type ClientOTPMailer struct{}

func NewClientOTPMailer() *ClientOTPMailer { return &ClientOTPMailer{} }

func (m *ClientOTPMailer) Send(subject identity.Subject, purpose identity.OTPPurpose, code string) error {
	mailable, err := clientMailable(purpose, subject.SubjectEmail(), code)
	if err != nil {
		return err
	}
	if err := facades.Mail().Queue(mailable); err != nil {
		return fmt.Errorf("client otp mail: queue %s: %w", purpose, err)
	}
	return nil
}

// adminMailable maps a purpose to the admin template that carries it.
//
// Both login purposes share one template: to the reader they are the same
// event — "here is your code to sign in" — and they are distinct in the data
// model for a reason that is nobody's business but the verify endpoint's.
//
// An unmapped purpose is an error rather than a default, so adding a flow
// without its copy fails where it is added instead of mailing the wrong words.
func adminMailable(purpose identity.OTPPurpose, to, code string) (mail.Mailable, error) {
	switch purpose {
	case identity.OTPPurposeLoginOTP, identity.OTPPurposePasswordlessLogin:
		return &otpMail{to: to, code: code, view: "admin_login_otp.tmpl",
			subject: "Your XIP admin login code", title: "XIP admin login",
			intro: "Your admin login code is:", expiry: "This code expires in 5 minutes."}, nil
	case identity.OTPPurposePasswordReset:
		return &otpMail{to: to, code: code, view: "admin_password_reset.tmpl",
			subject: "Reset your XIP admin password", title: "Reset your admin password",
			intro: "Your admin password reset code is:", expiry: "This code expires in 15 minutes."}, nil
	default:
		return nil, fmt.Errorf("admin otp mail: no template for purpose %q", purpose)
	}
}

// clientMailable is adminMailable for the customer's templates.
func clientMailable(purpose identity.OTPPurpose, to, code string) (mail.Mailable, error) {
	switch purpose {
	case identity.OTPPurposeEmailVerify:
		return &otpMail{to: to, code: code, view: "verification_code.tmpl",
			subject: "Verify your XIP email", title: "Welcome to XIP",
			intro: "Your verification code is:", expiry: "This code expires in 15 minutes."}, nil
	case identity.OTPPurposeLoginOTP, identity.OTPPurposePasswordlessLogin:
		return &otpMail{to: to, code: code, view: "client_login_otp.tmpl",
			subject: "Your XIP login code", title: "Sign in to XIP",
			intro: "Your login code is:", expiry: "This code expires in 15 minutes."}, nil
	case identity.OTPPurposePasswordReset:
		return &otpMail{to: to, code: code, view: "password_reset.tmpl",
			subject: "Reset your XIP password", title: "Reset your password",
			intro: "Your password reset code is:", expiry: "This code expires in 15 minutes."}, nil
	case identity.OTPPurposeWalletRestore:
		return &otpMail{to: to, code: code, view: "client_login_otp.tmpl",
			subject: "Your XIP wallet restore code", title: "Restore your wallets",
			intro: "Your wallet restore code is:", expiry: "This code expires in 15 minutes."}, nil
	default:
		return nil, fmt.Errorf("client otp mail: no template for purpose %q", purpose)
	}
}
