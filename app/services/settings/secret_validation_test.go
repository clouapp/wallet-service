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
	service := NewService(newMemoryStore(), sealer, nopCache{}, discardActivity{})
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
