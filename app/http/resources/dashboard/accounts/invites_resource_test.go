package accounts_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

func TestInvite_Link_KeepsTheMapWire(t *testing.T) {
	t.Parallel()

	issued := &accountsvc.IssuedInvite{
		Invite: &models.AccountInvite{
			ID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Email: "new@example.com", Role: "auditor",
			TokenHash: "fixture-token-hash", ExpiresAt: time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC),
		},
		InviteLink: "https://wallet.example/accept-invite?token=raw",
		RawToken:   "raw",
	}

	// The handler answered this map before the resource existed.
	want, err := json.Marshal(map[string]any{
		"invite_id":   issued.Invite.ID,
		"email":       issued.Invite.Email,
		"role":        issued.Invite.Role,
		"expires_at":  issued.Invite.ExpiresAt,
		"invite_link": issued.InviteLink,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(accounts.NewInviteLink(issued))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("wire changed\n got %s\nwant %s", got, want)
	}
}

func TestInvite_View_KeepsTheListWire(t *testing.T) {
	t.Parallel()

	inviter := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	accepted := time.Date(2031, 2, 3, 4, 5, 7, 0, time.UTC)
	invite := models.AccountInvite{
		ID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), AccountID: inviter, Email: "new@example.com",
		Role: "user", TokenHash: "fixture-token-hash", InvitedBy: &inviter,
		ExpiresAt: time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC), AcceptedAt: &accepted,
	}
	invite.CreatedAt = carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))

	got, err := json.Marshal(accounts.NewInvites([]models.AccountInvite{invite}))
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"created_at":"2024-05-06 07:08:09","id":"11111111-1111-4111-8111-111111111111","account_id":"22222222-2222-4222-8222-222222222222","email":"new@example.com","role":"user","invited_by":"22222222-2222-4222-8222-222222222222","expires_at":"2031-02-03T04:05:06Z","accepted_at":"2031-02-03T04:05:07Z","revoked_at":null}]`
	if string(got) != want {
		t.Fatalf("wire changed\n got %s\nwant %s", got, want)
	}

	empty, err := json.Marshal(accounts.NewInvites(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != `[]` {
		t.Fatalf("an empty page is %s, want []", empty)
	}
}

func TestInvite_Preview_AndAcceptKeepTheMapWire(t *testing.T) {
	t.Parallel()

	preview := accountsvc.InvitePreview{AccountName: "Acme", Inviter: "Ada", Role: "user", Email: "new@example.com", NeedsPassword: true}
	want, err := json.Marshal(map[string]any{
		"account_name":   preview.AccountName,
		"inviter":        preview.Inviter,
		"role":           preview.Role,
		"email":          preview.Email,
		"needs_password": preview.NeedsPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(accounts.NewInvitePreview(preview))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("preview wire changed\n got %s\nwant %s", got, want)
	}

	user := &models.User{ID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Email: "new@example.com", Status: "active", PasswordHash: "hidden"}
	want, err = json.Marshal(map[string]any{"user_id": user.ID, "email": user.Email, "status": user.Status})
	if err != nil {
		t.Fatal(err)
	}
	got, err = json.Marshal(accounts.NewAcceptedInvite(user))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("accept wire changed\n got %s\nwant %s", got, want)
	}
}
