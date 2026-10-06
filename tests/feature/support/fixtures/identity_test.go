package fixtures

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestEmailAccountWalletLabelAndClientIPShareTheRunNonce(t *testing.T) {
	n := sequence.Load()

	email := Email()
	accountID := AccountID()
	label := WalletLabel("eth")
	clientIP := ClientIP()

	for i, got := range []string{email, label} {
		if !strings.Contains(got, runNonce) {
			t.Fatalf("value %d %q does not contain run nonce %s", i, got, runNonce)
		}
	}
	if email == Email() {
		t.Fatal("two e-mails collided")
	}
	if accountID == AccountID() {
		t.Fatal("two account ids collided")
	}
	if label == WalletLabel("eth") {
		t.Fatal("two wallet labels collided")
	}
	if clientIP == ClientIP() {
		t.Fatal("two client IPs collided")
	}

	wantAccount := uuid.NewSHA1(fixtureSpace, []byte("account:"+runNonce+"-"+strconv.FormatUint(n+2, 10)))
	if accountID != wantAccount {
		t.Fatalf("account id = %s, want %s", accountID, wantAccount)
	}
	if accountID.Version() != 5 {
		t.Fatalf("account id version = %d, want 5", accountID.Version())
	}

	if !strings.HasPrefix(label, "eth test wallet "+runNonce+"-") {
		t.Fatalf("label = %q", label)
	}
	parsed := net.ParseIP(clientIP)
	if parsed == nil || parsed.To16() == nil || parsed.To4() != nil {
		t.Fatalf("client IP = %q, want an IPv6 address", clientIP)
	}
	if !strings.HasPrefix(strings.ToLower(clientIP), "2001:db8:"+runNonce[0:4]+":"+runNonce[4:8]) {
		t.Fatalf("client IP = %q, want the run nonce in the address", clientIP)
	}
}
