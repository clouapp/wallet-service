package facades

import (
	"github.com/goravel/framework/contracts/mail"
	goravelfacades "github.com/goravel/framework/facades"
)

// Mail returns the process mailer. Callers keep the same recipients and messages.
func Mail() mail.Mail {
	return goravelfacades.Mail()
}
