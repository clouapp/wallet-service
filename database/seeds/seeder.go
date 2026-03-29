// Package seeds provides deterministic seed data for local development and testing.
// Invoked by Goravel artisan: `go run . artisan db:seed` (see database/seeders).
package seeds

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"golang.org/x/crypto/bcrypt"

	"github.com/macrowallets/waas/app/models"
)

const placeholderRPCURL = "https://placeholder.invalid"

// predefined UUIDs so the seed is idempotent (same IDs every run)
var (
	adminUserID       = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	aliceUserID       = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	bobUserID         = uuid.MustParse("00000000-0000-0000-0000-000000000003")
	acmeAccountID     = uuid.MustParse("00000000-0000-0000-0000-000000000010")
	acmeTestAccountID = uuid.MustParse("00000000-0000-0000-0000-000000000011")
	ethWalletID       = uuid.MustParse("00000000-0000-0000-0000-000000000020")
	btcWalletID       = uuid.MustParse("00000000-0000-0000-0000-000000000021")
	polyWalletID      = uuid.MustParse("00000000-0000-0000-0000-000000000022")
	tethWalletID      = uuid.MustParse("00000000-0000-0000-0000-000000000023")
	tbtcWalletID      = uuid.MustParse("00000000-0000-0000-0000-000000000024")
	tpolyWalletID     = uuid.MustParse("00000000-0000-0000-0000-000000000025")
)

// Run inserts seed data. It is idempotent: existing rows (matched by primary key)
// are skipped so the command is safe to re-run.
func Run(ctx context.Context) error {
	slog.Info("seeding database…")

	if err := seedChains(ctx); err != nil {
		return fmt.Errorf("seed chains: %w", err)
	}
	if err := seedTokens(ctx); err != nil {
		return fmt.Errorf("seed tokens: %w", err)
	}
	if err := seedChainResources(ctx); err != nil {
		return fmt.Errorf("seed chain resources: %w", err)
	}
	if err := seedPairedAccounts(ctx); err != nil {
		return fmt.Errorf("seed accounts: %w", err)
	}
	if err := seedUsers(ctx); err != nil {
		return fmt.Errorf("seed users: %w", err)
	}
	if err := seedAccountUsers(ctx); err != nil {
		return fmt.Errorf("seed account users: %w", err)
	}
	if err := seedWallets(ctx); err != nil {
		return fmt.Errorf("seed wallets: %w", err)
	}

	slog.Info("seed complete ✓")
	printCredentials()
	return nil
}

func encryptRPCFromEnv(envKey string) (string, error) {
	raw := os.Getenv(envKey)
	if raw == "" {
		raw = placeholderRPCURL
	}
	return facades.Crypt().EncryptString(raw)
}

// ---------------------------------------------------------------------------
// Chains
// ---------------------------------------------------------------------------

func seedChains(_ context.Context) error {
	const cmcBase = "https://s2.coinmarketcap.com/static/img/coins/64x64"

	type chainSeed struct {
		id                    string
		name                  string
		adapterType           string
		nativeSymbol          string
		nativeDecimals        int
		networkID             *int64
		envVar                string
		isTestnet             bool
		mainnetChainID        *string
		requiredConfirmations int
		displayOrder          int
		iconURL               string
	}

	// Mainnets first (FK targets), then testnets.
	mainnets := []chainSeed{
		{"eth", "Ethereum", models.AdapterTypeEVM, "eth", 18, i64p(1), "ETH_RPC_URL", false, nil, 12, 1, cmcBase + "/1027.png"},
		{"btc", "Bitcoin", models.AdapterTypeBitcoin, "btc", 8, nil, "BTC_RPC_URL", false, nil, 6, 3, cmcBase + "/1.png"},
		{"polygon", "Polygon", models.AdapterTypeEVM, "matic", 18, i64p(137), "POLYGON_RPC_URL", false, nil, 128, 5, cmcBase + "/3890.png"},
		{"sol", "Solana", models.AdapterTypeSolana, "sol", 9, nil, "SOLANA_RPC_URL", false, nil, 1, 7, cmcBase + "/5426.png"},
	}
	testnets := []chainSeed{
		{"teth", "Sepolia", models.AdapterTypeEVM, "eth", 18, i64p(11155111), "TETH_RPC_URL", true, strp("eth"), 12, 2, cmcBase + "/1027.png"},
		{"tbtc", "Bitcoin Testnet", models.AdapterTypeBitcoin, "btc", 8, nil, "TBTC_RPC_URL", true, strp("btc"), 6, 4, cmcBase + "/1.png"},
		{"tpolygon", "Polygon Amoy", models.AdapterTypeEVM, "matic", 18, i64p(80002), "TPOLYGON_RPC_URL", true, strp("polygon"), 128, 6, cmcBase + "/3890.png"},
		{"tsol", "Solana Devnet", models.AdapterTypeSolana, "sol", 9, nil, "TSOL_RPC_URL", true, strp("sol"), 1, 8, cmcBase + "/5426.png"},
	}

	for _, c := range append(mainnets, testnets...) {
		var existing models.Chain
		if err := facades.Orm().Query().Where("id", c.id).First(&existing); err == nil && existing.ID != "" {
			slog.Info("chain already exists, skipping", "id", c.id)
			continue
		}
		encRPC, err := encryptRPCFromEnv(c.envVar)
		if err != nil {
			return fmt.Errorf("encrypt RPC for chain %s: %w", c.id, err)
		}
		iconURL := c.iconURL
		ch := models.Chain{
			ID:                    c.id,
			Name:                  c.name,
			AdapterType:           c.adapterType,
			NativeSymbol:          c.nativeSymbol,
			NativeDecimals:        c.nativeDecimals,
			NetworkID:             c.networkID,
			RpcURL:                encRPC,
			IsTestnet:             c.isTestnet,
			MainnetChainID:        c.mainnetChainID,
			RequiredConfirmations: c.requiredConfirmations,
			IconURL:               &iconURL,
			DisplayOrder:          c.displayOrder,
			Status:                "active",
		}
		if err := facades.Orm().Query().Create(&ch); err != nil {
			return fmt.Errorf("create chain %s: %w", c.id, err)
		}
		slog.Info("created chain", "id", c.id)
	}
	return nil
}

func i64p(v int64) *int64   { return &v }
func strp(s string) *string { return &s }

// ---------------------------------------------------------------------------
// Tokens
// ---------------------------------------------------------------------------

func seedTokens(_ context.Context) error {
	const cmcBase = "https://s2.coinmarketcap.com/static/img/coins/64x64"

	type tokenSeed struct {
		chainID         string
		symbol          string
		name            string
		contractAddress string
		decimals        int
		iconURL         string
	}

	tokens := []tokenSeed{
		// Ethereum
		{"eth", "USDT", "Tether USD", "0xdAC17F958D2ee523a2206206994597C13D831ec7", 6, cmcBase + "/825.png"},
		{"eth", "USDC", "USD Coin", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", 6, cmcBase + "/3408.png"},
		{"eth", "WETH", "Wrapped Ether", "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", 18, cmcBase + "/2396.png"},
		{"eth", "WBTC", "Wrapped Bitcoin", "0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599", 8, cmcBase + "/3717.png"},
		{"eth", "DAI", "Dai Stablecoin", "0x6B175474E89094C44Da98b954EedeAC495271d0F", 18, cmcBase + "/4943.png"},
		{"eth", "LINK", "Chainlink", "0x514910771AF9Ca656af840dff83E8264EcF986CA", 18, cmcBase + "/1975.png"},
		{"eth", "UNI", "Uniswap", "0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984", 18, cmcBase + "/7083.png"},
		// Sepolia
		{"teth", "USDT", "Tether USD (Test)", "0x7169D38820dfd117C3FA1f22a697dBA58d90BA06", 6, cmcBase + "/825.png"},
		{"teth", "USDC", "USD Coin (Test)", "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238", 6, cmcBase + "/3408.png"},
		{"teth", "LINK", "Chainlink (Test)", "0x779877A7B0D9E8603169DdbD7836e478b4624789", 18, cmcBase + "/1975.png"},
		// Polygon
		{"polygon", "USDT", "Tether USD", "0xc2132D05D31c914a87C6611C10748AEb04B58e8F", 6, cmcBase + "/825.png"},
		{"polygon", "USDC", "USD Coin", "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359", 6, cmcBase + "/3408.png"},
		{"polygon", "WETH", "Wrapped Ether", "0x7ceB23fD6bC0adD59E62ac25578270cFf1b9f619", 18, cmcBase + "/2396.png"},
		{"polygon", "WBTC", "Wrapped Bitcoin", "0x1BFD67037B42Cf73acF2047067bd4F2C47D9BfD6", 8, cmcBase + "/3717.png"},
		{"polygon", "DAI", "Dai Stablecoin", "0x8f3Cf7ad23Cd3CaDbD9735AFf958023239c6A063", 18, cmcBase + "/4943.png"},
		{"polygon", "LINK", "Chainlink", "0x53E0bca35eC356BD5ddDFebbD1Fc0fD03FaBad39", 18, cmcBase + "/1975.png"},
		// Polygon Amoy
		{"tpolygon", "USDT", "Tether USD (Test)", "0xBDE550eCd4C18B3A3C522E1298DC6B1530710B13", 6, cmcBase + "/825.png"},
		{"tpolygon", "USDC", "USD Coin (Test)", "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582", 6, cmcBase + "/3408.png"},
		// Solana
		{"sol", "USDC", "USD Coin", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", 6, cmcBase + "/3408.png"},
		{"sol", "USDT", "Tether USD", "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", 6, cmcBase + "/825.png"},
		{"sol", "WSOL", "Wrapped SOL", "So11111111111111111111111111111111111111112", 9, cmcBase + "/5426.png"},
		{"sol", "BONK", "Bonk", "DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263", 5, cmcBase + "/23095.png"},
		{"sol", "JUP", "Jupiter", "JUPyiwrYJFskUPiHa7hkeR8VUtAeFoSYbKedZNsDvCN", 6, cmcBase + "/29210.png"},
		{"sol", "WIF", "dogwifhat", "EKpQGSJtjMFqKZ9KQanSqYXRcF8fBopzLHYxdM65zcjm", 6, cmcBase + "/28752.png"},
		// Solana Devnet
		{"tsol", "USDC", "USD Coin (Test)", "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU", 6, cmcBase + "/3408.png"},
	}

	for _, t := range tokens {
		var existing models.Token
		q := facades.Orm().Query().
			Where("chain_id", t.chainID).
			Where("contract_address", t.contractAddress)
		if err := q.First(&existing); err == nil && existing.ID != uuid.Nil {
			continue
		}
		iconURL := t.iconURL
		tok := models.Token{
			ID:              uuid.New(),
			ChainID:         t.chainID,
			Symbol:          t.symbol,
			Name:            t.name,
			ContractAddress: t.contractAddress,
			Decimals:        t.decimals,
			IconURL:         &iconURL,
			Status:          "active",
		}
		if err := facades.Orm().Query().Create(&tok); err != nil {
			return fmt.Errorf("create token %s %s: %w", t.chainID, t.symbol, err)
		}
		slog.Info("created token", "chain_id", t.chainID, "symbol", t.symbol)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Chain resources
// ---------------------------------------------------------------------------

func seedChainResources(_ context.Context) error {
	type resSeed struct {
		chainID string
		type_   string
		name    string
		url     string
	}

	resources := []resSeed{
		{"eth", "explorer", "Etherscan", "https://etherscan.io"},
		{"teth", "explorer", "Sepolia Etherscan", "https://sepolia.etherscan.io"},
		{"teth", "faucet", "Sepolia Faucet", "https://sepoliafaucet.com"},
		{"btc", "explorer", "Blockstream", "https://blockstream.info"},
		{"tbtc", "explorer", "Blockstream Testnet", "https://blockstream.info/testnet"},
		{"tbtc", "faucet", "Bitcoin Testnet Faucet", "https://coinfaucet.eu/en/btc-testnet"},
		{"polygon", "explorer", "Polygonscan", "https://polygonscan.com"},
		{"tpolygon", "explorer", "Amoy Polygonscan", "https://amoy.polygonscan.com"},
		{"tpolygon", "faucet", "Polygon Amoy Faucet", "https://faucet.polygon.technology"},
		{"sol", "explorer", "Solana Explorer", "https://explorer.solana.com"},
		{"tsol", "explorer", "Solana Explorer (Devnet)", "https://explorer.solana.com/?cluster=devnet"},
		{"tsol", "faucet", "Solana Devnet Faucet", "https://faucet.solana.com"},
	}

	for _, r := range resources {
		var existing models.ChainResource
		err := facades.Orm().Query().
			Where("chain_id", r.chainID).
			Where("type", r.type_).
			Where("name", r.name).
			First(&existing)
		if err == nil && existing.ID != uuid.Nil {
			continue
		}
		cr := models.ChainResource{
			ID:      uuid.New(),
			ChainID: r.chainID,
			Type:    r.type_,
			Name:    r.name,
			URL:     r.url,
			Status:  "active",
		}
		if err := facades.Orm().Query().Create(&cr); err != nil {
			return fmt.Errorf("create chain resource %s %s: %w", r.chainID, r.name, err)
		}
		slog.Info("created chain resource", "chain_id", r.chainID, "name", r.name)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Paired accounts (prod + test)
// ---------------------------------------------------------------------------

func seedPairedAccounts(_ context.Context) error {
	var prodExisting models.Account
	prodExists := facades.Orm().Query().Where("id", acmeAccountID).First(&prodExisting) == nil && prodExisting.ID != uuid.Nil

	if !prodExists {
		prod := models.Account{
			ID:              acmeAccountID,
			Name:            "Acme Corp",
			Status:          "active",
			ViewAllWallets:  true,
			Environment:     models.EnvironmentProd,
			LinkedAccountID: nil,
		}
		if err := facades.Orm().Query().Create(&prod); err != nil {
			return fmt.Errorf("create prod account: %w", err)
		}
		slog.Info("created prod account", "name", prod.Name)
	} else {
		slog.Info("prod account already exists, skipping create", "id", acmeAccountID)
	}

	var testExisting models.Account
	testExists := facades.Orm().Query().Where("id", acmeTestAccountID).First(&testExisting) == nil && testExisting.ID != uuid.Nil

	if !testExists {
		testLinked := acmeAccountID
		test := models.Account{
			ID:              acmeTestAccountID,
			Name:            "Acme Corp (Test)",
			Status:          "active",
			ViewAllWallets:  true,
			Environment:     models.EnvironmentTest,
			LinkedAccountID: &testLinked,
		}
		if err := facades.Orm().Query().Create(&test); err != nil {
			return fmt.Errorf("create test account: %w", err)
		}
		slog.Info("created test account", "name", test.Name)
	} else {
		slog.Info("test account already exists, skipping create", "id", acmeTestAccountID)
	}

	// Ensure cross-links and environments (idempotent for upgraded DBs).
	if _, err := facades.Orm().Query().Model(&models.Account{}).Where("id = ?", acmeAccountID).Update(map[string]any{
		"environment":       models.EnvironmentProd,
		"linked_account_id": acmeTestAccountID,
	}); err != nil {
		return fmt.Errorf("link prod account: %w", err)
	}
	if _, err := facades.Orm().Query().Model(&models.Account{}).Where("id = ?", acmeTestAccountID).Update(map[string]any{
		"environment":       models.EnvironmentTest,
		"linked_account_id": acmeAccountID,
	}); err != nil {
		return fmt.Errorf("link test account: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

func seedUsers(_ context.Context) error {
	users := []struct {
		id       uuid.UUID
		email    string
		password string
		fullName string
	}{
		{adminUserID, "admin@macro.markets", "secret", "Admin User"},
		{aliceUserID, "alice@macro.markets", "secret", "Alice Smith"},
		{bobUserID, "bob@macro.markets", "secret", "Bob Jones"},
	}

	for _, u := range users {
		var existing models.User
		if err := facades.Orm().Query().Where("id", u.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			slog.Info("user already exists, ensuring default account", "email", u.email)
			if existing.DefaultAccountID == nil || *existing.DefaultAccountID != acmeAccountID {
				if _, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", u.id).Update("default_account_id", acmeAccountID); err != nil {
					return fmt.Errorf("update user default account %s: %w", u.email, err)
				}
			}
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(u.password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		defAcc := acmeAccountID
		user := models.User{
			ID:               u.id,
			Email:            u.email,
			PasswordHash:     string(hash),
			FullName:         u.fullName,
			Status:           "active",
			DefaultAccountID: &defAcc,
		}
		if err := facades.Orm().Query().Create(&user); err != nil {
			return fmt.Errorf("create user %s: %w", u.email, err)
		}
		slog.Info("created user", "email", u.email)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Account users (prod + test)
// ---------------------------------------------------------------------------

func seedAccountUsers(_ context.Context) error {
	members := []struct {
		id        uuid.UUID
		accountID uuid.UUID
		userID    uuid.UUID
		role      string
	}{
		{uuid.MustParse("00000000-0000-0000-0000-000000000030"), acmeAccountID, adminUserID, "owner"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000031"), acmeAccountID, aliceUserID, "admin"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000032"), acmeAccountID, bobUserID, "auditor"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000033"), acmeTestAccountID, adminUserID, "owner"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000034"), acmeTestAccountID, aliceUserID, "admin"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000035"), acmeTestAccountID, bobUserID, "auditor"},
	}
	for _, m := range members {
		var existing models.AccountUser
		if err := facades.Orm().Query().Where("id", m.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			continue
		}
		au := models.AccountUser{
			ID:        m.id,
			AccountID: m.accountID,
			UserID:    m.userID,
			Role:      m.role,
			Status:    "active",
		}
		if err := facades.Orm().Query().Create(&au); err != nil {
			return fmt.Errorf("create account_user %s: %w", m.role, err)
		}
		slog.Info("added user to account", "account_id", m.accountID, "role", m.role)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Wallets + wallet_users
// ---------------------------------------------------------------------------

// placeholder MPC values — realistic hex lengths, not real key material
const (
	fakePubKey    = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	fakeShareHex  = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	fakeIVHex     = "000102030405060708090a0b"
	fakeSaltHex   = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	fakeSecretARN = "arn:aws:secretsmanager:us-east-1:000000000000:secret:vault/wallet/seed/share-b"
)

func seedWallets(_ context.Context) error {
	wallets := []struct {
		id             uuid.UUID
		accountID      uuid.UUID
		chain          string
		label          string
		depositAddress string
		curve          string
	}{
		{ethWalletID, acmeAccountID, "eth", "Primary ETH Wallet", "0xDEADbeef00000000000000000000000000000001", "secp256k1"},
		{btcWalletID, acmeAccountID, "btc", "Primary BTC Wallet", "bc1qseed000000000000000000000000000000000001", "secp256k1"},
		{polyWalletID, acmeAccountID, "polygon", "Polygon Wallet", "0xDEADbeef00000000000000000000000000000002", "secp256k1"},
		{tethWalletID, acmeTestAccountID, "teth", "Sepolia ETH Wallet", "0xDEADbeef00000000000000000000000000000003", "secp256k1"},
		{tbtcWalletID, acmeTestAccountID, "tbtc", "Bitcoin Testnet Wallet", "bc1qseed000000000000000000000000000000000002", "secp256k1"},
		{tpolyWalletID, acmeTestAccountID, "tpolygon", "Polygon Amoy Wallet", "0xDEADbeef00000000000000000000000000000004", "secp256k1"},
	}

	for _, w := range wallets {
		var existing models.Wallet
		if err := facades.Orm().Query().Where("id", w.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			slog.Info("wallet already exists, skipping", "label", w.label)
			continue
		}
		aid := w.accountID
		wallet := models.Wallet{
			ID:                w.id,
			Chain:             w.chain,
			Label:             w.label,
			MPCCustomerShare:  fakeShareHex,
			MPCShareIV:        fakeIVHex,
			MPCShareSalt:      fakeSaltHex,
			MPCSecretARN:      fakeSecretARN,
			MPCPublicKey:      fakePubKey,
			MPCCurve:          w.curve,
			DepositAddress:    w.depositAddress,
			AccountID:         &aid,
			Status:            "active",
			RequiredApprovals: 1,
		}
		if err := facades.Orm().Query().Create(&wallet); err != nil {
			return fmt.Errorf("create wallet %s: %w", w.label, err)
		}
		slog.Info("created wallet", "label", w.label, "chain", w.chain)
	}

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
		{uuid.MustParse("00000000-0000-0000-0000-000000000050"), tethWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000051"), tethWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000052"), tbtcWalletID, aliceUserID, "viewer,spender"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000053"), tbtcWalletID, bobUserID, "viewer"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000054"), tpolyWalletID, aliceUserID, "viewer"},
	}
	for _, wu := range walletUserSeeds {
		var existing models.WalletUser
		if err := facades.Orm().Query().Where("id", wu.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			continue
		}
		walletUser := models.WalletUser{
			ID:       wu.id,
			WalletID: wu.walletID,
			UserID:   wu.userID,
			Roles:    wu.roles,
			Status:   "active",
		}
		if err := facades.Orm().Query().Create(&walletUser); err != nil {
			return fmt.Errorf("create wallet_user: %w", err)
		}
	}
	slog.Info("seeded wallet users")
	return nil
}

// ---------------------------------------------------------------------------
// Webhook Subscriptions (created via provider APIs, not seeded)
//
// Expected configuration after provider setup:
// | chain_id | provider  | network              |
// |----------|-----------|----------------------|
// | eth      | alchemy   | ETH_MAINNET          |
// | polygon  | alchemy   | MATIC_MAINNET        |
// | teth     | alchemy   | ETH_SEPOLIA          |
// | tpolygon | alchemy   | MATIC_AMOY           |
// | sol      | helius    | enhanced             |
// | tsol     | helius    | enhancedDevnet       |
// | btc      | quicknode | bitcoin-mainnet      |
// | tbtc     | quicknode | bitcoin-testnet      |
// ---------------------------------------------------------------------------

func printCredentials() {
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  Seed data created — login credentials")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  admin@macro.markets / secret  (account owner)")
	fmt.Println("  alice@macro.markets / secret  (account admin)")
	fmt.Println("  bob@macro.markets   / secret  (auditor)")
	fmt.Println()
	fmt.Println("  Accounts: Acme Corp (prod) · Acme Corp (Test) — paired, default = prod")
	fmt.Println("  Wallets: ETH · BTC · Polygon (prod) · Sepolia · Bitcoin testnet · Amoy (test)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
}
