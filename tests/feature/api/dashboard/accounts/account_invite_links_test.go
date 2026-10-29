package accounts

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/tests/feature/support"
)

// The invite answers that mint a token carry its link, so an operator can hand
// it over outside email: POST .../invites, .../invites/{id}/resend and
// .../invites/{id}/link. The last one sends no mail.

// issuedInviteKeys is the list view of an invite, in its order, then the link.
var issuedInviteKeys = []string{
	"created_at", "id", "account_id", "email", "role", "invited_by",
	"expires_at", "accepted_at", "revoked_at", "invite_link",
}

func (s *AccountMembersTestSuite) postInviteLink(token string, accountID, inviteID uuid.UUID) contractstesting.Response {
	resp := s.Post("/v1/accounts/"+accountID.String()+"/invites/"+inviteID.String()+"/link", support.Session{AccessToken: token}, "")
	return resp
}

// issuedInviteToken checks an answer is the list view plus invite_link, in
// that key order, and returns the raw token of the link.
func (s *AccountMembersTestSuite) issuedInviteToken(resp contractstesting.Response, status int) string {
	s.T().Helper()
	resp.AssertStatus(status)
	content, err := resp.Content()
	s.Require().NoError(err)
	decoder := json.NewDecoder(strings.NewReader(content))
	_, err = decoder.Token()
	s.Require().NoError(err)
	var keys []string
	for decoder.More() {
		key, err := decoder.Token()
		s.Require().NoError(err)
		keys = append(keys, key.(string))
		var skip json.RawMessage
		s.Require().NoError(decoder.Decode(&skip))
	}
	s.Equal(issuedInviteKeys, keys)

	var body struct {
		InviteLink string `json:"invite_link"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &body))
	prefix := membersFrontendURL + "/accept-invite?token="
	s.Require().True(strings.HasPrefix(body.InviteLink, prefix), "invite link must use the front-end URL")
	raw := strings.TrimPrefix(body.InviteLink, prefix)
	s.Require().NotEmpty(raw)
	s.NotContains(raw, "&")
	return raw
}

func (s *AccountMembersTestSuite) assertPreview(rawToken string, status int) {
	s.T().Helper()
	resp := s.Get("/v1/auth/invites/"+rawToken, support.Session{})
	if status == 200 {
		resp.AssertOk()
		return
	}
	s.AssertError(resp, 404, "not_found", "invite is invalid or expired")
}

// countMailDials points the SMTP mailer at a local listener that counts the
// connections it accepts and closes them (each send then fails, which the
// invite routes log and answer as before). The mail config is restored when
// the test ends.
func (s *AccountMembersTestSuite) countMailDials() *atomic.Int64 {
	s.T().Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	s.Require().NoError(err)
	var dials atomic.Int64
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			_ = conn.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	config := facades.Config()
	keys := []string{"mail.host", "mail.port", "mail.mailers.smtp.host", "mail.mailers.smtp.port"}
	saved := make(map[string]any, len(keys))
	for _, key := range keys {
		saved[key] = config.Get(key)
	}
	s.T().Cleanup(func() {
		_ = listener.Close()
		for _, key := range keys {
			config.Add(key, saved[key])
		}
	})
	config.Add("mail.host", "127.0.0.1")
	config.Add("mail.port", port)
	config.Add("mail.mailers.smtp.host", "127.0.0.1")
	config.Add("mail.mailers.smtp.port", port)
	return &dials
}

func (s *AccountMembersTestSuite) TestCreate_Invite_AnswersTheLinkOfTheStoredToken() {
	accountID := s.createAccount()
	owner := s.loginUser(models.AccountRoleOwner, models.MembershipStatusActive, accountID)

	resp := s.postInvite(owner.token, accountID, `{"email":"linked-invite@example.com","role":"user"}`)
	raw := s.issuedInviteToken(resp, 202)

	var invite models.AccountInvite
	s.Require().NoError(facades.Orm().Query().Where("account_id = ? AND email = ?", accountID, "linked-invite@example.com").First(&invite))
	s.Equal(accountsvc.HashInviteToken(raw), invite.TokenHash)
	content, err := resp.Content()
	s.Require().NoError(err)
	s.NotContains(content, invite.TokenHash)
	s.assertPreview(raw, 200)
}

func (s *AccountMembersTestSuite) TestResend_Invite_AnswersTheLinkItMailed() {
	accountID := s.createAccount()
	owner := s.loginUser(models.AccountRoleOwner, models.MembershipStatusActive, accountID)
	const oldToken = "resend-link-old-secret"
	inviteID := s.insertOpenInvite(accountID, owner.id, "resend-link@example.com", models.AccountRoleAuditor, accountsvc.HashInviteToken(oldToken))
	dials := s.countMailDials()

	raw := s.issuedInviteToken(s.postResend(owner.token, accountID, inviteID), 202)

	s.NotEqual(oldToken, raw)
	s.Equal(accountsvc.HashInviteToken(raw), s.inviteTokenHash(inviteID))
	s.Eventually(func() bool { return dials.Load() >= 1 }, 5*time.Second, 20*time.Millisecond, "resend mails the link")
	s.assertPreview(oldToken, 404)
	s.assertPreview(raw, 200)
}

func (s *AccountMembersTestSuite) TestLink_Invite_MintsALinkWithoutMail() {
	accountID := s.createAccount()
	owner := s.loginUser(models.AccountRoleOwner, models.MembershipStatusActive, accountID)
	admin := s.loginUser(models.AccountRoleAdmin, models.MembershipStatusActive, accountID)
	const oldToken = "copy-link-old-secret"
	const email = "copy-link@example.com"
	inviteID := s.insertOpenInvite(accountID, owner.id, email, models.AccountRoleAuditor, accountsvc.HashInviteToken(oldToken))
	expiresBefore := s.inviteExpiresEpoch(inviteID)
	dials := s.countMailDials()

	first := s.issuedInviteToken(s.postInviteLink(owner.token, accountID, inviteID), 200)
	second := s.issuedInviteToken(s.postInviteLink(admin.token, accountID, inviteID), 200)

	s.NotEqual(first, second)
	s.Equal(accountsvc.HashInviteToken(second), s.inviteTokenHash(inviteID))
	s.Equal(expiresBefore, s.inviteExpiresEpoch(inviteID))
	s.Equal(models.AccountRoleAuditor, s.storedInviteRole(inviteID))
	s.assertPreview(oldToken, 404)
	s.assertPreview(first, 404)
	s.assertPreview(second, 200)
	s.Equal(int64(0), s.countActivity(`SELECT count(*) FROM account_activity WHERE action = 'member.invited' AND account_id = ?`, accountID))
	props := s.activityText(`SELECT COALESCE(string_agg(properties::text, ' '), '') FROM activity_log`)
	s.NotContains(props, second)
	s.NotContains(props, accountsvc.HashInviteToken(second))

	// A resend afterwards dials the mailer once: the two links above dialed nothing.
	s.issuedInviteToken(s.postResend(owner.token, accountID, inviteID), 202)
	s.Eventually(func() bool { return dials.Load() == 1 }, 5*time.Second, 20*time.Millisecond, "resend mails the link")
	time.Sleep(100 * time.Millisecond)
	s.Equal(int64(1), dials.Load(), "copying a link must not send mail")

	// The link answered last is the one that works: accept it.
	linked := s.issuedInviteToken(s.postInviteLink(owner.token, accountID, inviteID), 200)
	body := fmt.Sprintf(`{"token":%q,"password":%q,"full_name":"Copied Link"}`, linked, membersTestPassword)
	s.Post("/v1/auth/invites/accept", support.Session{}, body).AssertOk()
	s.Equal(int64(1), s.countRows(&models.User{}, "email = ?", email))
	s.Equal(int64(1), s.countRows(&models.AccountInvite{}, "id = ? AND accepted_at IS NOT NULL", inviteID))
}

func (s *AccountMembersTestSuite) TestLink_Invite_RefusesWithoutUsersWrite() {
	accountID := s.createAccount()
	owner := s.loginUser(models.AccountRoleOwner, models.MembershipStatusActive, accountID)
	auditor := s.loginUser(models.AccountRoleAuditor, models.MembershipStatusActive, accountID)
	user := s.loginUser(models.AccountRoleUser, models.MembershipStatusActive, accountID)
	const storedHash = "copy-link-forbidden-digest"
	inviteID := s.insertOpenInvite(accountID, owner.id, "copy-forbidden@example.com", models.AccountRoleUser, storedHash)

	s.assertForbidden(s.postInviteLink(auditor.token, accountID, inviteID), "forbidden")
	s.assertForbidden(s.postInviteLink(user.token, accountID, inviteID), "forbidden")
	s.Equal(storedHash, s.inviteTokenHash(inviteID))

	missingAccount := s.postInviteLink(user.token, uuid.New(), inviteID)
	s.AssertError(missingAccount, 404, "not_found", "account not found")
}

func (s *AccountMembersTestSuite) TestLink_Invite_RefusesAnInviteThatIsNotOpen() {
	accountID := s.createAccount()
	otherID := s.createAccount()
	owner := s.loginUser(models.AccountRoleOwner, models.MembershipStatusActive, accountID)

	invalidID := s.Post("/v1/accounts/"+accountID.String()+"/invites/not-a-uuid/link", support.Session{AccessToken: owner.token}, "")
	s.AssertError(invalidID, 400, "invalid_request", "invalid invite id")

	unknown := s.postInviteLink(owner.token, accountID, uuid.New())
	s.AssertError(unknown, 404, "not_found", "invite is invalid or expired")

	closed := map[string]string{
		"accepted": `UPDATE account_invites SET accepted_at = NOW() WHERE id = ?`,
		"revoked":  `UPDATE account_invites SET revoked_at = NOW() WHERE id = ?`,
		"expired":  `UPDATE account_invites SET expires_at = NOW() - INTERVAL '1 minute' WHERE id = ?`,
	}
	for state, update := range closed {
		digest := "copy-link-" + state + "-digest"
		inviteID := s.insertOpenInvite(accountID, owner.id, "copy-"+state+"@example.com", models.AccountRoleUser, digest)
		_, err := facades.Orm().Query().Exec(update, inviteID)
		s.Require().NoError(err)

		resp := s.postInviteLink(owner.token, accountID, inviteID)
		s.AssertError(resp, 404, "not_found", "invite is invalid or expired")
		s.Equal(digest, s.inviteTokenHash(inviteID), state)
	}

	const otherHash = "copy-link-other-account-digest"
	otherInviteID := s.insertOpenInvite(otherID, owner.id, "copy-other@example.com", models.AccountRoleUser, otherHash)
	other := s.postInviteLink(owner.token, accountID, otherInviteID)
	s.AssertError(other, 404, "not_found", "invite is invalid or expired")
	s.Equal(otherHash, s.inviteTokenHash(otherInviteID))
}
