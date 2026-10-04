package accounts

import (
	"os"
	"strings"
	"testing"
)

func TestHandlersDispatchCredentialMailInsteadOfSendingIt(t *testing.T) {
	checks := []struct {
		path      string
		signature string
		dispatch  string
	}{
		{"account_controller.go", "func (ctrl *AccountsController) AddAccountUser", "DispatchAccountInvite"},
		{"invites_controller.go", "func dispatchInviteMail", "Dispatch("},
		{"../auth/auth_controller.go", "func (ctrl *AuthController) ForgotPassword", "PurposePasswordReset"},
	}
	for _, check := range checks {
		source, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatal(err)
		}
		body := functionBody(t, string(source), check.signature)
		if !strings.Contains(body, check.dispatch) {
			t.Fatalf("%s does not dispatch the credential job", check.signature)
		}
		for _, forbidden := range []string{"Mail()", "SendAccountInvite", "SendPasswordReset", ".Queue(", "PasswordResetMail", "UserInviteMail"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s still sends a credential mail via %s", check.signature, forbidden)
			}
		}
	}
}

func functionBody(t *testing.T, source, signature string) string {
	t.Helper()
	start := strings.Index(source, signature)
	if start < 0 {
		t.Fatalf("missing %s", signature)
	}
	relBrace := strings.Index(source[start:], "{")
	if relBrace < 0 {
		t.Fatalf("%s has no body", signature)
	}
	opening := start + relBrace
	depth := 0
	for pos, char := range source[opening:] {
		switch char {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[opening : opening+pos+1]
			}
		}
	}
	t.Fatalf("%s body is unclosed", signature)
	return ""
}
