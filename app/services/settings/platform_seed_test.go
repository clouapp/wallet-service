package settings

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

const (
	seedHostFixture     = "smtp.seed.example"
	seedPasswordFixture = "seed-fixture-password"
	seedSealedMarker    = "enc:v1:sealed-marker"
)

func TestPlatform_Settings_SeedSkipsBlankEnv(t *testing.T) {
	rows, err := PlatformSettingsSeed(func(string) string { return "" }, nil)
	if err != nil {
		t.Fatalf("blank env: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("blank env inserted %d rows", len(rows))
	}

	rows, err = PlatformSettingsSeed(func(name string) string {
		if name == envMailUsername || name == envMailPassword {
			return "   "
		}
		return ""
	}, nil)
	if err != nil {
		t.Fatalf("whitespace env: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("whitespace env inserted %d rows", len(rows))
	}
}

func TestPlatform_Settings_SeedInsertsANonSecret(t *testing.T) {
	rows, err := PlatformSettingsSeed(func(name string) string {
		if name == envMailHost {
			return "  " + seedHostFixture + "  "
		}
		return ""
	}, nil)
	if err != nil {
		t.Fatalf("seed host: %v", err)
	}
	if len(rows) != 1 || rows[0].Group != groupMailSMTP || rows[0].Key != keyMailHost || rows[0].Value != seedHostFixture {
		t.Fatal("seeded host did not match the fixture")
	}
}

func TestPlatform_Settings_SeedSealsASecretWithoutLoggingIt(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	sealer := &rememberSealer{}
	rows, err := PlatformSettingsSeed(func(name string) string {
		if name == envMailPassword {
			return seedPasswordFixture
		}
		return ""
	}, sealer)
	if err != nil {
		t.Fatal("seed password failed")
	}
	if len(rows) != 1 || rows[0].Group != groupMailSMTP || rows[0].Key != keyMailPassword {
		t.Fatal("seeded password row was missing")
	}
	if !strings.HasPrefix(rows[0].Value, "enc:v1:") || strings.Contains(rows[0].Value, seedPasswordFixture) {
		t.Fatal("stored password is not sealed")
	}
	opened, err := sealer.Open(rows[0].Value)
	if err != nil || opened != seedPasswordFixture {
		t.Fatal("opened password did not match the fixture")
	}
	if strings.Contains(logs.String(), seedPasswordFixture) || strings.Contains(logs.String(), "enc:v1:") || strings.Contains(logs.String(), rows[0].Value) {
		t.Fatal("seed output included a secret or a sealed blob")
	}
}

func TestPlatform_Settings_SeedSkipsFieldsWithoutAnEnvFallback(t *testing.T) {
	rows, err := PlatformSettingsSeed(func(name string) string {
		switch name {
		case envMailFromAddress:
			return "from-seed@example.test"
		case envCoinGeckoAPIKey:
			return "price-key-fixture"
		case envAlchemyAuthToken:
			return "alchemy-token-fixture"
		default:
			return ""
		}
	}, fixtureSealer{})
	if err != nil {
		t.Fatal("seed failed")
	}
	seen := map[string]string{}
	for _, row := range rows {
		seen[row.Group+"\x00"+row.Key] = row.Value
	}
	if seen[groupMailDelivery+"\x00"+keyMailFromAddress] != "from-seed@example.test" {
		t.Fatal("from address was not seeded")
	}
	if _, ok := seen[groupMailDelivery+"\x00"+keyMailDriver]; ok {
		t.Fatal("driver was seeded without an env fallback")
	}
	if _, ok := seen[groupMailSES+"\x00"+keyMailFromAddress]; ok {
		t.Fatal("mail provider from address was seeded from the delivery env")
	}
	if _, ok := seen[groupPriceCoinGecko+"\x00"+keyPriceEnabled]; ok {
		t.Fatal("price enabled was seeded without an env fallback")
	}
	if _, ok := seen[groupProviderAlchemy+"\x00"+keyProviderEnabled]; ok {
		t.Fatal("provider enabled was seeded without an env fallback")
	}
	if _, ok := seen[groupPriceLookup+"\x00"+keyProviderOrder]; ok {
		t.Fatal("provider order was seeded without an env fallback")
	}
	if _, ok := seen[groupDepositScan+"\x00"+keyBatchBlocks]; ok {
		t.Fatal("deposit scan was seeded")
	}
	sealed := seen[groupPriceCoinGecko+"\x00"+keyPriceAPIKey]
	if !strings.HasPrefix(sealed, "enc:v1:") || strings.Contains(sealed, "price-key-fixture") {
		t.Fatal("price key was not sealed")
	}
	token := seen[groupProviderAlchemy+"\x00"+keyProviderAuthToken]
	if !strings.HasPrefix(token, "enc:v1:") || strings.Contains(token, "alchemy-token-fixture") {
		t.Fatal("alchemy token was not sealed")
	}
}

func TestPlatform_Settings_SeedRefusesAnUnstorableValueWithoutEchoingIt(t *testing.T) {
	const invalid = "not-an-encryption-mode"
	_, err := PlatformSettingsSeed(func(name string) string {
		if name == envMailEncryption {
			return invalid
		}
		return ""
	}, nil)
	if err == nil {
		t.Fatal("invalid encryption was accepted")
	}
	if strings.Contains(err.Error(), invalid) {
		t.Fatal("seed error included the env value")
	}
}

func TestPlatform_Seed_EnvMatchesTheRegistry(t *testing.T) {
	if len(platformSeedEnv) == 0 {
		t.Fatal("platform seed env map is empty")
	}
	for token, envName := range platformSeedEnv {
		group, key, ok := strings.Cut(token, "\x00")
		if !ok || !platformSeedGroup(group) {
			t.Fatalf("mapped group %s is outside the seed wildcards", group)
		}
		if _, found := Find(group, key); !found {
			t.Fatalf("mapped field %s %s is not in the registry", group, key)
		}
		if strings.TrimSpace(envName) == "" {
			t.Fatal("mapped env name is empty")
		}
	}
}

type fixtureSealer struct{}

func (fixtureSealer) Seal(plaintext string) (string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", nil
	}
	return seedSealedMarker, nil
}

func (fixtureSealer) Open(value string) (string, error) {
	if value != seedSealedMarker {
		return "", errSealFailed
	}
	return seedPasswordFixture, nil
}

type rememberSealer struct {
	plain string
}

func (s *rememberSealer) Seal(plaintext string) (string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", nil
	}
	s.plain = plaintext
	return seedSealedMarker, nil
}

func (s *rememberSealer) Open(value string) (string, error) {
	if value != seedSealedMarker || s.plain == "" {
		return "", errSealFailed
	}
	return s.plain, nil
}
