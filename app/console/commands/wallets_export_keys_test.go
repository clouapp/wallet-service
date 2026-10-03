package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/models"
)

const (
	exportKeysTestWalletA = "01e5e921-9443-4718-bf60-d624e65b03d9"
	exportKeysTestWalletB = "536e5579-bb17-4e45-aa97-13a1dd60af1f"
)

func TestWalletsExportKeys_SignatureAndFlags(t *testing.T) {
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

func TestParseWalletsExportKeysFlags_AcceptsRepeatedAndCommaSeparatedUUIDs(t *testing.T) {
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

func TestParseWalletsExportKeysFlags_RequiresAnExplicitSelection(t *testing.T) {
	if _, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{}); err == nil || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("no --wallet and no --all must be refused: %v", err)
	}
	if _, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{All: true, Wallets: []string{exportKeysTestWalletA}}); err == nil {
		t.Fatal("--wallet together with --all must be refused")
	}
}

func TestParseWalletsExportKeysFlags_RefusesBadWalletIDs(t *testing.T) {
	for _, bad := range []string{"", "not-a-uuid", uuid.Nil.String(), exportKeysTestWalletA + ",", "base_deposit"} {
		if _, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{Wallets: []string{bad}}); err == nil {
			t.Fatalf("--wallet %q must be refused", bad)
		}
	}
}

type fakeSecretValues struct {
	binary []byte
	err    error
	asked  string
}

func (f *fakeSecretValues) GetSecretValue(_ context.Context, input *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.asked = *input.SecretId
	if f.err != nil {
		return nil, f.err
	}
	return &secretsmanager.GetSecretValueOutput{SecretBinary: f.binary}, nil
}

func TestSecretsManagerShareB_FetchesTheWalletSecretWithoutLeakingErrors(t *testing.T) {
	wallet := models.Wallet{ID: uuid.MustParse(exportKeysTestWalletA), MPCSecretARN: "arn:aws:secretsmanager:test"}
	secrets := &fakeSecretValues{binary: []byte(`{"Xi":1}`)}
	share, err := secretsManagerShareB{secrets: secrets}.FetchShareB(context.Background(), wallet)
	if err != nil || string(share) != `{"Xi":1}` || secrets.asked != wallet.MPCSecretARN {
		t.Fatalf("share %q err %v asked %s", share, err, secrets.asked)
	}
	failing := &fakeSecretValues{err: errors.New("AccessDenied: secret value sk-123")}
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
