package commands

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/models"
)

const (
	exportKeysTestWalletA = "01e5e921-9443-4718-bf60-d624e65b03d9"
	exportKeysTestWalletB = "536e5579-bb17-4e45-aa97-13a1dd60af1f"
)

type exportKeysWalletStub struct{}

func (exportKeysWalletStub) FindAll(context.Context) ([]models.Wallet, error) { return nil, nil }
func (exportKeysWalletStub) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return nil, nil
}

type exportKeysAddressStub struct{}

func (exportKeysAddressStub) FindByWalletID(context.Context, uuid.UUID) ([]models.Address, error) {
	return nil, nil
}

type exportKeysChainStub struct{}

func (exportKeysChainStub) FindByID(context.Context, string) (*models.Chain, error) { return nil, nil }

func TestNew_Wallets_ExportKeysKeepsItsDependencies(t *testing.T) {
	wallets := exportKeysWalletStub{}
	addresses := exportKeysAddressStub{}
	chains := exportKeysChainStub{}
	cmd := NewWalletsExportKeys(WalletsExportKeysDeps{Wallets: wallets, Addresses: addresses, Chains: chains})
	if cmd == nil {
		t.Fatal("NewWalletsExportKeys returned nil")
	}
	if cmd.wallets != wallets {
		t.Fatal("export keys did not keep the wallet source")
	}
	if cmd.addresses != addresses {
		t.Fatal("export keys did not keep the address source")
	}
	if cmd.chains != chains {
		t.Fatal("export keys did not keep the chain source")
	}
}

func TestNew_Wallets_ExportKeysStoresNilDependencies(t *testing.T) {
	cmd := NewWalletsExportKeys(WalletsExportKeysDeps{})
	if cmd == nil {
		t.Fatal("NewWalletsExportKeys returned nil")
	}
	if cmd.wallets != nil || cmd.addresses != nil || cmd.chains != nil {
		t.Fatal("omitted dependencies should stay nil")
	}
	if _, err := cmd.newWalletsExportService(); err == nil || !strings.Contains(err.Error(), "wallets, addresses and chains are required") {
		t.Fatalf("nil dependencies must be refused before secrets are touched: %v", err)
	}
}

func TestWallets_ExportKeys_SignatureAndFlags(t *testing.T) {
	cmd := &WalletsExportKeys{}
	if cmd.Signature() != "wallets:export-keys" {
		t.Fatalf("signature %s", cmd.Signature())
	}
	flags := map[string]command.Flag{}
	for _, flag := range cmd.Extend().Flags {
		switch typed := flag.(type) {
		case *command.StringSliceFlag:
			flags[typed.Name] = flag
		case *command.StringFlag:
			flags[typed.Name] = flag
		case *command.BoolFlag:
			flags[typed.Name] = flag
		default:
			t.Fatalf("unexpected flag type %T", flag)
		}
	}
	if _, ok := flags["wallet"].(*command.StringSliceFlag); !ok {
		t.Fatal("--wallet must be repeatable (StringSliceFlag)")
	}
	for _, name := range []string{"out", "passphrase-vault", "allow-production"} {
		if flags[name] == nil {
			t.Fatalf("missing --%s", name)
		}
	}
	for name := range flags {
		if strings.Contains(name, "password") || name == "passphrase" || name == "passphrase-stdin" {
			t.Fatalf("secrets must never come from flags, found --%s", name)
		}
	}
}

func TestParse_WalletsExportKeysFlags_AcceptsRepeatedAndCommaSeparatedUUIDs(t *testing.T) {
	invocation, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{
		Wallets: []string{exportKeysTestWalletA, " " + exportKeysTestWalletB + "," + exportKeysTestWalletA},
		Out:     " /tmp/x/keys.zip ", PassphraseVault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(invocation.walletIDs) != 2 || invocation.walletIDs[0].String() != exportKeysTestWalletA || invocation.walletIDs[1].String() != exportKeysTestWalletB {
		t.Fatalf("wallet ids %v", invocation.walletIDs)
	}
	if invocation.out != "/tmp/x/keys.zip" || !invocation.passphraseVault || invocation.allowProduction {
		t.Fatalf("invocation %+v", invocation)
	}
	all, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{All: true})
	if err != nil || len(all.walletIDs) != 0 {
		t.Fatalf("--all means every wallet: %+v %v", all, err)
	}
}

func TestParse_WalletsExportKeysFlags_RequiresAnExplicitSelection(t *testing.T) {
	if _, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{}); err == nil || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("no --wallet and no --all must be refused: %v", err)
	}
	if _, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{All: true, Wallets: []string{exportKeysTestWalletA}}); err == nil {
		t.Fatal("--wallet together with --all must be refused")
	}
}

func TestParse_WalletsExportKeysFlags_RefusesBadWalletIDs(t *testing.T) {
	for _, bad := range []string{"", "not-a-uuid", uuid.Nil.String(), exportKeysTestWalletA + ",", "base_deposit"} {
		if _, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{Wallets: []string{bad}}); err == nil {
			t.Fatalf("--wallet %q must be refused", bad)
		}
	}
}

func exportSecretsClient(t *testing.T, status int, message string, binary []byte, asked *string) *secretsmanager.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Amz-Target") != "secretsmanager.GetSecretValue" {
			t.Errorf("request %s target %s", r.Method, r.Header.Get("X-Amz-Target"))
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body: %v", err)
		}
		if asked != nil {
			*asked = body["SecretId"]
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		if status != http.StatusOK {
			w.Header().Set("X-Amzn-ErrorType", "AccessDeniedException")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"__type": "AccessDeniedException", "message": message})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"SecretBinary": base64.StdEncoding.EncodeToString(binary),
		})
	}))
	t.Cleanup(server.Close)
	return secretsmanager.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	}, func(o *secretsmanager.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.Retryer = retry.AddWithMaxAttempts(retry.NewStandard(), 1)
	})
}

func TestSecrets_ManagerShareB_FetchesTheWalletSecretWithoutLeakingErrors(t *testing.T) {
	wallet := models.Wallet{ID: uuid.MustParse(exportKeysTestWalletA), MPCSecretARN: "arn:aws:secretsmanager:test"}
	var asked string
	secrets := exportSecretsClient(t, http.StatusOK, "", []byte(`{"Xi":1}`), &asked)
	share, err := secretsManagerShareB{secrets: secrets}.FetchShareB(context.Background(), wallet)
	if err != nil || string(share) != `{"Xi":1}` || asked != wallet.MPCSecretARN {
		t.Fatalf("share %q err %v asked %s", share, err, asked)
	}
	failing := exportSecretsClient(t, http.StatusBadRequest, "AccessDenied: secret value sk-123", nil, nil)
	if _, err := (secretsManagerShareB{secrets: failing}).FetchShareB(context.Background(), wallet); err == nil || strings.Contains(err.Error(), "sk-123") {
		t.Fatalf("errors must not echo the provider message, got %v", err)
	}
	wallet.MPCSecretARN = " "
	if _, err := (secretsManagerShareB{secrets: secrets}).FetchShareB(context.Background(), wallet); err == nil {
		t.Fatal("a wallet without an ARN must be refused")
	}
	if _, err := (secretsManagerShareB{}).FetchShareB(context.Background(), wallet); err == nil {
		t.Fatal("a missing secrets manager must be refused")
	}
}
