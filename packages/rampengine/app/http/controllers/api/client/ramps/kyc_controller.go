package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/middleware"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	rampsrequests "github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/ramps"
	resources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// KycController exposes the multi-provider compliance flow.
type KycController struct {
	service      *kycsvc.ProviderKycService
	capabilities *kycsvc.CapabilitiesService
}

// NewKycController wires the controller.
func NewKycController(service *kycsvc.ProviderKycService, capabilities *kycsvc.CapabilitiesService) *KycController {
	return &KycController{service: service, capabilities: capabilities}
}

// Index godoc
//
//	@Summary		List KYC state per provider
//	@Description	One entry per enabled provider that serves the user, in provider order — the same set the submissions fan out to. A disabled provider's row is left out, and so is one that does not serve the tax identity and residence the user's retained profile declares (no retained profile: every enabled provider); an enabled provider the user never started appears as not_started. `needs_application` marks a provider holding no customer for the user: it owes the whole application, and `pending_steps` lists every step of it.
//	@Tags			kyc
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Success		200					{array}		resources.KycResource
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc [get]
func (c *KycController) Index(ctx contractshttp.Context) contractshttp.Response {
	customerID := middleware.CurrentCustomer(ctx).ID
	rows, err := c.service.List(customerID)
	if err != nil {
		return mapError(ctx, err, "list kyc")
	}
	return ctx.Response().Success().Json(resources.NewKycCollection(rows))
}

// Overview godoc
//
//	@Summary		One KYC state across every provider
//	@Description	The state the app renders, folded from every eligible provider (the list `GET /api/kyc` answers — only the providers that serve the user — repeated here as `providers`). Approved anywhere is `approved`; while any provider is still working — including a submission queued or on its way — it is `in_review`; `not_started` for a user who never submitted; `action_required` while any provider has something the user can send (`categories` lists the corrigible reasons, `pending_steps` the wizard steps, in wizard order); `rejected` only when every provider refused for good (`categories` is `["tax_id_in_use"]` or `["not_eligible"]`); `unavailable` when no provider is enabled, or none serves the user.
//	@Tags			kyc
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Success		200					{object}	resources.OverviewResource
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc/overview [get]
func (c *KycController) Overview(ctx contractshttp.Context) contractshttp.Response {
	customerID := middleware.CurrentCustomer(ctx).ID
	view, err := c.service.Overview(customerID)
	if err != nil {
		return mapError(ctx, err, "kyc overview")
	}
	return ctx.Response().Success().Json(resources.NewOverviewResource(view))
}

// RefreshOverview godoc
//
//	@Summary		Ask the providers still deciding, then answer the overview
//	@Description	The app's "Atualizar status". Re-reads, upstream, only the enabled providers serving the user whose row is still awaiting a decision (submitted, not approved, not refused for good, not in flight); a provider that fails keeps its stored row. Answers exactly what `GET /api/kyc/overview` answers.
//	@Tags			kyc
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Success		200					{object}	resources.OverviewResource
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Router			/api/kyc/refresh [post]
func (c *KycController) RefreshOverview(ctx contractshttp.Context) contractshttp.Response {
	view, err := c.service.RefreshEnabled(ctx, middleware.CurrentCustomer(ctx).ID)
	if err != nil {
		return mapError(ctx, err, "kyc refresh")
	}
	return ctx.Response().Success().Json(resources.NewOverviewResource(view))
}

// Show godoc
//
//	@Summary		Get KYC state at one provider
//	@Description	A user who has never touched the provider gets a synthesized not_started state; nothing is written for a read.
//	@Tags			kyc
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			provider			path		string	true	"Provider code"
//	@Success		200					{object}	resources.KycResource
//	@Failure		422					{object}	responses.ValidationErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc/{provider} [get]
func (c *KycController) Show(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ProviderPathRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	customerID := middleware.CurrentCustomer(ctx).ID
	row, err := c.service.Get(customerID, req.Provider)
	if err != nil {
		return mapError(ctx, err, "show kyc")
	}
	return ctx.Response().Success().Json(resources.NewKycResource(row))
}

// Capabilities godoc
//
//	@Summary		What the user may do right now, and who blocks the rest
//	@Description	Not folded into GET /me: that response is large and cached, and a capability changes on every provider webhook. `usable` is true when at least one capability is available — never "at least one provider approved", since an approved row can carry zero approved endorsements. `blocked_by.reason` is the code the operation itself would answer; `rail_not_eligible` names no provider and no steps — the tax identity and residence the user declared do not open that rail. `pending_providers` feeds the banner, naming only providers that serve the user's retained tax identity and residence; an empty list means nothing to show.
//	@Tags			kyc
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			funding_formats		query		string	false	"What the app can sign, comma-separated: deposit (default) and prepared. A Pix payout served only by a provider that prepares the funding is listed only when prepared is declared"
//	@Success		200					{object}	resources.CapabilitiesResource
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc/capabilities [get]
func (c *KycController) Capabilities(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.CapabilitiesRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	view, err := c.capabilities.ComputeFor(ctx, middleware.CurrentUser(ctx).ID, middleware.CurrentCustomer(ctx).ID, req.AcceptsPrepared())
	if err != nil {
		return mapError(ctx, err, "kyc capabilities")
	}
	return ctx.Response().Success().Json(resources.NewCapabilitiesResource(view))
}

// Profile godoc
//
//	@Summary		The user's retained profile fields
//	@Description	Only the fields ramp.Profile carries — never a photograph, never a `data:` URI — kept so a provider added later asks only for the images again. 204 when the user has never submitted an application.
//	@Tags			kyc
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Success		200					{object}	resources.ProfileResource
//	@Success		204					{object}	nil
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc/profile [get]
func (c *KycController) Profile(ctx contractshttp.Context) contractshttp.Response {
	customerID := middleware.CurrentCustomer(ctx).ID
	profile, err := c.service.Profile(customerID)
	if err != nil {
		return mapError(ctx, err, "kyc profile")
	}
	if profile == nil {
		return ctx.Response().NoContent(contractshttp.StatusNoContent)
	}
	return ctx.Response().Success().Json(resources.NewProfileResource(profile))
}

// Countries godoc
//
//	@Summary		The countries the KYC form serves
//	@Description	Every ISO 3166-1 country where an enabled provider opens a Pix rail to some tax identity, sorted by `code` (alpha-3; the app names it from its locale). `form_profile` picks the form (`br` or `generic`); `postal_code_pattern` is the anchored regular expression a postal code must match, null for a country that issues none; `subdivisions` lists the ones an address must name, empty when it need not. `tax_identities` are what a resident may declare — `cpf` in Brazil, `cpf` or `passport` elsewhere — each with the `rails` it opens (`pix_payin` before `pix_payout`), the `document_types` that prove it and the `providers` that serve it (a step only one provider requires, such as Ozav's bank statement, is owed only when that provider is listed); empty where no resident is served — the United States while its residence flag is off — and the country is then listed for its companies alone. `business_tax_identities` are what a company incorporated there may declare, with its `rails`, `providers`, `document_purposes` (a company with a website) and `document_purposes_without_website` (one that declares none); `company_types` what its form offers. No enabled provider: an empty list.
//	@Tags			kyc
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Success		200					{object}	resources.CountryCollection
//	@Failure		401					{object}	responses.ErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc/countries [get]
func (c *KycController) Countries(ctx contractshttp.Context) contractshttp.Response {
	countries, err := c.service.Countries(ctx)
	if err != nil {
		return mapError(ctx, err, "kyc countries")
	}
	return ctx.Response().Success().Json(resources.NewCountryCollection(countries))
}

// Requirements godoc
//
//	@Summary	What the user still owes the provider
//	@Tags		kyc
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		provider			path		string	true	"Provider code"
//	@Success	200					{object}	resources.RequirementsResource
//	@Failure	409					{object}	responses.ErrorResponse
//	@Failure	422					{object}	responses.ValidationErrorResponse
//	@Failure	500					{object}	responses.ErrorResponse
//	@Failure	404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router		/api/kyc/{provider}/requirements [get]
func (c *KycController) Requirements(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ProviderPathRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	customerID := middleware.CurrentCustomer(ctx).ID
	view, err := c.service.Requirements(ctx, customerID, req.Provider)
	if err != nil {
		return mapError(ctx, err, "kyc requirements")
	}
	return ctx.Response().Success().Json(resources.NewRequirementsResource(view))
}

// StartTos godoc
//
//	@Summary	Issue the provider's terms-of-service page
//	@Tags		kyc
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string							false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		provider			path		string							true	"Provider code"
//	@Param		request				body		rampsrequests.StartTosRequest	true	"Redirect target"
//	@Success	201					{object}	resources.TosSessionResource
//	@Failure	409					{object}	responses.ErrorResponse				"tos_not_required; or customer_closed: the customer X-Xip-Customer-Id names is closed; or, retrying a rejected customer of a provider that creates customers atomically, virtual_account_active / transaction_open while money can still move on it, submission_in_flight / submission_held while its create is active"
//	@Failure	422					{object}	responses.ValidationErrorResponse	"validation failed; or {\"error\":\"jurisdiction_not_served\"} when the provider does not serve the tax identity and residence the retained profile declares"
//	@Failure	500					{object}	responses.ErrorResponse
//	@Failure	502					{object}	responses.ErrorResponse
//	@Failure	404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router		/api/kyc/{provider}/tos-sessions [post]
func (c *KycController) StartTos(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.StartTosRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	customerID := middleware.CurrentCustomer(ctx).ID
	row, err := c.service.StartTos(ctx, customerID, req.Provider, req.RedirectURI)
	if err != nil {
		return mapError(ctx, err, "start tos")
	}
	return ctx.Response().Status(contractshttp.StatusCreated).
		Json(resources.NewTosSessionResource(row))
}

// SubmitApplication godoc
//
//	@Summary		Submit or resubmit the KYC application
//	@Description	Accepted and queued: the provider call runs on a worker, and the outcome arrives on the SSE stream as an `kyc.updated` frame. Poll GET /api/kyc/{provider} if the stream is not connected. A provider that refused the user for good (`terminal` on its KYC state) answers 409 `kyc_rejected` and receives nothing. With a business's X-Xip-Customer-Id the body is the KYB application (SubmitBusinessApplicationRequest, API contract §5.2): a provider taking no company answers 503 `no_provider_available`, a field refused past the form request 422 `validation failed` or `profile_incomplete` in the validation shape, and a document another business past draft took 409 `business_exists`.
//	@Tags			kyc
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string									false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			provider			path		string									true	"Provider code"
//	@Param			request				body		rampsrequests.SubmitApplicationRequest	true	"Identity profile"
//	@Success		202					{object}	resources.KycResource
//	@Failure		409					{object}	responses.ErrorResponse				"tax_id_in_use (the CPF belongs to another XIP account; no resubmission fixes it) or phone_in_use (the phone does; correct the field), both before any provider is asked; also submission_in_flight, tos_required; or customer_closed: the customer X-Xip-Customer-Id names is closed"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; or {\"error\":\"jurisdiction_not_served\"} when the provider does not serve the tax identity and residence the application declares, before anything is staged; or {\"error\":\"kyc_data_invalid\"} when the provider would refuse the application as sent (a provider that creates its customer with the documents checks it now and holds it)"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc/{provider}/applications [post]
//	@Router			/api/kyc/{provider}/applications [patch]
func (c *KycController) SubmitApplication(ctx contractshttp.Context) contractshttp.Response {
	if actsForBusiness(ctx) {
		return c.submitBusinessApplication(ctx)
	}
	var req rampsrequests.SubmitApplicationRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	row, err := c.service.SubmitApplication(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx),
		req.Provider, req.Input())
	if err != nil {
		return mapError(ctx, err, "submit application")
	}
	// 202, not 201: the provider has not seen this yet.
	return ctx.Response().Status(contractshttp.StatusAccepted).
		Json(resources.NewKycResource(row))
}

// SubmitApplicationFanOut godoc
//
//	@Summary		Submit the KYC application to every enabled provider
//	@Description	Accepted and queued, one job per enabled and implemented provider — the client sends one form, and the app does not name a provider because it cannot know which ones exist. Each provider's own outcome arrives on its own SSE `kyc.updated` frame; poll GET /api/kyc if the stream is not connected. Accepted whenever at least one provider took it; a refusal is reported per provider in the body, and that provider alone can be retried through POST /api/kyc/{provider}/applications. A `submission_in_flight` error on one entry of `submissions` means that provider already has a submission in progress from an earlier call, not that it refused this one — it must not be rendered as a failure. With a business's X-Xip-Customer-Id the body is the KYB application (SubmitBusinessApplicationFanOutRequest, API contract §5.2), sent to the providers taking companies; it is kept with its people before any is asked. Refusals before any provider: 422 `validation failed` or `profile_incomplete` in the validation shape, 422 `jurisdiction_not_served`, 409 `business_exists`, all with no list.
//	@Tags			kyc
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string											false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			request				body		rampsrequests.SubmitApplicationFanOutRequest	true	"Identity profile"
//	@Success		202					{object}	resources.SubmissionListResponse
//	@Failure		409					{object}	resources.SubmissionListResponse	"tos_required, with the per-provider list; or tax_id_in_use (the CPF belongs to another XIP account; no resubmission fixes it) / phone_in_use (the phone does; correct the field) alone, with no list, because no provider was asked; or customer_closed: the customer X-Xip-Customer-Id names is closed"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; or {\"error\":\"jurisdiction_not_served\"}, with no list, when no enabled provider serves the tax identity and residence the application declares — nothing is retained and no provider is asked"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		503					{object}	resources.SubmissionListResponse	"no_provider_available"
//	@Failure		404					{object}	responses.ErrorResponse				"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc/applications [post]
func (c *KycController) SubmitApplicationFanOut(ctx contractshttp.Context) contractshttp.Response {
	if actsForBusiness(ctx) {
		return c.submitBusinessApplicationFanOut(ctx)
	}
	var req rampsrequests.SubmitApplicationFanOutRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	outcomes, err := c.service.SubmitApplicationToEnabled(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx),
		req.Input())
	if err != nil && len(outcomes) == 0 {
		// The fan-out never ran, so there is nothing per provider to report.
		return mapError(ctx, err, "submit application fan-out")
	}
	return fanOutResponse(ctx, outcomes, err, "submit application fan-out")
}

// SubmitDocuments godoc
//
//	@Summary		Submit identity documents
//	@Description	Accepted and queued, like the application. The images never travel through the queue — only a handle to an encrypted, short-lived staging entry does. A provider that refused the user for good answers 409 `kyc_rejected`. With a business's X-Xip-Customer-Id the body is the KYB documents (SubmitBusinessDocumentsRequest, API contract §6.1): each person's identity document by the local_ref of the last application, the company's by purpose.
//	@Tags			kyc
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string									false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			provider			path		string									true	"Provider code"
//	@Param			request				body		rampsrequests.SubmitDocumentsRequest	true	"Document images"
//	@Success		202					{object}	resources.KycResource
//	@Failure		409					{object}	responses.ErrorResponse				"kyc_rejected, submission_in_flight; or application_required when the provider creates its customer from the application and the documents together and no application is held for these documents; or submission_held when the provider's customer create for this person is held for manual review; or customer_closed: the customer X-Xip-Customer-Id names is closed"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; or {\"error\":\"documents_required\"} when none of image_front, proof_of_address, bank_statement or selfie was sent, or {\"error\":\"document_type_required\"} when image_front was sent without document_type, or {\"error\":\"jurisdiction_not_served\"} when the provider does not serve the tax identity and residence the retained profile declares, or {\"error\":\"kyc_data_invalid\"} when the provider would refuse the documents as sent — or, for a provider that records it, the request's IP address — before anything is staged"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc/{provider}/documents [post]
func (c *KycController) SubmitDocuments(ctx contractshttp.Context) contractshttp.Response {
	if actsForBusiness(ctx) {
		return c.submitBusinessDocuments(ctx)
	}
	var req rampsrequests.SubmitDocumentsRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	row, err := c.service.SubmitDocuments(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx).ID,
		req.Provider, documentsInput(ctx, req.Input()))
	if err != nil {
		return mapError(ctx, err, "submit documents")
	}
	return ctx.Response().Status(contractshttp.StatusAccepted).
		Json(resources.NewKycResource(row))
}

// SubmitDocumentsFanOut godoc
//
//	@Summary		Submit identity documents to every enabled provider
//	@Description	Accepted and queued, one job per enabled and implemented provider — same fan-out shape as POST /api/kyc/applications. Each provider's own outcome arrives on its own SSE `kyc.updated` frame; poll GET /api/kyc if the stream is not connected. Accepted whenever at least one provider took it; a refusal is reported per provider in the body, and that provider alone can be retried through POST /api/kyc/{provider}/documents. A `submission_in_flight` error on one entry of `submissions` means that provider already has a submission in progress from an earlier call, not that it refused this one — it must not be rendered as a failure. With a business's X-Xip-Customer-Id the body is the KYB documents (SubmitBusinessDocumentsFanOutRequest, API contract §6.1); a local_ref the last application did not name answers 422 in the validation shape.
//	@Tags			kyc
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string										false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			request				body		rampsrequests.SubmitDocumentsFanOutRequest	true	"Document images"
//	@Success		202					{object}	resources.SubmissionListResponse
//	@Failure		409					{object}	resources.SubmissionListResponse	"the per-provider list travels with the code; or customer_closed: the customer X-Xip-Customer-Id names is closed"
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; or {\"error\":\"documents_required\"} when none of image_front, proof_of_address, bank_statement or selfie was sent, or {\"error\":\"document_type_required\"} when image_front was sent without document_type, or {\"error\":\"jurisdiction_not_served\"} when no enabled provider serves the tax identity and residence the retained profile declares, or {\"error\":\"kyc_data_invalid\"}, with the per-provider list, when the only provider asked would refuse the documents as sent"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		503					{object}	resources.SubmissionListResponse	"no_provider_available"
//	@Failure		404					{object}	responses.ErrorResponse				"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc/documents [post]
func (c *KycController) SubmitDocumentsFanOut(ctx contractshttp.Context) contractshttp.Response {
	if actsForBusiness(ctx) {
		return c.submitBusinessDocumentsFanOut(ctx)
	}
	var req rampsrequests.SubmitDocumentsFanOutRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	outcomes, err := c.service.SubmitDocumentsToEnabled(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx).ID,
		documentsInput(ctx, req.Input()))
	if err != nil && len(outcomes) == 0 {
		// The fan-out never ran, so there is nothing per provider to report.
		return mapError(ctx, err, "submit documents fan-out")
	}
	return fanOutResponse(ctx, outcomes, err, "submit documents fan-out")
}

// Refresh godoc
//
//	@Summary	Re-read the provider's compliance state
//	@Tags		kyc
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string	false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		provider			path		string	true	"Provider code"
//	@Success	200					{object}	resources.KycResource
//	@Failure	409					{object}	responses.ErrorResponse
//	@Failure	422					{object}	responses.ValidationErrorResponse
//	@Failure	500					{object}	responses.ErrorResponse
//	@Failure	404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router		/api/kyc/{provider}/refresh [post]
func (c *KycController) Refresh(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.ProviderPathRequest
	if response := requests.ValidatePath(ctx, &req); response != nil {
		return response
	}
	customerID := middleware.CurrentCustomer(ctx).ID
	row, err := c.service.Refresh(ctx, customerID, req.Provider)
	if err != nil {
		return mapError(ctx, err, "refresh kyc")
	}
	return ctx.Response().Success().Json(resources.NewKycResource(row))
}

// RequestEndorsements godoc
//
//	@Summary	Ask the provider for additional capabilities
//	@Tags		kyc
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		X-Xip-Customer-Id	header		string										false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param		provider			path		string										true	"Provider code"
//	@Param		request				body		rampsrequests.RequestEndorsementsRequest	true	"Endorsement names"
//	@Success	200					{object}	resources.KycResource
//	@Failure	409					{object}	responses.ErrorResponse				"customer_closed: the customer X-Xip-Customer-Id names is closed"
//	@Failure	422					{object}	responses.ValidationErrorResponse	"validation failed; or {\"error\":\"jurisdiction_not_served\"} when the provider does not serve the tax identity and residence the retained profile declares"
//	@Failure	500					{object}	responses.ErrorResponse
//	@Failure	404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router		/api/kyc/{provider}/endorsements [post]
func (c *KycController) RequestEndorsements(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.RequestEndorsementsRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	customerID := middleware.CurrentCustomer(ctx).ID
	row, err := c.service.RequestEndorsements(ctx, customerID, req.Provider, req.Endorsements)
	if err != nil {
		return mapError(ctx, err, "request endorsements")
	}
	return ctx.Response().Success().Json(resources.NewKycResource(row))
}

// HostedLink godoc
//
//	@Summary		Issue a hosted verification page
//	@Description	The fallback when the document flow cannot complete, and the primary flow for providers that prefer it.
//	@Tags			kyc
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string							false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			provider			path		string							true	"Provider code"
//	@Param			request				body		rampsrequests.HostedLinkRequest	true	"Link options"
//	@Success		201					{object}	resources.HostedLinkResource
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; or {\"error\":\"jurisdiction_not_served\"} when the provider does not serve the tax identity and residence the retained profile declares"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		502					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		409					{object}	responses.ErrorResponse	"customer_closed: the customer X-Xip-Customer-Id names is closed"
//	@Router			/api/kyc/{provider}/hosted-link [post]
func (c *KycController) HostedLink(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.HostedLinkRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	row, err := c.service.HostedLink(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx).ID, req.Provider, dto.HostedLinkDTO{
		FullName:     req.FullName,
		RedirectURI:  req.RedirectURI,
		Reason:       "client_request",
		Endorsements: req.Endorsements,
	})
	if err != nil {
		return mapError(ctx, err, "hosted link")
	}
	return ctx.Response().Status(contractshttp.StatusCreated).
		Json(resources.NewHostedLinkResource(row))
}

// StartTosFanOut godoc
//
//	@Summary		Open a terms-of-service session at every provider that has terms
//	@Description	One call, one session per enabled provider that requires terms and has not had them accepted yet, in provider order. Send `country`, `tax_id_type` and `tax_id_country` — the identity about to be declared, derived as an application's when the type or issuing country is omitted — and only the providers that serve that person are offered; without `country` every enabled provider is. A provider with no terms contributes nothing rather than an error, so an empty list is a valid answer — and the normal one for a stack whose providers have no terms. Each session carries a `mode`: `hosted` means the provider serves the page, signs the terms there and posts an agreement id back, and the session also carries `allowed_hosts` — the set of hosts the client may let its WebView reach, since it renders that page with a JavaScript bridge attached. `attested` means the provider hands over a document and takes XIP's word for the acceptance: the client presents the terms itself, with a checkbox, and reports it to `/api/kyc/tos-acceptances` with `legal_name` instead of an agreement id. Send `modes` with the shapes this client can present; omitting it means `hosted` only, and a provider the client cannot render is simply not listed.
//	@Tags			kyc
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string								false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			request				body		rampsrequests.StartTosFanOutRequest	true	"Redirect target"
//	@Success		201					{object}	resources.TosSessionListResponse
//	@Failure		422					{object}	responses.ValidationErrorResponse	"validation failed; or {\"error\":\"jurisdiction_not_served\"} when no enabled provider serves the declared tax identity and residence"
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		503					{object}	responses.ErrorResponse	"no_provider_available"
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Failure		409					{object}	responses.ErrorResponse	"customer_closed: the customer X-Xip-Customer-Id names is closed"
//	@Router			/api/kyc/tos-sessions [post]
func (c *KycController) StartTosFanOut(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.StartTosFanOutRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	customerID := middleware.CurrentCustomer(ctx).ID

	sessions, err := c.service.StartTosForEnabled(ctx, customerID, req.Input())
	if err != nil {
		return mapError(ctx, err, "start tos fan-out")
	}
	return ctx.Response().Status(contractshttp.StatusCreated).
		Json(resources.TosSessionListResponse{Sessions: resources.NewTosSessionCollection(sessions)})
}

// RecordTosAcceptances godoc
//
//	@Summary		Record the terms each provider's page returned
//	@Description	One entry per provider whose terms the user accepted, reported as each acceptance happens rather than batched at the end — a user who accepts one provider's terms and closes the app must not be asked to accept them twice. The agreement is issued by one provider and means nothing at another, which is why it is recorded per provider here instead of travelling on the application body that fans out to all of them.
//	@Tags			kyc
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			X-Xip-Customer-Id	header		string										false	"The customer the request acts for: a business the user is a member of; without it, the user's individual customer"
//	@Param			request				body		rampsrequests.RecordTosAcceptancesRequest	true	"Signed agreements"
//	@Success		200					{array}		resources.KycResource
//	@Failure		409					{object}	responses.ErrorResponse	"tos_not_required; or customer_closed: the customer X-Xip-Customer-Id names is closed"
//	@Failure		422					{object}	responses.ValidationErrorResponse
//	@Failure		500					{object}	responses.ErrorResponse
//	@Failure		404					{object}	responses.ErrorResponse	"customer_not_found: X-Xip-Customer-Id names no customer the user acts for"
//	@Router			/api/kyc/tos-acceptances [post]
func (c *KycController) RecordTosAcceptances(ctx contractshttp.Context) contractshttp.Response {
	var req rampsrequests.RecordTosAcceptancesRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	// The evidence of where the acceptance came from is taken from the request
	// rather than its body: a client that could name its own IP could name
	// anyone's, and this travels upstream as XIP's attestation.
	ipAddress, userAgent := ctx.Request().Ip(), ctx.Request().Header("User-Agent")

	acceptances := make([]kycsvc.TosAcceptance, 0, len(req.Acceptances))
	for _, input := range req.Acceptances {
		acceptances = append(acceptances, kycsvc.TosAcceptance{
			Provider:          input.Provider,
			SignedAgreementID: input.SignedAgreementID,
			LegalName:         input.LegalName,
			IPAddress:         ipAddress,
			UserAgent:         userAgent,
		})
	}

	rows, err := c.service.RecordTosAcceptances(ctx, middleware.CurrentUser(ctx), middleware.CurrentCustomer(ctx).ID,
		acceptances)
	if err != nil {
		return mapError(ctx, err, "record tos acceptances")
	}
	return ctx.Response().Success().Json(resources.NewKycCollection(rows))
}

// documentsInput is the documents as the client sent them, with the address
// the request came from: a provider that creates its customer with the
// documents records it. It comes from ctx.Request().Ip(), which the ClientIP
// middleware made the address the trusted proxy vouches for, and never from
// the body — the DTO field does not bind. The service canonicalises it.
func documentsInput(ctx contractshttp.Context, input dto.DocumentsDTO) dto.DocumentsDTO {
	input.ClientIPAddress = ctx.Request().Ip()
	return input
}
