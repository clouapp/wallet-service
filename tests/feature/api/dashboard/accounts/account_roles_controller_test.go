package accounts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const accountRolesPassword = "correct-horse-battery"

type accountRolesSuite struct {
	support.HTTPSuite
}

func TestAccount_Roles_Suite(t *testing.T) {
	support.RunSuite(t, new(accountRolesSuite))
}

func (s *accountRolesSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *accountRolesSuite) TestOwner_Reads_EffectiveGrants() {
	accountID, token := s.member("owner")

	body := s.get(token, accountID, 200)
	s.Equal([]string{"owner", "admin", "auditor", "user"}, rolesOf(body))
	s.Contains(permissionsOf(body, "owner"), "withdrawals.create")
	s.Contains(permissionsOf(body, "owner"), "roles.read")
	s.NotContains(permissionsOf(body, "owner"), "roles.write")
	s.Equal([]string{"addresses.create"}, permissionsOf(body, "user"))
	s.NotContains(permissionsOf(body, "auditor"), "withdrawals.create")
	s.Contains(permissionsOf(body, "auditor"), "roles.read")
}

func (s *accountRolesSuite) TestUser_Is_ForbiddenAndWritesAreNotFound() {
	accountID, owner := s.member("owner")
	user := s.join(accountID, "user")

	s.get(user, accountID, 403, "forbidden")
	s.get(owner, accountID, 200)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		s.write(method, owner, accountID, 404)
	}
}

func (s *accountRolesSuite) TestUnknown_Account_IsNotFoundBeforeTheRoleCheck() {
	_, token := s.member("owner")
	s.get(token, uuid.New(), 404)
}

func (s *accountRolesSuite) TestOutsider_Is_Forbidden() {
	accountID, _ := s.member("owner")
	_, outsider := s.member("owner")
	s.get(outsider, accountID, 403, "not a member of this account")
}

func (s *accountRolesSuite) member(role string) (uuid.UUID, string) {
	s.T().Helper()
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID:          accountID,
		Name:        "Roles " + accountID.String()[:8],
		Status:      "active",
		Environment: "prod",
	}))
	return accountID, s.join(accountID, role)
}

func (s *accountRolesSuite) join(accountID uuid.UUID, role string) string {
	s.T().Helper()
	hash, err := authsvc.NewService(appfacades.Hash()).HashPassword(accountRolesPassword)
	s.Require().NoError(err)
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role,
	}))
	return s.login(email)
}

func (s *accountRolesSuite) login(email string) string {
	s.T().Helper()
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, accountRolesPassword)
	resp := s.Post("/v1/auth/login", support.Session{}, body)
	resp.AssertStatus(200)
	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Require().NotEmpty(parsed.AccessToken)
	return parsed.AccessToken
}

func (s *accountRolesSuite) get(token string, accountID uuid.UUID, status int, message ...string) roleListBody {
	s.T().Helper()
	resp := s.Get("/v1/accounts/"+accountID.String()+"/roles", support.Session{AccessToken: token})
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	if status == 403 {
		s.Require().Len(message, 1)
		s.AssertError(resp, 403, "forbidden", message[0])
	}
	if status == 404 {
		s.AssertError(resp, 404, "not_found", "account not found")
	}
	if status != 200 {
		return roleListBody{}
	}
	assertRoleListHasNoGrantsField(s.T(), content)
	var parsed roleListBody
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	return parsed
}

func assertRoleListHasNoGrantsField(t *testing.T, content string) {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &body); err != nil {
		t.Fatalf("success body: %v", err)
	}
	if _, ok := body["grants"]; ok {
		t.Fatal("success body includes grants")
	}
	if len(body) != 1 {
		t.Fatalf("success body keys = %v", keysOf(body))
	}
	rawRoles, ok := body["roles"]
	if !ok {
		t.Fatal("success body has no roles")
	}
	var roles []map[string]json.RawMessage
	if err := json.Unmarshal(rawRoles, &roles); err != nil {
		t.Fatalf("roles: %v", err)
	}
	for _, role := range roles {
		if _, ok := role["grants"]; ok {
			t.Fatal("a role object includes grants")
		}
		if len(role) != 2 {
			t.Fatalf("role keys = %v", keysOf(role))
		}
		if _, ok := role["role"]; !ok {
			t.Fatal("a role object has no role")
		}
		if _, ok := role["permissions"]; !ok {
			t.Fatal("a role object has no permissions")
		}
	}
}

func keysOf(body map[string]json.RawMessage) []string {
	names := make([]string, 0, len(body))
	for key := range body {
		names = append(names, key)
	}
	return names
}

func (s *accountRolesSuite) write(method, token string, accountID uuid.UUID, status int) {
	s.T().Helper()
	path := "/v1/accounts/" + accountID.String() + "/roles"
	session := support.Session{AccessToken: token}
	body := `{"permissions":[]}`
	var resp contractstestinghttp.Response
	switch method {
	case http.MethodPost:
		resp = s.Post(path, session, body)
	case http.MethodPut:
		resp = s.Put(path, session, body)
	case http.MethodPatch:
		resp = s.Patch(path, session, body)
	case http.MethodDelete:
		resp = s.Delete(path, session, body)
	default:
		s.FailNow("unsupported method " + method)
		return
	}
	resp.AssertStatus(status)
	if status == http.StatusNotFound {
		content, err := resp.Content()
		s.Require().NoError(err)
		s.Contains(content, "404 page not found")
	}
}

type roleListBody struct {
	Roles []struct {
		Role        string   `json:"role"`
		Permissions []string `json:"permissions"`
	} `json:"roles"`
}

func rolesOf(body roleListBody) []string {
	names := make([]string, 0, len(body.Roles))
	for _, role := range body.Roles {
		names = append(names, role.Role)
	}
	return names
}

func permissionsOf(body roleListBody, role string) []string {
	for _, grant := range body.Roles {
		if grant.Role == role {
			return grant.Permissions
		}
	}
	return nil
}
