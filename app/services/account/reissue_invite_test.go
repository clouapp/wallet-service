package account

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// rotatingInvites keeps one invite in memory: FindOpenByAccountAndID answers it
// while it is open and of the account, Rotate replaces its hash, and
// FindPendingByTokenHash finds it by the hash it holds now. Any other call
// panics on the nil embedded interface.
type rotatingInvites struct {
	InviteStore
	invite  *models.AccountInvite
	rotated int
}

func (s *rotatingInvites) FindOpenByAccountAndID(_ context.Context, accountID, id uuid.UUID) (*models.AccountInvite, error) {
	if s.invite == nil || s.invite.ID != id || s.invite.AccountID != accountID ||
		s.invite.AcceptedAt != nil || s.invite.RevokedAt != nil {
		return nil, models.ErrRepositoryNotFound
	}
	found := *s.invite
	return &found, nil
}

func (s *rotatingInvites) Rotate(_ context.Context, id uuid.UUID, tokenHash, role string, expiresAt time.Time) error {
	if s.invite == nil || s.invite.ID != id {
		return models.ErrRepositoryNotFound
	}
	s.rotated++
	s.invite.TokenHash = tokenHash
	s.invite.Role = role
	s.invite.ExpiresAt = expiresAt
	return nil
}

func (s *rotatingInvites) FindPendingByTokenHash(_ context.Context, tokenHash string, now time.Time) (*models.AccountInvite, error) {
	if s.invite == nil || s.invite.TokenHash != tokenHash || !s.invite.ExpiresAt.After(now) ||
		s.invite.AcceptedAt != nil || s.invite.RevokedAt != nil {
		return nil, models.ErrRepositoryNotFound
	}
	found := *s.invite
	return &found, nil
}

const reissueBase = "https://app.example"

func reissueService(t *testing.T, invites *rotatingInvites) *Service {
	t.Helper()
	return NewService(Deps{
		Invites:  invites,
		Users:    previewUsers{inviter: &models.User{FullName: "Ada Lovelace"}},
		Accounts: previewAccounts{account: &models.Account{Name: "Acme"}},
		InviteMail: inviteMailFunc(func(uuid.UUID) (string, error) {
			t.Fatal("a reissued invite link must not dispatch the invite mail")
			return "", nil
		}),
	})
}

func TestReissue_Invite_MintsAWorkingLinkWithoutMailAndRetiresThePreviousOne(t *testing.T) {
	const oldToken = "previous-raw-token"
	invite := pendingInvite()
	invite.TokenHash = HashInviteToken(oldToken)
	expires := invite.ExpiresAt
	invites := &rotatingInvites{invite: invite}
	svc := reissueService(t, invites)
	ctx := context.Background()

	if _, err := svc.PreviewInvite(ctx, oldToken); err != nil {
		t.Fatalf("the stored token must preview before the reissue: %v", err)
	}

	issued, err := svc.ReissueInvite(ctx, invite.AccountID, invite.ID, reissueBase)
	if err != nil {
		t.Fatal(err)
	}
	prefix := reissueBase + "/accept-invite?token="
	if !strings.HasPrefix(issued.InviteLink, prefix) {
		t.Fatalf("link = %q", issued.InviteLink)
	}
	newToken := strings.TrimPrefix(issued.InviteLink, prefix)
	if newToken == "" || newToken == oldToken || newToken != issued.RawToken {
		t.Fatalf("link token %q, raw token %q", newToken, issued.RawToken)
	}
	if invites.rotated != 1 || invites.invite.TokenHash != HashInviteToken(newToken) {
		t.Fatalf("rotated %d times, stored hash %q", invites.rotated, invites.invite.TokenHash)
	}
	if !invites.invite.ExpiresAt.Equal(expires) || invites.invite.Role != models.AccountRoleAuditor {
		t.Fatalf("expiry %v role %q changed", invites.invite.ExpiresAt, invites.invite.Role)
	}
	if issued.MailErr != nil || issued.Invite == nil || issued.Invite.ID != invite.ID {
		t.Fatalf("issued = %+v", issued)
	}

	if _, err := svc.PreviewInvite(ctx, oldToken); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("the previous token must stop working, err = %v", err)
	}
	if _, err := svc.PreviewInvite(ctx, newToken); err != nil {
		t.Fatalf("the new token must preview: %v", err)
	}
}

func TestReissue_Invite_RefusesAnInviteThatIsNotOpen(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	for name, change := range map[string]func(*models.AccountInvite){
		"accepted": func(i *models.AccountInvite) { i.AcceptedAt = &now },
		"revoked":  func(i *models.AccountInvite) { i.RevokedAt = &now },
		"expired":  func(i *models.AccountInvite) { i.ExpiresAt = now.Add(-time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			invite := pendingInvite()
			invite.TokenHash = "stored-digest"
			change(invite)
			invites := &rotatingInvites{invite: invite}

			if _, err := reissueService(t, invites).ReissueInvite(ctx, invite.AccountID, invite.ID, reissueBase); !errors.Is(err, ErrInviteInvalid) {
				t.Fatalf("err = %v, want ErrInviteInvalid", err)
			}
			if invites.rotated != 0 || invites.invite.TokenHash != "stored-digest" {
				t.Fatal("a refused reissue must leave the stored hash")
			}
		})
	}

	invite := pendingInvite()
	invites := &rotatingInvites{invite: invite}
	svc := reissueService(t, invites)
	if _, err := svc.ReissueInvite(ctx, uuid.New(), invite.ID, reissueBase); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("another account's invite: err = %v", err)
	}
	if _, err := svc.ReissueInvite(ctx, invite.AccountID, uuid.New(), reissueBase); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("an unknown invite: err = %v", err)
	}
	if _, err := svc.ReissueInvite(ctx, invite.AccountID, invite.ID, ""); err == nil {
		t.Fatal("a missing front-end base must refuse")
	}
	if invites.rotated != 0 {
		t.Fatal("a refused reissue must not rotate")
	}
}

func TestReissue_InviteLink_RefusesWithoutAFrontendURL(t *testing.T) {
	t.Setenv(frontendURLEnv, "")
	invite := pendingInvite()
	invites := &rotatingInvites{invite: invite}

	_, err := reissueService(t, invites).ReissueInviteLink(context.Background(), invite.AccountID, invite.ID)
	if !errors.Is(err, ErrFrontendURLRequired) || invites.rotated != 0 {
		t.Fatalf("err = %v, rotated %d", err, invites.rotated)
	}
}
