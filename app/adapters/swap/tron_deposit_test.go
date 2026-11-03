package swapproviders

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/stretchr/testify/require"
)

func TestTronDeposit_DoesNotReplaceAContractCallWithATransfer(t *testing.T) {
	ownerHex, destinationHex := "41"+strings.Repeat("11", 20), "41"+strings.Repeat("22", 20)
	owner, err := addressing.TronAddressFromHex(ownerHex)
	require.NoError(t, err)
	destination, err := addressing.TronAddressFromHex(destinationHex)
	require.NoError(t, err)
	request := swap.QuoteRequest{From: swap.Asset{Symbol: "TRX", Chain: "tron", Native: true, Decimals: 6}, FromAddress: owner, AmountRaw: "1000000"}
	funding := &swap.Funding{Kind: "transfer", To: destination, AmountRaw: request.AmountRaw}
	payload := json.RawMessage(`{"raw_data":{"contract":[{"type":"TransferContract","parameter":{"value":{"owner_address":"` + ownerHex + `","to_address":"` + destinationHex + `","amount":1000000}}}]}}`)
	require.NoError(t, validateTronDeposit(payload, request, funding))
	call := json.RawMessage(`{"raw_data":{"contract":[{"type":"TriggerSmartContract","parameter":{"value":{"owner_address":"` + ownerHex + `","contract_address":"` + destinationHex + `","data":"12345678"}}}]}}`)
	require.ErrorIs(t, validateTronDeposit(call, request, funding), swap.ErrUnsupported)
	changed := strings.Replace(string(payload), "1000000", "2000000", 1)
	require.ErrorIs(t, validateTronDeposit(json.RawMessage(changed), request, funding), swap.ErrInvalidQuote)
}
