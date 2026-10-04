package wallets

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	wallet "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

// WalletsController serves the dashboard wallet list, create, and activate routes.
type WalletsController struct {
	wallets       *walletrecords.Wallets
	members       *walletrecords.Members
	chains        *chainsvc.Service
	walletService func() *wallet.Service
}

func NewWalletsController(
	wallets *walletrecords.Wallets,
	chains *chainsvc.Service,
	walletService func() *wallet.Service,
	members *walletrecords.Members,
) *WalletsController {
	if wallets == nil {
		panic("dashboard wallets controller: wallets service is required")
	}
	if members == nil {
		panic("dashboard wallets controller: wallet members service is required")
	}
	if chains == nil {
		panic("dashboard wallets controller: chains service is required")
	}
	if walletService == nil || walletService() == nil {
		panic("dashboard wallets controller: wallet service is required")
	}
	return &WalletsController{
		wallets:       wallets,
		members:       members,
		chains:        chains,
		walletService: walletService,
	}
}

// ListWallets godoc
// @Summary      List all wallets
// @Description  Returns the account wallets with their network (testnet flag) and the native and configured token balances of the last refresh. Testnet wallets carry no USD value.
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Success      200  {object}  WalletListResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /v1/wallets [get]
func (ctrl *WalletsController) ListWallets(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "account is required",
		})
	}

	account, accountOK := requestctx.Account(ctx)
	role, _ := requestctx.AccountRole(ctx)
	if !accountOK || account == nil || account.ID != accountID {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "account is required",
		})
	}

	limit, offset := pagination.ParseParams(ctx, 20)
	var query requests.ListWalletsRequest
	query.Load(ctx)
	chain := query.Chain

	var (
		wallets []models.Wallet
		total   int64
		err     error
	)
	if policies.SeesEveryAccountWallet(role, account.ViewAllWallets) {
		wallets, total, err = ctrl.wallets.PaginateByAccount(ctx.Context(), accountID, chain, limit, offset)
	} else {
		userID, userOK := requestctx.UserID(ctx)
		if !userOK || userID == uuid.Nil {
			return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
		}
		wallets, total, err = ctrl.wallets.PaginateByAccountAndMember(ctx.Context(), accountID, userID, chain, limit, offset)
	}
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{
			"error": "failed to fetch wallets",
		})
	}
	items, err := controllers.LoadWalletListItems(ctx.Context(), wallets)
	if err != nil {
		slog.Error("load wallet list balances", "account", accountID, "error", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{
			"error": "failed to fetch wallet balances",
		})
	}
	return responses.Send(ctx, http.StatusOK, pagination.Response(items, total, limit, offset))
}

// GetWallet godoc
// @Summary      Get a wallet
// @Description  Returns a single wallet by its UUID
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        walletId   path      string  true  "Wallet UUID"  format(uuid)
// @Success      200  {object}  WalletView
// @Failure      400  {object}  ErrorResponse  "Invalid UUID"
// @Failure      404  {object}  ErrorResponse  "Wallet not found"
// @Router       /v1/wallets/{walletId} [get]
func (ctrl *WalletsController) GetWallet(ctx http.Context) http.Response {
	id, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}

	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{
			"error": "account is required",
		})
	}

	w, err := ctrl.wallets.FindByIDAndAccount(ctx.Context(), id, accountID)
	if err != nil || w == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{
			"error": "wallet not found",
		})
	}
	if resp := ctrl.hideUnlessVisible(ctx, w.ID); resp != nil {
		return resp
	}
	return ctx.Response().Success().Json(controllers.NewWalletView(w, controllers.ResolveWalletChainNetwork(ctx.Context(), w.Chain)))
}

// hideUnlessVisible answers 404 when view_all_wallets is off and the caller
// is a user or auditor who is not a member of this wallet. Owner and admin
// pass. A nil response means the wallet may be shown.
func (ctrl *WalletsController) hideUnlessVisible(ctx http.Context, walletID uuid.UUID) http.Response {
	account, ok := requestctx.Account(ctx)
	if !ok || account == nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "account is required"})
	}
	role, _ := requestctx.AccountRole(ctx)
	if policies.SeesEveryAccountWallet(role, account.ViewAllWallets) {
		return nil
	}
	userID, userOK := requestctx.UserID(ctx)
	if !userOK || userID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	_, err := ctrl.members.FindByWalletAndUser(ctx.Context(), walletID, userID)
	if err == nil {
		return nil
	}
	if errors.Is(err, models.ErrRepositoryNotFound) {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "wallet not found"})
	}
	return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch wallet"})
}

// CreateWalletAdmin creates a wallet from the admin panel with full MPC keygen.
// Returns keycard data including activation_code for the two-step setup flow.
func (ctrl *WalletsController) CreateWalletAdmin(ctx http.Context) http.Response {
	var req requests.CreateWalletAdminRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	if env, ok := requestctx.AccountEnvironment(ctx); ok && env != "" {
		chainRecord, _ := ctrl.chains.FindByID(ctx.Context(), req.Chain)
		if chainRecord != nil && chainRecord.IsTestnet != (env == models.EnvironmentTest) {
			return responses.Send(ctx, http.StatusForbidden, http.Json{"error": "chain not available in current environment"})
		}
	}

	accountID, _ := requestctx.AccountID(ctx)
	result, err := ctrl.walletService().CreateWallet(ctx.Context(), accountID, req.Chain, req.Label, req.Passphrase)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unknown chain") {
			return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": msg})
		}
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": msg})
	}

	return responses.Send(ctx, http.StatusCreated, http.Json{
		"wallet":             controllers.WalletBodyViewPtr(result.Wallet),
		"encrypted_user_key": result.EncryptedUserKey,
		"service_public_key": result.ServicePublicKey,
		"encrypted_passcode": result.EncryptedPasscode,
		"activation_code":    result.ActivationCode,
	})
}

// ActivateWallet confirms the user has saved their KeyCard by validating the activation code.
func (ctrl *WalletsController) ActivateWallet(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	var req requests.ActivateWalletRequest
	if resp := validateRequest(ctx, &req); resp != nil {
		return resp
	}

	_, err = ctrl.walletService().ActivateWallet(ctx.Context(), walletID, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, wallet.ErrWalletNotFound):
			return responses.Send(ctx, http.StatusNotFound, http.Json{"error": err.Error()})
		case errors.Is(err, wallet.ErrWalletAlreadyActive):
			return responses.Send(ctx, http.StatusConflict, http.Json{"error": err.Error()})
		case errors.Is(err, wallet.ErrInvalidActivationCode):
			return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
		default:
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal error"})
		}
	}

	return responses.Send(ctx, http.StatusOK, http.Json{"status": "active"})
}
