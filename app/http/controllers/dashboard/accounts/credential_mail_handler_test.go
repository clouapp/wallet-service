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

func TestRegisterSendsWelcomeWithoutTheCredentialJob(t *testing.T) {
	source, err := os.ReadFile("../auth/auth_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	body := functionBody(t, string(source), "func (ctrl *AuthController) Register")
	onboard := strings.Index(body, "Onboard(")
	login := strings.Index(body, "LoginUsingID(")
	send := strings.Index(body, "SendWelcome(")
	if onboard < 0 || login < 0 || send < 0 || onboard > send || send > login {
		t.Fatal("welcome mail must be sent after onboard returns and before login")
	}
	for _, forbidden := range []string{"Mail()", "WelcomeMail", "Dispatch(", "PurposeWelcome", ".Queue(", "SendCredentialMailJob"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("register still queues welcome mail via %s", forbidden)
		}
	}
	call := body[send:]
	open := strings.Index(call, "(")
	if open < 0 {
		t.Fatal("welcome send call is unopened")
	}
	depth := 0
	end := -1
	for i, char := range call[open:] {
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = open + i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		t.Fatal("welcome send call is unclosed")
	}
	args := strings.ToLower(call[open : end+1])
	if strings.Contains(args, "password") || strings.Contains(args, "hash") || strings.Contains(args, "token") || strings.Contains(args, "email") {
		t.Fatal("welcome send carries more than the user id")
	}
}

func TestSettingsTestMailUsesSend(t *testing.T) {
	source, err := os.ReadFile("../../platform/settings/settings_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	body := functionBody(t, string(source), "func (ctrl *SettingsController) TestMail")
	if !strings.Contains(body, "Mail().To(") || !strings.Contains(body, ".Send(") || !strings.Contains(body, "SettingsTestMail") {
		t.Fatal("settings test mail must use Mail().Send")
	}
	for _, forbidden := range []string{".Queue(", "SendCredentialMailJob", "Dispatch("} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("settings test mail is queued via %s", forbidden)
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
