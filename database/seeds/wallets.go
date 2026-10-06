package seeds

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/addressing"
	mpc "github.com/macrowallets/waas/app/services/mpc"
)

// SeedPassphrase is the fixed passphrase protecting every seed wallet's share_A.
// The value stays in this package for the seed ceremony and is never written to
// a log or the post-seed banner. Kept long enough to satisfy the >=12-char
// service policy.
const SeedPassphrase = "macro-seed-pass-2026"

// seedWalletSpec describes one demo wallet. curve is derived from chain but kept
// explicit for readability.
type seedWalletSpec struct {
	id        uuid.UUID
	addressID uuid.UUID
	accountID uuid.UUID
	chain     string
	label     string
	curve     mpc.Curve
}

type seedEndpointResolver struct{ url string }

type seedSecretsManagerAPI interface {
	GetSecretValue(
		context.Context,
		*secretsmanager.GetSecretValueInput,
		...func(*secretsmanager.Options),
	) (*secretsmanager.GetSecretValueOutput, error)
}

func (r seedEndpointResolver) ResolveEndpoint(
	_ context.Context,
	_ secretsmanager.EndpointParameters,
) (smithyendpoints.Endpoint, error) {
	u, err := url.Parse(r.url)
	if err != nil {
		return smithyendpoints.Endpoint{}, err
	}
	return smithyendpoints.Endpoint{URI: *u}, nil
}

// SeedWallets inserts fully-functional MPC wallets (real keygen, real Secrets
// Manager entry, real derived deposit address) plus wallet_users memberships.
// The shared seed passphrase is SeedPassphrase. Idempotent: existing rows are
// skipped so the command is safe to re-run.
func SeedWallets(ctx context.Context) error {
	specs := seedWalletSpecs()

	var smClient *secretsmanager.Client
	loadSecretsManager := func() (*secretsmanager.Client, error) {
		if smClient != nil {
			return smClient, nil
		}
		client, err := buildSeedSecretsManager(ctx)
		if err != nil {
			return nil, err
		}
		smClient = client
		return smClient, nil
	}

	wallets := repositories.NewWalletRepository(nil)
	pending := make([]seedWalletSpec, 0, len(specs))
	for _, spec := range specs {
		existing, err := wallets.FindByID(ctx, spec.id)
		if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
			return fmt.Errorf("seed wallet %s: %w", spec.label, err)
		}
		if existing != nil && existing.ID != uuid.Nil {
			manager, managerErr := loadSecretsManager()
			if managerErr != nil {
				return fmt.Errorf("seed wallet %s: build secrets manager: %w", spec.label, managerErr)
			}
			if secretErr := validateExistingSeedWalletSecret(ctx, manager, existing); secretErr != nil {
				return fmt.Errorf("seed wallet %s: %w", spec.label, secretErr)
			}
			slog.Info("wallet already exists, skipping", "label", spec.label)
			continue
		}
		pending = append(pending, spec)
	}

	if len(pending) > 0 {
		smClient, err := loadSecretsManager()
		if err != nil {
			return fmt.Errorf("seed wallets: build secrets manager: %w", err)
		}
		mpcSvc := mpc.NewTSSService()

		materials, err := generateSeedMaterials(ctx, mpcSvc, pending)
		if err != nil {
			return fmt.Errorf("seed wallets: mpc keygen: %w", err)
		}

		for i, spec := range pending {
			if err := persistSeedWallet(ctx, smClient, spec, materials[i]); err != nil {
				return fmt.Errorf("seed wallet %s: %w", spec.label, err)
			}
			slog.Info("created wallet", "label", spec.label, "chain", spec.chain, "address", materials[i].address)
		}
	}

	return seedWalletUsers(ctx)
}

func validateExistingSeedWalletSecret(
	ctx context.Context,
	manager seedSecretsManagerAPI,
	wallet *models.Wallet,
) error {
	if manager == nil {
		return fmt.Errorf("secrets manager is required")
	}
	if wallet == nil {
		return fmt.Errorf("wallet is required")
	}
	if wallet.MPCSecretARN == "" {
		return fmt.Errorf(
			"wallet %s has no share_B ARN; reset PostgreSQL and Secrets Manager together",
			wallet.ID,
		)
	}

	arn := wallet.MPCSecretARN
	output, err := manager.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &arn,
	})
	if err != nil {
		return fmt.Errorf(
			"wallet %s share_B is unavailable: %w; reset PostgreSQL and Secrets Manager together",
			wallet.ID,
			err,
		)
	}
	if output == nil || len(output.SecretBinary) == 0 {
		return fmt.Errorf(
			"wallet %s has an empty share_B; reset PostgreSQL and Secrets Manager together",
			wallet.ID,
		)
	}
	return nil
}

type seedMaterial struct {
	shareACipher []byte
	shareAIV     []byte
	shareASalt   []byte
	shareB       []byte
	pubKey       []byte
	chainCode    []byte
	address      string
}

func generateSeedMaterials(ctx context.Context, mpcSvc mpc.Service, specs []seedWalletSpec) ([]seedMaterial, error) {
	type result struct {
		idx      int
		material seedMaterial
		err      error
	}

	results := make([]result, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func(i int, spec seedWalletSpec) {
			defer wg.Done()
			mat, err := generateOneMaterial(ctx, mpcSvc, spec)
			results[i] = result{idx: i, material: mat, err: err}
		}(i, spec)
	}
	wg.Wait()

	out := make([]seedMaterial, len(specs))
	for _, r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("%s: %w", specs[r.idx].label, r.err)
		}
		out[r.idx] = r.material
	}
	return out, nil
}

func generateOneMaterial(ctx context.Context, mpcSvc mpc.Service, spec seedWalletSpec) (seedMaterial, error) {
	start := time.Now()
	kg, err := mpcSvc.Keygen(ctx, spec.curve)
	if err != nil {
		return seedMaterial{}, fmt.Errorf("keygen: %w", err)
	}
	slog.Info("seed mpc keygen done", "label", spec.label, "elapsed", time.Since(start).String())

	enc, err := mpc.EncryptShare(kg.ShareA, SeedPassphrase)
	if err != nil {
		return seedMaterial{}, fmt.Errorf("encrypt share_A: %w", err)
	}

	testnet, err := seedChainIsTestnet(spec.chain)
	if err != nil {
		return seedMaterial{}, err
	}
	addr, err := addressing.DeriveAddressOnNetwork(spec.chain, testnet, kg.CombinedPubKey)
	if err != nil {
		return seedMaterial{}, fmt.Errorf("derive address: %w", err)
	}

	return seedMaterial{
		shareACipher: enc.Ciphertext,
		shareAIV:     enc.IV,
		shareASalt:   enc.Salt,
		shareB:       kg.ShareB,
		pubKey:       kg.CombinedPubKey,
		chainCode:    kg.ChainCode,
		address:      addr,
	}, nil
}

func persistSeedWallet(ctx context.Context, sm *secretsmanager.Client, spec seedWalletSpec, mat seedMaterial) error {
	secretName := fmt.Sprintf("vault/wallet/%s/share-b", spec.id.String())
	arn, err := putSeedShareB(ctx, sm, secretName, mat.shareB)
	if err != nil {
		return fmt.Errorf("store share_B: %w", err)
	}

	return insertSeedWallet(ctx, spec, mat, arn)
}

func seedWalletSpecs() []seedWalletSpec {
	return []seedWalletSpec{
		{ethWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a0"), acmeAccountID, models.ChainETH, "Primary ETH Wallet", mpc.CurveSecp256k1},
		{btcWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a1"), acmeAccountID, models.ChainBTC, "Primary BTC Wallet", mpc.CurveSecp256k1},
		{polyWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a2"), acmeAccountID, models.ChainPolygon, "Polygon Wallet", mpc.CurveSecp256k1},
		{solWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a6"), acmeAccountID, models.ChainSOL, "Primary SOL Wallet", mpc.CurveEd25519},
		{tethWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a3"), acmeTestAccountID, models.ChainTETH, "Sepolia ETH Wallet", mpc.CurveSecp256k1},
		{tbtcWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a4"), acmeTestAccountID, models.ChainTBTC, "Bitcoin Testnet Wallet", mpc.CurveSecp256k1},
		{tpolyWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a5"), acmeTestAccountID, models.ChainTPolygon, "Polygon Amoy Wallet", mpc.CurveSecp256k1},
		{tsolWalletID, uuid.MustParse("00000000-0000-0000-0000-0000000000a7"), acmeTestAccountID, models.ChainTSOL, "Solana Devnet Wallet", mpc.CurveEd25519},
	}
}

func insertSeedWallet(ctx context.Context, spec seedWalletSpec, mat seedMaterial, arn string) error {
	wallets := repositories.NewWalletRepository(nil)
	aid := spec.accountID
	w := models.Wallet{
		ID:                spec.id,
		Chain:             spec.chain,
		Label:             spec.label,
		MPCCustomerShare:  hex.EncodeToString(mat.shareACipher),
		MPCShareIV:        hex.EncodeToString(mat.shareAIV),
		MPCShareSalt:      hex.EncodeToString(mat.shareASalt),
		MPCSecretARN:      arn,
		MPCPublicKey:      hex.EncodeToString(mat.pubKey),
		MPCCurve:          string(spec.curve),
		MPCChainCode:      hex.EncodeToString(mat.chainCode),
		AccountID:         &aid,
		Status:            "active",
		RequiredApprovals: 1,
	}
	if err := wallets.Create(ctx, &w); err != nil {
		return fmt.Errorf("insert wallet: %w", err)
	}

	addr := models.Address{
		ID:              spec.addressID,
		WalletID:        spec.id,
		Chain:           spec.chain,
		Address:         mat.address,
		DerivationIndex: 0,
		ExternalUserID:  "system",
		IsActive:        true,
		Label:           "Deposit Address",
		DerivationType:  "genesis",
	}
	if err := repositories.NewAddressRepository(nil).Create(ctx, &addr); err != nil {
		return fmt.Errorf("insert deposit address: %w", err)
	}

	if err := wallets.SetDepositAddressID(ctx, spec.id, spec.addressID); err != nil {
		return fmt.Errorf("link deposit address: %w", err)
	}

	return nil
}

func putSeedShareB(ctx context.Context, sm *secretsmanager.Client, name string, payload []byte) (string, error) {
	out, err := sm.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(name),
		SecretBinary: payload,
	})
	if err == nil {
		return aws.ToString(out.ARN), nil
	}

	// Re-seed after a wiped DB may hit pre-existing LocalStack secrets. Reuse
	// the existing ARN instead of aborting — the payload will be overwritten
	// below to keep the ShareB in sync with the freshly generated key.
	var exists *smtypes.ResourceExistsException
	if !errors.As(err, &exists) {
		return "", err
	}

	upd, updErr := sm.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(name),
		SecretBinary: payload,
	})
	if updErr != nil {
		return "", fmt.Errorf("put secret value: %w", updErr)
	}
	return aws.ToString(upd.ARN), nil
}

func buildSeedSecretsManager(ctx context.Context) (*secretsmanager.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	endpoint := facades.Config().GetString("vault.aws.endpoint_url")
	if endpoint == "" {
		return secretsmanager.NewFromConfig(awsCfg), nil
	}
	return secretsmanager.NewFromConfig(awsCfg,
		secretsmanager.WithEndpointResolverV2(seedEndpointResolver{url: endpoint})), nil
}

func seedWalletUsers(ctx context.Context) error {
	walletUserSeeds := []struct {
		id       uuid.UUID
		walletID uuid.UUID
		userID   uuid.UUID
		roles    string
	}{
		{uuid.MustParse("00000000-0000-0000-0000-000000000040"), ethWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000041"), ethWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000042"), btcWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000043"), btcWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000044"), polyWalletID, aliceUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000045"), solWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000050"), tethWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000051"), tethWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000052"), tbtcWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000053"), tbtcWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000054"), tpolyWalletID, aliceUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000055"), tsolWalletID, aliceUserID, "viewer,spender"},
	}
	members := repositories.NewWalletUserRepository(nil)
	for _, wu := range walletUserSeeds {
		existing, err := members.FindByID(ctx, wu.id)
		if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
			return fmt.Errorf("find wallet user: %w", err)
		}
		if existing != nil && existing.ID != uuid.Nil {
			continue
		}
		row := models.WalletUser{
			ID:       wu.id,
			WalletID: wu.walletID,
			UserID:   wu.userID,
			Roles:    wu.roles,
			Status:   "active",
		}
		if err := members.Create(ctx, &row); err != nil {
			return fmt.Errorf("create wallet_user: %w", err)
		}
	}
	slog.Info("seeded wallet users")
	return nil
}
