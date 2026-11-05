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
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

type RampSuite struct{ support.HTTPSuite }

func TestRampCustomers(t *testing.T) {
	support.RunSuite(t, new(RampSuite))
}
func (s *RampSuite) SetupTest() { fixtures.TestDB(s.T()) }
func (s *RampSuite) account(environment string) *models.Account {
	a := &models.Account{ID: uuid.New(), Name: "Ramp QA", Environment: environment, Status: "active"}
	s.Require().NoError(facades.Orm().Query().Create(a))
	_, err := facades.Orm().Query().Exec("INSERT INTO global_features(key,enabled,created_at,updated_at) VALUES('ramp-integrations-enabled',true,now(),now()) ON CONFLICT(key) DO UPDATE SET enabled=true")
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec("INSERT INTO features(account_id,key,enabled,created_at,updated_at) VALUES(?,'ramp-integrations-enabled',true,now(),now())", a.ID)
	s.Require().NoError(err)
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

func (s *RampSuite) TestMasterFlagBlocksBothSurfacesAndReportsStatus() {
	a := s.account("test")
	token := s.token(a, `["ramp_customers.read"]`)
	_, err := facades.Orm().Query().Exec("UPDATE features SET enabled=false WHERE account_id=? AND key='ramp-integrations-enabled'", a.ID)
	s.Require().NoError(err)
	status := s.External("/api/v1/ramps/status", token).Get()
	status.AssertOk()
	body, err := status.Content()
	s.Require().NoError(err)
	s.JSONEq(`{"enabled":false}`, body)
	s.External("/api/v1/customers", token).Get().AssertStatus(403).AssertSee([]string{"ramp_integrations_disabled"})
}

func (s *RampSuite) TestTimelineRetentionPreservesRecentMetadataAndRejectsInvalidWindows() {
	a := s.account("test")
	customer, _, err := container.MustMake[*repositories.RampCustomers]().Create(context.Background(), a, models.RampCustomerDraft{ExternalID: "timeline-qa", Relationship: models.RampEndCustomer, EntityType: "individual", Name: "Timeline QA", Email: "timeline@example.com"})
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(`INSERT INTO macro_ramps.provider_calls(user_id,customer_id,provider,operation,outcome,created_at,updated_at) VALUES(?,?,'bridge','refresh','success',now()-interval '91 days',now()),(?,?,'bridge','refresh','success',now()-interval '1 day',now())`, customer.EngineContactID, customer.EngineCustomerID, customer.EngineContactID, customer.EngineCustomerID)
	s.Require().NoError(err)
	service := container.MustMake[*ramps.Service]()
	_, err = service.PruneTimeline(context.Background(), 0)
	s.Require().Error(err)
	_, err = service.CheckBlindPay(context.Background(), "invalid")
	s.Require().Error(err)
	removed, err := service.PruneTimeline(context.Background(), 90)
	s.Require().NoError(err)
	s.GreaterOrEqual(removed, int64(1))
	remaining, err := er.NewProviderCallRepository().ListByCustomer(customer.EngineCustomerID, 10)
	s.Require().NoError(err)
	s.Require().Len(remaining, 1)
}

func (s *RampSuite) TestMasterRequiresBothGlobalRolloutAndExplicitAccountOptIn() {
	a := s.account("test")
	token := s.token(a, `["ramp_customers.read"]`)
	_, err := facades.Orm().Query().Exec("DELETE FROM features WHERE account_id=? AND key='ramp-integrations-enabled'", a.ID)
	s.Require().NoError(err)
	response := s.External("/api/v1/ramps/status", token).Get()
	response.AssertOk()
	body, err := response.Content()
	s.Require().NoError(err)
	s.JSONEq(`{"enabled":false}`, body)
	_, err = facades.Orm().Query().Exec("INSERT INTO features(account_id,key,enabled,created_at,updated_at) VALUES(?,'ramp-integrations-enabled',true,now(),now())", a.ID)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec("UPDATE global_features SET enabled=false WHERE key='ramp-integrations-enabled'")
	s.Require().NoError(err)
	s.External("/api/v1/customers", token).Get().AssertStatus(403)
	_, err = facades.Orm().Query().Exec("UPDATE global_features SET enabled=true WHERE key='ramp-integrations-enabled'")
	s.Require().NoError(err)
	s.External("/api/v1/customers", token).Get().AssertOk()
	enabled, err := container.MustMake[*ramps.Service]().IntegrationsEnabled(context.Background(), uuid.Nil)
	s.Require().NoError(err)
	s.False(enabled)
}

func (s *RampSuite) TestFundingEvidenceRequiresAnExplicitTerminalRefusalAndOwnedCustomer() {
	a := s.account("test")
	otherAccount := s.account("prod")
	customer, _, err := container.MustMake[*repositories.RampCustomers]().Create(context.Background(), a, models.RampCustomerDraft{ExternalID: "evidence-player", Relationship: models.RampEndCustomer, EntityType: "individual", Name: "Evidence QA", Email: "evidence@example.com"})
	s.Require().NoError(err)
	order := uuid.NewString()
	_, err = facades.Orm().Query().Exec(`INSERT INTO macro_ramps.transactions(user_id,customer_id,provider,type,external_id,gateway_order_id,status,amount_fiat,amount_token,target_token,destination_wallet,raw_payload,created_at,updated_at) VALUES(?,?,'blindpay','offramp',?,?,'failed',100,20,'USDC','wallet','{}'::jsonb,now(),now())`, customer.EngineContactID, customer.EngineCustomerID, order, uuid.NewString())
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(`INSERT INTO macro_ramps.offramp_funding_authorizations(external_id,transaction_id,provider,action_type,submission_mode,provider_state_ciphertext,action_payload_ciphertext,unsigned_action_fingerprint,expires_at,action_revision,state,created_at,updated_at) SELECT ?,id,'blindpay','solana_transaction','backend_finalize','','',repeat('a',64),now(),1,'unknown',now(),now() FROM macro_ramps.transactions WHERE external_id=?`, uuid.NewString(), order)
	s.Require().NoError(err)
	token := s.token(a, `["ramp_orders.read"]`)
	path := "/api/v1/customers/" + customer.ID.String() + "/offramps/" + order + "/funding-evidence"
	response := s.External(path, token).Get()
	response.AssertOk()
	body, err := response.Content()
	s.Require().NoError(err)
	s.JSONEq(`{"funding_not_submitted":false}`, body)
	_, err = facades.Orm().Query().Exec(`UPDATE macro_ramps.offramp_funding_authorizations SET state='failed',failure_code='provider_refused' WHERE transaction_id=(SELECT id FROM macro_ramps.transactions WHERE external_id=?)`, order)
	s.Require().NoError(err)
	response = s.External(path, token).Get()
	response.AssertOk()
	body, err = response.Content()
	s.Require().NoError(err)
	s.JSONEq(`{"funding_not_submitted":true}`, body)
	s.External(path, s.token(otherAccount, `["ramp_orders.read"]`)).Get().AssertStatus(404)
	_, err = facades.Orm().Query().Exec(`DELETE FROM macro_ramps.offramp_funding_authorizations WHERE transaction_id=(SELECT id FROM macro_ramps.transactions WHERE external_id=?)`, order)
	s.Require().NoError(err)
	response = s.External(path, token).Get()
	response.AssertOk()
	body, err = response.Content()
	s.Require().NoError(err)
	s.JSONEq(`{"funding_not_submitted":false}`, body)
}

func (s *RampSuite) TestServerFundingDoesNotRequireADashboardAuthenticatorCode() {
	a := s.account("test")
	customer, _, err := container.MustMake[*repositories.RampCustomers]().Create(context.Background(), a, models.RampCustomerDraft{ExternalID: "api-funding-validation", Relationship: models.RampEndCustomer, EntityType: "individual", Name: "API Funding QA", Email: "api-funding@example.com"})
	s.Require().NoError(err)
	wallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &a.ID)
	token := s.token(a, `["ramp_orders.create","withdrawals.create"]`)
	path := "/api/v1/customers/" + customer.ID.String() + "/offramps/" + uuid.NewString() + "/wallets/" + wallet.ID.String() + "/fund"
	// Reaching the owned-order lookup proves body/auth validation accepted the server token without TOTP.
	s.External(path, token).Post(map[string]any{"passphrase": "test-only-passphrase", "action_revision": 0}).AssertStatus(404).AssertSee([]string{"order_not_found"})
}

func (s *RampSuite) TestTerminalDepositEvidenceChecksOwnedWalletAndBothNativeLedgers() {
	a := s.account("test")
	other := s.account("test")
	customer, _, err := container.MustMake[*repositories.RampCustomers]().Create(context.Background(), a, models.RampCustomerDraft{ExternalID: "deposit-evidence", Relationship: models.RampEndCustomer, EntityType: "individual", Name: "Deposit Evidence QA", Email: "deposit-evidence@example.com"})
	s.Require().NoError(err)
	wallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &a.ID)
	foreign := fixtures.InsertWalletWithAccount(s.T(), "eth", &other.ID)
	order := uuid.NewString()
	_, err = facades.Orm().Query().Exec(`INSERT INTO macro_ramps.transactions(user_id,customer_id,provider,type,external_id,gateway_order_id,status,amount_fiat,amount_token,target_token,destination_wallet,from_address,deposit_instructions,raw_payload,created_at,updated_at) VALUES(?,?,'ozav','offramp',?,?,'expired',100,20,'USDC','wallet',?,'{"address":"provider-destination"}','{}'::jsonb,now(),now())`, customer.EngineContactID, customer.EngineCustomerID, order, uuid.NewString(), wallet.DepositAddress.Address)
	s.Require().NoError(err)
	token := s.token(a, `["ramp_orders.read"]`)
	base := "/api/v1/customers/" + customer.ID.String() + "/offramps/" + order + "/funding-evidence"
	path := base + "?wallet_id=" + wallet.ID.String()
	assertEvidence := func(expected string) {
		response := s.External(path, token).Get()
		response.AssertOk()
		body, err := response.Content()
		s.Require().NoError(err)
		s.JSONEq(`{"funding_not_submitted":`+expected+`}`, body)
	}
	assertEvidence("true")
	s.External(base+"?wallet_id="+foreign.ID.String(), token).Get().AssertStatus(403)
	s.External(base+"?wallet_id=bad", token).Get().AssertStatus(422)
	_, err = facades.Orm().Query().Exec(`INSERT INTO withdrawals(id,wallet_id,status,amount,destination_address,created_at,updated_at) VALUES(?,?,'broadcasting','20','provider-destination',now(),now())`, order, wallet.ID)
	s.Require().NoError(err)
	assertEvidence("false")
	_, err = facades.Orm().Query().Exec(`DELETE FROM withdrawals WHERE id=?`, order)
	s.Require().NoError(err)
	tx := fixtures.InsertTransaction(s.T(), wallet.ID, wallet.DepositAddressID, "eth", "withdrawal", "pending", "USDC", "20", 0)
	_, err = facades.Orm().Query().Exec(`UPDATE transactions SET idempotency_key=? WHERE id=?`, order, tx.ID)
	s.Require().NoError(err)
	assertEvidence("false")
}

func (s *RampSuite) TestFundingAndEvidenceWaitForTheSameCanonicalOrderLock() {
	a := s.account("test")
	customer, _, err := container.MustMake[*repositories.RampCustomers]().Create(context.Background(), a, models.RampCustomerDraft{ExternalID: "funding-lock", Relationship: models.RampEndCustomer, EntityType: "individual", Name: "Funding Lock QA", Email: "funding-lock@example.com"})
	s.Require().NoError(err)
	order := uuid.NewString()
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- locking.NewAdvisoryLocker().WithLock(context.Background(), "macro-ramp-funding:"+order, func(context.Context) error {
			close(held)
			<-release
			return nil
		})
	}()
	defer func() { close(release); s.NoError(<-done) }()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		s.T().Fatal("funding lock was not acquired")
	}
	service := container.MustMake[*ramps.Service]()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = service.FundingWasNotSubmitted(ctx, customer, strings.ToUpper(order), uuid.Nil)
	s.ErrorIs(ctx.Err(), context.DeadlineExceeded)
	cancel()
	s.Error(err)
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = service.Fund(ctx, a, nil, customer, ramps.FundInput{APIToken: &models.AccessToken{AccountID: a.ID}, OrderID: strings.ToUpper(order)})
	s.ErrorIs(ctx.Err(), context.DeadlineExceeded)
	cancel()
	s.Error(err)
}
