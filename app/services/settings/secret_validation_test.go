package settings

import (
	"testing"
)

type countingSealer struct {
	calls int
}

func (c *countingSealer) Seal(plaintext string) (string, error) {
	c.calls++
	return sealedPrefix + plaintext, nil
}

func (c *countingSealer) Open(value string) (string, error) {
	return prefixSealer{}.Open(value)
}

func TestPrepareValueSkipsABlankSecretBeforeSealing(t *testing.T) {
	t.Parallel()

	sealer := &countingSealer{}
	service := NewService(Deps{Store: newMemoryStore(), Sealer: sealer, Cache: nopCache{}, Activity: discardActivity{}})
	definition, ok := Find(groupAccountWebhooks, keySigningSecret)
	if !ok {
		t.Fatal("signing secret is not in the registry")
	}

	for _, incoming := range []any{nil, "", "   "} {
		_, skip, err := service.prepareValue(definition, incoming)
		if err != nil || !skip {
			t.Fatalf("blank %v skip=%v err=%v", incoming, skip, err)
		}
	}
	if sealer.calls != 0 {
		t.Fatalf("blank secret was sealed %d times", sealer.calls)
	}

	stored, skip, err := service.prepareValue(definition, "plain-secret")
	if err != nil || skip || stored != sealedPrefix+"plain-secret" {
		t.Fatalf("stored = %q skip=%v err=%v", stored, skip, err)
	}
	if sealer.calls != 1 {
		t.Fatalf("seal calls = %d", sealer.calls)
	}

	if _, _, err := service.prepareValue(definition, 42); err == nil {
		t.Fatal("a non-string secret was accepted")
	}
	if sealer.calls != 1 {
		t.Fatal("a rejected secret was sealed")
	}
}

func TestPrepareValueCastsASecretBeforeSealing(t *testing.T) {
	t.Parallel()

	sealer := &countingSealer{}
	service := NewService(Deps{Store: newMemoryStore(), Sealer: sealer, Cache: nopCache{}, Activity: discardActivity{}})

	units := Definition{Key: "units", Type: TypeInt, Secret: true}
	if _, _, err := service.prepareValue(units, "nope"); err == nil {
		t.Fatal("a secret of the wrong type was accepted")
	}
	if sealer.calls != 0 {
		t.Fatal("a rejected secret was sealed")
	}

	stored, skip, err := service.prepareValue(units, " 7 ")
	if err != nil || skip || stored != sealedPrefix+"7" {
		t.Fatalf("stored = %q skip=%v err=%v", stored, skip, err)
	}
	if sealer.calls != 1 {
		t.Fatalf("seal calls = %d", sealer.calls)
	}

	_, skip, err = service.prepareValue(units, "")
	if err != nil || !skip {
		t.Fatalf("blank integer secret skip=%v err=%v", skip, err)
	}
	if sealer.calls != 1 {
		t.Fatal("a blank secret was sealed")
	}

	choice := Definition{Key: "mode", Type: TypeString, Secret: true, Options: []string{"tls"}}
	if _, _, err := service.prepareValue(choice, "plain"); err == nil {
		t.Fatal("a secret outside its options was accepted")
	}
	if sealer.calls != 1 {
		t.Fatal("a rejected option was sealed")
	}

	stored, skip, err = service.prepareValue(choice, " tls ")
	if err != nil || skip || stored != sealedPrefix+"tls" {
		t.Fatalf("stored = %q skip=%v err=%v", stored, skip, err)
	}
	if sealer.calls != 2 {
		t.Fatalf("seal calls = %d", sealer.calls)
	}
}

func TestEffectiveNonSecretsOmitTheSecret(t *testing.T) {
	t.Parallel()

	group, ok := FindGroup(groupAccountWebhooks)
	if !ok {
		t.Fatal("account_webhooks is not in the registry")
	}
	effective := effectiveNonSecrets(group.Settings, map[string]string{
		keySigningSecret: sealedPrefix + "ciphertext-that-must-not-be-exposed",
		keyDefaultEvents: "deposit.confirmed",
	}, nil)
	if _, exposed := effective[keySigningSecret]; exposed {
		t.Fatalf("group validation saw the secret: %v", effective)
	}
	if effective[keySigningAlgorithm] != signingAlgorithmHMACSHA256 {
		t.Fatalf("missing algorithm = %q", effective[keySigningAlgorithm])
	}
}
