package mail

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	contractsmail "github.com/goravel/framework/contracts/mail"

	"github.com/macrowallets/waas/app/services/settings"
)

type capturedRequest struct {
	path   string
	header http.Header
	body   []byte
	form   map[string][]string
}

type providerServer struct {
	mu       sync.Mutex
	requests []capturedRequest
	status   int
	server   *httptest.Server
}

func newProviderServer(t *testing.T, status int) *providerServer {
	t.Helper()
	p := &providerServer{status: status}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := capturedRequest{path: r.URL.Path, header: r.Header.Clone()}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err := r.ParseMultipartForm(1 << 20); err == nil {
				captured.form = r.MultipartForm.Value
			}
		} else {
			captured.body, _ = io.ReadAll(r.Body)
		}
		p.mu.Lock()
		p.requests = append(p.requests, captured)
		p.mu.Unlock()
		w.WriteHeader(p.status)
		_, _ = w.Write([]byte(`{"message":"echo of the token: provider-secret"}`))
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *providerServer) only(t *testing.T) capturedRequest {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 1 {
		t.Fatalf("provider got %d requests, want 1", len(p.requests))
	}
	return p.requests[0]
}

type htmlMail struct{}

func (htmlMail) Envelope() *contractsmail.Envelope {
	return &contractsmail.Envelope{To: []string{"person@example.test"}, Subject: "Reset Your Password"}
}
func (htmlMail) Content() *contractsmail.Content { return &contractsmail.Content{Html: "<p>hi</p>"} }
func (htmlMail) Attachments() []string           { return nil }
func (htmlMail) Headers() map[string]string {
	return map[string]string{"X-Vault-Mail-Type": "password-reset"}
}
func (htmlMail) Queue() *contractsmail.Queue { return nil }

func apiMailer(t *testing.T, driver string, provider settings.MailProvider, server *providerServer) (*Mailer, *recordingMail) {
	t.Helper()
	cfg := newDocumentConfig(map[string]any{
		"driver": "smtp", "app_env": "local",
		"from":    map[string]any{"address": "env@example.test", "name": "Env"},
		"mailers": map[string]any{"smtp": map[string]any{"transport": "smtp", "host": "env-host"}},
	})
	transport := &recordingMail{}
	base := server.server.URL
	return NewMailer(MailerDeps{
		Transport: transport, Config: cfg,
		Settings: storedSettings{
			from:     settings.MailDelivery{Driver: driver, UseDriver: true, Address: "noreply@macro.test", Name: "Macro", UseAddress: true, UseName: true},
			provider: provider,
		},
		APIBases: APIBases{Mailgun: base, Resend: base, Postmark: base},
	}), transport
}

func TestSend_Mailgun_PostsTheFormWithBasicAuthAndNeverDialsSMTP(t *testing.T) {
	server := newProviderServer(t, http.StatusOK)
	mailer, smtp := apiMailer(t, driverMailgun, settings.MailProvider{Domain: "mg.macro.test", APIKey: "provider-secret"}, server)

	if err := mailer.To([]string{"ignored@example.test"}).Send(htmlMail{}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if smtp.sent {
		t.Fatal("a mailgun send dialed SMTP")
	}
	req := server.only(t)
	if req.path != "/v3/mg.macro.test/messages" {
		t.Fatalf("path = %q", req.path)
	}
	user, pass, ok := (&http.Request{Header: req.header}).BasicAuth()
	if !ok || user != "api" || pass != "provider-secret" {
		t.Fatal("mailgun basic auth is not api:<key>")
	}
	if got := req.form["to"]; len(got) != 1 || got[0] != "person@example.test" {
		t.Fatalf("to = %v, the mailable's envelope wins", got)
	}
	if req.form["from"][0] != `"Macro" <noreply@macro.test>` || req.form["subject"][0] != "Reset Your Password" ||
		req.form["html"][0] != "<p>hi</p>" || req.form["h:X-Vault-Mail-Type"][0] != "password-reset" {
		t.Fatalf("form = %v", req.form)
	}
}

func TestSend_Resend_PostsJSONWithTheBearerKeyAndTheProviderSenderOverride(t *testing.T) {
	server := newProviderServer(t, http.StatusOK)
	mailer, _ := apiMailer(t, driverResend, settings.MailProvider{APIKey: "provider-secret", FromAddress: "billing@macro.test"}, server)

	if err := mailer.Send(htmlMail{}); err != nil {
		t.Fatalf("send: %v", err)
	}
	req := server.only(t)
	if req.path != "/emails" || req.header.Get("Authorization") != "Bearer provider-secret" {
		t.Fatalf("path %q auth %q", req.path, req.header.Get("Authorization"))
	}
	var body map[string]any
	if err := json.Unmarshal(req.body, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["from"] != `"Macro" <billing@macro.test>` || body["html"] != "<p>hi</p>" || body["subject"] != "Reset Your Password" {
		t.Fatalf("body = %v", body)
	}
	if to, _ := body["to"].([]any); len(to) != 1 || to[0] != "person@example.test" {
		t.Fatalf("to = %v", body["to"])
	}
}

func TestSend_Postmark_PostsJSONWithTheServerTokenAndStream(t *testing.T) {
	server := newProviderServer(t, http.StatusOK)
	mailer, _ := apiMailer(t, driverPostmark, settings.MailProvider{APIKey: "provider-secret", MessageStream: "outbound"}, server)

	if err := mailer.Send(htmlMail{}); err != nil {
		t.Fatalf("send: %v", err)
	}
	req := server.only(t)
	if req.path != "/email" || req.header.Get("X-Postmark-Server-Token") != "provider-secret" {
		t.Fatalf("path %q token %q", req.path, req.header.Get("X-Postmark-Server-Token"))
	}
	var body map[string]any
	if err := json.Unmarshal(req.body, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["To"] != "person@example.test" || body["HtmlBody"] != "<p>hi</p>" || body["MessageStream"] != "outbound" {
		t.Fatalf("body = %v", body)
	}
}

func TestSend_ProviderRefusalIsAnErrorWithoutTheAnswerOrTheKey(t *testing.T) {
	server := newProviderServer(t, http.StatusUnauthorized)
	mailer, _ := apiMailer(t, driverPostmark, settings.MailProvider{APIKey: "provider-secret"}, server)

	err := mailer.Send(htmlMail{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("err = %v, want the status", err)
	}
	if strings.Contains(err.Error(), "provider-secret") {
		t.Fatal("the error carries the key the provider echoed")
	}
}

func TestSend_ATransportWithoutItsCredentialFailsAndNeverFallsBack(t *testing.T) {
	server := newProviderServer(t, http.StatusOK)
	mailer, smtp := apiMailer(t, driverResend, settings.MailProvider{}, server)

	err := mailer.Send(htmlMail{})
	if !errors.Is(err, errMissingCredential) {
		t.Fatalf("err = %v, want a missing credential", err)
	}
	if smtp.sent || len(server.requests) != 0 {
		t.Fatal("a send without credentials went out through another transport")
	}

	sesMailer, sesSMTP := apiMailer(t, driverSES, settings.MailProvider{}, server)
	if err := sesMailer.Send(htmlMail{}); err == nil || sesSMTP.sent {
		t.Fatalf("ses must fail without sending: err %v smtp %v", err, sesSMTP.sent)
	}
}

func TestSend_MailgunRefusesAnEndpointThatIsNotMailgun(t *testing.T) {
	transport := newAPITransport(APIBases{})
	_, err := transport.mailgunRequest(settings.MailProvider{Driver: driverMailgun, Domain: "mg.macro.test", APIKey: "k", Endpoint: "https://169.254.169.254"},
		message{to: []string{"a@example.test"}, from: contractsmail.Address{Address: "b@example.test"}}, nil)
	if err == nil {
		t.Fatal("a mailgun endpoint outside mailgun's API hosts was accepted")
	}
	req, err := transport.mailgunRequest(settings.MailProvider{Driver: driverMailgun, Domain: "mg.macro.test", APIKey: "k", Endpoint: "api.eu.mailgun.net"},
		message{to: []string{"a@example.test"}, from: contractsmail.Address{Address: "b@example.test"}}, nil)
	if err != nil || req.URL != "https://api.eu.mailgun.net/v3/mg.macro.test/messages" {
		t.Fatalf("eu endpoint: %v %q", err, req.URL)
	}
}
