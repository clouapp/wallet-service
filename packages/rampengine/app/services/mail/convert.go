// Package mail is the application's send path: it turns a Mailable into a
// rendered mailer.Message, queues it, and resolves the driver that carries it.
//
// facades.Mail() answers with the Facade in this package, so the application
// keeps its single way out and nothing outside these files knows which provider
// is sending.
package mail

import (
	"fmt"

	contractsmail "github.com/goravel/framework/contracts/mail"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/mailer"
)

// messageFrom renders a mailable into the message the transports carry.
//
// It refuses everything mailer.Message does not model. That is deliberate: the
// mailables in app/mails declare none of it today, and silently dropping an
// attachment a later one adds would be a bug invisible from the outside. When a
// mailable needs one of these, the field is added here and to every transport
// that can honour it -- not worked around at the call site.
func messageFrom(renderer contractsmail.Template, mailable contractsmail.Mailable) (mailer.Message, error) {
	if len(mailable.Attachments()) > 0 {
		return mailer.Message{}, fmt.Errorf("mail: attachments are not supported by the mail transports")
	}
	if len(mailable.Headers()) > 0 {
		return mailer.Message{}, fmt.Errorf("mail: custom headers are not supported by the mail transports")
	}
	envelope := mailable.Envelope()
	if envelope == nil {
		return mailer.Message{}, fmt.Errorf("mail: mailable declares no envelope")
	}
	if len(envelope.Cc) > 0 {
		return mailer.Message{}, fmt.Errorf("mail: cc is not supported by the mail transports")
	}
	if len(envelope.Bcc) > 0 {
		return mailer.Message{}, fmt.Errorf("mail: bcc is not supported by the mail transports")
	}
	content := mailable.Content()
	if content == nil {
		return mailer.Message{}, fmt.Errorf("mail: mailable declares no content")
	}

	html := content.Html
	if content.View != "" {
		rendered, err := renderer.Render(content.View, content.With)
		if err != nil {
			return mailer.Message{}, fmt.Errorf("mail: render %s: %w", content.View, err)
		}
		html = rendered
	}

	return mailer.Message{
		Subject: envelope.Subject,
		HTML:    html,
		To:      envelope.To,
		From:    mailer.Address{Address: envelope.From.Address, Name: envelope.From.Name},
	}, nil
}
