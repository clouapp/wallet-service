package blindpay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

var _ ramp.OfframpFinalizeAdapter = (*Adapter)(nil)

const (
	// payoutPageSize is the page the listing asks for; payoutPageCap bounds
	// how many pages a search reads before it gives up. A search that hits
	// the cap has not seen every payout, so it answers a failure, never "not
	// found".
	payoutPageSize = 100
	payoutPageCap  = 20
)

// definitiveRefusals are the provider codes of a 4xx that mean nothing was
// created and nothing will be. The list is closed on purpose: a code outside
// it is ambiguous, not a refusal. BlindPay can add a code at any time, and
// reading an unknown one as "failed" is how a payout that did get created is
// paid a second time. Ambiguous costs a recovery pass; wrong costs money.
var definitiveRefusals = map[string]bool{
	"VALIDATION_FAILED":                true,
	"CUSTOMERS_NOT_FOUND":              true,
	"WALLETS_NOT_FOUND":                true,
	"CUSTOMERS_ONBOARDING_INCOMPLETE":  true,
	"BLOCKCHAIN_NETWORK_NOT_SUPPORTED": true,
	"AUTH_FORBIDDEN":                   true,
}

// payoutSolanaRequest is the POST payouts/solana body. It is encoded once per
// call: BlindPay compares a replayed Idempotency-Key by the hash of the body.
type payoutSolanaRequest struct {
	QuoteID             string `json:"quote_id"`
	SenderWalletAddress string `json:"sender_wallet_address"`
	SignedTransaction   string `json:"signed_transaction"`
}

// FinalizeOfframp creates the payout from the transaction the wallet signed.
// It sends once, never retries: the caller repeats it, with the same bytes
// and the same key, only after the outcome is known to be unknown. The
// result is classified for the caller:
//
//   - a 200 naming the payout, confirmed by its bound read (GetBoundOrder
//     against the whole binding), is the order; a confirming read that does
//     not answer is ambiguous, one off the binding ErrBindingMismatch;
//   - quote_expired and quote_already_used are their sentinels;
//   - a 4xx whose code is in definitiveRefusals is a *ramp.FinalizeRefusedError;
//   - everything else (a timeout, a 5xx, 409 in flight, 429, an unreadable
//     body, a code this file does not know, a 200 that is not the bound
//     payout) is returned unmarked, and the caller must read it as ambiguous.
func (a *Adapter) FinalizeOfframp(ctx context.Context, ref ramp.CustomerRef, req ramp.FinalizeRequest) (*ramp.RampOrder, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("blindpay: finalize: %w", ramp.ErrInvalidIdempotencyKey)
	}
	if req.QuoteID == "" || req.SenderWallet == "" || req.SignedTransaction == "" {
		return nil, fmt.Errorf("blindpay: finalize: incomplete request: %w", ramp.ErrRequestNotSent)
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payoutSolanaRequest{
		QuoteID: req.QuoteID, SenderWalletAddress: req.SenderWallet, SignedTransaction: req.SignedTransaction,
	})
	if err != nil {
		return nil, fmt.Errorf("blindpay: finalize: encode: %w", ramp.ErrRequestNotSent)
	}

	// The payout reads its funding from the chain: the delegation lands first.
	if err := a.broadcastDelegation(ctx, client, req.SignedTransaction); err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: "payouts/solana", RawBody: body, IdempotencyKey: req.IdempotencyKey,
	}, &raw); err != nil {
		return nil, classifyFinalize(err)
	}
	var payout payoutResponse
	if err := json.Unmarshal(raw, &payout); err != nil {
		return nil, fmt.Errorf("blindpay: finalize: decode: %w", ramp.ErrProviderFailed)
	}
	if !createdMatches(payout, req.OrderBinding) {
		facades.Log().Errorf("ramp.attention: blindpay payout create for quote %s does not match its binding", req.QuoteID)
		return nil, fmt.Errorf("blindpay: finalize for quote %s: %w", req.QuoteID, ramp.ErrBindingMismatch)
	}
	return a.confirmCreated(ctx, ref, payout.ID, req.OrderBinding)
}

// confirmCreated reads the payout the create named and answers it only when
// the read holds it to the whole binding: the create's answer leaves out most
// of the binding (the sandbox's names no customer, bank account, quote, leg or
// amount), the read names all of it. It is read once, under the caller's
// bound. A read that names another payout is ErrBindingMismatch; a read that
// does not answer is ambiguous, never a refusal nor a quote condition: the
// payout was created.
func (a *Adapter) confirmCreated(ctx context.Context, ref ramp.CustomerRef, payoutID string, binding ramp.OrderBinding) (*ramp.RampOrder, error) {
	order, err := a.GetBoundOrder(ctx, ref, payoutID, binding)
	switch {
	case errors.Is(err, ramp.ErrBindingMismatch):
		return nil, err
	case err != nil:
		var upstream *UpstreamError
		if errors.As(err, &upstream) {
			return nil, ambiguousFinalize(upstream)
		}
		return nil, &unconfirmedPayout{payoutID: payoutID, cause: err}
	}
	return order, nil
}

// unconfirmedPayout is a created payout whose confirming read failed without
// an upstream answer (a timeout, a transport failure, an unreadable body). It
// reads as a failed provider call and unwraps to nothing else, so no sentinel
// the cause may carry is taken for the provider deciding the create.
type unconfirmedPayout struct {
	payoutID string
	cause    error
}

func (e *unconfirmedPayout) Error() string {
	return fmt.Sprintf("blindpay: finalize: confirm payout %s: %v", e.payoutID, e.cause)
}

func (e *unconfirmedPayout) Is(target error) bool { return target == ramp.ErrProviderFailed }

// classifyFinalize turns a failed send into its sentinel, its refusal, or
// itself. Only a 4xx the provider answered with a structured envelope can be
// anything but ambiguous, and an idempotency answer never is a refusal: it
// says a request under this key already exists.
func classifyFinalize(err error) error {
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		return err
	}
	if upstream.Status < 400 || upstream.Status >= 500 ||
		strings.HasPrefix(upstream.Message, "idempotency_key_") ||
		upstream.Status == http.StatusConflict && !isQuoteAnswer(upstream) ||
		upstream.Status == http.StatusTooManyRequests {
		return ambiguousFinalize(upstream)
	}
	switch {
	case upstream.Code == "QUOTES_EXPIRED" || upstream.Message == "quote_expired":
		return fmt.Errorf("blindpay: finalize: %w", ramp.ErrEstimateExpired)
	case upstream.Code == "QUOTES_ALREADY_USED" || upstream.Message == "quote_already_used":
		return fmt.Errorf("blindpay: finalize: %w", ramp.ErrQuoteAlreadyUsed)
	case definitiveRefusals[upstream.Code]:
		return &ramp.FinalizeRefusedError{Code: upstream.Code}
	}
	return ambiguousFinalize(upstream)
}

// unreadAnswer is an answer of the send that decides nothing. It reads like
// the upstream error it came from (status, trace id, a failed provider call)
// but never unwraps to a condition: the sentinel the status-blind client
// mapping gave it (quote expired on a 503, say) must not be taken for the
// provider saying so.
type unreadAnswer struct{ upstream *UpstreamError }

func (e *unreadAnswer) Error() string             { return e.upstream.Error() }
func (e *unreadAnswer) HTTPStatus() int           { return e.upstream.Status }
func (e *unreadAnswer) ProviderRequestID() string { return e.upstream.TraceID }
func (e *unreadAnswer) Is(target error) bool {
	return target == ramp.ErrProviderFailed || target == ramp.ErrRetryableUpstream && errors.Is(e.upstream, ramp.ErrRetryableUpstream)
}

// ambiguousFinalize is the upstream error without its condition.
func ambiguousFinalize(upstream *UpstreamError) error { return &unreadAnswer{upstream: upstream} }

// isQuoteAnswer is a quote condition named by its code or its legacy message.
func isQuoteAnswer(e *UpstreamError) bool {
	switch {
	case e.Code == "QUOTES_EXPIRED", e.Code == "QUOTES_ALREADY_USED":
		return true
	case e.Message == "quote_expired", e.Message == "quote_already_used":
		return true
	}
	return false
}

// createdMatches holds the answer of the create to the binding. The create's
// real answer (S11g) names only the payout and its wallet: those must be there
// and match. Everything else of the binding (customer, bank account, quote,
// leg, amount) must match only where the answer names it: a field it names
// with another value is some other payout, whatever its status. What it
// leaves out is held by the confirming read.
func createdMatches(p payoutResponse, b ramp.OrderBinding) bool {
	if !payoutIDWellFormed(p.ID) {
		return false
	}
	if b.SenderWallet == "" || p.SenderWalletAddress != b.SenderWallet {
		return false
	}
	named := [][2]string{
		{p.CustomerID, b.ProviderCustomerID},
		{p.BankAccountID, b.BankAccountID},
		{p.QuoteID, b.QuoteID},
		{p.Network, b.Network},
		{p.Token, b.Token},
	}
	for _, pair := range named {
		if pair[0] != "" && pair[0] != pair[1] {
			return false
		}
	}
	return p.SenderAmount.rat == nil || senderAmountMatches(p.SenderAmount, b.SenderAmount)
}

func payoutIDWellFormed(id string) bool {
	return len(id) == quoteIDLen && strings.HasPrefix(id, payoutIDPrefix) && !strings.ContainsAny(id, "/.?#%\\")
}

// payoutList is one answer of GET payouts: the paginated object, or a bare
// array (the OpenAPI allows both).
type payoutList struct {
	items   []json.RawMessage
	hasMore bool
	paged   bool
}

func decodePayoutList(raw json.RawMessage) (payoutList, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return payoutList{}, err
		}
		return payoutList{items: items}, nil
	}
	var page struct {
		Data       []json.RawMessage `json:"data"`
		Pagination *struct {
			HasMore *bool `json:"has_more"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return payoutList{}, err
	}
	if page.Data == nil || page.Pagination == nil || page.Pagination.HasMore == nil {
		return payoutList{}, errors.New("listing carries no pagination")
	}
	return payoutList{items: page.Data, hasMore: *page.Pagination.HasMore, paged: true}, nil
}

// FindOfframpByQuote looks for the payout a quote produced. It answers
// (nil, nil) only when every page of the listing answered and none had it;
// a failed page, an unreadable one, or more pages than the cap is an error,
// because "not found" is what lets the caller conclude the payout was never
// created. A payout found for the quote is held to the binding like any other
// read: one of another customer or bank account is ErrBindingMismatch and
// nothing is bound.
func (a *Adapter) FindOfframpByQuote(ctx context.Context, ref ramp.CustomerRef, req ramp.FindByQuoteRequest) (*ramp.RampOrder, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if req.QuoteID == "" || req.ProviderCustomerID == "" || req.BankAccountID == "" {
		return nil, fmt.Errorf("blindpay: find payout: incomplete binding: %w", ramp.ErrRequestNotSent)
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	cursor, seen := "", map[string]bool{}
	for range payoutPageCap {
		query := url.Values{
			"customer_id":     {req.ProviderCustomerID},
			"bank_account_id": {req.BankAccountID},
			"limit":           {strconv.Itoa(payoutPageSize)},
		}
		if cursor != "" {
			query.Set("starting_after", cursor)
		}
		var raw json.RawMessage
		if err := client.Do(ctx, Request{Method: http.MethodGet, Path: "payouts", Query: query}, &raw); err != nil {
			return nil, err
		}
		list, err := decodePayoutList(raw)
		if err != nil {
			return nil, fmt.Errorf("blindpay: payouts: decode: %w", ramp.ErrProviderFailed)
		}
		lastID := ""
		for _, item := range list.items {
			var payout payoutResponse
			if err := json.Unmarshal(item, &payout); err != nil {
				return nil, fmt.Errorf("blindpay: payouts: decode: %w", ramp.ErrProviderFailed)
			}
			lastID = payout.ID
			if payout.QuoteID != req.QuoteID {
				continue
			}
			if !payoutIDWellFormed(payout.ID) || !payoutMatches(payout, req.OrderBinding) {
				facades.Log().Errorf("ramp.attention: blindpay payout listed for quote %s does not match its binding", req.QuoteID)
				return nil, fmt.Errorf("blindpay: payout listed for quote %s: %w", req.QuoteID, ramp.ErrBindingMismatch)
			}
			return orderOfPayout(payout, item), nil
		}

		more := list.hasMore
		if !list.paged {
			// A bare array carries no end marker: a full page may have more.
			more = len(list.items) >= payoutPageSize
		}
		if !more {
			return nil, nil
		}
		// The next page starts after the last payout of this one. The
		// listing's next_page is the first item of the next page by its own
		// description, and starting after it would skip that payout.
		cursor = lastID
		if cursor == "" || seen[cursor] {
			return nil, fmt.Errorf("blindpay: payouts: pagination does not advance: %w", ramp.ErrProviderFailed)
		}
		seen[cursor] = true
	}
	return nil, fmt.Errorf("blindpay: payouts: more than %d pages: %w", payoutPageCap, ramp.ErrProviderFailed)
}
