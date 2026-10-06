package transactions

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/txkind"
)

func TestTransaction_From_KeepsTheModelWire(t *testing.T) {
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

	withContract := plain
	withContract.Chain = "polygon"
	withContract.Asset = "USDC"
	withContract.TokenContract = "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"

	cases := []struct {
		name string
		tx   models.Transaction
		want string
	}{
		{
			name: "zero",
			tx:   models.Transaction{},
			want: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","address_id":null,"wallet_id":"00000000-0000-0000-0000-000000000000","external_user_id":"","chain":"","tx_type":"","tx_hash":"","log_index":0,"from_address":"","to_address":"","amount":"","asset":"","token_contract":"","confirmations":0,"required_confs":0,"status":"","fee":"","block_number":0,"block_hash":"","error_message":"","confirmed_at":null,"type":"unknown","direction":"unknown"}`,
		},
		{
			name: "stamped",
			tx:   stamped,
			want: `{"created_at":"2024-05-06 07:08:09","updated_at":null,"id":"00000000-0000-0000-0000-000000000000","address_id":null,"wallet_id":"00000000-0000-0000-0000-000000000000","external_user_id":"","chain":"","tx_type":"","tx_hash":"","log_index":0,"from_address":"","to_address":"","amount":"","asset":"","token_contract":"","confirmations":0,"required_confs":0,"status":"","fee":"","block_number":0,"block_hash":"","error_message":"","confirmed_at":null,"source":"chain","type":"unknown","direction":"incoming","chain_direction":"inbound"}`,
		},
		{
			name: "plain",
			tx:   plain,
			want: `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","address_id":"22222222-2222-4222-8222-222222222222","wallet_id":"22222222-2222-4222-8222-222222222222","external_user_id":"user-1","chain":"eth","tx_type":"withdrawal","tx_hash":"0xabc","log_index":2,"from_address":"0xfrom","to_address":"0xto","amount":"1000","asset":"ETH","token_contract":"0xtoken","confirmations":3,"required_confs":12,"status":"confirmed","fee":"10","block_number":99,"block_hash":"0xblock","error_message":"none","idempotency_key":"idem-1","confirmed_at":"2024-05-06T07:09:00Z","source":"withdrawal_flow","parent_transaction_id":"33333333-3333-4333-8333-333333333333","origin":"user_request","synced_at":"2024-05-06T07:10:00Z","type":"withdrawal","direction":"outgoing","chain_direction":"outbound"}`,
		},
		{
			name: "contract",
			tx:   withContract,
			want: `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","address_id":"22222222-2222-4222-8222-222222222222","wallet_id":"22222222-2222-4222-8222-222222222222","external_user_id":"user-1","chain":"polygon","tx_type":"withdrawal","tx_hash":"0xabc","log_index":2,"from_address":"0xfrom","to_address":"0xto","amount":"1000","asset":"USDC","token_contract":"0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582","confirmations":3,"required_confs":12,"status":"confirmed","fee":"10","block_number":99,"block_hash":"0xblock","error_message":"none","idempotency_key":"idem-1","confirmed_at":"2024-05-06T07:09:00Z","source":"withdrawal_flow","parent_transaction_id":"33333333-3333-4333-8333-333333333333","origin":"user_request","synced_at":"2024-05-06T07:10:00Z","type":"withdrawal","direction":"outgoing","chain_direction":"outbound"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(TransactionFrom(tc.tx))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("%s account wire changed\n got %s\nwant %s", tc.name, raw, tc.want)
		}
		if tc.tx.RawPayload != "" && strings.Contains(string(raw), tc.tx.RawPayload) {
			t.Fatalf("%s raw payload is on the wire", tc.name)
		}
	}
}

func TestTransaction_From_KeepsRelatedRowsOnTheirViews(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	const (
		payload = "raw-secret"
		share   = "share-secret"
		cipher  = "cipher-secret"
		iv      = "iv-secret"
		salt    = "salt-secret"
	)
	raw, err := json.Marshal(TransactionFrom(models.Transaction{
		ID: id, Chain: "eth", TxType: models.TxTypeDeposit, Amount: "1", Asset: "eth",
		RawPayload: payload,
		Address: &models.Address{
			ID: id, Chain: "eth", DerivationType: "genesis",
			EncryptedPrivateKey: cipher, EncryptionIV: iv, EncryptionSalt: salt,
		},
		Wallet: &models.Wallet{ID: id, Chain: "eth", Label: "hot", MPCCustomerShare: share},
	}))
	if err != nil {
		t.Fatal(err)
	}
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

func TestTransaction_From_CarriesAnUnsignedAmountWithTypeAndDirection(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		tx                           models.Transaction
		wantType, wantDirection      string
		wantChainDirection, wantTxTy string
	}{
		"5 SOL deposit": {
			tx:       models.Transaction{Chain: "sol", Asset: "sol", Amount: "5000000000", TxType: models.TxTypeDeposit, Direction: models.TxDirectionInbound},
			wantType: txkind.TypeDeposit, wantDirection: txkind.DirectionIncoming, wantChainDirection: models.TxDirectionInbound, wantTxTy: models.TxTypeDeposit,
		},
		"0.02 SOL withdrawal": {
			tx:       models.Transaction{Chain: "sol", Asset: "sol", Amount: "20000000", TxType: models.TxTypeWithdrawal, Direction: models.TxDirectionOutbound},
			wantType: txkind.TypeWithdrawal, wantDirection: txkind.DirectionOutgoing, wantChainDirection: models.TxDirectionOutbound, wantTxTy: models.TxTypeWithdrawal,
		},
		"manual consolidation": {
			tx:       models.Transaction{Chain: "eth", Asset: "ETH", Amount: "1954853353616000", TxType: models.TxTypeSweep, Origin: models.TxOriginManualConsolidation, Direction: models.TxDirectionSelf},
			wantType: txkind.TypeConsolidation, wantDirection: txkind.DirectionInternal, wantChainDirection: models.TxDirectionSelf, wantTxTy: models.TxTypeSweep,
		},
		"gas seed": {
			tx:       models.Transaction{Chain: "polygon", Asset: "POL", Amount: "10000000000000000", TxType: models.TxTypeGasSeed, Origin: models.TxOriginGasSeed, Direction: models.TxDirectionSelf},
			wantType: txkind.TypeGasFunding, wantDirection: txkind.DirectionInternal, wantChainDirection: models.TxDirectionSelf, wantTxTy: models.TxTypeGasSeed,
		},
		"deposit without stored direction": {
			tx:       models.Transaction{Chain: "eth", Asset: "eth", Amount: "1", TxType: models.TxTypeDeposit},
			wantType: txkind.TypeDeposit, wantDirection: txkind.DirectionIncoming, wantTxTy: models.TxTypeDeposit,
		},
	}
	for name, tc := range cases {
		raw, err := json.Marshal(TransactionFrom(tc.tx))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body["amount"] != tc.tx.Amount {
			t.Errorf("%s amount = %v, want the unsigned %s", name, body["amount"], tc.tx.Amount)
		}
		if body["type"] != tc.wantType || body["direction"] != tc.wantDirection || body["tx_type"] != tc.wantTxTy {
			t.Errorf("%s type/direction/tx_type = %v/%v/%v, want %s/%s/%s",
				name, body["type"], body["direction"], body["tx_type"], tc.wantType, tc.wantDirection, tc.wantTxTy)
		}
		chainDirection, present := body["chain_direction"]
		if tc.wantChainDirection == "" && present {
			t.Errorf("%s chain_direction = %v, want it omitted", name, chainDirection)
		}
		if tc.wantChainDirection != "" && chainDirection != tc.wantChainDirection {
			t.Errorf("%s chain_direction = %v, want %s", name, chainDirection, tc.wantChainDirection)
		}
	}
}

func TestTransactions_From_KeepsNilEmptyAndOrder(t *testing.T) {
	t.Parallel()

	if got := TransactionsFrom(nil); got != nil {
		t.Fatal("nil slice became an empty slice")
	}
	if got := TransactionsFrom([]models.Transaction{}); got == nil || len(got) != 0 {
		t.Fatalf("empty slice = %#v", got)
	}
	views := TransactionsFrom([]models.Transaction{
		{Amount: "1", TxType: models.TxTypeDeposit},
		{Amount: "2", TxType: models.TxTypeWithdrawal},
	})
	if len(views) != 2 || views[0].Type != txkind.TypeDeposit || views[1].Direction != txkind.DirectionOutgoing {
		t.Fatalf("views = %+v", views)
	}
}
