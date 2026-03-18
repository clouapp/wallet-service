package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/macromarkets/vault/app/providers"
	"github.com/macromarkets/vault/app/services/withdraw"
)

// ---------------------------------------------------------------------------
// Controller — thin HTTP layer. All logic lives in services.
// ---------------------------------------------------------------------------

type Controller struct {
	c *providers.Container
}

func New(c *providers.Container) *Controller {
	return &Controller{c: c}
}

func (ctrl *Controller) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/health", ctrl.health)
	r.GET("/chains", ctrl.listChains)

	r.POST("/wallets", ctrl.createWallet)
	r.GET("/wallets", ctrl.listWallets)
	r.GET("/wallets/:id", ctrl.getWallet)

	r.POST("/wallets/:id/addresses", ctrl.generateAddress)
	r.GET("/wallets/:id/addresses", ctrl.listWalletAddresses)
	r.GET("/addresses/:address", ctrl.lookupAddress)
	r.GET("/users/:external_id/addresses", ctrl.listUserAddresses)

	r.POST("/wallets/:id/withdrawals", ctrl.createWithdrawal)
	r.GET("/transactions", ctrl.listTransactions)
	r.GET("/transactions/:id", ctrl.getTransaction)
	r.GET("/users/:external_id/transactions", ctrl.listUserTransactions)

	r.POST("/webhooks", ctrl.createWebhook)
	r.GET("/webhooks", ctrl.listWebhooks)
}

// --- Health / Info ---

// health godoc
// @Summary Health check
// @Description Get API health status
// @Tags System
// @Produce json
// @Success 200 {object} map[string]string
// @Router /health [get]
func (ctrl *Controller) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "version": "0.1.0"})
}

// listChains godoc
// @Summary List supported chains
// @Description Get list of all supported blockchain networks
// @Tags Chains
// @Security ApiKeyAuth
// @Security SignatureAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "List of supported chains"
// @Router /chains [get]
func (ctrl *Controller) listChains(c *gin.Context) {
	type info struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		NativeAsset   string `json:"native_asset"`
		Confirmations uint64 `json:"required_confirmations"`
	}
	var chains []info
	for _, id := range ctrl.c.Registry.ChainIDs() {
		a, _ := ctrl.c.Registry.Chain(id)
		chains = append(chains, info{ID: a.ID(), Name: a.Name(), NativeAsset: a.NativeAsset(), Confirmations: a.RequiredConfirmations()})
	}
	c.JSON(http.StatusOK, gin.H{"data": chains})
}

// --- Wallets ---

// createWallet godoc
// @Summary Create a new wallet
// @Description Create a new wallet for a specific blockchain
// @Tags Wallets
// @Security ApiKeyAuth
// @Security SignatureAuth
// @Accept json
// @Produce json
// @Param request body object{chain=string,label=string} true "Wallet creation request"
// @Success 201 {object} map[string]interface{} "Created wallet"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 409 {object} map[string]string "Wallet already exists"
// @Router /wallets [post]
func (ctrl *Controller) createWallet(c *gin.Context) {
	var req struct {
		Chain string `json:"chain" binding:"required"`
		Label string `json:"label"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	w, err := ctrl.c.WalletService.CreateWallet(c.Request.Context(), req.Chain, req.Label)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, w)
}

// listWallets godoc
// @Summary List all wallets
// @Description Get list of all wallets
// @Tags Wallets
// @Security ApiKeyAuth
// @Security SignatureAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "List of wallets"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /wallets [get]
func (ctrl *Controller) listWallets(c *gin.Context) {
	ws, err := ctrl.c.WalletService.ListWallets(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ws})
}

func (ctrl *Controller) getWallet(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid wallet id"})
		return
	}
	w, err := ctrl.c.WalletService.GetWallet(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "wallet not found"})
		return
	}
	c.JSON(http.StatusOK, w)
}

// --- Addresses ---

func (ctrl *Controller) generateAddress(c *gin.Context) {
	walletID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid wallet id"})
		return
	}
	var req struct {
		ExternalUserID string `json:"external_user_id" binding:"required"`
		Metadata       string `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	addr, err := ctrl.c.WalletService.GenerateAddress(c.Request.Context(), walletID, req.ExternalUserID, req.Metadata)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Refresh Redis address cache for the chain
	if w, err := ctrl.c.WalletService.GetWallet(c.Request.Context(), walletID); err == nil {
		ctrl.c.DepositService.RefreshAddressCache(c.Request.Context(), w.Chain)
	}

	c.JSON(http.StatusCreated, addr)
}

func (ctrl *Controller) listWalletAddresses(c *gin.Context) {
	walletID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid wallet id"})
		return
	}
	addrs, err := ctrl.c.WalletService.ListWalletAddresses(c.Request.Context(), walletID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": addrs})
}

func (ctrl *Controller) lookupAddress(c *gin.Context) {
	address := c.Param("address")
	chainFilter := c.Query("chain")

	if chainFilter != "" {
		addr, err := ctrl.c.WalletService.LookupAddress(c.Request.Context(), chainFilter, address)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "address not found"})
			return
		}
		c.JSON(http.StatusOK, addr)
		return
	}

	// Try all chains
	for _, id := range ctrl.c.Registry.ChainIDs() {
		if addr, err := ctrl.c.WalletService.LookupAddress(c.Request.Context(), id, address); err == nil {
			c.JSON(http.StatusOK, addr)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "address not found"})
}

func (ctrl *Controller) listUserAddresses(c *gin.Context) {
	addrs, err := ctrl.c.WalletService.ListUserAddresses(c.Request.Context(), c.Param("external_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": addrs})
}

// --- Withdrawals ---

func (ctrl *Controller) createWithdrawal(c *gin.Context) {
	walletID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid wallet id"})
		return
	}
	var req struct {
		ExternalUserID string `json:"external_user_id" binding:"required"`
		ToAddress      string `json:"to_address" binding:"required"`
		Amount         string `json:"amount" binding:"required"`
		Asset          string `json:"asset" binding:"required"`
		IdempotencyKey string `json:"idempotency_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tx, err := ctrl.c.WithdrawalService.Request(c.Request.Context(), withdraw.WithdrawRequest{
		WalletID: walletID, ExternalUserID: req.ExternalUserID,
		ToAddress: req.ToAddress, Amount: req.Amount,
		Asset: req.Asset, IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, tx)
}

// --- Transactions ---

func (ctrl *Controller) listTransactions(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	txs, err := ctrl.c.WithdrawalService.ListTransactions(c.Request.Context(),
		c.Query("chain"), c.Query("type"), c.Query("status"), c.Query("user_id"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": txs})
}

func (ctrl *Controller) getTransaction(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tx id"})
		return
	}
	tx, err := ctrl.c.WithdrawalService.GetTransaction(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "transaction not found"})
		return
	}
	c.JSON(http.StatusOK, tx)
}

func (ctrl *Controller) listUserTransactions(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	txs, err := ctrl.c.WithdrawalService.ListTransactions(c.Request.Context(),
		"", "", "", c.Param("external_id"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": txs})
}

// --- Webhooks ---

func (ctrl *Controller) createWebhook(c *gin.Context) {
	var req struct {
		URL    string   `json:"url" binding:"required"`
		Secret string   `json:"secret" binding:"required"`
		Events []string `json:"events" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cfg, err := ctrl.c.WebhookService.CreateConfig(c.Request.Context(), req.URL, req.Secret, req.Events)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, cfg)
}

func (ctrl *Controller) listWebhooks(c *gin.Context) {
	configs, err := ctrl.c.WebhookService.ListConfigs(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": configs})
}
