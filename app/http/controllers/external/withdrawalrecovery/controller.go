package withdrawalrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
)

// Workflow derives facts on the server and scopes every lookup or mutation to
// the authenticated wallet. It never accepts an alternate operation from a body.
type Workflow interface {
	Inspect(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, recovery.FeeEnvelope, *recovery.GasSeedPlan, string) (*recovery.Proposal, error)
	Apply(context.Context, uuid.UUID, uuid.UUID, recovery.Proposal, recovery.Review, string, string) (*recovery.Result, error)
	Resume(context.Context, uuid.UUID, uuid.UUID, string, string) (*recovery.Result, error)
	Lookup(context.Context, uuid.UUID, uuid.UUID) (*recovery.Result, error)
}

type Controller struct{ workflow Workflow }

func New(workflow Workflow) *Controller {
	if workflow == nil {
		panic("withdrawal recovery controller: workflow is required")
	}
	return &Controller{workflow: workflow}
}

type inspectRequest struct {
	OriginalTransactionID uuid.UUID             `json:"original_transaction_id"`
	Fees                  recovery.FeeEnvelope  `json:"fees"`
	GasSeed               *recovery.GasSeedPlan `json:"gas_seed,omitempty"`
	PreparedBy            string                `json:"prepared_by"`
}

type applyRequest struct {
	Proposal   recovery.Proposal `json:"proposal"`
	Review     recovery.Review   `json:"review"`
	Operator   string            `json:"operator"`
	Passphrase string            `json:"passphrase"`
}

type resumeRequest struct {
	Operator   string `json:"operator"`
	Passphrase string `json:"passphrase"`
}

func (c *Controller) Inspect(ctx http.Context) http.Response {
	wallet, operation, response := scopedOperation(ctx)
	if response != nil {
		return response
	}
	var body inspectRequest
	if requests.Bind(ctx, &body) != nil {
		return invalid(ctx)
	}
	proposal, err := c.workflow.Inspect(ctx.Context(), wallet.ID, operation, body.OriginalTransactionID, body.Fees, body.GasSeed, body.PreparedBy)
	if err != nil {
		return failure(ctx, err)
	}
	hash, err := recovery.Hash(proposal)
	if err != nil {
		return failure(ctx, err)
	}
	return ctx.Response().Success().Json(map[string]any{"proposal": proposal, "proposal_hash": hash})
}

func (c *Controller) Apply(ctx http.Context) http.Response {
	wallet, operation, response := scopedOperation(ctx)
	if response != nil {
		return response
	}
	if response := requireRecoveryWriteToken(ctx); response != nil {
		return response
	}
	var body applyRequest
	defer controllers.DiscardPassphrase(&body.Passphrase)
	if requests.Bind(ctx, &body) != nil || len(body.Passphrase) < 12 {
		return invalid(ctx)
	}
	if body.Proposal.Binding.OperationID != operation || body.Proposal.Binding.WalletID != wallet.ID || wallet.AccountID == nil || body.Proposal.Binding.AccountID != *wallet.AccountID {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "recovery operation not found")
	}
	result, err := c.workflow.Apply(ctx.Context(), wallet.ID, operation, body.Proposal, body.Review, body.Operator, body.Passphrase)
	if err != nil {
		return failure(ctx, err)
	}
	return ctx.Response().Success().Json(result)
}

func (c *Controller) Resume(ctx http.Context) http.Response {
	wallet, operation, response := scopedOperation(ctx)
	if response != nil {
		return response
	}
	if response := requireRecoveryWriteToken(ctx); response != nil {
		return response
	}
	var body resumeRequest
	defer controllers.DiscardPassphrase(&body.Passphrase)
	if requests.Bind(ctx, &body) != nil || len(body.Passphrase) < 12 {
		return invalid(ctx)
	}
	result, err := c.workflow.Resume(ctx.Context(), wallet.ID, operation, body.Operator, body.Passphrase)
	if err != nil {
		return failure(ctx, err)
	}
	return ctx.Response().Success().Json(result)
}

func (c *Controller) Show(ctx http.Context) http.Response {
	wallet, operation, response := scopedOperation(ctx)
	if response != nil {
		return response
	}
	result, err := c.workflow.Lookup(ctx.Context(), wallet.ID, operation)
	if err != nil {
		return failure(ctx, err)
	}
	return ctx.Response().Success().Json(result)
}

func scopedOperation(ctx http.Context) (*models.Wallet, uuid.UUID, http.Response) {
	wallet, ok := requestctx.Wallet(ctx)
	accountID, hasAccount := requestctx.AccountID(ctx)
	environment, hasEnvironment := requestctx.AccountEnvironment(ctx)
	if !ok || wallet == nil || !hasAccount || wallet.AccountID == nil || *wallet.AccountID != accountID || !hasEnvironment || environment != models.EnvironmentTest {
		return nil, uuid.Nil, responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "same-operation recovery is available only to the owning test account")
	}
	operationID, err := requests.RouteUUID(ctx, "idempotencyKey")
	if err != nil {
		return nil, uuid.Nil, invalid(ctx)
	}
	return wallet, operationID, nil
}

func invalid(ctx http.Context) http.Response {
	return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid recovery request")
}

// Recovery does not enter the ordinary per-token USD reservation flow. Until
// that reservation can be resumed by operation ID, limited tokens cannot use
// its write endpoints. Account ownership and route permissions still apply.
func requireRecoveryWriteToken(ctx http.Context) http.Response {
	token, ok := requestctx.APIToken(ctx)
	accountID, accountOK := requestctx.AccountID(ctx)
	if !ok || token == nil || !accountOK || token.AccountID != accountID {
		return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "recovery requires the owning test account token")
	}
	if strings.TrimSpace(token.SpendingLimit) != "" {
		var limits map[string]json.RawMessage
		if json.Unmarshal([]byte(token.SpendingLimit), &limits) != nil || limits == nil || len(limits) != 0 {
			return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "same-operation recovery is unavailable to spending-limited tokens")
		}
	}
	return nil
}

func failure(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, recovery.ErrNotFound), errors.Is(err, models.ErrRepositoryNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "recovery operation not found")
	case errors.Is(err, recovery.ErrOccupied), errors.Is(err, recovery.ErrConcurrent), errors.Is(err, recovery.ErrNeedsReconciliation):
		return responses.Fail(ctx, http.StatusConflict, responses.CodeConflict, "the same recovery operation remains occupied; inspect its state before continuing")
	case errors.Is(err, recovery.ErrRefused), errors.Is(err, recovery.ErrGasNotReady):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeInvalidRequest, "recovery preconditions were not met")
	default:
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "recovery could not be completed; inspect the existing operation")
	}
}
