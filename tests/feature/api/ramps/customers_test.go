package ramps_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/ramps"
	em "github.com/macrowallets/waas/packages/rampengine/app/models"
	er "github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("RAMP_TEST_DB_HOST") == "" {
		os.Exit(m.Run())
	}
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	os.Setenv("DB_HOST", os.Getenv("RAMP_TEST_DB_HOST"))
	os.Setenv("DB_PORT", os.Getenv("RAMP_TEST_DB_PORT"))
	os.Setenv("DB_DATABASE", "vault_unit_test_ramps")
	os.Setenv("DB_USERNAME", "ramps")
	os.Setenv("DB_PASSWORD", "macro-ramps-local-test")
	os.Setenv("REDIS_HOST", "127.0.0.1")
	os.Setenv("REDIS_PORT", "26379")
	os.Setenv("REDIS_DB", "15")
	os.Setenv("CACHE_STORE", "redis")
	os.Setenv("APP_KEY", "0123456789abcdef0123456789abcdef")
	os.Setenv("JWT_SECRET", "macro-ramps-test-signing-secret-32")
	testutil.BootApp()
	os.Exit(m.Run())
}

type RampSuite struct{ support.HTTPSuite }

func TestRampCustomers(t *testing.T) {
	if os.Getenv("RAMP_TEST_DB_HOST") == "" {
		t.Skip("set RAMP_TEST_DB_HOST/PORT for the isolated PostgreSQL integration test")
	}
	support.RunSuite(t, new(RampSuite))
}
func (s *RampSuite) SetupTest() { fixtures.TestDB(s.T()) }
func (s *RampSuite) account(environment string) *models.Account {
	a := &models.Account{ID: uuid.New(), Name: "Ramp QA", Environment: environment, Status: "active"}
	s.Require().NoError(facades.Orm().Query().Create(a))
	return a
}
func (s *RampSuite) token(a *models.Account, permissions string) support.Token {
	id := uuid.New()
	raw, err := middleware.MintAPIToken(&models.AccessToken{ID: id, AccountID: a.ID}, false)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec("INSERT INTO access_tokens(id,account_id,name,token_hash,permissions,spending_limit,created_at,updated_at) VALUES(?,?,?,?,?::jsonb,'{}',now(),now())", id, a.ID, "ramp QA", "test", permissions)
	s.Require().NoError(err)
	return support.Token{Bearer: raw}
}
func (s *RampSuite) TestCustomerCreationIsolationAndNativeContacts() {
	a, b := s.account("test"), s.account("test")
	r := container.MustMake[*repositories.RampCustomers]()
	draft := models.RampCustomerDraft{ExternalID: "player-1", Relationship: models.RampEndCustomer, EntityType: "individual", Name: "Player", Email: "player@example.com"}
	one, created, err := r.Create(context.Background(), a, draft)
	s.Require().NoError(err)
	s.True(created)
	replay, created, err := r.Create(context.Background(), a, draft)
	s.Require().NoError(err)
	s.False(created)
	s.Equal(one.ID, replay.ID)
	two, _, err := r.Create(context.Background(), b, draft)
	s.Require().NoError(err)
	s.NotEqual(one.ID, two.ID)
	s.NotEqual(one.EngineContactID, two.EngineContactID)
	_, err = r.Find(context.Background(), b.ID, one.ID)
	s.ErrorIs(err, ramps.ErrCustomerNotFound)
	_, err = container.MustMake[*ramps.Service]().Customer(context.Background(), a, two.ID)
	s.ErrorIs(err, ramps.ErrCustomerNotFound)
	draft.Email = "changed@example.com"
	_, _, err = r.Create(context.Background(), a, draft)
	s.ErrorIs(err, ramps.ErrCustomerConflict)
}
func (s *RampSuite) TestExternalRoutesRequireExplicitScopesAndHideAnotherTenant() {
	a, b := s.account("test"), s.account("prod")
	grants := `["ramp_customers.read","ramp_customers.write","ramp_compliance.read","ramp_compliance.profile.read","ramp_compliance.write","ramp_orders.read","ramp_orders.create"]`
	token := s.token(a, grants)
	other := s.token(b, grants)
	blank := s.token(a, `[]`)
	s.External("/api/v1/customers", blank).Get().AssertStatus(403)
	payload := map[string]any{"external_id": "player-api", "relationship": "end_customer", "entity_type": "individual", "name": "Player API", "email": "api@example.com"}
	resp := s.External("/api/v1/customers", token).Post(payload)
	resp.AssertStatus(201)
	body, err := resp.Content()
	s.Require().NoError(err)
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(body), &created))
	s.Require().NotEmpty(created.Data.ID)
	s.NotContains(body, "engine_")
	s.External("/api/v1/customers/"+created.Data.ID, other).Get().AssertStatus(404)
	s.External("/api/v1/customers/"+created.Data.ID+"/kyc/overview", other).Get().AssertStatus(404)
	s.External("/api/v1/customers/"+created.Data.ID+"/kyc/overview", token).Get().AssertStatus(200)
	s.External("/api/v1/customers/"+created.Data.ID+"/pix-keys", token).Get().AssertStatus(200)
	s.External("/api/v1/customers/"+created.Data.ID+"/onramps", token).Get().AssertStatus(200)
}
func TestAccountEnvironmentIsRequired(t *testing.T) {
	_, err := ramps.ModeForAccount(&models.Account{Environment: ""})
	require.Error(t, err)
	m, err := ramps.ModeForAccount(&models.Account{Environment: "test"})
	require.NoError(t, err)
	require.Equal(t, "sandbox", m)
}

func (s *RampSuite) TestIdentityReservationsStayInsideTenant() {
	a, b := s.account("test"), s.account("test")
	repo := container.MustMake[*repositories.RampCustomers]()
	draft := models.RampCustomerDraft{ExternalID: "one", Relationship: models.RampEndCustomer, EntityType: "individual", Name: "Identity QA", Email: "identity@example.com"}
	one, _, err := repo.Create(context.Background(), a, draft)
	s.Require().NoError(err)
	draft.ExternalID = "two"
	two, _, err := repo.Create(context.Background(), a, draft)
	s.Require().NoError(err)
	other, _, err := repo.Create(context.Background(), b, draft)
	s.Require().NoError(err)
	profile := em.KycProfile{FirstName: "Test", LastName: "Person", TaxID: "39053344705", TaxIDType: "cpf", TaxIDCountry: "BRA", Phone: "+5511999999999", Address: em.KycProfileAddress{Country: "BRA"}}
	profiles := er.NewCustomerIndividualProfileRepository()
	s.Require().NoError(profiles.Upsert(one.EngineCustomerID, profile))
	s.Require().Error(profiles.Upsert(two.EngineCustomerID, profile))
	s.Require().NoError(profiles.Upsert(other.EngineCustomerID, profile))
}
func (s *RampSuite) TestWorkerRejectsCustomerFromAnotherEnvironment() {
	a := s.account("test")
	customer, _, err := container.MustMake[*repositories.RampCustomers]().Create(context.Background(), a, models.RampCustomerDraft{ExternalID: "worker-test", Relationship: models.RampEndCustomer, EntityType: "individual", Name: "Worker", Email: "worker@example.com"})
	s.Require().NoError(err)
	tasks := container.MustMake[*ramps.Tasks]()
	s.Require().NoError(tasks.Enqueue(context.Background(), a.ID, "kyc", ramps.TaskPayload{Mode: "production", EngineCustomerID: customer.EngineCustomerID, SubmissionID: "never-dispatched"}, -time.Minute))
	done, err := container.MustMake[*ramps.Service]().Work(context.Background(), 1)
	s.Require().NoError(err)
	s.Equal(0, done)
	var row struct {
		CompletedAt *string
		LastError   string
	}
	s.Require().NoError(facades.Orm().Query().Table("ramp_tasks").First(&row))
	s.Nil(row.CompletedAt)
	s.NotEmpty(row.LastError)
}
func (s *RampSuite) TestCompanyDraftHasIndependentHolderAndRepresentative() {
	a := s.account("test")
	customer, _, err := container.MustMake[*repositories.RampCustomers]().Create(context.Background(), a, models.RampCustomerDraft{ExternalID: "company", Relationship: models.RampAccountHolder, EntityType: "business", Name: "Company QA", Email: "company@example.com", RepresentativeEmail: "representative@example.com", TaxID: "11222333000181", TaxIDType: "cnpj", IncorporationCountry: "BRA"})
	s.Require().NoError(err)
	scope, err := container.MustMake[*ramps.Service]().Customer(context.Background(), a, customer.ID)
	s.Require().NoError(err)
	s.NotEqual(scope.Contact.ID, scope.Holder.ID)
	s.Equal("business", scope.Holder.Type)
	s.Equal("sandbox", scope.Holder.Environment)
	s.NotNil(scope.Holder.TaxIDHash)
	s.Equal("representative@example.com", scope.Contact.Email)
}

func (s *RampSuite) TestAttentionFiltersHolderBeforeItsLimit() {
	a := s.account("test")
	customers := container.MustMake[*repositories.RampCustomers]()
	draft := models.RampCustomerDraft{ExternalID: "attention-other", Relationship: models.RampEndCustomer, EntityType: "individual", Name: "Attention QA", Email: "attention@example.com"}
	other, _, err := customers.Create(context.Background(), a, draft)
	s.Require().NoError(err)
	draft.ExternalID = "attention-target"
	target, _, err := customers.Create(context.Background(), a, draft)
	s.Require().NoError(err)
	for _, customer := range []*models.RampCustomer{other, target} {
		_, err := facades.Orm().Query().Exec(`INSERT INTO macro_ramps.transactions(user_id,customer_id,provider,type,external_id,gateway_order_id,status,amount_fiat,amount_token,target_token,destination_wallet,raw_payload,created_at,updated_at) VALUES(?,?,?,'offramp',?,?,'refund_attention',100,20,'USDC','wallet','{}'::jsonb,now(),now())`, customer.EngineContactID, customer.EngineCustomerID, "bridge", uuid.New(), uuid.NewString())
		s.Require().NoError(err)
	}
	_, err = facades.Orm().Query().Exec("UPDATE macro_ramps.transactions SET created_at=now()-interval '1 hour' WHERE customer_id=?", other.EngineCustomerID)
	s.Require().NoError(err)
	rows, err := er.NewTransactionRepositoryForHolder(a.ID.String(), "sandbox", target.EngineCustomerID).ListByStatus(em.TxStatusRefundAttention, 1)
	s.Require().NoError(err)
	s.Require().Len(rows, 1)
	s.Equal(target.EngineCustomerID, rows[0].CustomerID)
	items, err := container.MustMake[*ramps.Service]().Attention(context.Background(), a, target)
	s.Require().NoError(err)
	s.Require().Len(items, 1)
	s.Equal(target.EngineCustomerID, items[0].CustomerID)
}
