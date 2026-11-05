package blindpay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const (
	// minPayinCents is BlindPay's floor on request_amount (R$ 5,00).
	minPayinCents = 500
	// quoteIDLen is the length of every BlindPay id (qu_ + 12).
	quoteIDLen = 15
	// defaultQuoteTTL stands in for an expires_at BlindPay leaves null.
	defaultQuoteTTL = 5 * time.Minute
	// rateScale is the places a rate carries, as Ozav's do.
	rateScale = 6
	// epochMillisFloor tells an epoch in milliseconds from one in seconds:
	// seconds stay below it until the year 33658.
	epochMillisFloor = 1_000_000_000_000
)

// decimalAmount is the only shape a fiat or token amount may take: digits with an optional
// fraction. It rules out signs, exponents and separators.
var decimalAmount = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// payinQuoteRequest is the CreatePayinQuoteIn body; the field order is fixed.
type payinQuoteRequest struct {
	BlockchainWalletID string `json:"blockchain_wallet_id"`
	CurrencyType       string `json:"currency_type"`
	CoverFees          bool   `json:"cover_fees"`
	RequestAmount      int64  `json:"request_amount"`
	PaymentMethod      string `json:"payment_method"`
	Token              string `json:"token"`
	PartnerFeeID       string `json:"partner_fee_id,omitempty"`
}

// payinQuoteResponse is the slice of CreatePayinQuoteOut the adapter reads.
// Every amount is in cents of its currency.
type payinQuoteResponse struct {
	ID                string      `json:"id"`
	ExpiresAt         quoteNumber `json:"expires_at"`
	BlindpayQuotation quoteNumber `json:"blindpay_quotation"`
	ReceiverAmount    quoteNumber `json:"receiver_amount"`
	SenderAmount      quoteNumber `json:"sender_amount"`
	PartnerFeeAmount  quoteNumber `json:"partner_fee_amount"`
	FlatFee           quoteNumber `json:"flat_fee"`
}

// quoteNumber is a JSON number read exactly; nil when null or absent. A
// number sent as a string is refused rather than read.
type quoteNumber struct{ rat *big.Rat }

func (n *quoteNumber) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		n.rat = nil
		return nil
	}
	if len(b) == 0 || (b[0] != '-' && (b[0] < '0' || b[0] > '9')) {
		return errors.New("blindpay: quote field is not a number")
	}
	rat, ok := new(big.Rat).SetString(string(b))
	if !ok {
		return errors.New("blindpay: quote field is not a number")
	}
	n.rat = rat
	return nil
}

func (n quoteNumber) positive() bool    { return n.rat != nil && n.rat.Sign() > 0 }
func (n quoteNumber) nonNegative() bool { return n.rat == nil || n.rat.Sign() >= 0 }

// centsString writes an amount in cents as units with two places, rounding
// down so nothing is ever shown above what BlindPay answered; nil is zero.
func centsString(cents *big.Rat) string {
	if cents == nil {
		return "0.00"
	}
	whole := new(big.Int).Quo(cents.Num(), cents.Denom())
	units, rest := new(big.Int).QuoRem(whole, big.NewInt(100), new(big.Int))
	return fmt.Sprintf("%s.%02d", units.String(), rest.Int64())
}

// brlCents reads a BRL amount as integer cents, rounding fractional cents
// down so the customer is never quoted more than they typed.
func brlCents(amount string) (int64, error) { return decimalCents(amount) }

// tokenCents reads a stablecoin amount as integer cents of a dollar, with the
// same rounding down; the tokens BlindPay moves are pegged one to one.
func tokenCents(amount string) (int64, error) { return decimalCents(amount) }

func decimalCents(amount string) (int64, error) {
	if !decimalAmount.MatchString(amount) {
		return 0, fmt.Errorf("blindpay: source amount: %w", ramp.ErrRequestNotSent)
	}
	rat, ok := new(big.Rat).SetString(amount)
	if !ok {
		return 0, fmt.Errorf("blindpay: source amount: %w", ramp.ErrRequestNotSent)
	}
	rat.Mul(rat, big.NewRat(100, 1))
	cents := new(big.Int).Quo(rat.Num(), rat.Denom())
	if !cents.IsInt64() {
		return 0, fmt.Errorf("blindpay: source amount out of range: %w", ramp.ErrRequestNotSent)
	}
	return cents.Int64(), nil
}

// flatFeeBRL is BlindPay's flat fee, which it charges in the token, written
// in reais at the quote's own rate and rounded down to two places; empty when
// the quote leaves the fee out.
func flatFeeBRL(q payinQuoteResponse) string {
	if q.FlatFee.rat == nil || q.BlindpayQuotation.rat == nil {
		return ""
	}
	// token cents × BRL cents per token / 100 = BRL cents.
	brlCents := new(big.Rat).Mul(q.FlatFee.rat, q.BlindpayQuotation.rat)
	brlCents.Quo(brlCents, big.NewRat(100, 1))
	return centsString(brlCents)
}

// quoteExpiry reads expires_at, an epoch in seconds or milliseconds told
// apart by size; null means BlindPay's usual five minutes from now.
func quoteExpiry(n quoteNumber, now time.Time) time.Time {
	if n.rat == nil {
		return now.Add(defaultQuoteTTL)
	}
	epoch := new(big.Int).Quo(n.rat.Num(), n.rat.Denom())
	if !epoch.IsInt64() {
		return time.Time{}
	}
	if epoch.Int64() >= epochMillisFloor {
		return time.UnixMilli(epoch.Int64())
	}
	return time.Unix(epoch.Int64(), 0)
}

// EstimateOnramp quotes a Pix payin in BRL delivered to the customer's own
// Solana wallet, with XIP's fee as a BlindPay partner fee. The quote is
// binding: CreateOnramp redeems this id.
//
// Every refusal that does not need BlindPay happens before any call. The
// network and token come from LegFor on the instance's mode — the request
// only names the chain family and the product token. Fee figures are read
// from the quote, never computed here.
func (a *Adapter) EstimateOnramp(ctx context.Context, ref ramp.CustomerRef, req ramp.OnrampQuoteRequest) (*ramp.Estimate, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if !strings.EqualFold(strings.TrimSpace(req.Source.Currency), "BRL") {
		return nil, fmt.Errorf("blindpay: source currency must be BRL: %w", ramp.ErrRequestNotSent)
	}
	cents, err := brlCents(req.Source.Amount)
	if err != nil {
		return nil, err
	}
	if cents < minPayinCents {
		return nil, &ramp.LimitViolation{LimitBRL: centsString(big.NewRat(minPayinCents, 1)), Err: ramp.ErrAmountBelowMinimum}
	}
	if !validSolanaAddress(req.Destination.Address) {
		return nil, fmt.Errorf("blindpay: wallet address: %w", ramp.ErrInvalidWallet)
	}
	bps, err := feeBps(req.FeePercent)
	if err != nil {
		return nil, err
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	leg, err := LegFor(client.Mode(), req.Destination.PaymentRail, req.Destination.Currency)
	if err != nil {
		return nil, err
	}

	walletID, err := a.ensureBlockchainWallet(ctx, client, ref.ExternalID, leg.Network, req.Destination.Address)
	if err != nil {
		return nil, err
	}
	feeID, err := a.partnerFeeID(ctx, client, bps, 0)
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: "payin-quotes",
		Body: payinQuoteRequest{
			BlockchainWalletID: walletID, CurrencyType: "sender", CoverFees: false,
			RequestAmount: cents, PaymentMethod: "pix", Token: leg.Token, PartnerFeeID: feeID,
		},
	}, &raw); err != nil {
		if feeID != "" && partnerFeeRejected(err) {
			a.forgetPartnerFee(client.InstanceID(), bps, 0)
		}
		if walletRejected(err) {
			a.forgetBlockchainWallet(client.InstanceID(), ref.ExternalID, leg.Network, req.Destination.Address)
		}
		return nil, err
	}

	var quote payinQuoteResponse
	if err := json.Unmarshal(raw, &quote); err != nil {
		return nil, fmt.Errorf("blindpay: payin quote: decode: %w", ramp.ErrProviderFailed)
	}
	now := time.Now()
	expiresAt, err := validPayinQuote(quote, cents, now)
	if err != nil {
		return nil, err
	}

	feePercent := ""
	if req.FeePercent != "" {
		feePercent = fmt.Sprintf("%d.%02d", bps/100, bps%100)
	}
	return &ramp.Estimate{
		ProviderQuoteID:   quote.ID,
		Binding:           true,
		ExpiresAt:         &expiresAt,
		Rate:              new(big.Rat).Quo(quote.BlindpayQuotation.rat, big.NewRat(100, 1)).FloatString(rateScale),
		FeePercent:        feePercent,
		FeeAmount:         centsString(quote.PartnerFeeAmount.rat),
		FeeCurrency:       strings.ToLower(strings.TrimSpace(req.Destination.Currency)),
		EstimatedReceive:  centsString(quote.ReceiverAmount.rat),
		ProviderFeeAmount: flatFeeBRL(quote),
		SourceAmount:      centsString(big.NewRat(cents, 1)),
		Raw:               raw,
	}, nil
}

// validPayinQuote holds the answer to what was asked and answers its expiry.
// The errors never carry an amount.
func validPayinQuote(q payinQuoteResponse, requestCents int64, now time.Time) (time.Time, error) {
	fail := func(what string) (time.Time, error) {
		return time.Time{}, fmt.Errorf("blindpay: payin quote: %s: %w", what, ramp.ErrProviderFailed)
	}
	if len(q.ID) != quoteIDLen {
		return fail("id is malformed")
	}
	expiresAt := quoteExpiry(q.ExpiresAt, now)
	if !expiresAt.After(now) {
		return fail("already expired")
	}
	if q.SenderAmount.rat == nil || q.SenderAmount.rat.Cmp(big.NewRat(requestCents, 1)) != 0 {
		return fail("sender amount differs from the request")
	}
	if !q.ReceiverAmount.positive() {
		return fail("receiver amount is not positive")
	}
	if !q.BlindpayQuotation.positive() {
		return fail("quotation is not positive")
	}
	if !q.PartnerFeeAmount.nonNegative() || !q.FlatFee.nonNegative() {
		return fail("fee is negative")
	}
	return expiresAt, nil
}

// partnerFeeRejected reports a rejection that names the partner fee profile:
// BlindPay's FEES_NOT_FOUND, or a 404 that names neither of the other two
// things the quote points at.
func partnerFeeRejected(err error) bool {
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		return false
	}
	if upstream.Code == "FEES_NOT_FOUND" {
		return true
	}
	if upstream.Status != http.StatusNotFound {
		return false
	}
	switch upstream.Code {
	case "CUSTOMERS_NOT_FOUND", "WALLETS_NOT_FOUND":
		return false
	}
	return true
}

// walletRejected reports a rejection that says the blockchain wallet is gone.
func walletRejected(err error) bool {
	var upstream *UpstreamError
	return errors.As(err, &upstream) && upstream.Code == "WALLETS_NOT_FOUND"
}

// payinPixTTL is how long a payin's BR Code stays payable.
const payinPixTTL = 30 * time.Minute

// payinCreateRequest is the CreatePayinIn body.
type payinCreateRequest struct {
	PayinQuoteID string `json:"payin_quote_id"`
}

// payinCreateResponse is the slice of CreatePayinOut the adapter reads. Its
// amounts are in cents of their currency.
type payinCreateResponse struct {
	ID                  string        `json:"id"`
	Status              string        `json:"status"`
	CustomerID          string        `json:"customer_id"`
	PixCode             *string       `json:"pix_code"`
	SenderAmount        quoteNumber   `json:"sender_amount"`
	ReceiverAmount      quoteNumber   `json:"receiver_amount"`
	TrackingTransaction payinTracking `json:"tracking_transaction"`
	TrackingPayment     payinTracking `json:"tracking_payment"`
	TrackingComplete    payinTracking `json:"tracking_complete"`
}

// payin reads the create answer as the payin it is, so create and every later
// read share one status mapping.
func (r payinCreateResponse) payin() payinResponse {
	return payinResponse{
		ID: r.ID, Status: r.Status, CustomerID: r.CustomerID, PixCode: r.PixCode,
		SenderAmount: r.SenderAmount, ReceiverAmount: r.ReceiverAmount,
		TrackingTransaction: r.TrackingTransaction, TrackingPayment: r.TrackingPayment,
		TrackingComplete: r.TrackingComplete,
	}
}

// CreateOnramp redeems the estimate's quote as a Pix payin and answers the BR
// Code the user pays.
//
// The Idempotency-Key is the transaction's own, so a resume or a retry after
// an unknown outcome replays the same payin instead of creating a second one.
// A timeout or a cut answer surfaces as ErrOutcomeUnknown from the transport.
func (a *Adapter) CreateOnramp(ctx context.Context, ref ramp.CustomerRef, req ramp.OnrampCreateRequest) (*ramp.RampOrder, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	// Without the quote the payin would price at a rate the user never saw.
	if req.ProviderQuoteID == "" {
		return nil, ramp.ErrEstimateRequired
	}
	// A money-creating call without its key could not be retried safely.
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("blindpay: payin without an idempotency key: %w", ramp.ErrRequestNotSent)
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: "payins/evm",
		Body:           payinCreateRequest{PayinQuoteID: req.ProviderQuoteID},
		IdempotencyKey: req.IdempotencyKey,
	}, &raw); err != nil {
		return nil, err
	}
	var created payinCreateResponse
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, fmt.Errorf("blindpay: payin: decode: %w", ramp.ErrProviderFailed)
	}
	if len(created.ID) != quoteIDLen {
		return nil, fmt.Errorf("blindpay: payin: id is malformed: %w", ramp.ErrProviderFailed)
	}
	// The quote is bound to the customer's wallet, so another customer here
	// means a payin we cannot attribute; it is flagged, never adopted.
	if created.CustomerID != "" && created.CustomerID != ref.ExternalID {
		facades.Log().Errorf("ramp.attention: blindpay payin %s is not customer %s's", created.ID, ref.ExternalID)
		return nil, fmt.Errorf("blindpay: payin %s belongs to another customer: %w", created.ID, ramp.ErrProviderFailed)
	}
	// Without a code the user has nothing to pay. The sandbox answers a
	// placeholder, which is still a code. The payin exists, so its id goes in
	// the error and the log for reconciliation.
	if created.PixCode == nil || strings.TrimSpace(*created.PixCode) == "" {
		facades.Log().Errorf("ramp.attention: blindpay payin %s has no pix code", created.ID)
		return nil, fmt.Errorf("blindpay: payin %s: no pix code: %w", created.ID, ramp.ErrProviderFailed)
	}

	order, _ := payinOrder(created.payin(), raw)
	order.FeePercent = req.FeePercent
	order.DepositInstructions = map[string]any{"br_code": *created.PixCode}
	// CreatePayinOut carries no created_at, so a create replayed under the same
	// key can push the window later by at most the quote TTL.
	expiresAt := time.Now().Add(payinPixTTL)
	order.ExpiresAt = &expiresAt
	return order, nil
}

// GetOrder reads a payin back. The port names only BlindPay's id, so the
// customer is what binds the read: another customer's payin answers no status.
func (a *Adapter) GetOrder(ctx context.Context, ref ramp.CustomerRef, providerOrderID string) (*ramp.RampOrder, error) {
	// The id decides the leg: a payout is read only bound to its quote
	// (GetBoundOrder), and a read without the binding is what that forbids.
	if strings.HasPrefix(providerOrderID, payoutIDPrefix) {
		return nil, fmt.Errorf("blindpay: a payout is read bound to its quote: %w", ramp.ErrBindingMismatch)
	}
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := client.Do(ctx, Request{Method: http.MethodGet, Path: "payins/" + providerOrderID}, &raw); err != nil {
		return nil, err
	}
	var payin payinResponse
	if err := json.Unmarshal(raw, &payin); err != nil {
		return nil, fmt.Errorf("blindpay: payin: decode: %w", ramp.ErrProviderFailed)
	}
	if payin.CustomerID != ref.ExternalID {
		facades.Log().Errorf("ramp.attention: blindpay payin %s is not customer %s's", providerOrderID, ref.ExternalID)
		return nil, fmt.Errorf("blindpay: payin %s belongs to another customer: %w", providerOrderID, ramp.ErrProviderFailed)
	}
	// Every payin this adapter creates settles on the mode's Solana leg. One on
	// another network or token (a sandbox payin read by a production
	// instance, say) is not the order the row describes.
	if !onSolanaLeg(client.Mode(), payin) {
		facades.Log().Errorf("ramp.attention: blindpay payin %s is not on the %s solana leg", providerOrderID, client.Mode())
		return nil, fmt.Errorf("blindpay: payin %s is on another network or token: %w", providerOrderID, ramp.ErrProviderFailed)
	}

	order, attention := payinOrder(payin, raw)
	if attention {
		facades.Log().Errorf("ramp.attention: blindpay payin %s %s", providerOrderID, order.Status)
	}
	order.TxHash = payinTxHash(payin)
	return order, nil
}

// onSolanaLeg reports whether a payin's network and token are one of the legs
// LegFor answers for mode on Solana.
func onSolanaLeg(mode string, p payinResponse) bool {
	for _, token := range []string{models.TargetTokenUSDC, models.TargetTokenUSDT} {
		leg, err := LegFor(mode, models.ChainSolana, token)
		if err == nil && leg.Network == p.Network && leg.Token == p.Token {
			return true
		}
	}
	return false
}

// payinOrder normalizes a payin however it arrived. Its amounts are what
// BlindPay settled; one it left out is left out here too.
func payinOrder(p payinResponse, raw json.RawMessage) (*ramp.RampOrder, bool) {
	amounts := map[string]string{
		"source_amount":      payinAmount(p.SenderAmount),
		"destination_amount": payinAmount(p.ReceiverAmount),
	}
	maps.DeleteFunc(amounts, func(_, value string) bool { return value == "" })
	status, attention := payinStatus(p)
	return &ramp.RampOrder{
		ProviderOrderID: p.ID,
		Status:          status,
		Amounts:         amounts,
		Raw:             raw,
	}, attention
}

// payinAmount writes an amount in cents as units, or "" when it is absent.
func payinAmount(n quoteNumber) string {
	if n.rat == nil {
		return ""
	}
	return centsString(n.rat)
}
