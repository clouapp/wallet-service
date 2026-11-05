package blindpay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/solanatx"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// blockhashWindow bounds how long a prepared action is offered: the
// transaction carries a recent blockhash the network accepts for well under a
// minute (observed 40 s to 1 min), and the refresh covers a slower user.
const blockhashWindow = 30 * time.Second

var (
	_ ramp.OfframpPrepareAdapter        = (*Adapter)(nil)
	_ ramp.SignedOfframpActionValidator = (*Adapter)(nil)
)

// prepareDelegateRequest is the prepare-delegate-solana body. The quote names
// the token and the amount; nothing else is sent, so BlindPay cannot be told
// a different amount than it quoted.
type prepareDelegateRequest struct {
	OwnerAddress string `json:"owner_address"`
	QuoteID      string `json:"quote_id"`
}

// prepareDelegateResponse is the slice of the answer the adapter reads;
// debug is ignored.
type prepareDelegateResponse struct {
	Success     bool   `json:"success"`
	Transaction string `json:"transaction"`
}

// correlationState is what a prepare records for the validator and the
// finalize to check again without calling BlindPay: the delegation the
// transaction was held to.
type correlationState struct {
	QuoteID         string `json:"quote_id"`
	OwnerAddress    string `json:"owner_address"`
	Mint            string `json:"mint"`
	AmountBaseUnits uint64 `json:"amount_base_units"`
	Decimals        uint8  `json:"decimals"`
	Delegate        string `json:"delegate"`
	// TokenProgram is recorded for the operator, never trusted: the
	// revalidation re-runs solanatx.CheckDelegation with
	// solana.TokenProgramID from code, never this value, so a tampered state
	// cannot name Token-2022.
	TokenProgram string `json:"token_program"`
}

// PrepareOfframp asks BlindPay for the delegation transaction that funds a
// quoted bank payout, and accepts it only once it is exactly the SPL
// ApproveChecked the quote needs: the owner's tokens of the configured mint,
// the quote's amount, to the configured delegate, through the classic token
// program. Nothing is created on BlindPay's side, so the call carries no
// Idempotency-Key and a retry simply prepares again.
func (a *Adapter) PrepareOfframp(ctx context.Context, _ ramp.CustomerRef, req ramp.OfframpPrepareRequest) (*ramp.PreparedOfframpFunding, error) {
	owner, err := solana.PublicKeyFromBase58(req.OwnerAddress)
	if err != nil {
		return nil, fmt.Errorf("blindpay: prepare: owner address: %w", ramp.ErrInvalidWallet)
	}
	if req.ProviderQuoteID == "" {
		return nil, fmt.Errorf("blindpay: prepare: no quote id: %w", ramp.ErrRequestNotSent)
	}
	// The action never outlives the quote, so a quote without an expiry
	// cannot bound one: it is refused like an expired quote.
	now := a.now()
	if !req.QuoteExpiresAt.After(now) {
		return nil, fmt.Errorf("blindpay: prepare: quote expiry: %w", ramp.ErrEstimateExpired)
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	terms, err := delegationTerms(client, owner, req)
	if err != nil {
		return nil, err
	}

	var answer prepareDelegateResponse
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: "prepare-delegate-solana",
		Body: prepareDelegateRequest{OwnerAddress: req.OwnerAddress, QuoteID: req.ProviderQuoteID},
	}, &answer); err != nil {
		return nil, err
	}
	message, err := acceptPrepared(answer, terms)
	if err != nil {
		// The reason names the rule that failed, never an address or amount.
		facades.Log().Errorf("ramp.attention: blindpay prepare for quote %s refused: %s", req.ProviderQuoteID, err.Error())
		return nil, fmt.Errorf("blindpay: prepare for quote %s refused: %w", req.ProviderQuoteID, ramp.ErrProviderFailed)
	}

	state, err := json.Marshal(correlationState{
		QuoteID:         req.ProviderQuoteID,
		OwnerAddress:    terms.Owner.String(),
		Mint:            terms.Mint.String(),
		AmountBaseUnits: terms.Amount,
		Decimals:        terms.Decimals,
		Delegate:        terms.Delegate.String(),
		TokenProgram:    terms.TokenProgram.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("blindpay: prepare: encode state: %w", err)
	}
	expiresAt := now.Add(blockhashWindow)
	if req.QuoteExpiresAt.Before(expiresAt) {
		expiresAt = req.QuoteExpiresAt
	}
	action := ramp.FundingAction{
		Type:           ramp.FundingActionSolanaTransaction,
		SubmissionMode: ramp.SubmissionBackendFinalize,
		Payload: ramp.FundingActionPayload{
			ContentType:         ramp.ContentTypeSolanaTransaction,
			UnsignedTransaction: answer.Transaction,
		},
		ExpiresAt: expiresAt,
	}
	if err := action.Validate(); err != nil {
		return nil, fmt.Errorf("blindpay: prepare: %w: %w", ramp.ErrProviderFailed, err)
	}
	return &ramp.PreparedOfframpFunding{
		Action:             action,
		CorrelationState:   state,
		MessageFingerprint: solanatx.Fingerprint(message),
	}, nil
}

// delegationTerms is the delegation the quote's funding must carry. The mint,
// its decimals and the delegate come from the instance's settings, the token
// program from code: a transaction or an answer naming another program
// (Token-2022, say) is refused, never followed. A setting missing or not an
// address means the payout is not configured, which is
// ErrProviderNotImplemented and sends no request.
func delegationTerms(client *Client, owner solana.PublicKey, req ramp.OfframpPrepareRequest) (solanatx.DelegationTerms, error) {
	// LegFor holds the chain, the token and the mode to what BlindPay settles
	// here; the mint setting of each token names that leg's token (USDB on a
	// development instance).
	if _, err := LegFor(client.Mode(), req.Chain, req.Token); err != nil {
		return solanatx.DelegationTerms{}, err
	}
	notConfigured := func(what string) (solanatx.DelegationTerms, error) {
		return solanatx.DelegationTerms{}, fmt.Errorf("blindpay: prepare: %s not configured: %w", what, ramp.ErrProviderNotImplemented)
	}
	config, ok := client.Mint(req.Token)
	if !ok {
		return notConfigured("mint")
	}
	mint, err := solana.PublicKeyFromBase58(config.Address)
	if err != nil {
		return notConfigured("mint")
	}
	delegate, err := solana.PublicKeyFromBase58(client.Delegate())
	if err != nil {
		return notConfigured("delegate")
	}
	// The source amount is the quote's own: one this side cannot price in
	// base units is a quote it cannot fund, the provider's failure. The
	// BaseUnits error names no amount.
	amount, err := solanatx.BaseUnits(req.SourceAmount, config.Decimals)
	if err != nil {
		return solanatx.DelegationTerms{}, fmt.Errorf("blindpay: prepare: source amount: %w: %w", ramp.ErrProviderFailed, err)
	}
	return termsOf(owner, mint, delegate, amount, config.Decimals), nil
}

// termsOf is the one place the delegation terms are assembled, for the
// prepare (from the settings) and for the revalidation of a signed
// transaction (from the recorded state). The token program is always the
// classic one from code, so neither a tampered state nor an answer can name
// another.
func termsOf(owner, mint, delegate solana.PublicKey, amount uint64, decimals uint8) solanatx.DelegationTerms {
	return solanatx.DelegationTerms{
		Owner:        owner,
		Mint:         mint,
		Delegate:     delegate,
		TokenProgram: solana.TokenProgramID,
		Amount:       amount,
		Decimals:     decimals,
	}
}

// acceptPrepared holds BlindPay's answer to the terms and answers the message
// bytes the wallet will sign.
func acceptPrepared(answer prepareDelegateResponse, terms solanatx.DelegationTerms) ([]byte, error) {
	if !answer.Success {
		return nil, errors.New("answer is not a success")
	}
	if answer.Transaction == "" {
		return nil, errors.New("answer carries no transaction")
	}
	tx, message, err := solanatx.Unsigned(answer.Transaction)
	if err != nil {
		return nil, err
	}
	if err := solanatx.CheckDelegation(tx, terms); err != nil {
		return nil, err
	}
	return message, nil
}

// ValidateSignedOfframpAction checks the wallet's signed transaction against
// what the prepare recorded: the exact prepared message, every signature
// valid, the owner of the state as its signer, and the delegation the state
// recorded (checked again with the token program from code). Only a
// transaction that fails these is ErrInvalidSignedActionPayload; a state this
// side cannot read is its own fault and is answered as retryable.
func (*Adapter) ValidateSignedOfframpAction(_ context.Context, _ ramp.CustomerRef, req ramp.SignedOfframpActionValidationRequest) error {
	// The state errors below are unmarked on purpose: a state this side
	// stored and cannot read is not the wallet's fault, and the service maps
	// an unmarked error to a retryable 5xx rather than a refusal.
	var state correlationState
	if err := json.Unmarshal(req.CorrelationState, &state); err != nil {
		return errors.New("blindpay: signed action: correlation state does not decode")
	}
	owner, err := solana.PublicKeyFromBase58(state.OwnerAddress)
	if err != nil {
		return errors.New("blindpay: signed action: correlation state names no owner")
	}
	tx, err := solanatx.VerifySigned(req.SignedTransaction, req.MessageFingerprint)
	if err != nil {
		return fmt.Errorf("blindpay: signed action: %w", err)
	}
	if !tx.Message.AccountKeys[0].Equals(owner) {
		return fmt.Errorf("blindpay: signed action: signer is not the prepared owner: %w", ramp.ErrInvalidSignedActionPayload)
	}
	mint, err := solana.PublicKeyFromBase58(state.Mint)
	if err != nil {
		return errors.New("blindpay: signed action: correlation state names no mint")
	}
	delegate, err := solana.PublicKeyFromBase58(state.Delegate)
	if err != nil {
		return errors.New("blindpay: signed action: correlation state names no delegate")
	}
	// The recorded terms, never the recorded token program: a state that
	// names Token-2022 is refused here like any other mismatch.
	if err := solanatx.CheckDelegation(tx, termsOf(owner, mint, delegate, state.AmountBaseUnits, state.Decimals)); err != nil {
		return fmt.Errorf("blindpay: signed action: delegation: %w", ramp.ErrInvalidSignedActionPayload)
	}
	return nil
}
