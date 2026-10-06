package accounts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const accountRolesPassword = "correct-horse-battery"

type accountRolesSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountRolesSuite(t *testing.T) {
	suite.Run(t, new(accountRolesSuite))
}

func (s *accountRolesSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *accountRolesSuite) TestOwnerReadsEffectiveGrants() {
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

func (s *accountRolesSuite) TestUserIsForbiddenAndWritesAreNotFound() {
	accountID, owner := s.member("owner")
	user := s.join(accountID, "user")

	s.get(user, accountID, 403)
	s.get(owner, accountID, 200)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		s.write(method, owner, accountID, 404)
	}
}

func (s *accountRolesSuite) TestUnknownAccountIsNotFoundBeforeTheRoleCheck() {
	_, token := s.member("owner")
	s.get(token, uuid.New(), 404)
}

func (s *accountRolesSuite) TestOutsiderIsForbidden() {
	accountID, _ := s.member("owner")
	_, outsider := s.member("owner")
	s.get(outsider, accountID, 403)
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
	hash, err := authsvc.NewService().HashPassword(accountRolesPassword)
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
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/login", strings.NewReader(body))
	s.Require().NoError(err)
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

func (s *accountRolesSuite) get(token string, accountID uuid.UUID, status int) roleListBody {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get("/v1/accounts/" + accountID.String() + "/roles")
	s.Require().NoError(err)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	if status == 403 {
		s.Contains(content, `"code":"forbidden"`)
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
	request := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json")
	body := strings.NewReader(`{"permissions":[]}`)
	var (
		resp contractstestinghttp.Response
		err  error
	)
	switch method {
	case http.MethodPost:
		resp, err = request.Post(path, body)
	case http.MethodPut:
		resp, err = request.Put(path, body)
	case http.MethodPatch:
		resp, err = request.Patch(path, body)
	case http.MethodDelete:
		resp, err = request.Delete(path, body)
	default:
		s.FailNow("unsupported method " + method)
	}
	s.Require().NoError(err)
	resp.AssertStatus(status)
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
