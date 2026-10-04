package facades

import (
	"context"
	"testing"
)

func TestGateMailSendReadsTheInstalledDial(t *testing.T) {
	previousSMTP := SetMailSMTPReader(func(context.Context) (MailDial, error) {
		return MailDial{Host: "127.0.0.1", UseHost: true}, nil
	})
	previousFrom := SetMailFromReader(func(context.Context) (MailFrom, error) {
		return MailFrom{Address: "from-header@example.test", Name: "Macro", UseAddress: true, UseName: true}, nil
	})
	t.Cleanup(func() {
		SetMailSMTPReader(previousSMTP)
		SetMailFromReader(previousFrom)
	})

	var dial MailDial
	var from MailFrom
	if err := GateMailSend(func() error {
		var installed bool
		var err error
		dial, installed, err = ReadMailDial(context.Background())
		if err != nil || !installed {
			t.Fatal("the mail_smtp reader was not installed")
		}
		from, installed, err = ReadMailFrom(context.Background())
		if err != nil || !installed {
			t.Fatal("the mail_delivery reader was not installed")
		}
		return nil
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if dial.Host != "127.0.0.1" || !dial.UseHost || dial.UsePassword {
		t.Fatal("the mail_smtp dial was not visible during send")
	}
	if from.Address != "from-header@example.test" || from.Name != "Macro" || !from.UseAddress || !from.UseName {
		t.Fatal("the mail_delivery header was not visible during send")
	}
}
