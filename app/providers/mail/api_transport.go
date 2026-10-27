package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	contractsmail "github.com/goravel/framework/contracts/mail"

	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	driverSMTP     = "smtp"
	driverLog      = "log"
	driverSES      = "ses"
	driverMailgun  = "mailgun"
	driverResend   = "resend"
	driverPostmark = "postmark"

	apiSendTimeout = 20 * time.Second
	// apiErrorBodyLimit bounds how much of a provider's answer is read; none of it
	// goes into an error.
	apiErrorBodyLimit = 64 << 10
)

// APIBases are the HTTPS bases of the HTTP mail providers; tests point them at a
// local server. A zero field is the provider's public API.
type APIBases struct {
	Mailgun  string
	Resend   string
	Postmark string
}

var defaultAPIBases = APIBases{
	Mailgun:  "https://api.mailgun.net",
	Resend:   "https://api.resend.com",
	Postmark: "https://api.postmarkapp.com",
}

// mailgunHosts are the API hosts a mailgun endpoint may name.
var mailgunHosts = map[string]bool{"api.mailgun.net": true, "api.eu.mailgun.net": true}

// errMissingCredential is a send through a provider whose group lacks a credential.
var errMissingCredential = errors.New("mail: the selected transport has no credential stored; it does not fall back to another one")

// message is what a send carries, gathered from the builder calls and the
// mailables, for a transport that does not go through the framework (an HTTP API).
type message struct {
	from        contractsmail.Address
	to          []string
	cc          []string
	bcc         []string
	subject     string
	html        string
	text        string
	headers     map[string]string
	attachments []string
}

func (m message) clone() message {
	out := m
	out.to = append([]string(nil), m.to...)
	out.cc = append([]string(nil), m.cc...)
	out.bcc = append([]string(nil), m.bcc...)
	out.attachments = append([]string(nil), m.attachments...)
	out.headers = make(map[string]string, len(m.headers))
	for key, value := range m.headers {
		out.headers[key] = value
	}
	return out
}

// withMailable merges one mailable over the builder state, as the framework does:
// its envelope fields win when set, its headers and attachments are added.
func (m message) withMailable(mailable contractsmail.Mailable) (message, error) {
	out := m.clone()
	if mailable == nil {
		return out, nil
	}
	if envelope := mailable.Envelope(); envelope != nil {
		if envelope.From.Address != "" {
			out.from = envelope.From
		}
		if len(envelope.To) > 0 {
			out.to = envelope.To
		}
		if len(envelope.Cc) > 0 {
			out.cc = envelope.Cc
		}
		if len(envelope.Bcc) > 0 {
			out.bcc = envelope.Bcc
		}
		if envelope.Subject != "" {
			out.subject = envelope.Subject
		}
	}
	if content := mailable.Content(); content != nil {
		if content.View != "" && content.Html == "" && content.Text == "" {
			return message{}, errors.New("mail: a view is rendered by the smtp transport only; give the mailable an html body")
		}
		if content.Html != "" {
			out.html = content.Html
		}
		if content.Text != "" {
			out.text = content.Text
		}
	}
	for key, value := range mailable.Headers() {
		out.headers[key] = value
	}
	out.attachments = append(out.attachments, mailable.Attachments()...)
	return out, nil
}

type attachment struct {
	name        string
	contentType string
	content     []byte
}

func (m message) readAttachments() ([]attachment, error) {
	out := make([]attachment, 0, len(m.attachments))
	for _, path := range m.attachments {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("mail: read attachment %s: %w", filepath.Base(path), err)
		}
		name := filepath.Base(path)
		contentType := mime.TypeByExtension(filepath.Ext(name))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		out = append(out, attachment{name: name, contentType: contentType, content: content})
	}
	return out, nil
}

// apiTransport posts a message to one HTTP mail provider.
type apiTransport struct {
	client *httpclient.Client
	bases  APIBases
}

func newAPITransport(bases APIBases) *apiTransport {
	if bases.Mailgun == "" {
		bases.Mailgun = defaultAPIBases.Mailgun
	}
	if bases.Resend == "" {
		bases.Resend = defaultAPIBases.Resend
	}
	if bases.Postmark == "" {
		bases.Postmark = defaultAPIBases.Postmark
	}
	return &apiTransport{client: httpclient.NewClient(apiSendTimeout), bases: bases}
}

func (t *apiTransport) send(ctx context.Context, provider settings.MailProvider, msg message) error {
	if strings.TrimSpace(provider.APIKey) == "" {
		return errMissingCredential
	}
	if len(msg.to) == 0 {
		return errors.New("mail: a message needs a recipient")
	}
	if msg.from.Address == "" {
		return errors.New("mail: a message needs a from address")
	}
	attachments, err := msg.readAttachments()
	if err != nil {
		return err
	}
	var req httpclient.Request
	switch provider.Driver {
	case driverMailgun:
		req, err = t.mailgunRequest(provider, msg, attachments)
	case driverResend:
		req, err = t.resendRequest(provider, msg, attachments)
	case driverPostmark:
		req, err = t.postmarkRequest(provider, msg, attachments)
	default:
		return fmt.Errorf("mail: transport %q has no HTTP API", provider.Driver)
	}
	if err != nil {
		return err
	}
	req.Method, req.HasBody, req.MaxBytes = http.MethodPost, true, apiErrorBodyLimit
	resp, err := t.client.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("mail: %s did not answer", provider.Driver)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("mail: %s refused the message (HTTP %d)", provider.Driver, resp.StatusCode)
	}
	return nil
}

func formatAddress(address contractsmail.Address) string {
	if address.Name == "" {
		return address.Address
	}
	return (&mail.Address{Name: address.Name, Address: address.Address}).String()
}

func (t *apiTransport) mailgunRequest(provider settings.MailProvider, msg message, attachments []attachment) (httpclient.Request, error) {
	domain := strings.TrimSpace(provider.Domain)
	if domain == "" || strings.ContainsAny(domain, "/?#@ ") {
		return httpclient.Request{}, errors.New("mail: mailgun needs its sending domain")
	}
	base := t.bases.Mailgun
	if endpoint := strings.TrimSpace(provider.Endpoint); endpoint != "" && base == defaultAPIBases.Mailgun {
		host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), "/")
		if !mailgunHosts[host] {
			return httpclient.Request{}, errors.New("mail: the mailgun endpoint must be api.mailgun.net or api.eu.mailgun.net")
		}
		base = "https://" + host
	}
	fields := url.Values{}
	fields.Set("from", formatAddress(msg.from))
	for _, to := range msg.to {
		fields.Add("to", to)
	}
	for _, cc := range msg.cc {
		fields.Add("cc", cc)
	}
	for _, bcc := range msg.bcc {
		fields.Add("bcc", bcc)
	}
	fields.Set("subject", msg.subject)
	if msg.html != "" {
		fields.Set("html", msg.html)
	}
	if msg.text != "" {
		fields.Set("text", msg.text)
	}
	for key, value := range msg.headers {
		fields.Set("h:"+key, value)
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, values := range fields {
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				return httpclient.Request{}, err
			}
		}
	}
	for _, file := range attachments {
		part, err := writer.CreateFormFile("attachment", file.name)
		if err != nil {
			return httpclient.Request{}, err
		}
		if _, err := part.Write(file.content); err != nil {
			return httpclient.Request{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return httpclient.Request{}, err
	}
	return httpclient.Request{
		URL:      base + "/v3/" + url.PathEscape(domain) + "/messages",
		Header:   map[string]string{"Content-Type": writer.FormDataContentType()},
		Body:     body.Bytes(),
		Username: "api",
		Password: provider.APIKey,
	}, nil
}

func jsonRequest(rawURL string, payload any, header map[string]string) (httpclient.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return httpclient.Request{}, err
	}
	header["Content-Type"] = "application/json"
	header["Accept"] = "application/json"
	return httpclient.Request{URL: rawURL, Header: header, Body: body}, nil
}

func (t *apiTransport) resendRequest(provider settings.MailProvider, msg message, attachments []attachment) (httpclient.Request, error) {
	type resendAttachment struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}
	payload := map[string]any{
		"from":    formatAddress(msg.from),
		"to":      msg.to,
		"subject": msg.subject,
	}
	if len(msg.cc) > 0 {
		payload["cc"] = msg.cc
	}
	if len(msg.bcc) > 0 {
		payload["bcc"] = msg.bcc
	}
	if msg.html != "" {
		payload["html"] = msg.html
	}
	if msg.text != "" {
		payload["text"] = msg.text
	}
	if len(msg.headers) > 0 {
		payload["headers"] = msg.headers
	}
	if len(attachments) > 0 {
		files := make([]resendAttachment, 0, len(attachments))
		for _, file := range attachments {
			files = append(files, resendAttachment{Filename: file.name, Content: base64.StdEncoding.EncodeToString(file.content)})
		}
		payload["attachments"] = files
	}
	return jsonRequest(t.bases.Resend+"/emails", payload, map[string]string{"Authorization": "Bearer " + provider.APIKey})
}

func (t *apiTransport) postmarkRequest(provider settings.MailProvider, msg message, attachments []attachment) (httpclient.Request, error) {
	type header struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	}
	type postmarkAttachment struct {
		Name        string `json:"Name"`
		Content     string `json:"Content"`
		ContentType string `json:"ContentType"`
	}
	payload := map[string]any{
		"From":    formatAddress(msg.from),
		"To":      strings.Join(msg.to, ","),
		"Subject": msg.subject,
	}
	if len(msg.cc) > 0 {
		payload["Cc"] = strings.Join(msg.cc, ",")
	}
	if len(msg.bcc) > 0 {
		payload["Bcc"] = strings.Join(msg.bcc, ",")
	}
	if msg.html != "" {
		payload["HtmlBody"] = msg.html
	}
	if msg.text != "" {
		payload["TextBody"] = msg.text
	}
	if stream := strings.TrimSpace(provider.MessageStream); stream != "" {
		payload["MessageStream"] = stream
	}
	if len(msg.headers) > 0 {
		headers := make([]header, 0, len(msg.headers))
		for name, value := range msg.headers {
			headers = append(headers, header{Name: name, Value: value})
		}
		payload["Headers"] = headers
	}
	if len(attachments) > 0 {
		files := make([]postmarkAttachment, 0, len(attachments))
		for _, file := range attachments {
			files = append(files, postmarkAttachment{Name: file.name, Content: base64.StdEncoding.EncodeToString(file.content), ContentType: file.contentType})
		}
		payload["Attachments"] = files
	}
	return jsonRequest(t.bases.Postmark+"/email", payload, map[string]string{"X-Postmark-Server-Token": provider.APIKey})
}
