package chains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	activitylog "github.com/macrowallets/waas/app/services/activity"
	"github.com/macrowallets/waas/pkg/numeric"
)

const (
	fieldGasReadiness  = "gas_readiness_threshold_raw"
	fieldDustNative    = "dust_threshold_native_raw"
	fieldDustUSD       = "dust_threshold_usd"
	fieldConfirmations = "required_confirmations"
	fieldFinality      = "finality"

	maxRawDigits = 78
	// maxConfirmations is the largest chains.required_confirmations (INTEGER).
	maxConfirmations = math.MaxInt32
)

const (
	kindMissing = iota
	kindNull
	kindString
	kindNumber
)

// ThresholdCatalog is the code catalog entry PATCH /v1/platform/chains/{chainId}
// and PATCH /v1/platform/chains/{chainId}/rpc already use. The three threshold
// columns stay on chains, not a settings group. S1.4.7 names sweep.view and
// sweep.update on those columns, and chains.view and chains.update on this
// entry. Holding settings.update is not either pair. The RPC URL is write-only
// and is never returned. There is no platform permission catalog, so a
// platform_admins row stands in. The chains pair is not a second gate.
type ThresholdCatalog struct {
	ViewPermission        string
	UpdatePermission      string
	ChainViewPermission   string
	ChainUpdatePermission string
	ReturnsRPCURL         bool
	Fields                []string
}

// ChainThresholdCatalog is the code catalog entry both chain PATCH routes use.
func ChainThresholdCatalog() ThresholdCatalog {
	return ThresholdCatalog{
		ViewPermission:        policies.PermSweepView,
		UpdatePermission:      policies.PermSweepUpdate,
		ChainViewPermission:   policies.PermChainsView,
		ChainUpdatePermission: policies.PermChainsUpdate,
		ReturnsRPCURL:         false,
		Fields:                []string{fieldGasReadiness, fieldDustNative, fieldDustUSD},
	}
}

func requireSweepThresholdPair(catalog ThresholdCatalog) error {
	if catalog.ViewPermission == "" || catalog.UpdatePermission == "" {
		return fmt.Errorf("chain thresholds: sweep permissions are required")
	}
	if catalog.ViewPermission == policies.PermSettingsUpdate || catalog.UpdatePermission == policies.PermSettingsUpdate ||
		catalog.ViewPermission == policies.PermSettingsView || catalog.UpdatePermission == policies.PermSettingsView {
		return fmt.Errorf("chain thresholds: settings permissions are not the sweep pair")
	}
	if catalog.ViewPermission == catalog.UpdatePermission {
		return fmt.Errorf("chain thresholds: sweep.view and sweep.update must differ")
	}
	if len(catalog.Fields) != 3 {
		return fmt.Errorf("chain thresholds: sweep columns are required")
	}
	return nil
}

func requireChainPair(catalog ThresholdCatalog) error {
	if catalog.ChainViewPermission != policies.PermChainsView || catalog.ChainUpdatePermission != policies.PermChainsUpdate {
		return fmt.Errorf("chain routes: chains.view and chains.update are required")
	}
	if catalog.ChainViewPermission == policies.PermSettingsUpdate || catalog.ChainUpdatePermission == policies.PermSettingsUpdate ||
		catalog.ChainViewPermission == policies.PermSettingsView || catalog.ChainUpdatePermission == policies.PermSettingsView ||
		catalog.ChainViewPermission == policies.PermSweepView || catalog.ChainUpdatePermission == policies.PermSweepUpdate ||
		catalog.ChainViewPermission == policies.PermSweepUpdate || catalog.ChainUpdatePermission == policies.PermSweepView {
		return fmt.Errorf("chain routes: settings.update is not chains.update")
	}
	if catalog.ChainViewPermission == catalog.ChainUpdatePermission {
		return fmt.Errorf("chain routes: chains.view and chains.update must differ")
	}
	if catalog.ReturnsRPCURL {
		return fmt.Errorf("chain routes: the RPC URL is never returned")
	}
	return nil
}

// ThresholdStore reads one chain and writes only the threshold columns a
// patch names. A nil column pointer leaves that column unchanged.
type ThresholdStore interface {
	FindByID(ctx context.Context, id string) (*models.Chain, error)
	UpdateThresholds(ctx context.Context, id string, write models.ChainThresholdWrite) error
}

// PlatformAdmins reports whether a dashboard user may edit chain thresholds.
// A missing row is not an admin. The check is not cached.
type PlatformAdmins interface {
	Contains(ctx context.Context, userID uuid.UUID) (bool, error)
}

// ThresholdView is the stored chain settings row: the sweep thresholds, the
// confirmation count and the finality policy. The RPC URL is not included.
type ThresholdView struct {
	ID                       string `json:"id"`
	GasReadinessThresholdRaw string `json:"gas_readiness_threshold_raw"`
	DustThresholdNativeRaw   string `json:"dust_threshold_native_raw"`
	DustThresholdUSD         string `json:"dust_threshold_usd"`
	RequiredConfirmations    int    `json:"required_confirmations"`
	Finality                 string `json:"finality"`
}

// Thresholds edits the three sweep columns S1.4.4 keeps on the chain row.
type Thresholds struct {
	store    ThresholdStore
	admins   PlatformAdmins
	activity activitylog.Writer
}

// ThresholdDeps is everything the platform chain-threshold editor needs.
// Store, Admins and Activity are required.
type ThresholdDeps struct {
	Store    ThresholdStore
	Admins   PlatformAdmins
	Activity activitylog.Writer
}

// NewThresholds builds the platform chain-threshold editor. The store, the
// platform-admin lookup and the activity log are required.
func NewThresholds(deps ThresholdDeps) *Thresholds {
	if deps.Store == nil {
		panic("chain thresholds: store is required")
	}
	if deps.Admins == nil {
		panic("chain thresholds: platform admins are required")
	}
	if deps.Activity == nil {
		panic("chain thresholds: activity log is required")
	}
	return &Thresholds{store: deps.Store, admins: deps.Admins, activity: deps.Activity}
}

// Update writes the threshold fields present in body. An unknown chain is
// ErrNotFound before the platform-admin check. A caller who is not a platform
// admin is ErrPlatformForbidden. S1.4.7 names chains.view and chains.update
// on this catalog entry. This branch has no platform permission catalog, so
// the gate stays the platform_admins row. The pair is not a second gate.
// The columns still declare sweep.view and sweep.update. Holding
// settings.update is not chains.update.
// A negative amount or a confirmation count below 1 is a ValidationError and
// is not stored. An omitted field leaves that column unchanged. An empty gas
// string is stored only for a bitcoin chain, where it is the sentinel.
// finality is stored only when the chain's adapter can read it (finalized_tag on
// EVM, solidified on TRON). Moving a chain off finalized_tag or solidified to
// confirmations needs required_confirmations in the same patch: the count such a
// chain keeps is only its finality lag. Both take effect for the transactions
// recorded after the chain registry next loads the row; a transaction keeps the
// count and the policy it was recorded under.
func (s *Thresholds) Update(ctx context.Context, actorID uuid.UUID, chainID string, body map[string]json.RawMessage) (ThresholdView, error) {
	if ctx == nil {
		return ThresholdView{}, fmt.Errorf("update chain thresholds: context is required")
	}
	chainID = strings.TrimSpace(chainID)
	if chainID == "" {
		return ThresholdView{}, fmt.Errorf("update chain thresholds: chain id is required")
	}
	if s == nil || s.store == nil {
		return ThresholdView{}, fmt.Errorf("chain thresholds: store is required")
	}
	if s.admins == nil {
		return ThresholdView{}, fmt.Errorf("chain thresholds: platform admins are required")
	}
	if s.activity == nil {
		return ThresholdView{}, fmt.Errorf("chain thresholds: activity log is required")
	}
	chain, err := s.store.FindByID(ctx, chainID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return ThresholdView{}, ErrNotFound
		}
		return ThresholdView{}, err
	}
	if chain == nil || chain.ID == "" {
		return ThresholdView{}, ErrNotFound
	}
	if actorID == uuid.Nil {
		return ThresholdView{}, fmt.Errorf("update chain thresholds: actor is required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return ThresholdView{}, err
	}
	if !admin {
		return ThresholdView{}, ErrPlatformForbidden
	}
	catalog := ChainThresholdCatalog()
	if err := requireSweepThresholdPair(catalog); err != nil {
		return ThresholdView{}, err
	}
	if err := requireChainPair(catalog); err != nil {
		return ThresholdView{}, err
	}
	if body == nil {
		body = map[string]json.RawMessage{}
	}
	write, fields, invalid := validateThresholds(chain, body)
	if invalid != nil && !invalid.empty() {
		return ThresholdView{}, invalid
	}
	if len(fields) == 0 {
		return thresholdView(chain)
	}
	var stored *models.Chain
	err = s.activity.Within(ctx, func(ctx context.Context) error {
		if err := s.store.UpdateThresholds(ctx, chain.ID, write); err != nil {
			return err
		}
		reloaded, err := s.store.FindByID(ctx, chain.ID)
		if err != nil {
			return err
		}
		if reloaded == nil || reloaded.ID == "" {
			return ErrNotFound
		}
		stored = reloaded
		meta, err := activitylog.ChainThresholdsChange(chain.ID, fields)
		if err != nil {
			return err
		}
		return s.activity.Append(ctx, models.AccountActivity{
			ActorUserID: actorID,
			Action:      activitylog.ActionChainsUpdated,
			TargetType:  activitylog.TargetChain,
			TargetID:    chain.ID,
			Metadata:    meta,
		})
	})
	if err != nil {
		return ThresholdView{}, err
	}
	return thresholdView(stored)
}

func validateThresholds(chain *models.Chain, body map[string]json.RawMessage) (models.ChainThresholdWrite, []string, *ValidationError) {
	invalid := &ValidationError{}
	var write models.ChainThresholdWrite
	fields := make([]string, 0, 5)
	for _, key := range slices.Sorted(maps.Keys(body)) {
		raw := body[key]
		switch key {
		case fieldGasReadiness:
			value, message := parseRaw(raw, emptyGasAllowed(chain))
			if message != "" {
				invalid.add(key, message)
				continue
			}
			write.GasReadinessThresholdRaw = &value
			fields = append(fields, key)
		case fieldDustNative:
			value, message := parseRaw(raw, false)
			if message != "" {
				invalid.add(key, message)
				continue
			}
			write.DustThresholdNativeRaw = &value
			fields = append(fields, key)
		case fieldDustUSD:
			value, message := parseDustUSD(raw)
			if message != "" {
				invalid.add(key, message)
				continue
			}
			write.DustThresholdUSD = &value
			fields = append(fields, key)
		case fieldConfirmations:
			count, message := parseConfirmations(raw)
			if message != "" {
				invalid.add(key, message)
				continue
			}
			write.RequiredConfirmations = &count
			fields = append(fields, key)
		case fieldFinality:
			policy, message := parseFinality(raw, chain)
			if message != "" {
				invalid.add(key, message)
				continue
			}
			write.Finality = &policy
			fields = append(fields, key)
		default:
			invalid.add(key, "unknown field")
		}
	}
	if leavesNativeFinality(chain, write) && write.RequiredConfirmations == nil && len(invalid.Fields[fieldConfirmations]) == 0 {
		invalid.add(fieldConfirmations, "is required when finality changes to confirmations")
	}
	return write, fields, invalid
}

// leavesNativeFinality reports whether write moves a chain confirmed by its own
// finality (finalized_tag, solidified) back to counting confirmations.
func leavesNativeFinality(chain *models.Chain, write models.ChainThresholdWrite) bool {
	return chain != nil && write.Finality != nil &&
		*write.Finality == models.FinalityConfirmations &&
		chain.FinalityPolicy() != models.FinalityConfirmations
}

// parseFinality reads a finality policy the chain's adapter can read.
func parseFinality(raw json.RawMessage, chain *models.Chain) (string, string) {
	const oneOf = "must be one of " + models.FinalityConfirmations + ", " + models.FinalityFinalizedTag + ", " + models.FinalitySolidified
	text, kind, message := jsonScalar(raw, oneOf)
	if message != "" || kind != kindString || !models.IsFinalityPolicy(text) {
		return "", oneOf
	}
	if chain == nil || !models.FinalityFitsAdapter(text, chain.AdapterType) {
		return "", "is not supported on this chain"
	}
	return text, ""
}

// emptyGasAllowed is the bitcoin sentinel. btc and tbtc store "" because
// those chains have no separate gas asset. Any other adapter must keep a
// digit string.
func emptyGasAllowed(chain *models.Chain) bool {
	return chain != nil && chain.AdapterType == models.AdapterTypeBitcoin
}

func parseRaw(raw json.RawMessage, emptyAllowed bool) (string, string) {
	text, kind, message := jsonScalar(raw, "must be an unsigned integer")
	if message != "" {
		return "", message
	}
	if kind == kindNull {
		return "", "must not be null"
	}
	if strings.HasPrefix(text, "-") {
		return "", "must not be negative"
	}
	if text == "" {
		if emptyAllowed {
			return "", ""
		}
		return "", "must not be empty"
	}
	if !unsignedDigits(text) {
		return "", "must be an unsigned integer"
	}
	if len(text) > maxRawDigits {
		return "", "is too long"
	}
	return text, ""
}

func parseDustUSD(raw json.RawMessage) (string, string) {
	text, kind, message := jsonScalar(raw, "must be a decimal")
	if message != "" {
		return "", message
	}
	if kind == kindNull {
		return "", "must not be null"
	}
	value, err := numeric.ParseNonNegative(fieldDustUSD, text)
	if err != nil {
		if errors.Is(err, numeric.ErrNegative) {
			return "", "must not be negative"
		}
		return "", "must be a decimal"
	}
	if err := models.DustThresholdUSDColumn.Validate(value); err != nil {
		return "", "does not fit dust_threshold_usd"
	}
	return value.String(), ""
}

func parseConfirmations(raw json.RawMessage) (int, string) {
	text, kind, message := jsonScalar(raw, "must be an integer")
	if message != "" || kind == kindNull {
		return 0, "must be an integer"
	}
	count, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		if strings.HasPrefix(text, "-") {
			return 0, "must not be negative"
		}
		if unsignedDigits(text) {
			return 0, "is too large"
		}
		return 0, "must be an integer"
	}
	switch {
	case count < 0:
		return 0, "must not be negative"
	case count == 0:
		return 0, "must be at least 1"
	case count > maxConfirmations:
		return 0, "is too large"
	}
	return int(count), ""
}

func jsonScalar(raw json.RawMessage, typeMessage string) (string, int, string) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", kindMissing, typeMessage
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return "", kindNull, ""
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return "", kindMissing, typeMessage
		}
		return text, kindString, ""
	}
	if trimmed[0] == '-' || (trimmed[0] >= '0' && trimmed[0] <= '9') {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.UseNumber()
		var number json.Number
		if err := decoder.Decode(&number); err != nil || decoder.More() {
			return "", kindMissing, typeMessage
		}
		return number.String(), kindNumber, ""
	}
	return "", kindMissing, typeMessage
}

func unsignedDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func thresholdView(chain *models.Chain) (ThresholdView, error) {
	if chain == nil {
		return ThresholdView{}, fmt.Errorf("chain thresholds: chain is required")
	}
	if !chain.DustThresholdUSD.Valid {
		return ThresholdView{}, fmt.Errorf("chain thresholds: dust_threshold_usd is missing")
	}
	return ThresholdView{
		ID:                       chain.ID,
		GasReadinessThresholdRaw: deref(chain.GasReadinessThresholdRaw),
		DustThresholdNativeRaw:   deref(chain.DustThresholdNativeRaw),
		DustThresholdUSD:         chain.DustThresholdUSD.Decimal.String(),
		RequiredConfirmations:    chain.RequiredConfirmations,
		Finality:                 chain.FinalityPolicy(),
	}, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// PlatformChainView is one chain of the platform chain list: its identity, its
// status and the settings ThresholdView carries. The RPC URL is not included (the
// endpoint list is GET .../rpc-endpoints, by host only).
type PlatformChainView struct {
	ThresholdView
	Name         string `json:"name"`
	AdapterType  string `json:"adapter_type"`
	NativeSymbol string `json:"native_symbol"`
	NetworkID    *int64 `json:"network_id"`
	IsTestnet    bool   `json:"is_testnet"`
	Status       string `json:"status"`
	DisplayOrder int    `json:"display_order"`
}

// chainLister lists every chain row, whatever its status or environment.
type chainLister interface {
	FindAll(ctx context.Context) ([]models.Chain, error)
}

// List returns every chain, active or not, testnet or mainnet, in display order,
// to a platform admin (ErrPlatformForbidden otherwise). Unlike GET /v1/chains it
// does not follow an account's environment.
func (s *Thresholds) List(ctx context.Context, actorID uuid.UUID) ([]PlatformChainView, error) {
	if ctx == nil {
		return nil, fmt.Errorf("list platform chains: context is required")
	}
	if s == nil || s.store == nil || s.admins == nil {
		return nil, fmt.Errorf("chain thresholds: store and platform admins are required")
	}
	lister, ok := s.store.(chainLister)
	if !ok {
		return nil, fmt.Errorf("list platform chains: the store cannot list chains")
	}
	if actorID == uuid.Nil {
		return nil, fmt.Errorf("list platform chains: actor is required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if !admin {
		return nil, ErrPlatformForbidden
	}
	rows, err := lister.FindAll(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]PlatformChainView, 0, len(rows))
	for i := range rows {
		chain := &rows[i]
		thresholds, err := thresholdView(chain)
		if err != nil {
			return nil, fmt.Errorf("list platform chains: chain %s: %w", chain.ID, err)
		}
		views = append(views, PlatformChainView{
			ThresholdView: thresholds,
			Name:          chain.Name,
			AdapterType:   chain.AdapterType,
			NativeSymbol:  chain.NativeSymbol,
			NetworkID:     chain.NetworkID,
			IsTestnet:     chain.IsTestnet,
			Status:        chain.Status,
			DisplayOrder:  chain.DisplayOrder,
		})
	}
	return views, nil
}
