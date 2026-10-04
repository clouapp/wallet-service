package account_test

import (
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/mails"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

func TestInviteLinkIsARealTokenAndIsNotQueued(t *testing.T) {
	link, err := accountsvc.InviteLink("https://app.example", "tok_abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(link, "token=tok_abc") || strings.Contains(link, "vault.app/accept-invite") {
		t.Fatalf("link = %s", link)
	}
	if _, err := accountsvc.InviteLink("https://app.example", ""); err == nil {
		t.Fatal("empty token must be refused")
	}
	mail := &mails.UserInviteMail{InviteLink: link}
	if mail.Queue() != nil {
		t.Fatal("invite mail must not put the token on a queue")
	}
	hash := accountsvc.HashInviteToken("tok_abc")
	if hash == "" || hash == "tok_abc" || !accountsvc.InviteTokenMatches(hash, "tok_abc") || accountsvc.InviteTokenMatches(hash, "other") {
		t.Fatal("only the hash of the token may be stored")
	}
}
