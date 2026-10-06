package settings

import "testing"

func TestSigning_Algorithm_IsAClosedVocabulary(t *testing.T) {
	t.Parallel()

	definition, ok := Find(groupAccountWebhooks, keySigningAlgorithm)
	if !ok || len(definition.Options) == 0 {
		t.Fatal("signing algorithm has no options")
	}
	if _, err := castIn(definition.Options[0], definition); err != nil {
		t.Fatalf("offered algorithm: %v", err)
	}
	if _, err := castIn("sendgrid", definition); err == nil {
		t.Fatal("an algorithm outside the vocabulary was accepted")
	}
}

func TestValidate_Session_IdleRefusesAMinuteCountOutsideTheRange(t *testing.T) {
	t.Parallel()

	group, ok := FindGroup(groupAccountSecurity)
	if !ok || group.Validate == nil {
		t.Fatal("account_security has no validator")
	}
	for _, minutes := range []string{"4", "10081", "0", "-1", "nope"} {
		if err := group.Validate(map[string]string{keySessionIdleMinutes: minutes}); err == nil {
			t.Fatalf("minutes %q was accepted", minutes)
		}
	}
	for _, minutes := range []string{"5", "30", "10080"} {
		if err := group.Validate(map[string]string{keySessionIdleMinutes: minutes}); err != nil {
			t.Fatalf("minutes %q: %v", minutes, err)
		}
	}
}

func TestWebhook_Credential_IsDeclaredSecret(t *testing.T) {
	t.Parallel()

	definition, ok := Find(groupAccountWebhooks, keySigningSecret)
	if !ok || !definition.Secret || definition.Type != TypeString {
		t.Fatalf("signing secret = %+v present %v", definition, ok)
	}
}
