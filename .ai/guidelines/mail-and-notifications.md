# Mail & Notifications Guideline

> Status: DECIDED (B2.5). `facades.Mail().Queue()` refuses. A reset or invite
> mail is `jobs.SendCredentialMailJob` with `(subject_id, purpose)` only. The
> job mints the token and calls `Mail().Send()`. The add-user response still
> returns `invite_link`: that handler dispatches the same job, and the sync
> run returns the minted link without placing it on the queue. Welcome and
> the settings test mail stay on `Send`.

## The mailables

`app/mails`: `password_reset_mail.go` (credential: reset link),
`user_invite_mail.go` (credential: invite/setup link), `welcome_mail.go` (no
credential). Templates are embedded files (`app/mails/templates/*.html` via
`go:embed`), presentational only; the Mailable builds the data.

## Send, not Queue — deliberate

**`facades.Mail().Queue()` refuses.** It renders the message into a payload, and
a payload is at rest in the queue and in the worker's log.

A credential e-mail is dispatched as **`jobs.SendCredentialMailJob` carrying
`(user_id, purpose)` and nothing else.** The worker mints the reset/invite token
**at send time** (storing only its hash) and calls `Mail().Send()` inside the
job. A non-credential mail (welcome) may use the same job with its own purpose.

- No controller sends mail. The service that decides a mail is due dispatches
  the job through a dispatcher port.
- A send whose outcome is unknown (deadline hit mid-send) is **not retried**: a
  retry would mint a second credential and invalidate the one already in the
  inbox. Log at WARN; the user asks again.
- `POST /v1/auth/recover` answers the same whether or not the address exists.

## Drivers and environments

Driver and credentials come from config (`MAIL_*`); local uses Mailpit or a
`log` driver that is refused in production. SMTP passwords and API keys are
never hardcoded, never in fixtures, never logged.

`facades.Mail()` is `app/providers/mail.Mailer`, bound by `MailServiceProvider`
over the framework SMTP application. Each `Send` reads the platform `mail_smtp`
row and the `mail_delivery` From header from the settings service; a field not
in use, a missing row or a failed read keeps the `MAIL_*` value. The framework
dials with the process config, so the send writes its document to the `mail`
key, dials, and puts the previous document back, holding one process-wide lock
for the whole send: sends are serialised. The provider declares no
`Relationship()`: one naming `binding.Mail` would give the key back to the
framework mailer.

## Never logged

The rendered message, reset/invite tokens, and any provider credential.

## Tests

- The job payload carries no credential.
- The token is minted at send time and only its hash is stored.
- An unknown send outcome is not retried.
- Feature tests read the token from the database, not from an inbox.
