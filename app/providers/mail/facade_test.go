package mail_test

import (
	"os"
	"testing"

	appfacades "github.com/macrowallets/waas/app/facades"
	appmail "github.com/macrowallets/waas/app/providers/mail"
	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
)

func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	bootstrap.Boot()
	os.Exit(m.Run())
}

func TestMail_Resolves_ToTheSettingsMailer(t *testing.T) {
	resolved := appfacades.Mail()
	mailer, ok := resolved.(*appmail.Mailer)
	if !ok || mailer == nil {
		t.Fatalf("facades.Mail() type %T", resolved)
	}
	if _, ok := mailer.To([]string{"nobody@example.test"}).(*appmail.Mailer); !ok {
		t.Fatal("a builder call left the settings mailer")
	}
}
