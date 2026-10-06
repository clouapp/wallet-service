package accounts

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// PlatformAccountListTestSuite is GET /v1/platform/accounts from S3.4.1.
// accounts.view is the named permission; a platform_admins row is the gate.
type PlatformAccountListTestSuite struct {
	authSuite
}

func TestPlatform_Account_ListSuite(t *testing.T) {
	support.RunSuite(t, new(PlatformAccountListTestSuite))
}

func (s *PlatformAccountListTestSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

func (s *PlatformAccountListTestSuite) TestA_Platform_AdminListsAccountsOrderedByCreatedAtDescending() {
	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)

	older := s.seedListedAccount("Older Olive", models.StatusActive, "2024-01-01 00:00:00", uuid.Nil)
	middle := s.seedListedAccount("Middle Mia", models.AccountStatusFrozen, "2024-06-01 00:00:00", older)
	newer := s.seedListedAccount("Newer Ned", models.AccountStatusArchived, "2025-01-01 00:00:00", uuid.Nil)

	session := s.signIn(admin.Email)
	page := s.listAccounts(session.AccessToken, "")
	page.AssertOk()
	body := s.listedPlatformAccounts(page)
	s.Equal(int64(3), body.Total)
	s.Equal(20, body.Limit)
	s.Equal(0, body.Offset)
	s.Require().Len(body.Data, 3)
	s.Equal([]string{newer.String(), middle.String(), older.String()}, platformListedAccountIDs(body.Data))
	s.Equal("Newer Ned", body.Data[0].Name)
	s.Equal(models.AccountStatusArchived, body.Data[0].Status)
	s.Equal("Middle Mia", body.Data[1].Name)
	s.Equal(models.AccountStatusFrozen, body.Data[1].Status)
	s.Equal("Older Olive", body.Data[2].Name)
	s.Equal(models.StatusActive, body.Data[2].Status)

	raw, err := page.Content()
	s.Require().NoError(err)
	s.assertAccountListShape(raw)

	newest := s.listAccounts(session.AccessToken, "?limit=1&offset=0")
	newest.AssertOk()
	first := s.listedPlatformAccounts(newest)
	s.Equal(1, first.Limit)
	s.Equal(0, first.Offset)
	s.Equal(int64(3), first.Total)
	s.Require().Len(first.Data, 1)
	s.Equal(newer.String(), first.Data[0].ID)

	skipped := s.listAccounts(session.AccessToken, "?limit=1&offset=1")
	skipped.AssertOk()
	second := s.listedPlatformAccounts(skipped)
	s.Equal(1, second.Offset)
	s.Require().Len(second.Data, 1)
	s.Equal(middle.String(), second.Data[0].ID)

	past := s.listAccounts(session.AccessToken, "?limit=20&offset=3")
	past.AssertOk()
	empty := s.listedPlatformAccounts(past)
	s.Equal(int64(3), empty.Total)
	s.Empty(empty.Data)
	pastRaw, err := past.Content()
	s.Require().NoError(err)
	s.Contains(pastRaw, `"data":[]`)
}

func (s *PlatformAccountListTestSuite) TestA_Member_CannotListPlatformAccounts() {
	member := s.seedUser(false)
	session := s.signIn(member.Email)
	s.seedListedAccount("Hidden", models.StatusActive, "2024-01-01 00:00:00", uuid.Nil)

	resp := s.listAccounts(session.AccessToken, "")
	resp.AssertForbidden()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.decode(resp, &body)
	s.Equal(responses.CodeForbidden, body.Error.Code)
	s.Equal("you do not have permission to view accounts", body.Error.Message)

	anonymous := s.Get("/v1/platform/accounts", support.Session{})
	anonymous.AssertUnauthorized()
}

func (s *PlatformAccountListTestSuite) grantPlatformAdmin(userID uuid.UUID) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO platform_admins (user_id, created_at, updated_at) VALUES (?, NOW(), NOW())`,
		userID,
	)
	s.Require().NoError(err)
}

func (s *PlatformAccountListTestSuite) seedListedAccount(name, status, createdAt string, linked uuid.UUID) uuid.UUID {
	s.T().Helper()
	id := uuid.New()
	var linkedID any
	if linked != uuid.Nil {
		linkedID = linked
	}
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO accounts (
			id, name, status, view_all_wallets, environment, linked_account_id,
			created_at, updated_at
		) VALUES (?, ?, ?, TRUE, 'prod', ?, ?, ?)`,
		id, name, status, linkedID, createdAt, createdAt,
	)
	s.Require().NoError(err)
	return id
}

func (s *PlatformAccountListTestSuite) listAccounts(bearer, query string) contractstesting.Response {
	s.T().Helper()
	resp := s.Get("/v1/platform/accounts"+query, support.Session{AccessToken: bearer})
	return resp
}

type platformListedAccounts struct {
	Data   []platformListedAccount `json:"data"`
	Total  int64                   `json:"total"`
	Limit  int                     `json:"limit"`
	Offset int                     `json:"offset"`
}

type platformListedAccount struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (s *PlatformAccountListTestSuite) listedPlatformAccounts(resp contractstesting.Response) platformListedAccounts {
	s.T().Helper()
	var body platformListedAccounts
	s.decode(resp, &body)
	return body
}

func platformListedAccountIDs(rows []platformListedAccount) []string {
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	return ids
}

func (s *PlatformAccountListTestSuite) assertAccountListShape(raw string) {
	s.T().Helper()
	s.NotContains(raw, "sweep_limits")
	s.NotContains(raw, "view_all_wallets")
	s.NotContains(raw, "linked_account_id")
	s.NotContains(raw, "environment")
	var payload any
	s.Require().NoError(json.Unmarshal([]byte(raw), &payload))
	s.assertAccountListKeys(payload, "data", "limit", "offset", "total")
	object, ok := payload.(map[string]any)
	s.Require().True(ok)
	rows, ok := object["data"].([]any)
	s.Require().True(ok)
	for _, row := range rows {
		s.assertAccountListKeys(row, "id", "name", "status")
	}
}

func (s *PlatformAccountListTestSuite) assertAccountListKeys(node any, keys ...string) {
	s.T().Helper()
	object, ok := node.(map[string]any)
	s.Require().True(ok)
	s.Require().Len(object, len(keys))
	for _, key := range keys {
		_, present := object[key]
		s.True(present, key)
	}
}
