package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/middleware"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	rampsrequests "github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/ramps"
	resources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
	clientramps "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
)

// OnrampController turns BRL into crypto in the wallet of the customer the
// request acts for (middleware.CustomerScope).
type OnrampController struct {
	service *clientramps.OnrampService
}

// NewOnrampController wires the controller.
func NewOnrampController(service *clientramps.OnrampService) *OnrampController {
	return &OnrampController{service: service}
}

// Estimate godoc
//
//	@Summary		Quote a PIX on-ramp
//	@Description	Non-binding. The returned estimate_id is what makes the subsequent create idempotent and keeps it on the same provider.
//	@Tags			onramps
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string								false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			request				body		rampsrequests.EstimateOnrampRequest	true	"Amount and destination"
//	@Success		201					{object}	resources.EstimateResource
//	@Failure		409					{object}	responses.ErrorResponse				"integration_required; kyc_pending or kyc_rejected, naming the provider; or rail_not_eligible, naming none (the tax identity and residence the user declared do not open the deposit, or the provider does not offer it where they live); customer_closed: the customer the header names is closed (it reads, it moves no money)"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; wallet_not_registered: a company's wallet is not one it registered (POST /api/wallets)"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		503					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/onramps/estimate [post]
func (c *OnrampController) Estimate(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.EstimateOnrampRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.Estimate(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx), dto.EstimateOnrampDTO{
		AmountBRL: req.AmountBRL, Token: req.Token, Chain: req.Chain, Wallet: req.Wallet,
	})
	if err != nil {
		return mapError(ctx, err, "estimate onramp")
	}
	return ctx.Response().Status(contractshttp.StatusCreated).
		Json(resources.NewEstimateResource(view))
}

// Create godoc
//
//	@Summary		Place a PIX on-ramp order
//	@Description	Requires an estimate_id. Answers with the Pix BR Code the user pays, and its expiry. Only providers that collect per order serve this — one that collects through a standing deposit account answers 503, and the client provisions an account instead (see POST /api/onramps/account).
//	@Tags			onramps
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string								false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			request				body		rampsrequests.CreateOnrampRequest	true	"Order"
//	@Success		201					{object}	resources.OrderResource
//	@Success		200					{object}	resources.OrderResource
//	@Failure		401					{object}	responses.ErrorResponse				"unauthenticated: the account was deleted while this request was running"
//	@Failure		409					{object}	responses.ErrorResponse				"among the state refusals, rail_not_eligible (naming no provider) when the tax identity and residence the user declares now do not open the deposit at the estimate's provider; customer_closed: the customer the header names is closed (it reads, it moves no money)"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; wallet_not_registered: a company's wallet is not one it registered (POST /api/wallets)"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		503					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/onramps [post]
func (c *OnrampController) Create(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.CreateOnrampRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, created, err := c.service.Create(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx), dto.CreateOnrampDTO{
		EstimateOnrampDTO: dto.EstimateOnrampDTO{
			AmountBRL: req.AmountBRL, Token: req.Token, Chain: req.Chain, Wallet: req.Wallet,
		},
		EstimateID: req.EstimateID,
	})
	if err != nil {
		return mapError(ctx, err, "create onramp")
	}
	status := contractshttp.StatusOK
	if created {
		status = contractshttp.StatusCreated
	}
	return ctx.Response().Status(status).Json(resources.NewOrderResource(view))
}

// Show godoc
//
//	@Summary		Get one on-ramp order
//	@Description	Refreshes against the provider while the order is still in flight, so a dropped webhook does not leave it stuck.
//	@Tags			onramps
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			id					path		string	true	"Order external id"
//	@Success		200					{object}	resources.OrderResource
//	@Failure		404					{object}	responses.ErrorResponse	"not_found: no such order of the customer; customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		422					{object}	responses.ValidationErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Router			/api/onramps/{id} [get]
func (c *OnrampController) Show(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ShowOrderRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.Show(ctx, middleware.CurrentCustomer(ctx).ID, req.ID)
	if err != nil {
		return mapError(ctx, err, "show onramp")
	}
	return ctx.Response().Success().Json(resources.NewOrderResource(view))
}

// Index godoc
//
//	@Summary		List on-ramp orders
//	@Description	A summary list. It does not refresh against the provider — one upstream call per row would multiply the load by the page size for information GET /api/onramps/{id} already reconciles.
//	@Tags			onramps
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			page				query		int		false	"Page number"
//	@Param			per_page			query		int		false	"Items per page"
//	@Success		200					{object}	resources.OrderListResponse
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		422					{object}	responses.ValidationErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/onramps [get]
func (c *OnrampController) Index(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ListOrdersRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	page, perPage := requests.Paging(req.Page, req.PerPage)
	views, total, err := c.service.List(middleware.CurrentCustomer(ctx).ID, page, perPage)
	if err != nil {
		return mapError(ctx, err, "list onramps")
	}
	return ctx.Response().Success().Json(resources.NewOrderListResponse(views, page, perPage, total))
}

// OfframpController turns the crypto of the customer the request acts for
// into BRL on a PIX key.
type OfframpController struct {
	service *clientramps.OfframpService
}

// NewOfframpController wires the controller.
func NewOfframpController(service *clientramps.OfframpService) *OfframpController {
	return &OfframpController{service: service}
}

// Estimate godoc
//
//	@Summary		Quote a PIX or US bank off-ramp
//	@Description	`rail` omitted or pix_payout quotes a Pix key. ach_payout or wire_payout quotes a payout to a registered US bank account (`bank_beneficiary_id`), in USDC or USDT, and carries none of the Pix fields; its quote answers `destination_currency` usd and its limits in `limit_usd`. `funding_formats` declares what the app can sign: `deposit` (the default) and `prepared`. Only an app that declares `prepared` is ever quoted by a provider that funds a Pix payout by a prepared action (BlindPay); that quote answers `deposit_mode` prepared and `destination_currency` brl, and its create answers a `funding_action` like a bank payout's. Without the declaration the Pix quote is a deposit one, as before.
//	@Tags			offramps
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string									false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			request				body		rampsrequests.EstimateOfframpRequest	true	"Amount and PIX or bank destination"
//	@Success		201					{object}	resources.EstimateResource
//	@Failure		409					{object}	responses.ErrorResponse				"integration_required; kyc_pending or kyc_rejected, naming the provider; or rail_not_eligible, naming none (the tax identity and residence the user declared do not open the payout, or the provider does not offer it where they live); tos_required (a bank rail: the provider's terms are unsigned); bank_account_verifying (a bank rail: the provider has not approved the account yet, ask again later); customer_closed: the customer the header names is closed (it reads, it moves no money)"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; wallet_not_registered: a company's wallet is not one it registered (POST /api/wallets); holder_mismatch: a company pays out only to its own CNPJ key; bank_account_rejected (a bank rail: the provider refused the account); amount_below_minimum or amount_above_maximum, naming the bound in limit_usd on a bank rail (the rail's, or the customer's remaining allowance)"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		503					{object}	responses.ErrorResponse	"rail_disabled: an operator closed the rail; no_provider_available"
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for; bank_beneficiary_not_found: the bank beneficiary is not the customer's or does not exist"
//	@Router			/api/offramps/estimate [post]
func (c *OfframpController) Estimate(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.EstimateOfframpRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.Estimate(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx), dto.EstimateOfframpDTO{
		AmountToken: req.AmountToken, Token: req.Token, Chain: req.Chain,
		FromAddress: req.FromAddress, PixKey: req.PixKey, PixKeyType: req.PixKeyType,
		RecipientName: req.RecipientName, RecipientDocument: req.RecipientDocument,
		Rail: req.Rail, BankBeneficiaryID: req.BankBeneficiaryID,
		FundingFormats: req.FundingFormats,
	})
	if err != nil {
		return mapError(ctx, err, "estimate offramp")
	}
	return ctx.Response().Status(contractshttp.StatusCreated).
		Json(resources.NewEstimateResource(view))
}

// BankOptions godoc
//
//	@Summary		Which US bank payout rails the customer can quote now
//	@Description	One entry per bank rail (ach_payout, wire_payout). `available` is false with a `reason`: flag_closed (an operator closed the rail), tos_required (the provider's terms are unsigned), kyc_required (no usable approved provider application) or provider_unavailable (no provider serves it, or its payout allowance could not be read). Available rails carry the `tokens` and `chain` that can be quoted, `min_usd` and `max_usd` (the rail's bound, or the customer's remaining allowance when smaller; absent when unbounded).
//	@Tags			offramps
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Success		200					{object}	resources.BankOptionsResource
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/offramps/bank/options [get]
func (c *OfframpController) BankOptions(ctx contractshttp.Context) contractshttp.Response {
	view, err := c.service.BankOptions(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx))
	if err != nil {
		return mapError(ctx, err, "bank options")
	}
	return ctx.Response().Success().Json(resources.NewBankOptionsResource(view))
}

// Create godoc
//
//	@Summary		Place a PIX or US bank off-ramp order
//	@Description	Requires an estimate_id, and requires an MFA code when the user has MFA enabled — this endpoint sends money to an arbitrary PIX key or bank account. `rail` omitted or pix_payout places a Pix order; a Pix quote the provider prepares funding for (`deposit_mode` prepared) answers `awaiting_funds` with a `funding_action` the wallet signs, finished by POST /api/offramps/{id}/finalize, and `amount_brl` is what the recipient receives; Ozav Pix off-ramps return awaiting_funds with deposit_instructions first, and payout runs after the managed wallet is funded (Bridge unchanged). ach_payout or wire_payout places a payout to the registered US bank account the estimate quoted (`bank_beneficiary_id`), with none of the Pix fields; the order answers `awaiting_funds` with a `funding_action`, the Solana transaction the wallet signs, present while it is valid. Bank orders also carry `fiat_amount`/`fiat_currency` (the USD that lands), `bank_rail` and the account's `bank_account_last4`/`bank_routing_last4`; `amount_brl` is \"0\" on them. Repeating the create with the same estimate answers the same order (200) and the same action, never a second one. A create repeated while the first one's prepare is still in flight answers 200 with that order and no `funding_action` yet: fetch the order again (GET /api/offramps/{id}) to get it.
//	@Tags			offramps
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string								false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			request				body		rampsrequests.CreateOfframpRequest	true	"Order"
//	@Success		201					{object}	resources.OrderResource
//	@Success		200					{object}	resources.OrderResource
//	@Failure		401					{object}	responses.ErrorResponse				"unauthenticated: the account was deleted while this request was running"
//	@Failure		403					{object}	responses.ErrorResponse				"mfa_required"
//	@Failure		409					{object}	responses.ErrorResponse				"among the state refusals, estimate_expired, estimate_consumed and estimate_mismatch; rail_not_eligible (naming no provider) when the tax identity and residence the user declares now do not open the payout at the estimate's provider; beneficiary_changed (a bank rail: the beneficiary was edited after the estimate, quote again); customer_closed: the customer the header names is closed (it reads, it moves no money)"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; estimate_required; wallet_not_registered: a company's wallet is not one it registered (POST /api/wallets); holder_mismatch: a company pays out only to its own CNPJ key; amount_below_minimum or amount_above_maximum, naming the bound (limit_usd on a bank rail)"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		502					{object}	responses.ErrorResponse	"provider_unavailable: the provider failed, or (a bank rail) prepared a funding action this side refused; the estimate stays redeemable"
//	@Failure		503					{object}	responses.ErrorResponse	"rail_disabled: an operator closed the rail; no_provider_available"
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for; not_found: the estimate is not the customer's, or not for this rail; bank_beneficiary_not_found: the bank beneficiary is not the customer's or does not exist"
//	@Router			/api/offramps [post]
func (c *OfframpController) Create(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.CreateOfframpRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, created, err := c.service.Create(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx), dto.CreateOfframpDTO{
		EstimateOfframpDTO: dto.EstimateOfframpDTO{
			AmountToken: req.AmountToken, Token: req.Token, Chain: req.Chain,
			FromAddress: req.FromAddress, PixKey: req.PixKey, PixKeyType: req.PixKeyType,
			RecipientName: req.RecipientName, RecipientDocument: req.RecipientDocument,
			Rail: req.Rail, BankBeneficiaryID: req.BankBeneficiaryID,
		},
		EstimateID: req.EstimateID,
		Code:       req.Code,
		CodeType:   req.CodeType,
	})
	if err != nil {
		return mapError(ctx, err, "create offramp")
	}
	status := contractshttp.StatusOK
	if created {
		status = contractshttp.StatusCreated
	}
	return ctx.Response().Status(status).Json(resources.NewOrderResource(view))
}

// Show godoc
//
//	@Summary	Get one off-ramp order
//	@Tags		offramps
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		id					path		string	true	"Order external id"
//	@Success	200					{object}	resources.OrderResource
//	@Failure	404					{object}	responses.ErrorResponse	"not_found: no such order of the customer; customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure	422					{object}	responses.ValidationErrorResponse
//	@Failure	500					{object}	responses.ErrorResponse
//	@Router		/api/offramps/{id} [get]
func (c *OfframpController) Show(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ShowOrderRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.Show(ctx, middleware.CurrentCustomer(ctx).ID, req.ID)
	if err != nil {
		return mapError(ctx, err, "show offramp")
	}
	return ctx.Response().Success().Json(resources.NewOrderResource(view))
}

// SimulateDeposit godoc
//
//	@Summary		Simulate crypto deposit (sandbox only)
//	@Description	Completes an off-ramp order locally when Bridge runs in sandbox. No on-chain transfer.
//	@Tags			offramps
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			id					path		string	true	"Order external id"
//	@Success		200					{object}	resources.OrderResource
//	@Failure		404					{object}	responses.ErrorResponse	"not_found: no such order of the customer; customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		409					{object}	responses.ErrorResponse	"sandbox_only or invalid_order_state"
//	@Failure		422					{object}	responses.ValidationErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Router			/api/offramps/{id}/simulate-deposit [post]
func (c *OfframpController) SimulateDeposit(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.SimulateOfframpDepositRequest
	if response := requests.ValidatePath(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.SimulateDeposit(ctx, middleware.CurrentCustomer(ctx).ID, req.ID)
	if err != nil {
		return mapError(ctx, err, "simulate offramp deposit")
	}
	return ctx.Response().Success().Json(resources.NewOrderResource(view))
}

// Cancel godoc
//
//	@Summary		Cancel an off-ramp order nobody funded
//	@Description	Withdraws an order still in awaiting_funds, at the provider and here. The app calls it when the wallet cannot fund the deposit the order asks for (the first deposit to a Bridge address also pays its token account's rent), so the order does not sit open forever. The order answers as failed. Refused once the deposit is in.
//	@Tags			offramps
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			id					path		string	true	"Order external id"
//	@Success		200					{object}	resources.OrderResource
//	@Failure		404					{object}	responses.ErrorResponse	"not_found: no such order of the customer; customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		409					{object}	responses.ErrorResponse	"invalid_order_state"
//	@Failure		422					{object}	responses.ValidationErrorResponse
//	@Failure		502					{object}	responses.ErrorResponse
//	@Failure		503					{object}	responses.ErrorResponse	"no_provider_available: the provider cannot cancel"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Router			/api/offramps/{id}/cancel [post]
func (c *OfframpController) Cancel(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.CancelOfframpRequest
	if response := requests.ValidatePath(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.Cancel(ctx, middleware.CurrentCustomer(ctx).ID, req.ID)
	if err != nil {
		return mapError(ctx, err, "cancel offramp")
	}
	return ctx.Response().Success().Json(resources.NewOrderResource(view))
}

// RefreshFundingAction godoc
//
//	@Summary		Refresh a bank payout's funding action
//	@Description	The funding action the wallet signs lives for a short while; the order is fundable for as long as its quote. While the action has more than 15 seconds left it answers as it is, with no call to the provider. Otherwise the provider prepares again and the order answers a new action with a higher action_revision. Once the quote has lapsed the order answers funding_action_expired and is expired.
//	@Tags			offramps
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			id					path		string	true	"Order external id"
//	@Success		200					{object}	resources.OrderResource
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"not_found: no such order of the customer; customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		409					{object}	responses.ErrorResponse	"funding_action_expired: the order has no funding action to refresh (not a bank payout awaiting its funds, or its quote lapsed); customer_closed: the customer the header names is closed"
//	@Failure		422					{object}	responses.ValidationErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		502					{object}	responses.ErrorResponse	"provider_unavailable"
//	@Failure		503					{object}	responses.ErrorResponse	"no_provider_available"
//	@Router			/api/offramps/{id}/funding-action/refresh [post]
func (c *OfframpController) RefreshFundingAction(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.RefreshFundingActionRequest
	if response := requests.ValidatePath(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.RefreshFundingAction(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx), req.ID)
	if err != nil {
		return mapError(ctx, err, "refresh funding action")
	}
	return ctx.Response().Success().Json(resources.NewOrderResource(view))
}

// Finalize godoc
//
//	@Summary		Finalize a bank payout with the wallet's signed funding action
//	@Description	Sends the transaction the wallet signed (exactly as signed, base64) together with the action_revision it signed. The app does not broadcast: the provider creates the payout from it. The Idempotency-Key header (up to 128 characters) is required; the same key and body answers the order as it stands without a second call, the same key with another body is a 409. The answer is always the order: still awaiting_funds (without funding_action) while the outcome is not settled, or already processing. An outcome the provider left open is not an error; follow the order, which the server settles.
//	@Tags			offramps
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string									false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			Idempotency-Key		header		string									true	"Up to 128 characters; one per attempt, reused to repeat it"
//	@Param			id					path		string									true	"Order external id"
//	@Param			request				body		rampsrequests.FinalizeOfframpRequest	true	"The signed transaction and the revision of the action it signed"
//	@Success		200					{object}	resources.OrderResource
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse				"not_found: no such order of the customer; customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		409					{object}	responses.ErrorResponse				"funding_action_expired: the action_revision is not the current one, or the action or its quote lapsed (a lapsed quote expires the order); invalid_order_state: the order is not a bank payout awaiting its signature; idempotency_key_reused: the key was used with another body; customer_closed: the customer the header names is closed"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; invalid_signature: what was signed is not what was prepared, sign the current action again; idempotency_key_required: the Idempotency-Key header is missing or longer than 128 characters"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		502					{object}	responses.ErrorResponse	"provider_unavailable"
//	@Failure		503					{object}	responses.ErrorResponse	"rail_disabled: an operator closed the rail; no_provider_available"
//	@Router			/api/offramps/{id}/finalize [post]
func (c *OfframpController) Finalize(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.FinalizeOfframpRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.service.Finalize(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx), dto.FinalizeOfframpDTO{
		OrderID:           req.ID,
		SignedTransaction: req.SignedTransaction,
		ActionRevision:    req.ActionRevision,
		IdempotencyKey:    ctx.Request().Header("Idempotency-Key"),
	})
	if err != nil {
		return mapError(ctx, err, "finalize offramp")
	}
	return ctx.Response().Success().Json(resources.NewOrderResource(view))
}

// Index godoc
//
//	@Summary		List off-ramp orders
//	@Description	A summary list. It does not refresh against the provider — one upstream call per row would multiply the load by the page size for information GET /api/offramps/{id} already reconciles.
//	@Tags			offramps
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			page				query		int		false	"Page number"
//	@Param			per_page			query		int		false	"Items per page"
//	@Success		200					{object}	resources.OrderListResponse
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		422					{object}	responses.ValidationErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/offramps [get]
func (c *OfframpController) Index(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ListOrdersRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	page, perPage := requests.Paging(req.Page, req.PerPage)
	views, total, err := c.service.List(middleware.CurrentCustomer(ctx).ID, page, perPage)
	if err != nil {
		return mapError(ctx, err, "list offramps")
	}
	return ctx.Response().Success().Json(resources.NewOrderListResponse(views, page, perPage, total))
}
