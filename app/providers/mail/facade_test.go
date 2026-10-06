package mail_test

import (
	"os"
	"testing"

	appfacades "github.com/macrowallets/waas/app/facades"
	appmail "github.com/macrowallets/waas/app/providers/mail"
	"github.com/macrowallets/waas/app/providers/mailer"
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

func TestMailResolvesToTheFacadeOverTheMailerConfig(t *testing.T) {
	resolved := appfacades.Mail()
	facade, ok := resolved.(*appmail.Facade)
	if !ok || facade == nil {
		t.Fatalf("facades.Mail() type %T", resolved)
	}
	mailerInstance := facade.Mailer()
	if mailerInstance == nil {
		t.Fatal("facades.Mail() is not over the mailer")
	}
	if _, ok := any(mailerInstance.Config()).(*mailer.Config); !ok || mailerInstance.Config() == nil {
		t.Fatalf("mailer config type %T", mailerInstance.Config())
	}
}
