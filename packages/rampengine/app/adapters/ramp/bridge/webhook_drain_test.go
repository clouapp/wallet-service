package bridge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// A drain names its liquidation address and its customer, never an order: the
// first event of a drain is what creates the transaction.
func TestParseWebhookDecodesASignedDrainDelivery(t *testing.T) {
	s := newSigner(t)
	body := readFixture(t, "la_drain_funds_received.json")
	headers := map[string]string{WebhookSignatureHeader: s.sign(t, body, time.Now())}

	result, err := s.adapter().ParseWebhook(context.Background(), headers, body)
	require.NoError(t, err)

	require.Equal(t, ramp.WebhookKindLiquidationDrain, result.Kind)
	require.Equal(t, "wh_d02funds", result.EventID)
	require.Equal(t, "liquidation_address.drain.updated.status_transitioned", result.EventType)
	require.Equal(t, "cus_1", result.ExternalCustomerID)
	require.Equal(t, "la_1", result.ProviderAccountID)
	require.Empty(t, result.ProviderOrderID, "a drain names no order")
	require.Empty(t, result.Status, "the status travels on the drain")

	require.NotNil(t, result.Drain)
	require.Equal(t, "drn_6b2c3d4e5f6a7b8c", result.Drain.DrainID)
	require.Equal(t, "la_1", result.Drain.ProviderAddressID)
	require.Equal(t, models.TxStatusFundsReceived, result.Drain.Status)
	require.Equal(t, "250.00", result.Drain.SourceAmount)
	require.Equal(t, "usdc", result.Drain.SourceCurrency)
	require.Equal(t, "ach", result.Drain.PaymentRail)
	require.Equal(t, solanaAddress, result.Drain.FromAddress)
	require.NotEmpty(t, result.Drain.Raw)

	tampered := []byte(strings.Replace(string(body), `"250.00"`, `"2500.00"`, 1))
	_, err = s.adapter().ParseWebhook(context.Background(), headers, tampered)
	require.ErrorIs(t, err, ErrInvalidSignature)
}

// The settled drain carries the receipt: what was sent, the fee, and the
// bank's reference.
func TestDecodeWebhookReadsTheSettledDrain(t *testing.T) {
	result, err := New(nil, nil).DecodeWebhook(context.Background(), readFixture(t, "la_drain_payment_processed.json"))
	require.NoError(t, err)

	require.Equal(t, ramp.WebhookKindLiquidationDrain, result.Kind)
	require.Equal(t, models.TxStatusCompleted, result.Drain.Status)
	require.Equal(t, "250.00", result.Drain.SourceAmount)
	require.Equal(t, "247.50", result.Drain.DestinationAmount)
	require.Equal(t, "2.50", result.Drain.FeeAmount)
	require.Equal(t, "718268532664263", result.Drain.BankReference)
	require.True(t, result.Drain.OccurredAt.Equal(time.Date(2026, 10, 15, 8, 31, 0, 0, time.UTC)))
}

// An external account event is a cue to re-read the customer: it names the
// customer and nothing the processor would write.
func TestDecodeWebhookReadsAnExternalAccountEventsCustomer(t *testing.T) {
	body := []byte(`{"event_id":"wh_1","event_category":"external_account","event_type":"external_account.created",` +
		`"event_object_id":"ea_1","event_object":{"id":"ea_1","customer_id":"cus_1","account_owner_name":"Jane Doe"}}`)

	result, err := New(nil, nil).DecodeWebhook(context.Background(), body)
	require.NoError(t, err)

	require.Equal(t, ramp.WebhookKindExternalAccount, result.Kind)
	require.Equal(t, "cus_1", result.ExternalCustomerID)
	require.Equal(t, "external_account.created", result.EventType)
}
