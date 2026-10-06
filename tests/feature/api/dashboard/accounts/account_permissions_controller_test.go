package accounts

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"

	"github.com/macrowallets/waas/app/policies"
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
	s.catalog(user, accountID, 403)

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
	s.catalog(outsider, accountID, 403)
}

func (s *accountRolesSuite) catalog(token string, accountID uuid.UUID, status int) []string {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		Get("/v1/accounts/" + accountID.String() + "/permissions")
	s.Require().NoError(err)
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	if status == 403 {
		s.Contains(content, `"code":"forbidden"`)
	}
	if status != 200 {
		return nil
	}
	var parsed struct {
		Permissions []string `json:"permissions"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.NotNil(parsed.Permissions)
	return parsed.Permissions
}

func (s *accountRolesSuite) writePermissions(method, token string, accountID uuid.UUID, status int) {
	s.T().Helper()
	path := "/v1/accounts/" + accountID.String() + "/permissions"
	request := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json")
	body := strings.NewReader(`{"permissions":["roles.write"]}`)
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
