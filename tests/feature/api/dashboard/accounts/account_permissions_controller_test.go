package accounts

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"

	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/tests/feature/support"
)

func (s *accountRolesSuite) TestPermission_Catalog_IsReadableByRolesRead() {
	accountID, owner := s.member("owner")
	admin := s.join(accountID, "admin")
	auditor := s.join(accountID, "auditor")
	user := s.join(accountID, "user")

	want := policies.AccountPermissionCatalog()
	s.Equal(want, s.catalog(owner, accountID, 200))
	s.Equal(want, s.catalog(admin, accountID, 200))
	s.Equal(want, s.catalog(auditor, accountID, 200))
	s.catalog(user, accountID, 403, "forbidden")

	s.Contains(want, policies.PermWithdrawalsCreate)
	s.Contains(want, policies.PermSweepExecute)
	s.Contains(want, policies.PermWalletsCreate)
	s.Contains(want, policies.PermAddressesCreate)
	s.Contains(want, policies.PermRolesRead)
	s.NotContains(want, "roles.write")
}

func (s *accountRolesSuite) TestPermission_Catalog_HasNoWrite() {
	accountID, owner := s.member("owner")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		s.writePermissions(method, owner, accountID, 404)
	}
}

func (s *accountRolesSuite) TestPermission_Catalog_UnknownAccountIsNotFound() {
	_, token := s.member("owner")
	s.catalog(token, uuid.New(), 404)
}

func (s *accountRolesSuite) TestPermission_Catalog_OutsiderIsForbidden() {
	accountID, _ := s.member("owner")
	_, outsider := s.member("owner")
	s.catalog(outsider, accountID, 403, "not a member of this account")
}

func (s *accountRolesSuite) catalog(token string, accountID uuid.UUID, status int, message ...string) []string {
	s.T().Helper()
	resp := s.Get("/v1/accounts/"+accountID.String()+"/permissions", support.Session{AccessToken: token})
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
		return nil
	}
	var parsed struct {
		Permissions []string `json:"permissions"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Require().NotNil(parsed.Permissions)
	return parsed.Permissions
}

func (s *accountRolesSuite) writePermissions(method, token string, accountID uuid.UUID, status int) {
	s.T().Helper()
	path := "/v1/accounts/" + accountID.String() + "/permissions"
	session := support.Session{AccessToken: token}
	body := `{"permissions":["roles.write"]}`
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
