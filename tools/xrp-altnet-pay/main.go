// Command xrp-altnet-pay broadcasts one 1 XRP payment on the XRPL altnet from
// the experiment wallet, then records the faucet deposit and that withdrawal.
// It refuses every database except vault_pr95_xrp_addr and never prints key
// material. A second run does not broadcast again once the first submit is
// accepted.
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/macrowallets/waas/app/adapters/chain/xrp"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/hdkey"
	"github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/e2evault"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	databaseName = "vault_pr95_xrp_addr"
	walletIDText = "8f73e1ad-9c49-40d8-88af-fd4d476c8e4d"
	altnetRPC    = "https://s.altnet.rippletest.net:51234"
	payDrops     = 1_000_000
	claimPath    = "/home/raphaelcangucu/.local/state/macro-e2e/xrp-altnet-payment.json"
	faucetHash   = "CDBA7BD16D6450E9A310A4D66B2FB8D36CB5CC138D3DAC4E7A4E33655275ECC6"
	faucetFrom   = "rJjHYTCPpNA3qAM8ZpCDtip3a8xg7B8PFo"
	depositAddr  = "r3SG1Kf97vpZAmBV8bsKjukHVwhHs1wwAk"
)

type claim struct {
	WalletID    string `json:"wallet_id"`
	From        string `json:"from"`
	To          string `json:"to"`
	AmountDrops string `json:"amount_drops"`
	FeeDrops    string `json:"fee_drops"`
	Hash        string `json:"hash"`
	Accepted    bool   `json:"accepted"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "xrp-altnet-pay: %s\n", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	endpoint := strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL"))
	if err := requireLocalEndpoint(endpoint); err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, "postgres://vault:vault@127.0.0.1:5433/"+databaseName+"?sslmode=disable")
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer conn.Close(ctx)
	var current string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&current); err != nil {
		return err
	}
	if current != databaseName {
		return fmt.Errorf("connected to %s", current)
	}

	walletUUID := uuid.MustParse(walletIDText)
	child, err := childAddress(ctx, conn, walletUUID)
	if err != nil {
		return err
	}
	saved, err := readClaim()
	if err != nil {
		return err
	}
	if saved.Accepted && saved.Hash != "" {
		fmt.Printf("already accepted %s from %s to %s\n", saved.Hash, saved.From, saved.To)
		return record(ctx, conn, saved, child)
	}

	adapter := xrp.NewLive(xrp.Config{
		ChainIDStr: models.ChainTXRP, ChainName: "XRP Ledger Testnet", NativeSymbol: models.NativeXRP,
		RPCURL: altnetRPC, IsTestnet: true, Confirmations: 1,
	})
	balance, err := adapter.GetBalance(ctx, depositAddr)
	if err != nil {
		return err
	}
	fmt.Printf("before %s drops on %s sequence-check next\n", balance.Amount, depositAddr)

	payment, err := broadcastOnce(ctx, conn, adapter, walletUUID, child, endpoint)
	if err != nil {
		return err
	}
	fmt.Printf("withdrawal %s\nfrom %s\nto %s\namount %s drops fee %s drops\n", payment.Hash, payment.From, payment.To, payment.AmountDrops, payment.FeeDrops)
	deadline := time.Now().Add(90 * time.Second)
	var ledger uint64
	for {
		ledger, err = adapter.GetTransactionBlock(ctx, payment.Hash)
		if err != nil {
			return err
		}
		if ledger > 0 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("payment %s was accepted but is not validated yet", payment.Hash)
		}
		time.Sleep(3 * time.Second)
	}
	fmt.Printf("validated ledger %d\n", ledger)
	after, err := adapter.GetBalance(ctx, depositAddr)
	if err != nil {
		return err
	}
	dest, err := adapter.GetBalance(ctx, payment.To)
	if err != nil {
		return err
	}
	fmt.Printf("after source %s drops destination %s drops\n", after.Amount, dest.Amount)
	return record(ctx, conn, payment, child)
}

func broadcastOnce(ctx context.Context, conn *pgx.Conn, adapter *xrp.Live, walletID uuid.UUID, child, endpoint string) (claim, error) {
	if existing, err := existingOutgoing(ctx); err != nil {
		return claim{}, err
	} else if existing.Hash != "" {
		fmt.Printf("outgoing payment already on the altnet: %s\n", existing.Hash)
		if err := writeClaim(existing); err != nil {
			return claim{}, err
		}
		return existing, nil
	}
	key, err := walletKey(ctx, conn, walletID, endpoint)
	if err != nil {
		return claim{}, err
	}
	defer zero(key)
	unsigned, err := adapter.BuildTransfer(ctx, types.TransferRequest{
		From: depositAddr, To: child, Amount: big.NewInt(payDrops), Asset: models.NativeXRP,
	})
	if err != nil {
		return claim{}, err
	}
	signed, err := adapter.SignTransaction(ctx, unsigned, key)
	if err != nil {
		return claim{}, err
	}
	if err := adapter.VerifySignedTransaction(unsigned, signed, depositAddr); err != nil {
		return claim{}, err
	}
	hash, err := adapter.BroadcastTransaction(ctx, signed)
	if err != nil {
		return claim{}, err
	}
	saved := claim{
		WalletID: walletIDText, From: depositAddr, To: child, AmountDrops: fmt.Sprintf("%d", payDrops),
		FeeDrops: fmt.Sprint(unsigned.Metadata["fee_drops"]), Hash: hash, Accepted: true,
	}
	if err := writeClaim(saved); err != nil {
		return claim{}, fmt.Errorf("payment %s was accepted but the claim file was not written: %w", hash, err)
	}
	return saved, nil
}

func walletKey(ctx context.Context, conn *pgx.Conn, walletID uuid.UUID, endpoint string) ([]byte, error) {
	var row models.Wallet
	var arn string
	err := conn.QueryRow(ctx, `
		SELECT mpc_customer_share, mpc_share_iv, mpc_share_salt, mpc_secret_arn, mpc_public_key
		FROM wallets WHERE id = $1 AND chain = 'txrp'`, walletID).Scan(
		&row.MPCCustomerShare, &row.MPCShareIV, &row.MPCShareSalt, &arn, &row.MPCPublicKey,
	)
	if err != nil {
		return nil, fmt.Errorf("wallet: %w", err)
	}
	vault, err := e2evault.DefaultVaultPassphrase()
	if err != nil {
		return nil, err
	}
	passphrase, err := vault.Read(ctx, walletID)
	if err != nil {
		return nil, err
	}
	shareA, err := row.DecryptShareA(passphrase)
	if err != nil {
		return nil, fmt.Errorf("share A could not be opened")
	}
	defer zero(shareA)
	shareB, err := shareBFromLocal(ctx, endpoint, arn)
	if err != nil {
		return nil, err
	}
	defer zero(shareB)
	key, err := mpc.NewTSSService().ReconstructSecp256k1PrivateKey(shareA, shareB)
	if err != nil {
		return nil, fmt.Errorf("key could not be reconstructed")
	}
	pub, err := hdkey.PublicKeyOf(key)
	if err != nil {
		zero(key)
		return nil, err
	}
	want, err := hex.DecodeString(row.MPCPublicKey)
	if err != nil || subtle.ConstantTimeCompare(pub, want) != 1 {
		zero(key)
		return nil, fmt.Errorf("reconstructed key does not match the wallet public key")
	}
	derived, err := addressing.DeriveXRPAddress(pub)
	if err != nil || derived != depositAddr {
		zero(key)
		return nil, fmt.Errorf("reconstructed key does not own the deposit address")
	}
	return key, nil
}

func childAddress(ctx context.Context, conn *pgx.Conn, walletID uuid.UUID) (string, error) {
	var pubHex, chainHex string
	err := conn.QueryRow(ctx, `SELECT mpc_public_key, mpc_chain_code FROM wallets WHERE id = $1`, walletID).Scan(&pubHex, &chainHex)
	if err != nil {
		return "", err
	}
	pub, err := hex.DecodeString(pubHex)
	if err != nil {
		return "", fmt.Errorf("wallet public key")
	}
	chainCode, err := hex.DecodeString(chainHex)
	if err != nil {
		return "", fmt.Errorf("wallet chain code")
	}
	child, err := hdkey.DeriveSecp256k1Child(pub, chainCode, 0)
	if err != nil {
		return "", err
	}
	address, err := addressing.DeriveXRPAddress(child.PublicKey)
	if err != nil {
		return "", err
	}
	if address == depositAddr {
		return "", fmt.Errorf("child address matches the deposit address")
	}
	return address, nil
}

func shareBFromLocal(ctx context.Context, endpoint, arn string) ([]byte, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		return nil, err
	}
	client := secretsmanager.NewFromConfig(cfg, secretsmanager.WithEndpointResolverV2(localEndpoint{url: endpoint}))
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(arn)})
	if err != nil || out == nil || len(out.SecretBinary) == 0 {
		return nil, fmt.Errorf("share B is not in the local secrets manager")
	}
	return append([]byte(nil), out.SecretBinary...), nil
}

type localEndpoint struct{ url string }

func (e localEndpoint) ResolveEndpoint(context.Context, secretsmanager.EndpointParameters) (smithyendpoints.Endpoint, error) {
	parsed, err := url.Parse(e.url)
	if err != nil {
		return smithyendpoints.Endpoint{}, err
	}
	return smithyendpoints.Endpoint{URI: *parsed}, nil
}

func requireLocalEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("AWS_ENDPOINT_URL must be the local secrets manager")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "localhost" && host != "127.0.0.1" {
		return fmt.Errorf("AWS_ENDPOINT_URL is not local")
	}
	if strings.Contains(strings.ToLower(endpoint), "amazonaws.com") {
		return fmt.Errorf("AWS_ENDPOINT_URL is not local")
	}
	return nil
}

func readClaim() (claim, error) {
	raw, err := os.ReadFile(claimPath)
	if os.IsNotExist(err) {
		return claim{}, nil
	}
	if err != nil {
		return claim{}, err
	}
	var saved claim
	if err := json.Unmarshal(raw, &saved); err != nil {
		return claim{}, fmt.Errorf("claim file is not json")
	}
	return saved, nil
}

func writeClaim(saved claim) error {
	if err := os.MkdirAll("/home/raphaelcangucu/.local/state/macro-e2e", 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(claimPath, raw, 0o600)
}

func record(ctx context.Context, conn *pgx.Conn, payment claim, child string) error {
	adapter := xrp.NewLive(xrp.Config{
		ChainIDStr: models.ChainTXRP, NativeSymbol: models.NativeXRP, RPCURL: altnetRPC, IsTestnet: true,
	})
	source, err := adapter.GetBalance(ctx, payment.From)
	if err != nil {
		return err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var genesisID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM addresses WHERE wallet_id = $1 AND address = $2`, payment.WalletID, payment.From).Scan(&genesisID); err != nil {
		return fmt.Errorf("deposit address row: %w", err)
	}
	var childID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM addresses WHERE wallet_id = $1 AND address = $2`, payment.WalletID, child).Scan(&childID)
	if err != nil {
		childID = uuid.New()
		_, err = tx.Exec(ctx, `
			INSERT INTO addresses (id, wallet_id, chain, address, derivation_index, external_user_id, is_active, label, derivation_type, created_at, updated_at)
			VALUES ($1, $2, 'txrp', $3, 0, 'system', true, 'Second deposit address', 'bip32', now(), now())`,
			childID, payment.WalletID, child)
		if err != nil {
			return fmt.Errorf("child address: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE wallets SET address_index = GREATEST(address_index, 1), updated_at = now() WHERE id = $1`, payment.WalletID)
		if err != nil {
			return err
		}
	}
	if err := insertTx(ctx, tx, genesisID, payment.WalletID, "deposit", "inbound", "chain", faucetHash, faucetFrom, payment.From, "10000000", "12", 21330849); err != nil {
		return err
	}
	ledger, err := adapter.GetTransactionBlock(ctx, payment.Hash)
	if err != nil {
		return err
	}
	if err := insertTx(ctx, tx, genesisID, payment.WalletID, "withdrawal", "outbound", "withdrawal_flow", payment.Hash, payment.From, payment.To, payment.AmountDrops, payment.FeeDrops, int64(ledger)); err != nil {
		return err
	}
	display := amount.FormatBaseUnits(source.Amount, xrp.NativeDecimals)
	_, err = tx.Exec(ctx, `
		UPDATE wallets
		SET balance_asset = 'xrp', balance_raw = $2, balance_display = $3, balance_last_synced_at = now(), updated_at = now()
		WHERE id = $1`, payment.WalletID, source.Amount.String(), display)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE wallet_asset_balances
		SET amount_raw = $2, amount_display = $3, last_synced_at = now(), updated_at = now()
		WHERE wallet_id = $1 AND chain_id = 'txrp' AND asset_symbol = 'xrp'`, payment.WalletID, source.Amount.String(), display)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("recorded deposit %s and withdrawal %s balance %s XRP\n", faucetHash, payment.Hash, display)
	fmt.Printf("addresses %s %s\n", payment.From, child)
	return nil
}

func insertTx(ctx context.Context, tx pgx.Tx, addressID uuid.UUID, walletID, txType, direction, source, hash, from, to, drops, fee string, ledger int64) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO transactions (
			id, address_id, wallet_id, external_user_id, chain, tx_type, tx_hash,
			from_address, to_address, amount, asset, confirmations, required_confs, status,
			fee, block_number, direction, source, raw_payload, log_index, confirmed_at, created_at, updated_at
		)
		SELECT $1, $2, $3, 'system', 'txrp', $4::transaction_type, $5::text,
			$6, $7, $8, 'xrp', 1, 1, 'confirmed',
			$9, $10, $11::transaction_direction, $12::transaction_source, '{}', -1, now(), now(), now()
		WHERE NOT EXISTS (
			SELECT 1 FROM transactions WHERE chain = 'txrp' AND tx_hash = $5::text AND tx_type = $4::transaction_type
		)`,
		uuid.New(), addressID, walletID, txType, hash, from, to, drops, fee, ledger, direction, source)
	if err != nil {
		return fmt.Errorf("transaction %s: %w", txType, err)
	}
	_ = tag
	return nil
}

func existingOutgoing(ctx context.Context) (claim, error) {
	body, err := json.Marshal(map[string]any{
		"method": "account_tx",
		"params": []any{map[string]any{"account": depositAddr, "ledger_index_min": -1, "ledger_index_max": -1, "limit": 20}},
	})
	if err != nil {
		return claim{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, altnetRPC, bytes.NewReader(body))
	if err != nil {
		return claim{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return claim{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return claim{}, err
	}
	var parsed struct {
		Result struct {
			Transactions []struct {
				Validated bool `json:"validated"`
				Tx        struct {
					Hash            string `json:"hash"`
					TransactionType string `json:"TransactionType"`
					Account         string `json:"Account"`
					Destination     string `json:"Destination"`
					Amount          string `json:"Amount"`
					Fee             string `json:"Fee"`
				} `json:"tx"`
			} `json:"transactions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return claim{}, fmt.Errorf("account_tx is not json")
	}
	for _, item := range parsed.Result.Transactions {
		tx := item.Tx
		if item.Validated && tx.TransactionType == "Payment" && tx.Account == depositAddr && tx.Hash != "" {
			return claim{
				WalletID: walletIDText, From: tx.Account, To: tx.Destination, AmountDrops: tx.Amount,
				FeeDrops: tx.Fee, Hash: strings.ToUpper(tx.Hash), Accepted: true,
			}, nil
		}
	}
	return claim{}, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
