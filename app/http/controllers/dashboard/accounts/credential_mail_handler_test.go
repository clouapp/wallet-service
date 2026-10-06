package accounts

import (
	"os"
	"strings"
	"testing"
)

func TestHandlers_Leave_CredentialDispatchToTheDecidingService(t *testing.T) {
	checks := []struct {
		path      string
		signature string
	}{
		{"account_controller.go", "func (ctrl *AccountsController) AddAccountUser"},
		{"invites_controller.go", "func (ctrl *InvitesController) Create"},
		{"invites_controller.go", "func (ctrl *InvitesController) Resend"},
		{"../auth/auth_controller.go", "func (ctrl *AuthController) ForgotPassword"},
	}
	for _, check := range checks {
		source, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatal(err)
		}
		body := functionBody(t, string(source), check.signature)
		for _, forbidden := range []string{"Mail()", "SendAccountInvite", "SendPasswordReset", ".Queue(", "PasswordResetMail", "UserInviteMail", "DispatchAccountInvite", "Dispatch(", "SendCredentialMailJob", "facades.Queue"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s still dispatches mail via %s", check.signature, forbidden)
			}
		}
	}

	reset, err := os.ReadFile("../../../../services/users/password_reset_mail.go")
	if err != nil {
		t.Fatal(err)
	}
	resetBody := functionBody(t, string(reset), "func (s *Service) RequestPasswordReset")
	if !strings.Contains(resetBody, "DispatchPasswordReset(") {
		t.Fatal("password reset does not dispatch through the port")
	}
	for _, forbidden := range []string{"facades.Queue", ".Queue(", "token", "InviteLink", "ResetLink"} {
		if strings.Contains(resetBody, forbidden) {
			t.Fatalf("password reset dispatch carries %s", forbidden)
		}
	}

	invites, err := os.ReadFile("../../../../services/account/issue_invite.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, signature := range []string{"func (s *Service) IssueInvite", "func (s *Service) ResendInvite"} {
		body := functionBody(t, string(invites), signature)
		if !strings.Contains(body, "enqueueInviteMail(") {
			t.Fatalf("%s does not dispatch the invite job", signature)
		}
	}
	enqueue := functionBody(t, string(invites), "func (s *Service) enqueueInviteMail")
	if !strings.Contains(enqueue, "DispatchAccountInvite(") {
		t.Fatal("invite mail does not dispatch through the port")
	}
	for _, forbidden := range []string{"facades.Queue", ".Queue(", "RawToken"} {
		if strings.Contains(enqueue, forbidden) {
			t.Fatalf("invite dispatch carries %s", forbidden)
		}
	}
}

func TestRegister_Sends_WelcomeWithoutTheCredentialJob(t *testing.T) {
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

func TestSettings_Test_MailUsesSend(t *testing.T) {
	controller, err := os.ReadFile("../../platform/settings/settings_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	handler := functionBody(t, string(controller), "func (ctrl *SettingsController) TestMail")
	if !strings.Contains(handler, "SendPlatformMailTest(") {
		t.Fatal("settings test mail must be sent by the settings service")
	}
	for _, forbidden := range []string{"Mail()", "SettingsTestMail", ".Queue(", "SendCredentialMailJob", "Dispatch("} {
		if strings.Contains(handler, forbidden) {
			t.Fatalf("the settings controller sends mail via %s", forbidden)
		}
	}

	source, err := os.ReadFile("../../../../providers/platform_test_mail.go")
	if err != nil {
		t.Fatal(err)
	}
	body := functionBody(t, string(source), "func (platformTestMailer) Send")
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
