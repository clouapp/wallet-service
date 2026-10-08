package tron

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// tronTxFixture is a /wallet/gettransactionbyid answer.
type tronTxFixture struct {
	TxID       string   `json:"txID"`
	RawDataHex string   `json:"raw_data_hex"`
	Signature  []string `json:"signature"`
	RawData    struct {
		RefBlockBytes string `json:"ref_block_bytes"`
		RefBlockHash  string `json:"ref_block_hash"`
		Expiration    int64  `json:"expiration"`
		Timestamp     int64  `json:"timestamp"`
		FeeLimit      int64  `json:"fee_limit"`
		Contract      []struct {
			Type      string `json:"type"`
			Parameter struct {
				Value struct {
					OwnerAddress    string `json:"owner_address"`
					ToAddress       string `json:"to_address"`
					Amount          int64  `json:"amount"`
					ContractAddress string `json:"contract_address"`
					Data            string `json:"data"`
				} `json:"value"`
			} `json:"parameter"`
		} `json:"contract"`
	} `json:"raw_data"`
}

// Real transactions, as returned by /wallet/gettransactionbyid:
//   - tron_tx_nile_trx.json: Nile TRX transfer 35bd7fdf…, v = 0x00.
//   - tron_tx_nile_trx_activation.json: Nile TRX transfer 35ad1db7… that activated
//     its recipient (fee 1.1 TRX), v = 0x01.
//   - tron_tx_nile_usdt.json: Nile USDT transfer c4c6e63a… (block 71532743), v = 0x00.
//   - tron_tx_mainnet_usdt.json: mainnet USDT transfer 0b07474f…, v = 0x1c (TronWeb).
var tronTxFixtures = []string{
	"tron_tx_nile_trx.json",
	"tron_tx_nile_trx_activation.json",
	"tron_tx_nile_usdt.json",
	"tron_tx_mainnet_usdt.json",
}

func loadTronTx(t *testing.T, name string) tronTxFixture {
	t.Helper()
	var tx tronTxFixture
	if err := json.Unmarshal(readTronFixture(t, name), &tx); err != nil {
		t.Fatal(err)
	}
	if len(tx.RawData.Contract) != 1 || len(tx.Signature) != 1 {
		t.Fatalf("%s: want one contract and one signature", name)
	}
	return tx
}

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

// fixtureRawData rebuilds the raw data from the node's JSON view of the fixture,
// independently of its raw_data_hex.
func fixtureRawData(t *testing.T, tx tronTxFixture) tronRawData {
	t.Helper()
	contract := tx.RawData.Contract[0]
	value := contract.Parameter.Value
	built := tronContract{owner: mustHex(t, value.OwnerAddress)}
	switch contract.Type {
	case tronTransferContractName:
		built.contractType = tronContractTypeTransfer
		built.to = mustHex(t, value.ToAddress)
		built.amount = value.Amount
	case tronTriggerSmartContractName:
		built.contractType = tronContractTypeTriggerSmart
		built.contract = mustHex(t, value.ContractAddress)
		built.data = mustHex(t, value.Data)
	default:
		t.Fatalf("unexpected contract type %s", contract.Type)
	}
	return tronRawData{
		refBlockBytes: mustHex(t, tx.RawData.RefBlockBytes),
		refBlockHash:  mustHex(t, tx.RawData.RefBlockHash),
		expiration:    tx.RawData.Expiration,
		contract:      built,
		timestamp:     tx.RawData.Timestamp,
		feeLimit:      tx.RawData.FeeLimit,
	}
}

func TestTronRealTransactionsEncodeHashAndRecover(t *testing.T) {
	for _, name := range tronTxFixtures {
		t.Run(name, func(t *testing.T) {
			tx := loadTronTx(t, name)
			nodeRaw := mustHex(t, tx.RawDataHex)

			encoded, err := fixtureRawData(t, tx).encode()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(encoded, nodeRaw) {
				t.Fatalf("local encoding differs from the node's raw_data_hex\nlocal %x\nnode  %s", encoded, tx.RawDataHex)
			}
			if got := hex.EncodeToString(tronTxID(encoded)); got != tx.TxID {
				t.Fatalf("txID = %s, want %s", got, tx.TxID)
			}

			decoded, err := decodeTronRawData(nodeRaw)
			if err != nil {
				t.Fatal(err)
			}
			reencoded, err := decoded.encode()
			if err != nil || !bytes.Equal(reencoded, nodeRaw) {
				t.Fatalf("decode/encode round trip differs: %v", err)
			}

			signature := mustHex(t, tx.Signature[0])
			signer, err := recoverTronSigner(tronTxID(nodeRaw), signature)
			if err != nil {
				t.Fatal(err)
			}
			if want := tx.RawData.Contract[0].Parameter.Value.OwnerAddress; hex.EncodeToString(signer) != want {
				t.Fatalf("signature recovers %x, want owner %s", signer, want)
			}

			owner, err := addressing.TronAddressFromHex(tx.RawData.Contract[0].Parameter.Value.OwnerAddress)
			if err != nil {
				t.Fatal(err)
			}
			signedBytes := encodeTronTransaction(nodeRaw, signature)
			gotRaw, gotSigs, err := decodeTronTransaction(signedBytes)
			if err != nil || !bytes.Equal(gotRaw, nodeRaw) || len(gotSigs) != 1 || !bytes.Equal(gotSigs[0], signature) {
				t.Fatalf("transaction envelope round trip failed: %v", err)
			}
			unsigned := &types.UnsignedTx{RawBytes: tronTxID(nodeRaw), Metadata: map[string]interface{}{"raw_data_hex": tx.RawDataHex}}
			signed := &types.SignedTx{TxHash: tx.TxID, RawBytes: signedBytes}
			if err := (&TronLive{}).VerifySignedTransaction(unsigned, signed, owner); err != nil {
				t.Fatalf("VerifySignedTransaction: %v", err)
			}
		})
	}
}

func TestTronRealBandwidthMatchesNodeCharge(t *testing.T) {
	// net_usage / net_fee the node charged (gettransactioninfobyid): bandwidth is
	// the signed transaction without ret plus 64 bytes.
	cases := map[string]int64{"tron_tx_nile_trx.json": 270, "tron_tx_nile_usdt.json": 345}
	for name, charged := range cases {
		tx := loadTronTx(t, name)
		if got := tronBandwidthBytes(mustHex(t, tx.RawDataHex)); got != charged {
			t.Fatalf("%s: bandwidth = %d bytes, node charged %d", name, got, charged)
		}
	}
}

func TestTronTRC20TransferEncodingMatchesRealCall(t *testing.T) {
	tx := loadTronTx(t, "tron_tx_nile_usdt.json")
	recipient, err := addressing.DecodeTronAddress("TL1eeYCiqPwERsRVdscUBEWi5aHvnwtvfh")
	if err != nil {
		t.Fatal(err)
	}
	data, err := encodeTRC20Transfer(recipient, big.NewInt(5_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(data), tx.RawData.Contract[0].Parameter.Value.Data; got != want {
		t.Fatalf("calldata\n got %s\nwant %s", got, want)
	}

	for _, bad := range []*big.Int{nil, big.NewInt(0), big.NewInt(-1), new(big.Int).Lsh(big.NewInt(1), 256)} {
		if _, err := encodeTRC20Transfer(recipient, bad); err == nil {
			t.Fatalf("amount %v accepted", bad)
		}
	}
	if _, err := encodeTRC20Transfer(recipient[1:], big.NewInt(1)); err == nil {
		t.Fatal("20-byte recipient accepted")
	}
}

func TestTronRefBlock(t *testing.T) {
	blockID := mustHex(t, tronTestHeadBlockID)
	ref, err := newTronRefBlock(tronTestHeadNumber, blockID)
	if err != nil {
		t.Fatal(err)
	}
	// 71532773 = 0x044380e5: bytes [6:8] of the 8-byte big-endian number.
	if hex.EncodeToString(ref.bytes) != "80e5" || hex.EncodeToString(ref.hash) != "8a4462a4d528c77e" {
		t.Fatalf("ref = %x/%x", ref.bytes, ref.hash)
	}
	if _, err := newTronRefBlock(tronTestHeadNumber, blockID[:31]); err == nil {
		t.Fatal("short block id accepted")
	}
	if _, err := newTronRefBlock(0, blockID); err == nil {
		t.Fatal("block 0 accepted")
	}
}

func TestTronDecodeRejectsNonCanonicalOrUnknownData(t *testing.T) {
	tx := loadTronTx(t, "tron_tx_nile_trx.json")
	raw := mustHex(t, tx.RawDataHex)
	// Field 10 (data/memo) appended: valid protobuf, but not what this adapter builds.
	withMemo := append(append([]byte(nil), raw...), 0x52, 0x01, 0x78)
	if _, err := decodeTronRawData(withMemo); err == nil {
		t.Fatal("raw data with a memo accepted")
	}
	if _, err := decodeTronRawData(raw[:len(raw)-1]); err == nil {
		t.Fatal("truncated raw data accepted")
	}
	twoSignatures := encodeTronTransaction(raw, make([]byte, tronSignatureSize), make([]byte, tronSignatureSize))
	if _, sigs, err := decodeTronTransaction(twoSignatures); err != nil || len(sigs) != 2 {
		t.Fatalf("two-signature envelope: %v", err)
	}
}

func TestTronFeeProbeRecipientDerivation(t *testing.T) {
	digest := sha256.Sum256([]byte("macro-wallets tron fee probe"))
	probe, err := addressing.EncodeTronAddress(append([]byte{addressing.TronAddressPrefix}, digest[len(digest)-addressing.TronAddressBodySize:]...))
	if err != nil || probe != chain.TronFeeProbeRecipient() {
		t.Fatalf("probe = %s, documented %s (%v)", probe, chain.TronFeeProbeRecipient(), err)
	}
}
