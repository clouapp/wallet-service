package auth

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// sessionIDProbePath answers the session id SessionAuth stored for the request.
const sessionIDProbePath = "/v1/session-id-probe"

var sessionIDProbeOnce sync.Once

// SessionIDTestSuite proves that a login opens a session with an id (the
// "sid" claim and refresh_tokens.session_id), that a refresh keeps it, and
// that SessionAuth admits the token that carries it.
type SessionIDTestSuite struct {
	authSuite
}

func TestSession_ID_Suite(t *testing.T) {
	support.RunSuite(t, new(SessionIDTestSuite))
}

func (s *SessionIDTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	sessionIDProbeOnce.Do(func() {
		facades.Route().Middleware(middleware.SessionAuth()).Get(sessionIDProbePath, func(ctx http.Context) http.Response {
			sid, ok := requestctx.SessionID(ctx)
			body := map[string]any{"present": ok}
			if ok {
				body["sid"] = sid.String()
			}
			return ctx.Response().Json(http.StatusOK, body)
		})
	})
}

func (s *SessionIDTestSuite) storedSessionIDs(userID uuid.UUID) []string {
	var ids []string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT session_id::text FROM refresh_tokens WHERE user_id = ? ORDER BY created_at, id`, userID,
	).Scan(&ids))
	return ids
}

// probeSessionID is the session id SessionAuth put in the request context.
func (s *SessionIDTestSuite) probeSessionID(bearer string) (string, bool) {
	resp := s.Get(sessionIDProbePath, support.Session{AccessToken: bearer})
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var body struct {
		Present bool   `json:"present"`
		SID     string `json:"sid"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &body))
	return body.SID, body.Present
}

func (s *SessionIDTestSuite) TestLogin_And_RefreshKeepOneSessionID() {
	user := s.seedUser(false)

	session := s.signIn(user.Email)
	sid, ok := authsvc.SessionIDOf(session.AccessToken)
	s.Require().True(ok, "the access token carries a sid")
	s.Equal([]string{sid.String()}, s.storedSessionIDs(user.ID))
	s.getMe(session.AccessToken).AssertOk()
	got, present := s.probeSessionID(session.AccessToken)
	s.True(present, "SessionAuth stores the sid in the request context")
	s.Equal(sid.String(), got)

	resp, renewed := s.refresh(session.RefreshToken)
	resp.AssertOk()
	renewedSID, ok := authsvc.SessionIDOf(renewed.AccessToken)
	s.Require().True(ok)
	s.Equal(sid, renewedSID, "a refresh keeps the session id")
	s.Equal([]string{sid.String(), sid.String()}, s.storedSessionIDs(user.ID))
	s.getMe(renewed.AccessToken).AssertOk()

	other := s.signIn(user.Email)
	otherSID, ok := authsvc.SessionIDOf(other.AccessToken)
	s.Require().True(ok)
	s.NotEqual(sid, otherSID, "each login opens its own session")
}

func (s *SessionIDTestSuite) TestRegister_OpensASessionWithAnID() {
	resp := s.postJSON("/v1/auth/register",
		`{"email":"session-id-register@example.com","password":"secret123","full_name":"Reg User","organization_name":"Reg Org"}`)
	var body struct {
		AccessToken string `json:"access_token"`
	}
	s.decode(resp, &body)

	sid, ok := authsvc.SessionIDOf(body.AccessToken)
	s.Require().True(ok, "the registration's access token carries a sid")
	got, present := s.probeSessionID(body.AccessToken)
	s.True(present)
	s.Equal(sid.String(), got)
}

// A token issued before sessions had ids stays valid and carries none.
func (s *SessionIDTestSuite) TestLegacy_TokenWithoutASessionIDIsStillAdmitted() {
	user := s.seedUser(false)
	session := s.signIn(user.Email)

	claims := jwt.MapClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(session.AccessToken, claims)
	s.Require().NoError(err)
	delete(claims, authsvc.SessionIDClaim)
	legacy, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
		SignedString([]byte(facades.Config().GetString("jwt.secret")))
	s.Require().NoError(err)

	s.getMe(legacy).AssertOk()
	_, present := s.probeSessionID(legacy)
	s.False(present, "a token without a sid has no session id in the request context")
}
