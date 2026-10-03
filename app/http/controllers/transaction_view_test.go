package controllers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

func TestTransactionViewsKeepTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	parent := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	key := "idem-1"
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	confirmed := time.Date(2024, 5, 6, 7, 9, 0, 0, time.UTC)
	synced := time.Date(2024, 5, 6, 7, 10, 0, 0, time.UTC)
	plain := models.Transaction{
		ID: id, AddressID: &other, WalletID: other, ExternalUserID: "user-1",
		Chain: "eth", TxType: models.TxTypeWithdrawal, TxHash: "0xabc", LogIndex: 2,
		FromAddress: "0xfrom", ToAddress: "0xto", Amount: "1000", Asset: "ETH",
		TokenContract: "0xtoken", Confirmations: 3, RequiredConfs: 12, Status: "confirmed",
		Fee: "10", BlockNumber: 99, BlockHash: "0xblock", ErrorMessage: "none",
		IdempotencyKey: &key, ConfirmedAt: &confirmed, Direction: models.TxDirectionOutbound,
		Source: models.TxSourceWithdrawalFlow, ParentTransactionID: &parent,
		Origin: models.TxOriginUserRequest, RawPayload: "raw-secret", SyncedAt: &synced,
	}
	plain.CreatedAt = created
	plain.UpdatedAt = updated

	stamped := models.Transaction{Direction: models.TxDirectionInbound, Source: models.TxSourceChain}
	stamped.CreatedAt = created

	withDecimals := plain
	withDecimals.Chain = "polygon"
	withDecimals.Asset = "USDC"
	withDecimals.TokenContract = amoyUSDCContract

	cases := []struct {
		name    string
		tx      models.Transaction
		account string
		wallet  string
	}{
		{
			name:    "zero",
			tx:      models.Transaction{},
			account: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","address_id":null,"wallet_id":"00000000-0000-0000-0000-000000000000","external_user_id":"","chain":"","tx_type":"","tx_hash":"","log_index":0,"from_address":"","to_address":"","amount":"","asset":"","token_contract":"","confirmations":0,"required_confs":0,"status":"","fee":"","block_number":0,"block_hash":"","error_message":"","confirmed_at":null,"type":"unknown","direction":"unknown"}`,
			wallet:  `{"id":"00000000-0000-0000-0000-000000000000","address_id":null,"wallet_id":"00000000-0000-0000-0000-000000000000","external_user_id":"","chain":"","tx_type":"","tx_hash":"","log_index":0,"from_address":"","to_address":"","amount":"","asset":"","token_contract":"","confirmations":0,"required_confs":0,"status":"","fee":"","block_number":0,"block_hash":"","error_message":"","confirmed_at":null,"created_at":null,"updated_at":null,"type":"unknown","direction":"unknown"}`,
		},
		{
			name:    "stamped",
			tx:      stamped,
			account: `{"created_at":"2024-05-06 07:08:09","updated_at":null,"id":"00000000-0000-0000-0000-000000000000","address_id":null,"wallet_id":"00000000-0000-0000-0000-000000000000","external_user_id":"","chain":"","tx_type":"","tx_hash":"","log_index":0,"from_address":"","to_address":"","amount":"","asset":"","token_contract":"","confirmations":0,"required_confs":0,"status":"","fee":"","block_number":0,"block_hash":"","error_message":"","confirmed_at":null,"source":"chain","type":"unknown","direction":"incoming","chain_direction":"inbound"}`,
			wallet:  `{"id":"00000000-0000-0000-0000-000000000000","address_id":null,"wallet_id":"00000000-0000-0000-0000-000000000000","external_user_id":"","chain":"","tx_type":"","tx_hash":"","log_index":0,"from_address":"","to_address":"","amount":"","asset":"","token_contract":"","confirmations":0,"required_confs":0,"status":"","fee":"","block_number":0,"block_hash":"","error_message":"","confirmed_at":null,"source":"chain","created_at":"2024-05-06T07:08:09Z","updated_at":null,"type":"unknown","direction":"incoming","chain_direction":"inbound"}`,
		},
		{
			name:    "plain",
			tx:      plain,
			account: `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","address_id":"22222222-2222-4222-8222-222222222222","wallet_id":"22222222-2222-4222-8222-222222222222","external_user_id":"user-1","chain":"eth","tx_type":"withdrawal","tx_hash":"0xabc","log_index":2,"from_address":"0xfrom","to_address":"0xto","amount":"1000","asset":"ETH","token_contract":"0xtoken","confirmations":3,"required_confs":12,"status":"confirmed","fee":"10","block_number":99,"block_hash":"0xblock","error_message":"none","idempotency_key":"idem-1","confirmed_at":"2024-05-06T07:09:00Z","source":"withdrawal_flow","parent_transaction_id":"33333333-3333-4333-8333-333333333333","origin":"user_request","synced_at":"2024-05-06T07:10:00Z","type":"withdrawal","direction":"outgoing","chain_direction":"outbound"}`,
			wallet:  `{"id":"11111111-1111-4111-8111-111111111111","address_id":"22222222-2222-4222-8222-222222222222","wallet_id":"22222222-2222-4222-8222-222222222222","external_user_id":"user-1","chain":"eth","tx_type":"withdrawal","tx_hash":"0xabc","log_index":2,"from_address":"0xfrom","to_address":"0xto","amount":"1000","asset":"ETH","token_contract":"0xtoken","confirmations":3,"required_confs":12,"status":"confirmed","fee":"10","block_number":99,"block_hash":"0xblock","error_message":"none","idempotency_key":"idem-1","confirmed_at":"2024-05-06T07:09:00Z","source":"withdrawal_flow","parent_transaction_id":"33333333-3333-4333-8333-333333333333","origin":"user_request","synced_at":"2024-05-06T07:10:00Z","created_at":"2024-05-06T07:08:09Z","updated_at":"2024-05-06T07:08:10Z","type":"withdrawal","direction":"outgoing","chain_direction":"outbound"}`,
		},
		{
			name:    "decimals",
			tx:      withDecimals,
			account: `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","address_id":"22222222-2222-4222-8222-222222222222","wallet_id":"22222222-2222-4222-8222-222222222222","external_user_id":"user-1","chain":"polygon","tx_type":"withdrawal","tx_hash":"0xabc","log_index":2,"from_address":"0xfrom","to_address":"0xto","amount":"1000","asset":"USDC","token_contract":"0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582","confirmations":3,"required_confs":12,"status":"confirmed","fee":"10","block_number":99,"block_hash":"0xblock","error_message":"none","idempotency_key":"idem-1","confirmed_at":"2024-05-06T07:09:00Z","source":"withdrawal_flow","parent_transaction_id":"33333333-3333-4333-8333-333333333333","origin":"user_request","synced_at":"2024-05-06T07:10:00Z","type":"withdrawal","direction":"outgoing","chain_direction":"outbound"}`,
			wallet:  `{"id":"11111111-1111-4111-8111-111111111111","address_id":"22222222-2222-4222-8222-222222222222","wallet_id":"22222222-2222-4222-8222-222222222222","external_user_id":"user-1","chain":"polygon","tx_type":"withdrawal","tx_hash":"0xabc","log_index":2,"from_address":"0xfrom","to_address":"0xto","amount":"1000","asset":"USDC","token_contract":"0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582","confirmations":3,"required_confs":12,"status":"confirmed","fee":"10","block_number":99,"block_hash":"0xblock","error_message":"none","idempotency_key":"idem-1","confirmed_at":"2024-05-06T07:09:00Z","source":"withdrawal_flow","parent_transaction_id":"33333333-3333-4333-8333-333333333333","origin":"user_request","synced_at":"2024-05-06T07:10:00Z","created_at":"2024-05-06T07:08:09Z","updated_at":"2024-05-06T07:08:10Z","decimals":6,"type":"withdrawal","direction":"outgoing","chain_direction":"outbound"}`,
		},
	}
	for _, tc := range cases {
		account, err := json.Marshal(newTransactionView(tc.tx))
		if err != nil {
			t.Fatal(err)
		}
		wallet, err := json.Marshal(walletTransactionViews([]models.Transaction{tc.tx}, polygonCatalog())[0])
		if err != nil {
			t.Fatal(err)
		}
		if string(account) != tc.account {
			t.Fatalf("%s account wire changed\n got %s\nwant %s", tc.name, account, tc.account)
		}
		if string(wallet) != tc.wallet {
			t.Fatalf("%s wallet wire changed\n got %s\nwant %s", tc.name, wallet, tc.wallet)
		}
		if tc.tx.RawPayload != "" && (strings.Contains(string(account), tc.tx.RawPayload) || strings.Contains(string(wallet), tc.tx.RawPayload)) {
			t.Fatalf("%s raw payload is on the wire", tc.name)
		}
	}
}

func TestTransactionViewKeepsRelatedRowsOnTheirViews(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	const (
		payload = "raw-secret"
		share   = "share-secret"
		cipher  = "cipher-secret"
		iv      = "iv-secret"
		salt    = "salt-secret"
	)
	tx := models.Transaction{
		ID: id, Chain: "eth", TxType: models.TxTypeDeposit, Amount: "1", Asset: "eth",
		RawPayload: payload,
		Address: &models.Address{
			ID: id, Chain: "eth", DerivationType: "genesis",
			EncryptedPrivateKey: cipher, EncryptionIV: iv, EncryptionSalt: salt,
		},
		Wallet: &models.Wallet{ID: id, Chain: "eth", Label: "hot", MPCCustomerShare: share},
	}
	account, err := json.Marshal(newTransactionView(tx))
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := json.Marshal(walletTransactionViews([]models.Transaction{tx}, polygonCatalog())[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{account, wallet} {
		text := string(raw)
		for _, secret := range []string{payload, share, cipher, iv, salt} {
			if strings.Contains(text, secret) {
				t.Fatal("transaction key material is on the wire")
			}
		}
		if !strings.Contains(text, `"address":{"created_at":null,"updated_at":null,"id":"11111111-1111-4111-8111-111111111111","wallet_id":"00000000-0000-0000-0000-000000000000","chain":"eth","address":"","derivation_index":0,"external_user_id":"","metadata":"","is_active":false,"derivation_type":"genesis"}`) {
			t.Fatal("related address changed")
		}
		if !strings.Contains(text, `"wallet":{"created_at":null,"updated_at":null,"id":"11111111-1111-4111-8111-111111111111","chain":"eth","label":"hot","address_index":0,"status":"","required_approvals":0,"read_model_status":"","gas_status":"","sweep_policy_version":0}`) {
			t.Fatal("related wallet changed")
		}
	}
}
