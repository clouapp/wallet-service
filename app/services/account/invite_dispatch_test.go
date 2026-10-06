package account

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestEnqueue_Invite_MailDispatchesOnlyTheInviteID(t *testing.T) {
	inviteID := uuid.New()
	const (
		firstLink = "https://app.example/accept-invite?token=minted-at-issue"
		jobLink   = "https://app.example/accept-invite?token=minted-by-job"
	)
	var got uuid.UUID
	svc := &Service{inviteMail: inviteMailFunc(func(id uuid.UUID) (string, error) {
		got = id
		if strings.Contains(id.String(), "token") {
			t.Fatal("dispatch argument carried a token")
		}
		return jobLink, nil
	})}
	issued := &IssuedInvite{
		Invite:     &models.AccountInvite{ID: inviteID},
		InviteLink: firstLink,
		RawToken:   "minted-at-issue",
	}

	svc.enqueueInviteMail(issued)
	if got != inviteID {
		t.Fatalf("dispatched %s", got)
	}
	if issued.InviteLink != jobLink || issued.MailErr != nil {
		t.Fatalf("link %q err %v", issued.InviteLink, issued.MailErr)
	}
	if issued.RawToken != "minted-at-issue" {
		t.Fatal("dispatch rewrote the raw token onto the result")
	}
}

func TestEnqueue_Invite_MailKeepsTheStoredLinkWhenTheJobReturnsNone(t *testing.T) {
	const firstLink = "https://app.example/accept-invite?token=minted-at-issue"
	svc := &Service{inviteMail: inviteMailFunc(func(uuid.UUID) (string, error) {
		return "", errors.New("invite mail: send failed")
	})}
	issued := &IssuedInvite{
		Invite:     &models.AccountInvite{ID: uuid.New()},
		InviteLink: firstLink,
	}

	svc.enqueueInviteMail(issued)
	if issued.InviteLink != firstLink || issued.MailErr == nil {
		t.Fatalf("link %q err %v", issued.InviteLink, issued.MailErr)
	}
}

func TestEnqueue_Invite_MailWithoutAPortLeavesTheInvite(t *testing.T) {
	issued := &IssuedInvite{
		Invite:     &models.AccountInvite{ID: uuid.New()},
		InviteLink: "https://app.example/accept-invite?token=kept",
	}
	(&Service{}).enqueueInviteMail(issued)
	if issued.MailErr != nil || issued.InviteLink != "https://app.example/accept-invite?token=kept" {
		t.Fatal("nil dispatcher changed the invite")
	}
}

type inviteMailFunc func(uuid.UUID) (string, error)

func (f inviteMailFunc) DispatchAccountInvite(inviteID uuid.UUID) (string, error) {
	return f(inviteID)
}
