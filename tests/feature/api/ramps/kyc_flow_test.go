package ramps_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	goravelTesting "github.com/goravel/framework/testing"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/services/ramps"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support"
)

// bridgeStub stands in for api.bridge.xyz at the HTTP boundary, so the adapter,
// the engine and the native composition all run for real. It keeps what it was
// sent: the assertions below are about what would have reached the provider.
type bridgeStub struct {
	mu        sync.Mutex
	customers map[string]map[string]any
	calls     []stubCall
	apiKeys   []string
	base      string
}

type stubCall struct {
	Method, Path string
	Body         map[string]any
}

func newBridgeStub() *bridgeStub { return &bridgeStub{customers: map[string]map[string]any{}} }

func (b *bridgeStub) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.customers, b.calls, b.apiKeys = map[string]map[string]any{}, nil, nil
}

func (b *bridgeStub) sent(method, prefix string) []stubCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []stubCall
	for _, call := range b.calls {
		if call.Method == method && strings.HasPrefix(call.Path, prefix) {
			out = append(out, call)
		}
	}
	return out
}

func (b *bridgeStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)
	b.calls = append(b.calls, stubCall{r.Method, r.URL.Path, body})
	b.apiKeys = append(b.apiKeys, r.Header.Get("Api-Key"))
	reply := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/ozav/"):
		b.serveOzav(r, body, reply)
	case strings.HasPrefix(r.URL.Path, "/blindpay/"):
		b.serveBlindPay(r, body, reply)
	case r.Method == http.MethodPost && r.URL.Path == "/customers/tos_links":
		reply(201, map[string]any{"url": "https://dashboard.sandbox.bridge.xyz/accept-terms-of-service?session_token=stub"})
	case r.Method == http.MethodPost && r.URL.Path == "/customers":
		// The engine's schema outlives a test's truncation, so an id is never reused.
		id := "cus_stub_" + uuid.NewString()
		customer := map[string]any{"id": id, "type": body["type"], "email": body["email"], "status": "incomplete", "requirements_due": requirementsFor(body["type"]), "has_accepted_terms_of_service": true, "endorsements": []any{}, "rejection_reasons": []any{}}
		if people, ok := body["associated_persons"].([]any); ok {
			refs := make([]any, 0, len(people))
			for i, person := range people {
				refs = append(refs, map[string]any{"id": fmt.Sprintf("per_stub_%d", i+1), "email": person.(map[string]any)["email"]})
			}
			customer["associated_persons"] = refs
		}
		b.customers[id] = customer
		reply(201, customer)
	case strings.HasPrefix(r.URL.Path, "/customers/"):
		id := strings.Split(strings.TrimPrefix(r.URL.Path, "/customers/"), "/")[0]
		customer, ok := b.customers[id]
		if !ok {
			reply(404, map[string]any{"code": "not_found"})
			return
		}
		if r.Method == http.MethodPut || strings.HasSuffix(r.URL.Path, "/associated_persons") {
			customer["status"], customer["requirements_due"] = "under_review", []any{}
		}
		if strings.Contains(r.URL.Path, "/associated_persons") {
			reply(200, map[string]any{"id": "per_stub_1"})
			return
		}
		reply(200, customer)
	default:
		reply(404, map[string]any{"code": "not_found"})
	}
}

// serveOzav answers the wire the Ozav adapter speaks: terms sessions, the
// customer, and each document's declare-then-upload.
func (b *bridgeStub) serveOzav(r *http.Request, body map[string]any, reply func(int, any)) {
	path := strings.TrimPrefix(r.URL.Path, "/ozav")
	switch {
	case path == "/customers/terms-sessions/accept":
		reply(200, map[string]any{"status": "accepted"})
	case path == "/customers/terms-sessions":
		reply(201, map[string]any{"id": "ozav_terms_" + uuid.NewString(), "status": "pending", "terms_url": "https://ozav.example.com/terms", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	case r.Method == http.MethodPost && path == "/customers":
		id := "ozav_cus_" + uuid.NewString()
		b.customers[id] = map[string]any{"id": id, "email": body["email"], "status": "pending", "requirements": map[string]any{"due": []any{"government_id_document_missing", "selfie_missing"}}}
		reply(201, b.customers[id])
	case strings.HasSuffix(path, "/documents") && r.Method == http.MethodPost:
		reply(201, map[string]any{"id": "doc", "status": "pending", "upload_url": b.base + "/ozav/upload"})
	case strings.HasSuffix(path, "/documents"):
		reply(200, map[string]any{"data": []any{}})
	default:
		customer, ok := b.customers[strings.TrimPrefix(path, "/customers/")]
		if !ok {
			reply(404, map[string]any{"code": "not_found"})
			return
		}
		reply(200, customer)
	}
}

// serveBlindPay answers the wire the BlindPay adapter speaks: hosted terms,
// the uploads, and the one atomic create.
func (b *bridgeStub) serveBlindPay(r *http.Request, body map[string]any, reply func(int, any)) {
	path := strings.TrimPrefix(r.URL.Path, "/blindpay")
	switch {
	case path == "/upload":
		reply(200, map[string]any{"file_url": "https://files.blindpay.example.com/" + uuid.NewString()})
	case strings.HasSuffix(path, "/tos"):
		reply(200, map[string]any{"url": fmt.Sprintf("https://%s%s?session_token=stub&idempotency_key=%v", blindPayTosHost, blindPayTosPath, body["idempotency_key"])})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/customers"):
		id := "re_" + uuid.NewString()
		b.customers[id] = map[string]any{"id": id, "customer_id": id, "email": body["email"], "kyc_status": "verifying", "country": "BR", "kyc_type": "standard"}
		reply(201, b.customers[id])
	case strings.Contains(path, "/rfi"):
		reply(200, []any{})
	default:
		parts := strings.Split(path, "/")
		customer, ok := b.customers[parts[len(parts)-1]]
		if !ok {
			reply(404, map[string]any{"code": "not_found"})
			return
		}
		reply(200, customer)
	}
}

// requirementsFor is what an application leaves owed: only documents.
func requirementsFor(kind any) []any {
	if kind == "business" {
		return []any{"government_id", "business_formation_document"}
	}
	return []any{"government_id_document_missing", "selfie_missing"}
}

func (b *bridgeStub) setStatus(id, status string) map[string]any {
	return b.setField(id, "status", status)
}

func (b *bridgeStub) setField(id, field string, value any) map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.customers[id][field] = value
	return b.customers[id]
}

func (b *bridgeStub) onlyCustomer() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := range b.customers {
		return id
	}
	return ""
}

const (
	blindPayTosHost = "app.blindpay.com"
	blindPayTosPath = "/e/terms-of-service"
	fullGrants      = `["ramp_customers.read","ramp_customers.write","ramp_compliance.read","ramp_compliance.profile.read","ramp_compliance.write","ramp_orders.read","ramp_orders.create"]`
	stubAPIKey      = "sandbox-stub-key"
	anaRef          = "0c8e6c5e-3f4a-4a11-9c1d-1f6f7f2b3a10"
	tinyPNG         = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
)

// enableBridge turns Bridge on for one environment through the same sealed
// settings an operator writes.
func (s *RampSuite) enableBridge(mode string) *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	s.Require().NoError(err)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	s.Require().NoError(err)
	public := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	sealer := settings.CryptSealer{}
	sealedKey, err := sealer.Seal(stubAPIKey)
	s.Require().NoError(err)
	sealedPublic, err := sealer.Seal(public)
	s.Require().NoError(err)
	group := settings.RampGroupName("provider_bridge", mode)
	for k, v := range map[string]string{"enabled": "true", "mode": mode, "api_key": sealedKey, "webhook_public_key": sealedPublic} {
		_, err = facades.Orm().Query().Exec(`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at) VALUES (NULL, ?, ?, ?, NOW(), NOW())`, group, k, v)
		s.Require().NoError(err)
	}
	return key
}

func decodeBody[T any](s *RampSuite, resp interface{ Content() (string, error) }) (T, string) {
	var out T
	body, err := resp.Content()
	s.Require().NoError(err)
	s.Require().NoError(json.Unmarshal([]byte(body), &out), body)
	return out, body
}

func (s *RampSuite) createCustomer(token support.Token, payload map[string]any) string {
	resp := s.External("/api/v1/customers", token).Post(payload)
	resp.AssertStatus(201)
	created, _ := decodeBody[struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}](s, resp)
	s.Require().NotEmpty(created.Data.ID)
	return created.Data.ID
}

func (s *RampSuite) work() {
	_, err := container.MustMake[*ramps.Service]().Work(context.Background(), 20)
	s.Require().NoError(err)
	var failed []struct {
		Kind      string `gorm:"column:kind"`
		LastError string `gorm:"column:last_error"`
	}
	s.Require().NoError(facades.Orm().Query().Raw("SELECT kind,last_error FROM ramp_tasks WHERE last_error IS NOT NULL").Scan(&failed))
	s.Empty(failed, "a task the worker could not finish")
}

// deliver posts a provider callback through the real, unauthenticated route.
func (s *RampSuite) deliver(mode string, key *rsa.PrivateKey, body []byte) contractstestinghttp.Response {
	stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	digest := sha256.Sum256([]byte(stamp + "." + string(body)))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	s.Require().NoError(err)
	tc := goravelTesting.TestCase{}
	resp, err := tc.Http(s.T()).WithHeader("Content-Type", "application/json").
		WithHeader("X-Webhook-Signature", "t="+stamp+",v0="+base64.StdEncoding.EncodeToString(signature)).
		Post("/v1/webhooks/ramps/"+mode+"/bridge", strings.NewReader(string(body)))
	s.Require().NoError(err)
	return resp
}

func customerEvent(id string, object map[string]any) []byte {
	body, _ := json.Marshal(map[string]any{"event_id": "evt_" + id + "_" + fmt.Sprint(time.Now().UnixNano()), "event_category": "customer", "event_type": "customer.updated.status_transitioned", "event_object_id": id, "event_created_at": time.Now().UTC().Format(time.RFC3339), "event_object": object})
	return body
}

func individualApplication(email string) map[string]any {
	return map[string]any{"first_name": "Ana", "last_name": "Souza", "email": email, "birth_date": "1990-05-20", "tax_id": "52998224725", "tax_id_type": "cpf", "tax_id_country": "BRA", "phone": "+5511987654321", "street_line_1": "Avenida Paulista 1000", "city": "Sao Paulo", "subdivision": "SP", "postal_code": "01310-100", "country": "BRA", "nationality": "BRA", "source_of_funds": "employment_salary", "account_purpose": "receive_payments", "is_pep": false}
}

// TestKycIndividualJourney walks one person from creation to approval through
// the public API, the worker and a signed provider callback.
func (s *RampSuite) TestKycIndividualJourney() {
	providerStub.reset()
	webhookKey := s.enableBridge("sandbox")
	account := s.account("test")
	token := s.token(account, fullGrants)
	const email = "ana.kyc@example.com"
	id := s.createCustomer(token, map[string]any{"external_id": "kyc-pf-1", "relationship": "end_customer", "entity_type": "individual", "name": "Ana Souza", "email": email})
	base := "/api/v1/customers/" + id + "/kyc"

	s.External(base+"/countries", token).Get().AssertStatus(200)
	s.External(base+"/overview", token).Get().AssertStatus(200)

	tos := s.External(base+"/tos", token).Post(map[string]any{"redirect_uri": "https://wallet.example.com/cash/terms-return", "country": "BRA", "tax_id_type": "cpf", "tax_id_country": "BRA"})
	body, err := tos.Content()
	s.Require().NoError(err)
	s.T().Log("tos:", body)
	tos.AssertStatus(201)

	accepted := s.External(base+"/tos/acceptances", token).Post(map[string]any{"acceptances": []map[string]any{{"provider": "bridge", "signed_agreement_id": "agr_stub_1"}}})
	body, err = accepted.Content()
	s.Require().NoError(err)
	s.T().Log("acceptances:", body)
	accepted.AssertStatus(200)

	application := s.External(base+"/application", token).Post(individualApplication(email))
	body, err = application.Content()
	s.Require().NoError(err)
	s.T().Log("application:", body)
	application.AssertStatus(202)
	s.NotContains(body, "52998224725", "the reply must not echo the tax id")

	s.work()
	afterWorker := s.External(base+"/bridge", token).Get()
	workerBody, err := afterWorker.Content()
	s.Require().NoError(err)
	s.T().Log("after worker:", workerBody)
	created := providerStub.sent(http.MethodPost, "/customers")
	var customerID string
	for _, call := range created {
		if call.Path == "/customers" {
			s.Equal("individual", call.Body["type"])
			s.Equal(email, call.Body["email"])
			s.Equal("agr_stub_1", call.Body["signed_agreement_id"])
		}
	}
	for existing := range providerStub.customers {
		customerID = existing
	}
	s.Require().NotEmpty(customerID, "the worker must reach the provider: %+v", providerStub.calls)
	for _, used := range providerStub.apiKeys {
		s.Equal(stubAPIKey, used)
	}

	documents := s.External(base+"/documents", token).Post(map[string]any{"document_type": "national_id", "issuing_country": "BRA", "image_front": tinyPNG, "image_back": tinyPNG, "selfie": tinyPNG})
	body, err = documents.Content()
	s.Require().NoError(err)
	s.T().Log("documents:", body)
	documents.AssertStatus(202)
	s.NotContains(body, "iVBORw0KGgo", "the reply must not echo the document")
	s.work()
	s.NotEmpty(providerStub.sent(http.MethodPut, "/customers/"+customerID), "documents must reach the provider")

	object := providerStub.setStatus(customerID, "active")
	s.deliver("production", webhookKey, customerEvent(customerID, object)).AssertStatus(401)
	s.deliver("sandbox", webhookKey, customerEvent(customerID, object)).AssertStatus(202)
	s.work()

	overview := s.External(base+"/bridge", token).Get()
	body, err = overview.Content()
	s.Require().NoError(err)
	s.T().Log("provider state:", body)
	overview.AssertStatus(200)
	state, _ := decodeBody[map[string]any](s, overview)
	s.Equal("approved", state["kyc_status"], body)
	s.Equal("approved", state["tos_status"], body)
}

// enablePixProvider turns one provider on for the sandbox and routes the Pix
// rails to it, through the same sealed settings an operator writes.
func (s *RampSuite) enablePixProvider(code, key string, plain map[string]string) {
	sealed, err := settings.CryptSealer{}.Seal(key)
	s.Require().NoError(err)
	values := map[string]string{"enabled": "true", "mode": "sandbox", "api_key": sealed}
	for k, v := range plain {
		values[k] = v
	}
	const insert = `INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at) VALUES (NULL, ?, ?, ?, NOW(), NOW())`
	for k, v := range values {
		_, err = facades.Orm().Query().Exec(insert, settings.RampGroupName("provider_"+code, "sandbox"), k, v)
		s.Require().NoError(err)
	}
	for _, rail := range []string{"rail_pix_payin", "rail_pix_payout"} {
		_, err = facades.Orm().Query().Exec(insert, settings.RampGroupName(rail, "sandbox"), "provider_order", code)
		s.Require().NoError(err)
	}
}

func (s *RampSuite) person(token support.Token, reference, email string) string {
	id := s.createCustomer(token, map[string]any{"external_id": reference, "relationship": "end_customer", "entity_type": "individual", "name": "Ana Souza", "email": email})
	return "/api/v1/customers/" + id + "/kyc"
}

var termsSession = map[string]any{"redirect_uri": "https://wallet.example.com/cash/terms-return", "modes": []string{"hosted", "attested"}, "country": "BRA", "tax_id_type": "cpf", "tax_id_country": "BRA"}

// TestKycJourneyAtOzav: terms attested by us, documents declared one by one.
func (s *RampSuite) TestKycJourneyAtOzav() {
	providerStub.reset()
	s.enablePixProvider("ozav", "ozav_sk_test_stub", nil)
	token := s.token(s.account("test"), fullGrants)
	const email = "ana.ozav@example.com"
	base := s.person(token, "kyc-ozav-1", email)

	s.External(base+"/tos", token).Post(termsSession).AssertStatus(201)
	s.External(base+"/tos/acceptances", token).Post(map[string]any{"acceptances": []map[string]any{{"provider": "ozav", "legal_name": "Ana Souza"}}}).AssertStatus(200)
	s.External(base+"/application", token).Post(individualApplication(email)).AssertStatus(202)
	s.work()
	customerID := providerStub.onlyCustomer()
	s.Require().NotEmpty(customerID, "the worker must reach the provider: %+v", providerStub.calls)

	s.External(base+"/documents", token).Post(map[string]any{"document_type": "national_id", "issuing_country": "BRA", "image_front": tinyPNG, "image_back": tinyPNG, "selfie": tinyPNG}).AssertStatus(202)
	s.work()
	// The adapter only sends its key to allow-listed https hosts, so the upload
	// that follows the declare is covered by the adapter's own tests.
	s.NotEmpty(providerStub.sent(http.MethodPost, "/ozav/customers/"+customerID+"/documents"), "the documents must be declared: %+v", providerStub.calls)

	providerStub.setField(customerID, "requirements", map[string]any{})
	providerStub.setField(customerID, "verification", map[string]any{"mode": "standard"})
	providerStub.setStatus(customerID, "approved")
	refreshed := s.External(base+"/ozav/refresh", token).Post(map[string]any{})
	refreshed.AssertStatus(200)
	state, raw := decodeBody[map[string]any](s, refreshed)
	s.Equal("approved", state["kyc_status"], raw)
}

// TestKycJourneyAtBlindPay: nothing reaches the provider until the application
// and the documents can create the customer in one call.
func (s *RampSuite) TestKycJourneyAtBlindPay() {
	providerStub.reset()
	s.enablePixProvider("blindpay", "blindpay-stub-key", map[string]string{"instance_id": "in_stubinstance"})
	token := s.token(s.account("test"), fullGrants)
	const email = "ana.blindpay@example.com"
	base := s.person(token, "kyc-blindpay-1", email)

	opened := s.External(base+"/tos", token).Post(termsSession)
	opened.AssertStatus(201)
	s.External(base+"/tos/acceptances", token).Post(map[string]any{"acceptances": []map[string]any{{"provider": "blindpay", "signed_agreement_id": "11111111-1111-4111-8111-111111111111"}}}).AssertStatus(200)
	s.External(base+"/application", token).Post(individualApplication(email)).AssertStatus(202)
	s.work()
	s.Empty(providerStub.onlyCustomer(), "the application alone must not create the customer")

	s.External(base+"/documents", token).Post(map[string]any{"document_type": "national_id", "issuing_country": "BRA", "image_front": tinyPNG, "image_back": tinyPNG, "selfie": tinyPNG, "proof_of_address": tinyPNG}).AssertStatus(202)
	s.work()
	customerID := providerStub.onlyCustomer()
	s.Require().NotEmpty(customerID, "the documents must create the customer: %+v", providerStub.calls)
	s.NotEmpty(providerStub.sent(http.MethodPost, "/blindpay/upload"))

	providerStub.setField(customerID, "kyc_status", "approved")
	refreshed := s.External(base+"/blindpay/refresh", token).Post(map[string]any{})
	refreshed.AssertStatus(200)
	state, raw := decodeBody[map[string]any](s, refreshed)
	s.Equal("approved", state["kyc_status"], raw)
}

func (s *RampSuite) companyDraft(email string) map[string]any {
	return map[string]any{"external_id": "kyb-pj-1", "relationship": "end_customer", "entity_type": "business", "name": "Acme Tecnologia", "email": email, "tax_id": "11222333000181", "tax_id_type": "cnpj", "incorporation_country": "BRA", "representative_email": "ana.socia@example.com"}
}

func businessApplication(email string) map[string]any {
	address := map[string]any{"street_line_1": "Avenida Paulista 1000", "city": "Sao Paulo", "subdivision": "SP", "postal_code": "01310-100", "country": "BRA"}
	return map[string]any{"legal_name": "Acme Tecnologia Ltda", "trade_name": "Acme", "company_type": "ltda", "incorporation_date": "2019-03-01", "email": email, "phone": "+551130000000", "website": "https://acme.example.com", "industry": "software_technology", "description": "Synthetic company for automated tests", "registered_address": address, "account_purpose": "receive_from_customers", "source_of_funds": "sales", "annual_revenue_band": "100000_999999", "expected_monthly_volume_band": "10000_49999", "ownership_attested": true, "regulated_activity": false, "conducts_money_services": false,
		"associated_persons": []map[string]any{{"local_ref": anaRef, "from_member": true, "roles": []string{"owner", "control", "signer"}, "ownership_percent": 100, "title": "Socia", "first_name": "Ana", "last_name": "Souza", "email": "ana.socia@example.com", "phone": "+5511987654321", "birth_date": "1986-04-12", "tax_id": "52998224725", "tax_id_type": "cpf", "tax_id_country": "BRA", "nationality": "BRA", "street_line_1": "Rua Augusta 500", "city": "Sao Paulo", "subdivision": "SP", "postal_code": "01305-000", "country": "BRA"}}}
}

// TestKybBusinessJourney walks one company, with its partner, to approval.
func (s *RampSuite) TestKybBusinessJourney() {
	providerStub.reset()
	webhookKey := s.enableBridge("sandbox")
	account := s.account("test")
	_, err := facades.Orm().Query().Exec("INSERT INTO features(account_id,key,enabled,created_at,updated_at) VALUES(?,'kyb',true,now(),now())", account.ID)
	s.Require().NoError(err)
	token := s.token(account, fullGrants)
	const email = "kyb@acme.example.com"
	create := s.External("/api/v1/customers", token).Post(s.companyDraft(email))
	created, raw := decodeBody[struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}](s, create)
	s.Require().NotEmpty(created.Data.ID, raw)
	base := "/api/v1/customers/" + created.Data.ID + "/kyc"

	s.External(base+"/tos", token).Post(map[string]any{"redirect_uri": "https://wallet.example.com/cash/terms-return", "country": "BRA"}).AssertStatus(201)
	s.External(base+"/tos/acceptances", token).Post(map[string]any{"acceptances": []map[string]any{{"provider": "bridge", "signed_agreement_id": "agr_stub_kyb"}}}).AssertStatus(200)

	application := s.External(base+"/application", token).Post(businessApplication(email))
	body, err := application.Content()
	s.Require().NoError(err)
	s.T().Log("application:", body)
	application.AssertStatus(202)
	s.NotContains(body, "52998224725")
	s.work()

	var sent map[string]any
	for _, call := range providerStub.sent(http.MethodPost, "/customers") {
		if call.Path == "/customers" {
			sent = call.Body
		}
	}
	s.Require().NotNil(sent, "the company must reach the provider: %+v", providerStub.calls)
	s.Equal("business", sent["type"])
	s.Len(sent["associated_persons"], 1)
	customerID := ""
	for existing := range providerStub.customers {
		customerID = existing
	}

	documents := s.External(base+"/documents", token).Post(map[string]any{"company_documents": []map[string]any{{"purpose": "formation", "file": tinyPNG}, {"purpose": "proof_of_address", "file": tinyPNG}}, "person_identity_documents": []map[string]any{{"local_ref": anaRef, "document_type": "national_id", "issuing_country": "BRA", "image_front": tinyPNG, "image_back": tinyPNG}}})
	body, err = documents.Content()
	s.Require().NoError(err)
	s.T().Log("documents:", body)
	documents.AssertStatus(202)
	s.work()
	s.NotEmpty(providerStub.sent(http.MethodPut, "/customers/"+customerID+"/associated_persons/"), "the partner's ID must reach the provider: %+v", providerStub.calls)

	profile := s.External(base+"/business-profile", token).Get()
	profile.AssertStatus(200)

	object := providerStub.setStatus(customerID, "active")
	s.deliver("sandbox", webhookKey, customerEvent(customerID, object)).AssertStatus(202)
	s.work()
	state, raw := decodeBody[map[string]any](s, s.External(base+"/bridge", token).Get())
	s.Equal("approved", state["kyc_status"], raw)
}

// kycRoutes is every compliance route a customer scope guards.
var kycRoutes = []struct {
	method, path, scope string
}{
	{"GET", "", "ramp_compliance.read"}, {"GET", "/overview", "ramp_compliance.read"}, {"GET", "/capabilities", "ramp_compliance.read"}, {"GET", "/countries", "ramp_compliance.read"},
	{"GET", "/bridge", "ramp_compliance.read"}, {"GET", "/bridge/requirements", "ramp_compliance.read"},
	{"GET", "/profile", "ramp_compliance.profile.read"}, {"GET", "/business-profile", "ramp_compliance.profile.read"},
	{"POST", "/application", "ramp_compliance.write"}, {"POST", "/documents", "ramp_compliance.write"}, {"POST", "/tos", "ramp_compliance.write"}, {"POST", "/tos/acceptances", "ramp_compliance.write"}, {"POST", "/overview/refresh", "ramp_compliance.write"},
	{"POST", "/bridge/application", "ramp_compliance.write"}, {"POST", "/bridge/documents", "ramp_compliance.write"}, {"POST", "/bridge/tos", "ramp_compliance.write"}, {"POST", "/bridge/hosted-link", "ramp_compliance.write"}, {"POST", "/bridge/refresh", "ramp_compliance.write"}, {"POST", "/bridge/endorsements", "ramp_compliance.write"},
}

func (s *RampSuite) callKyc(method, path string, token support.Token) contractstestinghttp.Response {
	if method == "GET" {
		return s.External(path, token).Get()
	}
	return s.External(path, token).Post(map[string]any{})
}

// TestKycRoutesRefuseAnotherTenantAndMissingScope proves, route by route, that a
// foreign tenant learns nothing, that the scope is the one declared, and that
// none of the refusals reach a provider.
func (s *RampSuite) TestKycRoutesRefuseAnotherTenantAndMissingScope() {
	providerStub.reset()
	s.enableBridge("sandbox")
	owner := s.account("test")
	token := s.token(owner, fullGrants)
	id := s.createCustomer(token, map[string]any{"external_id": "scope-1", "relationship": "end_customer", "entity_type": "individual", "name": "Scope", "email": "scope@example.com"})
	foreign := s.token(s.account("test"), fullGrants)
	blank := s.token(owner, `["ramp_customers.read"]`)
	for _, route := range kycRoutes {
		path := "/api/v1/customers/" + id + "/kyc" + route.path
		s.Equal(404, statusOf(s, s.callKyc(route.method, path, foreign)), "foreign tenant %s %s", route.method, path)
		s.Equal(403, statusOf(s, s.callKyc(route.method, path, blank)), "without scope %s %s", route.method, path)
		s.Empty(providerStub.calls, "a refused %s %s reached the provider", route.method, path)
		narrow := s.token(owner, `["`+route.scope+`"]`)
		s.NotEqual(403, statusOf(s, s.callKyc(route.method, path, narrow)), "declared scope %s %s", route.method, path)
		providerStub.reset()
	}
}

// statusOf reads the status the framework's response keeps to its assertions.
func statusOf(s *RampSuite, resp contractstestinghttp.Response) int {
	s.T().Helper()
	return int(reflect.ValueOf(resp).Elem().FieldByName("response").Elem().FieldByName("StatusCode").Int())
}

// TestProductionAccountNeverUsesSandboxCredentials is the environment boundary:
// a production account with only the sandbox configured reaches no provider.
func (s *RampSuite) TestProductionAccountNeverUsesSandboxCredentials() {
	providerStub.reset()
	s.enableBridge("sandbox")
	token := s.token(s.account("prod"), fullGrants)
	const email = "prod@example.com"
	id := s.createCustomer(token, map[string]any{"external_id": "prod-1", "relationship": "end_customer", "entity_type": "individual", "name": "Prod", "email": email})
	reply := s.External("/api/v1/customers/"+id+"/kyc/application", token).Post(individualApplication(email))
	body, err := reply.Content()
	s.Require().NoError(err)
	s.T().Log("prod application:", body)
	s.work()
	providerStub.mu.Lock()
	defer providerStub.mu.Unlock()
	s.Empty(providerStub.calls, "no provider call may use the sandbox key for a production account")
}

// TestForgedWebhookChangesNothing delivers a status it did not sign.
func (s *RampSuite) TestForgedWebhookChangesNothing() {
	providerStub.reset()
	webhookKey := s.enableBridge("sandbox")
	forger, err := rsa.GenerateKey(rand.Reader, 2048)
	s.Require().NoError(err)
	token := s.token(s.account("test"), fullGrants)
	const email = "forged@example.com"
	id := s.createCustomer(token, map[string]any{"external_id": "forged-1", "relationship": "end_customer", "entity_type": "individual", "name": "Forged", "email": email})
	base := "/api/v1/customers/" + id + "/kyc"
	s.External(base+"/tos", token).Post(map[string]any{"redirect_uri": "https://wallet.example.com/cash/terms-return", "country": "BRA", "tax_id_type": "cpf", "tax_id_country": "BRA"}).AssertStatus(201)
	s.External(base+"/tos/acceptances", token).Post(map[string]any{"acceptances": []map[string]any{{"provider": "bridge", "signed_agreement_id": "agr_forged"}}}).AssertStatus(200)
	s.External(base+"/application", token).Post(individualApplication(email)).AssertStatus(202)
	s.work()
	customerID := ""
	for existing := range providerStub.customers {
		customerID = existing
	}
	s.Require().NotEmpty(customerID)
	object := map[string]any{"id": customerID, "type": "individual", "email": email, "status": "active"}

	s.deliver("sandbox", forger, customerEvent(customerID, object)).AssertStatus(401)
	s.work()
	state, raw := decodeBody[map[string]any](s, s.External(base+"/bridge", token).Get())
	s.NotEqual("approved", state["kyc_status"], raw)

	// The same event twice is one approval, and a stale event cannot undo it.
	providerStub.setStatus(customerID, "active")
	event := customerEvent(customerID, object)
	s.deliver("sandbox", webhookKey, event).AssertStatus(202)
	s.deliver("sandbox", webhookKey, event).AssertStatus(202)
	s.work()
	state, raw = decodeBody[map[string]any](s, s.External(base+"/bridge", token).Get())
	s.Equal("approved", state["kyc_status"], raw)
}

// TestKycStopsWhenTheMasterFlagIsOff is the kill switch on the compliance routes.
func (s *RampSuite) TestKycStopsWhenTheMasterFlagIsOff() {
	providerStub.reset()
	s.enableBridge("sandbox")
	account := s.account("test")
	token := s.token(account, fullGrants)
	id := s.createCustomer(token, map[string]any{"external_id": "flag-1", "relationship": "end_customer", "entity_type": "individual", "name": "Flag", "email": "flag@example.com"})
	_, err := facades.Orm().Query().Exec("UPDATE features SET enabled=false WHERE account_id=? AND key='ramp-integrations-enabled'", account.ID)
	s.Require().NoError(err)
	for _, route := range kycRoutes {
		path := "/api/v1/customers/" + id + "/kyc" + route.path
		s.Equal(403, statusOf(s, s.callKyc(route.method, path, token)), "%s %s", route.method, path)
	}
	s.Empty(providerStub.sent(http.MethodPost, "/customers"))
}

// TestIdentityDocumentsAreNotStoredInClear looks everywhere a submission lands.
func (s *RampSuite) TestIdentityDocumentsAreNotStoredInClear() {
	providerStub.reset()
	s.enableBridge("sandbox")
	token := s.token(s.account("test"), fullGrants)
	const email = "pii@example.com"
	id := s.createCustomer(token, map[string]any{"external_id": "pii-1", "relationship": "end_customer", "entity_type": "individual", "name": "Pii", "email": email})
	base := "/api/v1/customers/" + id + "/kyc"
	s.External(base+"/tos", token).Post(map[string]any{"redirect_uri": "https://wallet.example.com/cash/terms-return", "country": "BRA", "tax_id_type": "cpf", "tax_id_country": "BRA"}).AssertStatus(201)
	s.External(base+"/tos/acceptances", token).Post(map[string]any{"acceptances": []map[string]any{{"provider": "bridge", "signed_agreement_id": "agr_pii"}}}).AssertStatus(200)
	s.External(base+"/application", token).Post(individualApplication(email)).AssertStatus(202)
	s.External(base+"/documents", token).Post(map[string]any{"document_type": "national_id", "issuing_country": "BRA", "image_front": tinyPNG, "selfie": tinyPNG}).AssertStatus(202)

	var hits []struct {
		Table string `gorm:"column:t"`
	}
	s.Require().NoError(facades.Orm().Query().Raw(piiScanQuery()).Scan(&hits))
	s.Empty(hits, "a document or tax id in clear at rest: %+v", hits)
	s.work()
	s.Require().NoError(facades.Orm().Query().Raw(piiScanQuery()).Scan(&hits))
	s.Empty(hits, "a document or tax id in clear at rest after the worker: %+v", hits)
}

// piiScanQuery checks the queue and every table a submission writes to.
func piiScanQuery() string {
	var parts []string
	for _, table := range []string{"ramp_tasks", "account_activity", "macro_ramps.provider_calls", "macro_ramps.provider_kyc_submission_intents", "macro_ramps.customer_provider_kycs", "macro_ramps.customer_provider_integrations"} {
		parts = append(parts, fmt.Sprintf("SELECT '%s' AS t FROM %s x WHERE x::text LIKE '%%iVBORw0KGgo%%' OR x::text LIKE '%%52998224725%%'", table, table))
	}
	return strings.Join(parts, " UNION ALL ")
}

// TestTermsEvidenceIgnoresAForgedForwardedAddress: the address attested to a
// provider is evidence of who accepted, so the caller must not get to choose it.
func (s *RampSuite) TestTermsEvidenceIgnoresAForgedForwardedAddress() {
	providerStub.reset()
	sealer := settings.CryptSealer{}
	sealed, err := sealer.Seal("ozav_sk_test_ozav_stub_key")
	s.Require().NoError(err)
	group := settings.RampGroupName("provider_ozav", "sandbox")
	for k, v := range map[string]string{"enabled": "true", "mode": "sandbox", "api_key": sealed} {
		_, err = facades.Orm().Query().Exec(`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at) VALUES (NULL, ?, ?, ?, NOW(), NOW())`, group, k, v)
		s.Require().NoError(err)
	}
	_, err = facades.Orm().Query().Exec(`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at) VALUES (NULL, ?, 'provider_order', 'ozav', NOW(), NOW())`, settings.RampGroupName("rail_pix_payin", "sandbox"))
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at) VALUES (NULL, ?, 'provider_order', 'ozav', NOW(), NOW())`, settings.RampGroupName("rail_pix_payout", "sandbox"))
	s.Require().NoError(err)
	token := s.token(s.account("test"), fullGrants)
	id := s.createCustomer(token, map[string]any{"external_id": "evidence-1", "relationship": "end_customer", "entity_type": "individual", "name": "Evidence", "email": "evidence@example.com"})
	base := "/api/v1/customers/" + id + "/kyc"
	opened := s.External(base+"/tos", token).Post(map[string]any{"redirect_uri": "https://wallet.example.com/cash/terms-return", "modes": []string{"hosted", "attested"}, "country": "BRA", "tax_id_type": "cpf", "tax_id_country": "BRA"})
	opened.AssertStatus(201)

	const forged = "198.51.100.77"
	tc := goravelTesting.TestCase{}
	resp, err := tc.Http(s.T()).WithHeader("Content-Type", "application/json").WithHeader("Authorization", "Bearer "+token.Bearer).WithHeader("X-Forwarded-For", forged).
		Post(base+"/tos/acceptances", strings.NewReader(`{"acceptances":[{"provider":"ozav","legal_name":"Ana Souza"}]}`))
	s.Require().NoError(err)
	body, err := resp.Content()
	s.Require().NoError(err)
	s.T().Log("acceptance:", body)
	resp.AssertStatus(200)

	var attested map[string]any
	for _, call := range providerStub.sent(http.MethodPost, "/ozav/") {
		if strings.HasSuffix(call.Path, "/accept") {
			attested = call.Body
		}
	}
	s.Require().NotNil(attested, "the acceptance must reach the provider: %+v", providerStub.calls)
	s.NotEqual(forged, attested["ip_address"], "the caller chose the address attested to the provider")
}
