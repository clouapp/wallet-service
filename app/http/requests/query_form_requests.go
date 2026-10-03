package requests

import "github.com/goravel/framework/contracts/http"

// ConvertCurrencyRequest is the convert query. Amount stays a string; the
// handler still rejects a missing pair and a non-positive amount with 400.
type ConvertCurrencyRequest struct {
	Open
	From   string `form:"from" json:"from"`
	To     string `form:"to" json:"to"`
	Amount string `form:"amount" json:"amount"`
}

func (r *ConvertCurrencyRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("from", "to", "amount")
}

func (r *ConvertCurrencyRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.From = queryValue(ctx, "from", "")
	r.To = queryValue(ctx, "to", "")
	r.Amount = queryValue(ctx, "amount", "0")
}

// ListMyAccountsRequest is the my-accounts query. Limit and offset stay raw
// strings so pagination.ParseStrict keeps its 400 messages.
type ListMyAccountsRequest struct {
	Open
	Limit       string `form:"limit" json:"limit"`
	Offset      string `form:"offset" json:"offset"`
	Search      string `form:"search" json:"search"`
	Environment string `form:"environment" json:"environment"`
}

func (r *ListMyAccountsRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("limit", "offset", "search", "environment")
}

func (r *ListMyAccountsRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Limit = queryValue(ctx, "limit", "")
	r.Offset = queryValue(ctx, "offset", "")
	r.Search = queryValue(ctx, "search", "")
	r.Environment = queryValue(ctx, "environment", "")
}

// ListWalletTransactionsRequest is the wallet transaction list filter.
type ListWalletTransactionsRequest struct {
	Open
	Type   string `form:"type" json:"type"`
	Status string `form:"status" json:"status"`
}

func (r *ListWalletTransactionsRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("type", "status")
}

func (r *ListWalletTransactionsRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Type = queryValue(ctx, "type", "")
	r.Status = queryValue(ctx, "status", "")
}

// ListWalletsRequest is the optional chain filter on the wallet list.
type ListWalletsRequest struct {
	Open
	Chain string `form:"chain" json:"chain"`
}

func (r *ListWalletsRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("chain")
}

func (r *ListWalletsRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Chain = queryValue(ctx, "chain", "")
}

// ListWithdrawalsRequest is the optional status filter on the withdrawal list.
type ListWithdrawalsRequest struct {
	Open
	Status string `form:"status" json:"status"`
}

func (r *ListWithdrawalsRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("status")
}

func (r *ListWithdrawalsRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Status = queryValue(ctx, "status", "")
}

// ListTransactionsRequest is the external transaction list filter.
type ListTransactionsRequest struct {
	Open
	Chain  string `form:"chain" json:"chain"`
	Type   string `form:"type" json:"type"`
	Status string `form:"status" json:"status"`
	UserID string `form:"user_id" json:"user_id"`
}

func (r *ListTransactionsRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("chain", "type", "status", "user_id")
}

func (r *ListTransactionsRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Chain = queryValue(ctx, "chain", "")
	r.Type = queryValue(ctx, "type", "")
	r.Status = queryValue(ctx, "status", "")
	r.UserID = queryValue(ctx, "user_id", "")
}

// FeeEstimateRequest is the optional fee-estimate query. Amount stays a string;
// the estimator rejects a non-decimal amount.
type FeeEstimateRequest struct {
	Open
	Asset  string `form:"asset" json:"asset"`
	Amount string `form:"amount" json:"amount"`
	To     string `form:"to" json:"to"`
}

func (r *FeeEstimateRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("asset", "amount", "to")
}

func (r *FeeEstimateRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Asset = queryValue(ctx, "asset", "")
	r.Amount = queryValue(ctx, "amount", "")
	r.To = queryValue(ctx, "to", "")
}
