package middleware_test

import (
	"flag"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/contract"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const permissionMatrixPath = "testdata/permission_matrix.txt"

var updatePermissionMatrix = flag.Bool("update-permission-matrix", false, "rewrite "+permissionMatrixPath+" from the current answers")

// TestPermission_Guards_KeepTheirAnswers drives every route behind a user
// permission guard (Can, AccountUpdateMember, the May* guards and the Wallet*
// guards) through the booted router, once per caller role and per state of the
// child the path names (present, absent, malformed), and compares status,
// content type and body with testdata/permission_matrix.txt byte for byte.
// Each row runs on a fresh account and wallet, so an allowed write does not
// change what the next row sees. A difference is either a regression or a
// decided change: rewrite with -update-permission-matrix only for the latter.
func TestPermission_Guards_KeepTheirAnswers(t *testing.T) {
	fixtures.TestDB(t)
	facades.Config().Add("mail.host", "::1")
	facades.Config().Add("mail.port", 59999)

	m := newPermissionMatrix(t)
	var rows []string
	for _, route := range accountMatrixRoutes() {
		for _, state := range route.child.states() {
			for _, role := range matrixAccountRoles {
				rows = append(rows, m.accountRow(route, state, role))
			}
		}
	}
	for _, route := range walletMatrixRoutes() {
		for _, state := range route.child.states() {
			for _, member := range matrixWalletMembers {
				rows = append(rows, m.walletRow(route, state, member))
			}
		}
	}
	got := strings.Join(rows, "\n") + "\n"

	if *updatePermissionMatrix {
		if err := os.MkdirAll(filepath.Dir(permissionMatrixPath), 0o750); err != nil {
			t.Fatalf("create testdata: %v", err)
		}
		if err := os.WriteFile(permissionMatrixPath, []byte(got), 0o600); err != nil {
			t.Fatalf("write matrix: %v", err)
		}
		t.Logf("matrix rewritten: %d rows", len(rows))
		return
	}
	raw, err := os.ReadFile(permissionMatrixPath)
	if err != nil {
		t.Fatalf("read matrix (run with -update-permission-matrix to create it): %v", err)
	}
	want := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(want) != len(rows) {
		t.Errorf("matrix has %d rows, this run has %d", len(want), len(rows))
	}
	for i := 0; i < len(want) && i < len(rows); i++ {
		if want[i] != rows[i] {
			t.Errorf("row %d changed\n--- want\n%s\n+++ got\n%s", i+1, want[i], rows[i])
		}
	}
}

// matrixAccountRoles are the account_users roles the database accepts, and a
// caller with no membership at all.
var matrixAccountRoles = []string{
	models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleAuditor, models.AccountRoleUser, "none",
}

// walletMember is the caller's place on a wallet: the account role, an
// optional wallet_users role, and whether the account shows every wallet.
type walletMember struct {
	accountRole string
	walletRole  string
	viewAll     bool
}

func (w walletMember) String() string {
	walletRole := w.walletRole
	if walletRole == "" {
		walletRole = "-"
	}
	return fmt.Sprintf("account=%s wallet=%s view_all=%t", w.accountRole, walletRole, w.viewAll)
}

// matrixWalletMembers reach every branch of the wallet policies: each account
// role on an account that shows every wallet, the user role holding each
// wallet role, an auditor who owns the wallet, and a user who cannot see it.
var matrixWalletMembers = []walletMember{
	{accountRole: models.AccountRoleOwner, viewAll: true},
	{accountRole: models.AccountRoleAdmin, viewAll: true},
	{accountRole: models.AccountRoleAuditor, viewAll: true},
	{accountRole: models.AccountRoleUser, viewAll: true},
	{accountRole: models.AccountRoleUser},
	{accountRole: models.AccountRoleUser, walletRole: models.WalletRoleViewer},
	{accountRole: models.AccountRoleUser, walletRole: models.WalletRoleSpender},
	{accountRole: models.AccountRoleUser, walletRole: models.WalletRoleApprover},
	{accountRole: models.AccountRoleUser, walletRole: models.WalletRoleAdmin},
	{accountRole: models.AccountRoleUser, walletRole: "owner"},
	{accountRole: models.AccountRoleAuditor, walletRole: "owner"},
}

// matrixChild is the resource a path names after the account or the wallet.
type matrixChild int

const (
	noChild matrixChild = iota
	accountMemberChild
	accessTokenChild
	openInviteChild
	activityChild
	settingsGroupChild
	settingsSectionChild
	walletMemberChild
	whitelistEntryChild
	walletWebhookChild
	withdrawalChild
)

func (c matrixChild) states() []string {
	switch c {
	case noChild:
		return []string{"-"}
	case settingsGroupChild:
		// An account group, a platform-only group, and an unknown name.
		return []string{"account_security", "deposit_scan", "nope"}
	case settingsSectionChild:
		// An account page, a page holding a platform-managed group, a platform page, and an unknown name.
		return []string{"security", "limits", "scanning", "nope"}
	case withdrawalChild:
		return []string{"own", "other", "absent", "malformed"}
	default:
		return []string{"present", "absent", "malformed"}
	}
}

// matrixRoute is one guarded route. path names the child as {child}.
type matrixRoute struct {
	guard  string
	method string
	path   string
	child  matrixChild
	body   string
}

func accountMatrixRoutes() []matrixRoute {
	const base = "/v1/accounts/{accountId}"
	return []matrixRoute{
		{guard: "Can(account.write)", method: "PATCH", path: base, body: `{}`},
		{guard: "Can(account.lifecycle)", method: "POST", path: base + "/archive"},
		{guard: "Can(account.lifecycle)", method: "POST", path: base + "/freeze"},
		{guard: "Can(users.read)", method: "GET", path: base + "/users"},
		{guard: "Can(users.write)", method: "POST", path: base + "/users", body: `{}`},
		{guard: "AccountUpdateMember", method: "PATCH", path: base + "/users/{child}", child: accountMemberChild, body: `{}`},
		{guard: "Can(users.write)", method: "DELETE", path: base + "/users/{child}", child: accountMemberChild},
		{guard: "Can(users.read)", method: "GET", path: base + "/invites"},
		{guard: "Can(users.write)", method: "POST", path: base + "/invites", body: `{}`},
		{guard: "Can(users.write)", method: "POST", path: base + "/invites/{child}/resend", child: openInviteChild},
		{guard: "Can(users.write)", method: "DELETE", path: base + "/invites/{child}", child: openInviteChild},
		{guard: "Can(roles.read)", method: "GET", path: base + "/roles"},
		{guard: "Can(roles.read)", method: "GET", path: base + "/permissions"},
		{guard: "Can(tokens.read)", method: "GET", path: base + "/tokens"},
		{guard: "Can(tokens.write)", method: "POST", path: base + "/tokens", body: `{}`},
		{guard: "Can(tokens.write)", method: "DELETE", path: base + "/tokens/{child}", child: accessTokenChild},
		{guard: "MayViewSettings", method: "GET", path: base + "/settings"},
		{guard: "MayViewSettings", method: "GET", path: base + "/settings/{child}", child: settingsGroupChild},
		{guard: "MayUpdateSettings", method: "PATCH", path: base + "/settings/{child}", child: settingsGroupChild, body: `{}`},
		{guard: "MayUpdateSettings", method: "PUT", path: base + "/settings/{child}", child: settingsGroupChild, body: `{}`},
		{guard: "MayUpdateSettings", method: "POST", path: base + "/settings/sections/{child}/cache", child: settingsSectionChild},
		{guard: "MayUpdateSettings", method: "POST", path: base + "/settings/sections/{child}/reset", child: settingsSectionChild},
		{guard: "MayReadActivity", method: "GET", path: base + "/activity"},
		{guard: "MayReadActivity", method: "GET", path: base + "/activity/{child}", child: activityChild},
		{guard: "MayViewAccountFeatures", method: "GET", path: base + "/features"},
	}
}

func walletMatrixRoutes() []matrixRoute {
	const base = "/v1/wallets/{walletId}"
	return []matrixRoute{
		{guard: "Can(addresses.create)", method: "POST", path: base + "/addresses", body: `{"label":"` + strings.Repeat("a", 256) + `"}`},
		{guard: "WalletAddUser", method: "POST", path: base + "/users", body: `{}`},
		{guard: "WalletRemoveUser", method: "DELETE", path: base + "/users/{child}", child: walletMemberChild},
		{guard: "WalletWhitelist", method: "POST", path: base + "/whitelist", body: `{}`},
		{guard: "WalletWhitelist", method: "DELETE", path: base + "/whitelist/{child}", child: whitelistEntryChild},
		{guard: "WalletManageWebhooks", method: "POST", path: base + "/webhooks", body: `{}`},
		{guard: "WalletManageWebhooks", method: "POST", path: base + "/webhooks/{child}/test", child: walletWebhookChild},
		{guard: "WalletManageWebhooks", method: "DELETE", path: base + "/webhooks/{child}", child: walletWebhookChild},
		{guard: "WalletFreeze", method: "POST", path: base + "/freeze", body: `{"frozen_until":"not-a-timestamp"}`},
		{guard: "WalletArchive", method: "POST", path: base + "/archive"},
		{guard: "WalletCancelWithdrawal", method: "POST", path: base + "/withdrawals/{child}/cancel", child: withdrawalChild},
	}
}

const matrixPassword = "permission-matrix-1"

// permissionMatrix holds the caller, signed in once, and another user the
// child rows point at.
type permissionMatrix struct {
	t       *testing.T
	caller  uuid.UUID
	session string
	other   uuid.UUID
	wallets int
}

func newPermissionMatrix(t *testing.T) *permissionMatrix {
	t.Helper()
	m := &permissionMatrix{t: t}
	m.caller = m.insertUser("matrix-caller@example.com")
	m.other = m.insertUser("matrix-other@example.com")
	m.session = m.login("matrix-caller@example.com")
	return m
}

func (m *permissionMatrix) accountRow(route matrixRoute, state, role string) string {
	m.t.Helper()
	account := fixtures.InsertAccount(m.t, "permission matrix")
	if role != "none" {
		m.member(account.ID, m.caller, role)
	}
	child := m.accountChild(route.child, state, account.ID)
	path := strings.NewReplacer("{accountId}", account.ID.String(), "{child}", child).Replace(route.path)
	return m.row(route, state, role, path, "")
}

func (m *permissionMatrix) walletRow(route matrixRoute, state string, member walletMember) string {
	m.t.Helper()
	account := fixtures.InsertAccount(m.t, "permission matrix")
	m.exec(`UPDATE accounts SET view_all_wallets = ? WHERE id = ?`, member.viewAll, account.ID)
	wallet := fixtures.InsertWalletWithAccount(m.t, models.ChainETH, &account.ID)
	// The fixture address is random and an archived wallet echoes it; number it by row instead.
	m.wallets++
	m.exec(`UPDATE addresses SET address = ? WHERE id = ?`, fmt.Sprintf("0x%040x", m.wallets), wallet.DepositAddress.ID)
	m.member(account.ID, m.caller, member.accountRole)
	if member.walletRole != "" {
		m.walletUser(wallet.ID, m.caller, member.walletRole)
	}
	child := m.walletChild(route.child, state, account.ID, wallet.ID)
	path := strings.NewReplacer("{walletId}", wallet.ID.String(), "{child}", child).Replace(route.path)
	return m.row(route, state, member.String(), path, account.ID.String())
}

// row sends one request and renders it on one line: the guard, the route
// template, the child state, the caller, then the answer.
func (m *permissionMatrix) row(route matrixRoute, state, caller, path, accountHeader string) string {
	m.t.Helper()
	request := httptest.NewRequest(route.method, path, strings.NewReader(route.body))
	if route.body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+m.session)
	if accountHeader != "" {
		request.Header.Set("X-Account-Id", accountHeader)
	}
	response, err := facades.Route().Test(request)
	if err != nil {
		m.t.Fatalf("%s %s: %v", route.method, path, err)
	}
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		m.t.Fatalf("%s %s: read body: %v", route.method, path, err)
	}
	body := contract.NewNormalizer("secret", "token").Normalize(string(raw))
	return fmt.Sprintf("%s | %s %s | %s | %s | %d | %s | %s",
		route.guard, route.method, route.path, state, caller,
		response.StatusCode, response.Header.Get("Content-Type"), strings.TrimSuffix(body, "\n")+newlineMark(body))
}

// newlineMark keeps the trailing newline of the responses.Error writer visible
// on a one-line row.
func newlineMark(body string) string {
	if strings.HasSuffix(body, "\n") {
		return `\n`
	}
	return ""
}

func (m *permissionMatrix) accountChild(child matrixChild, state string, accountID uuid.UUID) string {
	m.t.Helper()
	switch child {
	case noChild:
		return ""
	case settingsGroupChild, settingsSectionChild:
		return state
	}
	switch state {
	case "absent":
		return uuid.NewString()
	case "malformed":
		return "not-a-uuid"
	}
	switch child {
	case accountMemberChild:
		m.member(accountID, m.other, models.AccountRoleUser)
		return m.other.String()
	case accessTokenChild:
		id := uuid.New()
		m.exec(`INSERT INTO access_tokens (id, account_id, created_by, name, token_hash, spending_limit, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`, id, accountID, m.other, "matrix token", "matrix-"+id.String(), "{}")
		return id.String()
	case openInviteChild:
		invite := models.AccountInvite{
			ID: uuid.New(), AccountID: accountID, Email: "matrix-invitee@example.com", Role: models.AccountRoleUser,
			TokenHash: "matrix-" + uuid.NewString(), InvitedBy: &m.other, ExpiresAt: time.Now().Add(24 * time.Hour),
		}
		m.create(&invite)
		return invite.ID.String()
	case activityChild:
		id := uuid.New()
		m.exec(`INSERT INTO account_activity (id, account_id, actor_user_id, action, target_type, target_id, metadata, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, '{}', NOW())`, id, accountID, m.other, "token.revoked", "token", uuid.NewString())
		return id.String()
	default:
		m.t.Fatalf("no account child %d", child)
		return ""
	}
}

func (m *permissionMatrix) walletChild(child matrixChild, state string, accountID, walletID uuid.UUID) string {
	m.t.Helper()
	if child == noChild {
		return ""
	}
	switch state {
	case "absent":
		return uuid.NewString()
	case "malformed":
		return "not-a-uuid"
	}
	switch child {
	case walletMemberChild:
		m.member(accountID, m.other, models.AccountRoleUser)
		m.walletUser(walletID, m.other, models.WalletRoleViewer)
		return m.other.String()
	case whitelistEntryChild:
		entry := models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Label: "matrix", Address: "0x0000000000000000000000000000000000000001"}
		m.create(&entry)
		return entry.ID.String()
	case walletWebhookChild:
		// A just-released local port refuses the test delivery at once.
		webhook := fixtures.InsertScopedWebhookConfig(m.t, testutil.ClosedLocalURL(m.t)+"/hook", "matrix-webhook-secret",
			[]string{"deposit.confirmed"}, &accountID, &walletID)
		return webhook.ID.String()
	case withdrawalChild:
		creator := m.other
		if state == "own" {
			creator = m.caller
		}
		withdrawal := models.Withdrawal{
			ID: uuid.New(), WalletID: walletID, AccountID: &accountID, Status: "pending", Amount: "4", FeeEstimate: "0",
			DestinationAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12", CreatedBy: &creator,
		}
		m.create(&withdrawal)
		return withdrawal.ID.String()
	default:
		m.t.Fatalf("no wallet child %d", child)
		return ""
	}
}

func (m *permissionMatrix) member(accountID, userID uuid.UUID, role string) {
	m.t.Helper()
	m.create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	})
}

func (m *permissionMatrix) walletUser(walletID, userID uuid.UUID, roles string) {
	m.t.Helper()
	m.create(&models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: roles, Status: models.StatusActive})
}

func (m *permissionMatrix) insertUser(email string) uuid.UUID {
	m.t.Helper()
	hash, err := authsvc.NewService(appfacades.Hash()).HashPassword(matrixPassword)
	if err != nil {
		m.t.Fatalf("hash password: %v", err)
	}
	id := uuid.New()
	m.exec(`INSERT INTO users (id, email, password_hash, status, created_at, updated_at) VALUES (?, ?, ?, ?, NOW(), NOW())`,
		id, email, hash, "active")
	return id
}

func (m *permissionMatrix) login(email string) string {
	m.t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, matrixPassword)
	request := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := facades.Route().Test(request)
	if err != nil {
		m.t.Fatalf("login: %v", err)
	}
	raw, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		m.t.Fatalf("login: %d %s", response.StatusCode, raw)
	}
	token, ok := strings.CutPrefix(string(raw), `{"access_token":"`)
	if !ok {
		m.t.Fatalf("login body: %s", raw)
	}
	token, _, _ = strings.Cut(token, `"`)
	return token
}

func (m *permissionMatrix) create(value any) {
	m.t.Helper()
	if err := facades.Orm().Query().Create(value); err != nil {
		m.t.Fatalf("create %T: %v", value, err)
	}
}

func (m *permissionMatrix) exec(query string, args ...any) {
	m.t.Helper()
	if _, err := facades.Orm().Query().Exec(query, args...); err != nil {
		m.t.Fatalf("exec %q: %v", query, err)
	}
}
