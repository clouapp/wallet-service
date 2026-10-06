package wallets

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

const (
	amoyUSDCContract    = "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	mainnetWETHContract = "0x7ceB23fD6bC0adD59E62ac25578270cFf1b9f619"
	devnetUSDCMint      = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
)

func polygonCatalog() assetDecimalsCatalog {
	return newAssetDecimalsCatalog(
		&models.Chain{ID: "polygon", NativeSymbol: "pol", NativeDecimals: 18},
		[]models.Token{{ChainID: "polygon", Symbol: "USDC", ContractAddress: amoyUSDCContract, Decimals: 6}},
	)
}

func TestToken_Transaction_GetsTheTokenDecimals(t *testing.T) {
	t.Parallel()

	tx := models.Transaction{Chain: "polygon", Asset: "USDC", Amount: "3000000", TokenContract: "0x41e94eb019c0762f9bfcf9fb1e58725bfb0e7582"}

	got := polygonCatalog().decimalsFor(tx)

	if got == nil || *got != 6 {
		t.Fatalf("decimals = %v, want 6", got)
	}
}

func TestNative_Transaction_GetsTheChainNativeDecimals(t *testing.T) {
	t.Parallel()

	tx := models.Transaction{Chain: "polygon", Asset: "MATIC", Amount: "1000000000000000000"}

	got := polygonCatalog().decimalsFor(tx)

	if got == nil || *got != 18 {
		t.Fatalf("decimals = %v, want 18", got)
	}
}

func TestUnknown_Token_ContractHasNoDecimals(t *testing.T) {
	t.Parallel()

	tx := models.Transaction{Chain: "polygon", Asset: "WETH", Amount: "1", TokenContract: mainnetWETHContract}

	if got := polygonCatalog().decimalsFor(tx); got != nil {
		t.Fatalf("decimals = %d, want none for a token the chain does not configure", *got)
	}
}

func TestSolana_Mint_MatchesExactly(t *testing.T) {
	t.Parallel()

	catalog := newAssetDecimalsCatalog(
		&models.Chain{ID: "tsol", NativeDecimals: 9},
		[]models.Token{{ChainID: "tsol", Symbol: "USDC", ContractAddress: devnetUSDCMint, Decimals: 6}},
	)

	exact := catalog.decimalsFor(models.Transaction{Chain: "tsol", Asset: "USDC", TokenContract: devnetUSDCMint})
	lowered := catalog.decimalsFor(models.Transaction{Chain: "tsol", Asset: "USDC", TokenContract: "4zmmc9srt5ri5x14gagxhaHii3gnpaeeryPJgZJDncDU"})

	if exact == nil || *exact != 6 {
		t.Fatalf("exact mint decimals = %v, want 6", exact)
	}
	if lowered != nil {
		t.Fatalf("case-changed mint decimals = %d, want none", *lowered)
	}
}

func TestMissing_Chain_LeavesNativeDecimalsUnknown(t *testing.T) {
	t.Parallel()

	catalog := newAssetDecimalsCatalog(nil, nil)

	if got := catalog.decimalsFor(models.Transaction{Chain: "polygon", Asset: "MATIC", Amount: "1"}); got != nil {
		t.Fatalf("decimals = %d, want none without a chain record", *got)
	}
}

func TestTransaction_Keeps_TheTransactionFieldsAndAddsDecimals(t *testing.T) {
	t.Parallel()

	txID := uuid.New()
	views := transactionsFrom(
		[]models.Transaction{{ID: txID, Chain: "polygon", Asset: "USDC", Amount: "3000000", TokenContract: amoyUSDCContract, TxType: models.TxTypeWithdrawal}},
		polygonCatalog(),
	)

	raw, err := json.Marshal(views[0])
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != txID.String() || body["amount"] != "3000000" || body["tx_type"] != models.TxTypeWithdrawal {
		t.Fatalf("transaction fields changed: %s", raw)
	}
	if body["decimals"] != float64(6) {
		t.Fatalf("decimals = %v, want 6 in %s", body["decimals"], raw)
	}
}

func TestTransactionViewDescribesTheFeeInTheNativeAsset(t *testing.T) {
	t.Parallel()

	withFee := marshalTransaction(t, models.Transaction{
		Chain: "polygon", Asset: "USDC", Amount: "3000000", TokenContract: amoyUSDCContract,
		TxType: models.TxTypeWithdrawal, Fee: "52500000000000",
	})
	if withFee["fee"] != "52500000000000" || withFee["fee_asset"] != "POL" || withFee["fee_decimals"] != float64(18) {
		t.Fatalf("fee fields = %v %v %v, want the fee in POL with 18 decimals", withFee["fee"], withFee["fee_asset"], withFee["fee_decimals"])
	}

	withoutFee := marshalTransaction(t, models.Transaction{Chain: "polygon", Asset: "POL", Amount: "1", TxType: models.TxTypeDeposit})
	if _, ok := withoutFee["fee_asset"]; ok {
		t.Fatalf("fee_asset present without a fee: %v", withoutFee)
	}
	if _, ok := withoutFee["fee_decimals"]; ok {
		t.Fatalf("fee_decimals present without a fee: %v", withoutFee)
	}
}

func marshalTransaction(t *testing.T, tx models.Transaction) map[string]any {
	t.Helper()
	raw, err := json.Marshal(transactionsFrom([]models.Transaction{tx}, polygonCatalog())[0])
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTransaction_Serializes_EveryTimestampWithAZone(t *testing.T) {
	t.Parallel()

	const (
		createdUTC   = "2026-10-01T04:03:27Z"
		confirmedUTC = "2026-10-01T04:06:28Z"
	)
	saoPaulo := time.FixedZone("UTC-3", -3*60*60)
	createdInstant, _ := time.Parse(time.RFC3339, createdUTC)
	confirmedInstant, _ := time.Parse(time.RFC3339, confirmedUTC)
	confirmedLocal := confirmedInstant.In(saoPaulo)
	tx := models.Transaction{Chain: "polygon", Asset: "USDC", Amount: "3000000", ConfirmedAt: &confirmedLocal}
	tx.CreatedAt = carbon.NewDateTime(carbon.FromStdTime(createdInstant.In(saoPaulo)))
	tx.UpdatedAt = carbon.NewDateTime(carbon.FromStdTime(confirmedInstant))

	body := marshalTransaction(t, tx)

	if body["created_at"] != createdUTC || body["updated_at"] != confirmedUTC {
		t.Fatalf("created_at = %v, updated_at = %v, want %s and %s", body["created_at"], body["updated_at"], createdUTC, confirmedUTC)
	}
	confirmed, err := time.Parse(time.RFC3339, body["confirmed_at"].(string))
	if err != nil || !confirmed.Equal(confirmedInstant) {
		t.Fatalf("confirmed_at = %v (%v), want the instant %s", body["confirmed_at"], err, confirmedUTC)
	}
}

func TestTransaction_Without_TimestampsSerializesNull(t *testing.T) {
	t.Parallel()

	body := marshalTransaction(t, models.Transaction{Chain: "polygon", Asset: "MATIC", Amount: "1"})

	if body["created_at"] != nil || body["updated_at"] != nil {
		t.Fatalf("created_at = %v, updated_at = %v, want null", body["created_at"], body["updated_at"])
	}
}

func TestTransaction_Keeps_TheModelWire(t *testing.T) {
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
		name string
		tx   models.Transaction
		want string
	}{
		{
			name: "zero",
			tx:   models.Transaction{},
			want: `{"id":"00000000-0000-0000-0000-000000000000","address_id":null,"wallet_id":"00000000-0000-0000-0000-000000000000","external_user_id":"","chain":"","tx_type":"","tx_hash":"","log_index":0,"from_address":"","to_address":"","amount":"","asset":"","token_contract":"","confirmations":0,"required_confs":0,"status":"","fee":"","block_number":0,"block_hash":"","error_message":"","confirmed_at":null,"created_at":null,"updated_at":null,"type":"unknown","direction":"unknown"}`,
		},
		{
			name: "stamped",
			tx:   stamped,
			want: `{"id":"00000000-0000-0000-0000-000000000000","address_id":null,"wallet_id":"00000000-0000-0000-0000-000000000000","external_user_id":"","chain":"","tx_type":"","tx_hash":"","log_index":0,"from_address":"","to_address":"","amount":"","asset":"","token_contract":"","confirmations":0,"required_confs":0,"status":"","fee":"","block_number":0,"block_hash":"","error_message":"","confirmed_at":null,"source":"chain","created_at":"2024-05-06T07:08:09Z","updated_at":null,"type":"unknown","direction":"incoming","chain_direction":"inbound"}`,
		},
		{
			name: "plain",
			tx:   plain,
			want: `{"id":"11111111-1111-4111-8111-111111111111","address_id":"22222222-2222-4222-8222-222222222222","wallet_id":"22222222-2222-4222-8222-222222222222","external_user_id":"user-1","chain":"eth","tx_type":"withdrawal","tx_hash":"0xabc","log_index":2,"from_address":"0xfrom","to_address":"0xto","amount":"1000","asset":"ETH","token_contract":"0xtoken","confirmations":3,"required_confs":12,"status":"confirmed","fee":"10","block_number":99,"block_hash":"0xblock","error_message":"none","idempotency_key":"idem-1","confirmed_at":"2024-05-06T07:09:00Z","source":"withdrawal_flow","parent_transaction_id":"33333333-3333-4333-8333-333333333333","origin":"user_request","synced_at":"2024-05-06T07:10:00Z","created_at":"2024-05-06T07:08:09Z","updated_at":"2024-05-06T07:08:10Z","type":"withdrawal","direction":"outgoing","chain_direction":"outbound"}`,
		},
		{
			name: "decimals",
			tx:   withDecimals,
			want: `{"id":"11111111-1111-4111-8111-111111111111","address_id":"22222222-2222-4222-8222-222222222222","wallet_id":"22222222-2222-4222-8222-222222222222","external_user_id":"user-1","chain":"polygon","tx_type":"withdrawal","tx_hash":"0xabc","log_index":2,"from_address":"0xfrom","to_address":"0xto","amount":"1000","asset":"USDC","token_contract":"0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582","confirmations":3,"required_confs":12,"status":"confirmed","fee":"10","block_number":99,"block_hash":"0xblock","error_message":"none","idempotency_key":"idem-1","confirmed_at":"2024-05-06T07:09:00Z","source":"withdrawal_flow","parent_transaction_id":"33333333-3333-4333-8333-333333333333","origin":"user_request","synced_at":"2024-05-06T07:10:00Z","created_at":"2024-05-06T07:08:09Z","updated_at":"2024-05-06T07:08:10Z","decimals":6,"type":"withdrawal","direction":"outgoing","chain_direction":"outbound"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(transactionsFrom([]models.Transaction{tc.tx}, polygonCatalog())[0])
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("%s wallet wire changed\n got %s\nwant %s", tc.name, raw, tc.want)
		}
		if tc.tx.RawPayload != "" && strings.Contains(string(raw), tc.tx.RawPayload) {
			t.Fatalf("%s raw payload is on the wire", tc.name)
		}
	}
}

func TestTransaction_Keeps_RelatedRowsOnTheirViews(t *testing.T) {
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
	raw, err := json.Marshal(transactionsFrom([]models.Transaction{tx}, polygonCatalog())[0])
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

func TestTransaction_Carries_AnUnsignedAmountWithTypeAndDirection(t *testing.T) {
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
		raw, err := json.Marshal(transactionsFrom([]models.Transaction{tc.tx}, polygonCatalog())[0])
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

	if got := transactionsFrom(nil, polygonCatalog()); got != nil {
		t.Fatal("nil wallet page became an empty slice")
	}
	if got := transactionsFrom([]models.Transaction{}, polygonCatalog()); got == nil || len(got) != 0 {
		t.Fatalf("empty wallet page = %#v", got)
	}
	views := transactionsFrom([]models.Transaction{
		{Amount: "1", TxType: models.TxTypeDeposit},
		{Amount: "2", TxType: models.TxTypeWithdrawal},
	}, polygonCatalog())
	if len(views) != 2 || views[0].Amount != "1" || views[1].Amount != "2" {
		t.Fatalf("order = %s then %s", views[0].Amount, views[1].Amount)
	}
}
