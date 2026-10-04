package requests

import "github.com/goravel/framework/contracts/http"

// ChainIDRequest is the chain id already read from the path, query, or body.
// An empty value stays a 400 from the handler; this request does not add a 422.
type ChainIDRequest struct {
	Open
	ChainID string `form:"chainId" json:"chainId"`
}

func (r *ChainIDRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("chainId")
}

func (r *ChainIDRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.ChainID = inputValue(ctx, "chainId")
}

// CurrencyCodeRequest is the currency code path parameter.
type CurrencyCodeRequest struct {
	Open
	Code string `form:"code" json:"code"`
}

func (r *CurrencyCodeRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("code")
}

func (r *CurrencyCodeRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Code = routeValue(ctx, "code")
}

// FeatureKeyRequest is the feature key path parameter.
// The flag body stays on AccountFeatureEnabled so the signature bytes are unchanged.
type FeatureKeyRequest struct {
	Open
	Key string `form:"key" json:"key"`
}

func (r *FeatureKeyRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("key")
}

func (r *FeatureKeyRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Key = trimmedRoute(ctx, "key")
}

// SettingsGroupRequest is the settings group path parameter.
// The document body stays on AccountSettingsDocument.
type SettingsGroupRequest struct {
	Open
	Group string `form:"group" json:"group"`
}

func (r *SettingsGroupRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("group")
}

func (r *SettingsGroupRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Group = trimmedRoute(ctx, "group")
}

// PlatformAccountSettingsRequest is the account id and group on
// GET /v1/platform/accounts/{accountId}/settings/{group}.
type PlatformAccountSettingsRequest struct {
	Open
	AccountID string `form:"accountId" json:"accountId"`
	Group     string `form:"group" json:"group"`
}

func (r *PlatformAccountSettingsRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("accountId", "group")
}

func (r *PlatformAccountSettingsRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.AccountID = trimmedRoute(ctx, "accountId")
	r.Group = trimmedRoute(ctx, "group")
}

// SettingsSectionRequest is the settings section path parameter.
// Reset and the section cache flush take no document.
type SettingsSectionRequest struct {
	Open
	Section string `form:"section" json:"section"`
}

func (r *SettingsSectionRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("section")
}

func (r *SettingsSectionRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Section = trimmedRoute(ctx, "section")
}

// WalletTransactionPathRequest is the transaction id path parameter.
// The handler looks the id up as a string; a bad id stays 404.
type WalletTransactionPathRequest struct {
	Open
	TxID string `form:"txId" json:"txId"`
}

func (r *WalletTransactionPathRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("txId")
}

func (r *WalletTransactionPathRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.TxID = routeValue(ctx, "txId")
}

// LookupAddressRequest is the on-chain address path and the optional chain query.
type LookupAddressRequest struct {
	Open
	Address string `form:"address" json:"address"`
	Chain   string `form:"chain" json:"chain"`
}

func (r *LookupAddressRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("address", "chain")
}

func (r *LookupAddressRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.Address = routeValue(ctx, "address")
	r.Chain = queryValue(ctx, "chain", "")
}

// ExternalIDRequest is the external user id path parameter.
type ExternalIDRequest struct {
	Open
	ExternalID string `form:"external_id" json:"external_id"`
}

func (r *ExternalIDRequest) Rules(http.Context) map[string]string {
	return optionalStringRules("external_id")
}

func (r *ExternalIDRequest) Load(ctx http.Context) {
	if r == nil {
		return
	}
	r.ExternalID = routeValue(ctx, "external_id")
}
