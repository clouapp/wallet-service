package wallets

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	"github.com/pquerna/otp/totp"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const (
	dashboardFundTOTPPassword = "correct-horse-battery"
	fundWhitelistAddress      = "0x0000000000000000000000000000000000000001"
)

// DashboardFundTOTPSuite checks the dashboard whitelist and webhook-endpoint
// changes: a user who already has TOTP on must send a code, a user who does
// not still proceeds, and a refusal writes nothing.
type DashboardFundTOTPSuite struct {
	support.HTTPSuite
}

func TestDashboard_Fund_TOTPSuite(t *testing.T) {
	support.RunSuite(t, new(DashboardFundTOTPSuite))
}

func (s *DashboardFundTOTPSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *DashboardFundTOTPSuite) TestAdd_Whitelist_RequiresCodeWhenTOTPIsOn() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleOwner, account.ID, true)
	label := "no-code-" + uuid.NewString()[:8]

	resp := s.postWhitelist(caller.token, account.ID, wallet.ID, fundWhitelistAddress, label, "")

	s.assertCodeDenied(resp)
	s.Equal(int64(0), s.whitelistCount(wallet.ID, label))
	s.Equal(int64(0), s.totpCounter(caller.id))
}

func (s *DashboardFundTOTPSuite) TestAdd_Whitelist_WithValidCodeWhenTOTPIsOn() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleOwner, account.ID, true)
	label := "with-code-" + uuid.NewString()[:8]
	code := s.currentCode(caller.secret)

	resp := s.postWhitelist(caller.token, account.ID, wallet.ID, fundWhitelistAddress, label, code)

	resp.AssertStatus(201)
	s.Equal(int64(1), s.whitelistCount(wallet.ID, label))
	s.Positive(s.totpCounter(caller.id))

	again := "replay-" + uuid.NewString()[:8]
	reused := s.postWhitelist(caller.token, account.ID, wallet.ID, fundWhitelistAddress, again, code)
	s.assertCodeDenied(reused)
	s.Equal(int64(0), s.whitelistCount(wallet.ID, again))
}

func (s *DashboardFundTOTPSuite) TestAdd_Whitelist_WithoutTOTPStillProceeds() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleOwner, account.ID, false)
	label := "no-totp-" + uuid.NewString()[:8]

	resp := s.postWhitelist(caller.token, account.ID, wallet.ID, fundWhitelistAddress, label, "")

	resp.AssertStatus(201)
	s.Equal(int64(1), s.whitelistCount(wallet.ID, label))
}

func (s *DashboardFundTOTPSuite) TestDelete_Whitelist_RequiresCodeWhenTOTPIsOn() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleOwner, account.ID, true)
	label := "delete-no-code-" + uuid.NewString()[:8]
	entryID := s.insertEntry(wallet.ID, label)

	resp := s.deleteWhitelist(caller.token, account.ID, wallet.ID, entryID, "")

	s.assertCodeDenied(resp)
	s.Equal(int64(1), s.whitelistCount(wallet.ID, label))
	s.Equal(int64(0), s.totpCounter(caller.id))
}

func (s *DashboardFundTOTPSuite) TestDelete_Whitelist_WithValidCodeWhenTOTPIsOn() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleOwner, account.ID, true)
	label := "delete-code-" + uuid.NewString()[:8]
	entryID := s.insertEntry(wallet.ID, label)

	resp := s.deleteWhitelist(caller.token, account.ID, wallet.ID, entryID, s.currentCode(caller.secret))

	resp.AssertStatus(204)
	s.Equal(int64(0), s.whitelistCount(wallet.ID, label))
	s.Positive(s.totpCounter(caller.id))
}

func (s *DashboardFundTOTPSuite) TestDelete_Whitelist_WithoutTOTPStillProceeds() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleOwner, account.ID, false)
	label := "delete-open-" + uuid.NewString()[:8]
	entryID := s.insertEntry(wallet.ID, label)

	resp := s.deleteWhitelist(caller.token, account.ID, wallet.ID, entryID, "")

	resp.AssertStatus(204)
	s.Equal(int64(0), s.whitelistCount(wallet.ID, label))
}

func (s *DashboardFundTOTPSuite) TestCreate_Webhook_RequiresCodeWhenTOTPIsOn() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleOwner, account.ID, true)
	hookURL := s.hookURL("no-code")

	resp := s.postWebhook(caller.token, account.ID, wallet.ID, hookURL, "")

	s.assertCodeDenied(resp)
	s.Equal(int64(0), s.webhookCount(wallet.ID, hookURL))
	s.Equal(int64(0), s.webhookEvents())
	s.Equal(int64(0), s.totpCounter(caller.id))
}

func (s *DashboardFundTOTPSuite) TestCreate_Webhook_WithValidCodeWhenTOTPIsOn() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleOwner, account.ID, true)
	hookURL := s.hookURL("with-code")

	resp := s.postWebhook(caller.token, account.ID, wallet.ID, hookURL, s.currentCode(caller.secret))

	resp.AssertStatus(201)
	s.Equal(int64(1), s.webhookCount(wallet.ID, hookURL))
	s.Equal(int64(0), s.webhookEvents())
	s.Positive(s.totpCounter(caller.id))
}

func (s *DashboardFundTOTPSuite) TestCreate_Webhook_WithoutTOTPStillProceeds() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleOwner, account.ID, false)
	hookURL := s.hookURL("no-totp")

	resp := s.postWebhook(caller.token, account.ID, wallet.ID, hookURL, "")

	resp.AssertStatus(201)
	s.Equal(int64(1), s.webhookCount(wallet.ID, hookURL))
}

func (s *DashboardFundTOTPSuite) TestDelete_Webhook_RequiresCodeWhenTOTPIsOn() {
	account, wallet := s.wallet()
	owner := s.member(models.AccountRoleOwner, account.ID, false)
	caller := s.member(models.AccountRoleAdmin, account.ID, true)
	hookURL := s.hookURL("delete-no-code")
	created := s.postWebhook(owner.token, account.ID, wallet.ID, hookURL, "")
	created.AssertStatus(201)
	webhookID := s.createdID(created)

	resp := s.deleteWebhook(caller.token, account.ID, wallet.ID, webhookID, "")

	s.assertCodeDenied(resp)
	s.Equal(int64(1), s.webhookCount(wallet.ID, hookURL))
	s.Equal(int64(0), s.webhookEvents())
	s.Equal(int64(0), s.totpCounter(caller.id))
}

func (s *DashboardFundTOTPSuite) TestDelete_Webhook_WithValidCodeWhenTOTPIsOn() {
	account, wallet := s.wallet()
	owner := s.member(models.AccountRoleOwner, account.ID, false)
	caller := s.member(models.AccountRoleAdmin, account.ID, true)
	hookURL := s.hookURL("delete-code")
	created := s.postWebhook(owner.token, account.ID, wallet.ID, hookURL, "")
	created.AssertStatus(201)

	resp := s.deleteWebhook(caller.token, account.ID, wallet.ID, s.createdID(created), s.currentCode(caller.secret))

	resp.AssertStatus(204)
	s.Equal(int64(0), s.webhookCount(wallet.ID, hookURL))
	s.Positive(s.totpCounter(caller.id))
}

func (s *DashboardFundTOTPSuite) TestDelete_Webhook_WithoutTOTPStillProceeds() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleOwner, account.ID, false)
	hookURL := s.hookURL("delete-open")
	created := s.postWebhook(caller.token, account.ID, wallet.ID, hookURL, "")
	created.AssertStatus(201)

	resp := s.deleteWebhook(caller.token, account.ID, wallet.ID, s.createdID(created), "")

	resp.AssertStatus(204)
	s.Equal(int64(0), s.webhookCount(wallet.ID, hookURL))
}

func (s *DashboardFundTOTPSuite) TestMissing_Whitelist_EntryStays404WhenTOTPIsOn() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleUser, account.ID, true)
	s.assign(caller.id, wallet.ID, models.WalletRoleViewer)

	resp := s.deleteWhitelist(caller.token, account.ID, wallet.ID, uuid.New(), "")

	resp.AssertNotFound()
	s.Contains(s.body(resp), "whitelist entry not found")
	s.Equal(int64(0), s.totpCounter(caller.id))
}

func (s *DashboardFundTOTPSuite) TestMissing_Webhook_Stays404WhenTOTPIsOn() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleUser, account.ID, true)
	s.assign(caller.id, wallet.ID, models.WalletRoleViewer)

	resp := s.deleteWebhook(caller.token, account.ID, wallet.ID, uuid.New(), "")

	resp.AssertNotFound()
	s.Contains(s.body(resp), "webhook not found")
	s.Equal(int64(0), s.totpCounter(caller.id))
}

func (s *DashboardFundTOTPSuite) TestAccount_User_WhoIsNotAWalletMemberStays404() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleUser, account.ID, true)
	label := "hidden-" + uuid.NewString()[:8]

	resp := s.postWhitelist(caller.token, account.ID, wallet.ID, fundWhitelistAddress, label, s.currentCode(caller.secret))

	resp.AssertNotFound()
	s.Contains(s.body(resp), "wallet not found")
	s.Equal(int64(0), s.whitelistCount(wallet.ID, label))
	s.Equal(int64(0), s.totpCounter(caller.id))
}

func (s *DashboardFundTOTPSuite) TestViewer_With_TOTPIsStillForbidden() {
	account, wallet := s.wallet()
	caller := s.member(models.AccountRoleUser, account.ID, true)
	s.assign(caller.id, wallet.ID, models.WalletRoleViewer)
	label := "viewer-" + uuid.NewString()[:8]

	resp := s.postWhitelist(caller.token, account.ID, wallet.ID, fundWhitelistAddress, label, s.currentCode(caller.secret))

	resp.AssertStatus(403)
	s.Equal(int64(0), s.whitelistCount(wallet.ID, label))
	s.Equal(int64(0), s.totpCounter(caller.id))
}

type fundCaller struct {
	id       uuid.UUID
	token    string
	secret   string
	recovery string
}

func (s *DashboardFundTOTPSuite) wallet() (models.Account, models.Wallet) {
	s.T().Helper()
	account := fixtures.InsertAccount(s.T(), "dashboard totp")
	s.Require().NoError(facades.Orm().Query().Create(&models.Chain{
		ID: models.ChainETH, Name: models.ChainETH, AdapterType: models.AdapterTypeEVM,
		NativeSymbol: "ETH", NativeDecimals: 18, RpcURL: "encrypted-rpc",
		RequiredConfirmations: 1, Status: "active",
	}))
	return account, fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
}

func (s *DashboardFundTOTPSuite) member(role string, accountID uuid.UUID, withTOTP bool) fundCaller {
	s.T().Helper()
	caller := s.insertUser(withTOTP)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: caller.id, Role: role, Status: models.MembershipStatusActive,
	}))
	caller.token = s.session(caller)
	return caller
}

func (s *DashboardFundTOTPSuite) assign(userID, walletID uuid.UUID, roles string) {
	s.T().Helper()
	s.Require().NoError(facades.Orm().Query().Create(&models.WalletUser{
		ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: roles, Status: "active",
	}))
}

func (s *DashboardFundTOTPSuite) insertUser(withTOTP bool) fundCaller {
	s.T().Helper()
	svc := authsvc.NewService()
	userID := uuid.New()
	email := "fund-" + userID.String()[:8] + "@example.com"
	hash, err := svc.HashPassword(dashboardFundTOTPPassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'active', NOW(), NOW())`,
		userID, email, hash,
	)
	s.Require().NoError(err)
	caller := fundCaller{id: userID}
	if !withTOTP {
		return caller
	}
	secret, _, err := svc.GenerateTOTP(email)
	s.Require().NoError(err)
	encrypted, err := facades.Crypt().EncryptString(secret)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(`UPDATE users SET totp_enabled = TRUE WHERE id = ?`, userID)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_credentials (
			id, subject_type, subject_id, secret, confirmed_at, last_used_counter, created_at, updated_at
		) VALUES (?, 'users', ?, ?, NOW(), 0, NOW(), NOW())`,
		uuid.New(), userID, "enc:v1:"+encrypted,
	)
	s.Require().NoError(err)
	codes, hashes, err := svc.GenerateRecoveryCodes()
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(`
		INSERT INTO mfa_backup_codes (
			id, subject_type, subject_id, code_hash, created_at, updated_at
		) VALUES (?, 'users', ?, ?, NOW(), NOW())`,
		uuid.New(), userID, hashes[0],
	)
	s.Require().NoError(err)
	caller.secret = secret
	caller.recovery = codes[0]
	return caller
}

func (s *DashboardFundTOTPSuite) session(caller fundCaller) string {
	s.T().Helper()
	var email string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT email FROM users WHERE id = ?`, caller.id).Scan(&email))
	login := s.postJSON("/v1/auth/login", fmt.Sprintf(
		`{"email":%q,"password":%q}`, email, dashboardFundTOTPPassword,
	))
	var body loginBody
	s.decode(login, &body)
	if caller.secret == "" {
		login.AssertStatus(200)
		s.Require().NotEmpty(body.AccessToken)
		return body.AccessToken
	}
	login.AssertStatus(200)
	s.Require().NotEmpty(body.ChallengeToken)
	s.Empty(body.AccessToken)
	verified := s.postJSON("/v1/auth/2fa/verify", fmt.Sprintf(
		`{"challenge_token":%q,"recovery_code":%q}`, body.ChallengeToken, caller.recovery,
	))
	verified.AssertOk()
	var session loginBody
	s.decode(verified, &session)
	s.Require().NotEmpty(session.AccessToken)
	return session.AccessToken
}

func (s *DashboardFundTOTPSuite) currentCode(secret string) string {
	s.T().Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	s.Require().NoError(err)
	return code
}

func (s *DashboardFundTOTPSuite) insertEntry(walletID uuid.UUID, label string) uuid.UUID {
	s.T().Helper()
	entryID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.WhitelistEntry{
		ID: entryID, WalletID: walletID, Address: fundWhitelistAddress, Label: label,
	}))
	return entryID
}

func (s *DashboardFundTOTPSuite) hookURL(label string) string {
	return "https://example.com/hooks/" + label + "-" + uuid.NewString()[:8]
}

func (s *DashboardFundTOTPSuite) postWhitelist(token string, accountID, walletID uuid.UUID, address, label, code string) contractstesting.Response {
	s.T().Helper()
	body := fmt.Sprintf(`{"address":%q,"label":%q}`, address, label)
	if code != "" {
		body = fmt.Sprintf(`{"address":%q,"label":%q,"totp_code":%q}`, address, label, code)
	}
	return s.send(token, accountID, "POST", "/v1/wallets/"+walletID.String()+"/whitelist", body)
}

func (s *DashboardFundTOTPSuite) deleteWhitelist(token string, accountID, walletID, entryID uuid.UUID, code string) contractstesting.Response {
	s.T().Helper()
	body := ""
	if code != "" {
		body = fmt.Sprintf(`{"totp_code":%q}`, code)
	}
	return s.send(token, accountID, "DELETE", "/v1/wallets/"+walletID.String()+"/whitelist/"+entryID.String(), body)
}

func (s *DashboardFundTOTPSuite) postWebhook(token string, accountID, walletID uuid.UUID, hookURL, code string) contractstesting.Response {
	s.T().Helper()
	body := fmt.Sprintf(`{"url":%q,"events":"deposit.confirmed"}`, hookURL)
	if code != "" {
		body = fmt.Sprintf(`{"url":%q,"events":"deposit.confirmed","totp_code":%q}`, hookURL, code)
	}
	return s.send(token, accountID, "POST", "/v1/wallets/"+walletID.String()+"/webhooks", body)
}

func (s *DashboardFundTOTPSuite) deleteWebhook(token string, accountID, walletID, webhookID uuid.UUID, code string) contractstesting.Response {
	s.T().Helper()
	body := ""
	if code != "" {
		body = fmt.Sprintf(`{"totp_code":%q}`, code)
	}
	return s.send(token, accountID, "DELETE", "/v1/wallets/"+walletID.String()+"/webhooks/"+webhookID.String(), body)
}

func (s *DashboardFundTOTPSuite) send(token string, accountID uuid.UUID, method, path, body string) contractstesting.Response {
	s.T().Helper()
	session := support.Session{AccessToken: token, AccountID: accountID.String()}
	var payload any
	if body != "" {
		payload = body
	}
	switch method {
	case "POST":
		return s.Post(path, session, payload)
	case "DELETE":
		return s.Delete(path, session, payload)
	default:
		s.FailNow("unsupported method")
		return nil
	}
}

func (s *DashboardFundTOTPSuite) postJSON(path, body string) contractstesting.Response {
	s.T().Helper()
	resp := s.Post(path, support.Session{}, body)
	return resp
}

func (s *DashboardFundTOTPSuite) decode(resp contractstesting.Response, into any) {
	s.T().Helper()
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), into))
}

func (s *DashboardFundTOTPSuite) createdID(resp contractstesting.Response) uuid.UUID {
	s.T().Helper()
	var parsed struct {
		ID string `json:"id"`
	}
	s.decode(resp, &parsed)
	id, err := uuid.Parse(parsed.ID)
	s.Require().NoError(err)
	return id
}

func (s *DashboardFundTOTPSuite) assertCodeDenied(resp contractstesting.Response) {
	s.T().Helper()
	s.AssertError(resp, 401, "unauthorized", "invalid 2FA code")
}

func (s *DashboardFundTOTPSuite) whitelistCount(walletID uuid.UUID, label string) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM whitelist_entries WHERE wallet_id = ? AND label = ?`,
		walletID, label,
	).Scan(&total))
	return total
}

func (s *DashboardFundTOTPSuite) webhookCount(walletID uuid.UUID, hookURL string) int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM webhook_configs WHERE wallet_id = ? AND url = ?`,
		walletID, hookURL,
	).Scan(&total))
	return total
}

func (s *DashboardFundTOTPSuite) webhookEvents() int64 {
	s.T().Helper()
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT count(*) FROM webhook_events`).Scan(&total))
	return total
}

func (s *DashboardFundTOTPSuite) totpCounter(userID uuid.UUID) int64 {
	s.T().Helper()
	var counter int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT last_used_counter FROM mfa_credentials WHERE subject_type = 'users' AND subject_id = ?`,
		userID,
	).Scan(&counter))
	return counter
}

func (s *DashboardFundTOTPSuite) body(resp contractstesting.Response) string {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}
