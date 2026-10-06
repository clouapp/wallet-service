package seeds

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type fakeSeedSecretsManager struct {
	output *secretsmanager.GetSecretValueOutput
	err    error
}

func (f *fakeSeedSecretsManager) GetSecretValue(
	context.Context,
	*secretsmanager.GetSecretValueInput,
	...func(*secretsmanager.Options),
) (*secretsmanager.GetSecretValueOutput, error) {
	return f.output, f.err
}

func TestValidateExistingSeedWalletSecret(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{
		MPCSecretARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:vault/wallet/test/share-b",
	}
	manager := &fakeSeedSecretsManager{
		output: &secretsmanager.GetSecretValueOutput{SecretBinary: []byte("share-b")},
	}

	if err := validateExistingSeedWalletSecret(context.Background(), manager, wallet); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateExistingSeedWalletSecretRejectsMissingSecret(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{
		MPCSecretARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:missing",
	}
	manager := &fakeSeedSecretsManager{
		err: &types.ResourceNotFoundException{Message: stringPointer("missing")},
	}

	err := validateExistingSeedWalletSecret(context.Background(), manager, wallet)
	if err == nil {
		t.Fatal("expected missing share_B error")
	}
	if !strings.Contains(err.Error(), "share_B") || !strings.Contains(err.Error(), "reset PostgreSQL and Secrets Manager together") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateExistingSeedWalletSecretRejectsEmptyPayload(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{MPCSecretARN: "arn:empty"}
	manager := &fakeSeedSecretsManager{
		output: &secretsmanager.GetSecretValueOutput{},
	}

	err := validateExistingSeedWalletSecret(context.Background(), manager, wallet)
	if err == nil || !strings.Contains(err.Error(), "empty share_B") {
		t.Fatalf("expected empty share_B error, got %v", err)
	}
}

func TestValidateExistingSeedWalletSecretRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	if err := validateExistingSeedWalletSecret(context.Background(), nil, &models.Wallet{}); err == nil {
		t.Fatal("expected nil manager error")
	}
	manager := &fakeSeedSecretsManager{err: errors.New("unexpected")}
	if err := validateExistingSeedWalletSecret(context.Background(), manager, nil); err == nil {
		t.Fatal("expected nil wallet error")
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestWalletsSeedDoesNotQueryOutsideTheRepository(t *testing.T) {
	source, err := os.ReadFile("wallets.go")
	if err != nil {
		t.Fatalf("read wallets seed: %v", err)
	}
	text := string(source)
	for _, needle := range []string{"facades.Orm", "Orm()", ".Query()", ".Exec(", ".Raw("} {
		if strings.Contains(text, needle) {
			t.Fatalf("wallets seed still queries outside the repository (%s)", needle)
		}
	}
}

func TestSeedWalletRowsAndMembershipsStayOnRerun(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	specs := seedWalletSpecs()
	if len(specs) != 8 {
		t.Fatalf("seed wallet catalog has %d rows, want 8", len(specs))
	}

	wallets := repositories.NewWalletRepository(nil)
	addresses := repositories.NewAddressRepository(nil)
	for _, spec := range specs {
		material := seedMaterial{
			shareACipher: []byte{0x11},
			shareAIV:     []byte{0x22},
			shareASalt:   []byte{0x33},
			pubKey:       []byte{0x44},
			chainCode:    []byte{0x55},
			address:      "seed-deposit-" + spec.id.String(),
		}
		if err := insertSeedWallet(ctx, spec, material, "arn:seed-wallet-test"); err != nil {
			t.Fatalf("insert %s: %v", spec.label, err)
		}
		stored, err := wallets.FindByID(ctx, spec.id)
		if err != nil {
			t.Fatalf("find %s: %v", spec.label, err)
		}
		if stored.ID != spec.id || stored.Chain != spec.chain || stored.Label != spec.label || stored.Status != "active" || stored.RequiredApprovals != 1 {
			t.Fatalf("stored %s does not match the seed catalog", spec.label)
		}
		if stored.AccountID == nil || *stored.AccountID != spec.accountID {
			t.Fatalf("stored %s account does not match the seed catalog", spec.label)
		}
		if stored.MPCCurve != string(spec.curve) || stored.MPCSecretARN == "" || stored.MPCCustomerShare != hex.EncodeToString(material.shareACipher) {
			t.Fatalf("stored %s key material does not match the seed catalog", spec.label)
		}
		if stored.DepositAddressID == nil || *stored.DepositAddressID != spec.addressID {
			t.Fatalf("stored %s deposit address was not linked", spec.label)
		}
		addr, err := addresses.FindByID(ctx, spec.addressID)
		if err != nil {
			t.Fatalf("find deposit address for %s: %v", spec.label, err)
		}
		if addr.Address != material.address || addr.WalletID != spec.id || addr.Chain != spec.chain || addr.DerivationIndex != 0 || addr.ExternalUserID != "system" || !addr.IsActive || addr.Label != "Deposit Address" || addr.DerivationType != "genesis" {
			t.Fatalf("deposit address for %s does not match the seed catalog", spec.label)
		}
	}

	if err := wallets.SetLabel(ctx, ethWalletID, "renamed"); err != nil {
		t.Fatalf("rename eth wallet: %v", err)
	}
	existing, err := wallets.FindByID(ctx, ethWalletID)
	if err != nil || existing == nil || existing.ID == uuid.Nil {
		t.Fatalf("rerun would not see the existing eth wallet: %v", err)
	}
	if existing.Label != "renamed" {
		t.Fatal("lookup rewrote the wallet label")
	}

	if err := seedWalletUsers(ctx); err != nil {
		t.Fatalf("seed wallet users: %v", err)
	}
	members := repositories.NewWalletUserRepository(nil)
	aliceMembership := uuid.MustParse("00000000-0000-0000-0000-000000000040")
	alice, err := members.FindByID(ctx, aliceMembership)
	if err != nil {
		t.Fatalf("find alice membership: %v", err)
	}
	if alice.WalletID != ethWalletID || alice.UserID != aliceUserID || alice.Roles != "viewer,spender" || alice.Status != "active" {
		t.Fatal("alice membership does not match the seed catalog")
	}
	if err := members.SetRoles(ctx, aliceMembership, "viewer"); err != nil {
		t.Fatalf("drift alice roles: %v", err)
	}
	if err := seedWalletUsers(ctx); err != nil {
		t.Fatalf("reseed wallet users: %v", err)
	}
	alice, err = members.FindByID(ctx, aliceMembership)
	if err != nil {
		t.Fatalf("find alice membership after reseed: %v", err)
	}
	if alice.Roles != "viewer" || alice.Status != "active" {
		t.Fatal("reseed refreshed an existing membership")
	}
	ethMembers, err := members.FindByWalletID(ctx, ethWalletID)
	if err != nil {
		t.Fatalf("list eth memberships: %v", err)
	}
	if len(ethMembers) != 2 {
		t.Fatalf("eth wallet has %d memberships, want 2", len(ethMembers))
	}
}
