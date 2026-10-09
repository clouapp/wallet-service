package seeders

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func seedSecretsClient(t *testing.T, respond func(http.ResponseWriter)) *secretsmanager.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Amz-Target") != "secretsmanager.GetSecretValue" {
			t.Errorf("request %s target %s", r.Method, r.Header.Get("X-Amz-Target"))
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		_, _ = io.Copy(io.Discard, r.Body)
		respond(w)
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

func TestValidate_Existing_SeedWalletSecret(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{
		MPCSecretARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:vault/wallet/test/share-b",
	}
	manager := seedSecretsClient(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"SecretBinary": base64.StdEncoding.EncodeToString([]byte("share-b")),
		})
	})

	if err := validateExistingSeedWalletSecret(context.Background(), manager, wallet); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_Existing_SeedWalletSecretRejectsMissingSecret(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{
		MPCSecretARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:missing",
	}
	manager := seedSecretsClient(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.Header().Set("X-Amzn-ErrorType", "ResourceNotFoundException")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"__type": "ResourceNotFoundException", "message": "missing"})
	})

	err := validateExistingSeedWalletSecret(context.Background(), manager, wallet)
	if err == nil {
		t.Fatal("expected missing share_B error")
	}
	if !strings.Contains(err.Error(), "share_B") || !strings.Contains(err.Error(), "reset PostgreSQL and Secrets Manager together") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_Existing_SeedWalletSecretRejectsEmptyPayload(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{MPCSecretARN: "arn:empty"}
	manager := seedSecretsClient(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = w.Write([]byte("{}"))
	})

	err := validateExistingSeedWalletSecret(context.Background(), manager, wallet)
	if err == nil || !strings.Contains(err.Error(), "empty share_B") {
		t.Fatalf("expected empty share_B error, got %v", err)
	}
}

func TestValidate_Existing_SeedWalletSecretRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	if err := validateExistingSeedWalletSecret(context.Background(), nil, &models.Wallet{}); err == nil {
		t.Fatal("expected nil manager error")
	}
	manager := seedSecretsClient(t, func(http.ResponseWriter) {
		t.Fatal("a nil wallet must not call Secrets Manager")
	})
	if err := validateExistingSeedWalletSecret(context.Background(), manager, nil); err == nil {
		t.Fatal("expected nil wallet error")
	}
}

func TestWallets_Seed_DoesNotQueryOutsideTheRepository(t *testing.T) {
	source, err := os.ReadFile("wallet_seeder.go")
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

func TestSeed_Wallet_RowsAndMembershipsStayOnRerun(t *testing.T) {
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
