package chains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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

	maxRawDigits = 78
)

const (
	kindMissing = iota
	kindNull
	kindString
	kindNumber
)

// ThresholdCatalog is the chain-row sweep columns
// PATCH /v1/platform/chains/{chainId} already writes. They are columns on
// chains, not a settings group. S1.4.7 names sweep.view and sweep.update
// on this entry. Holding settings.update is not that pair. There is no
// platform permission catalog, so a platform_admins row stands in.
type ThresholdCatalog struct {
	ViewPermission   string
	UpdatePermission string
	Fields           []string
}

// ChainThresholdCatalog is the code catalog entry the chain threshold PATCH uses.
func ChainThresholdCatalog() ThresholdCatalog {
	return ThresholdCatalog{
		ViewPermission:   policies.PermSweepView,
		UpdatePermission: policies.PermSweepUpdate,
		Fields:           []string{fieldGasReadiness, fieldDustNative, fieldDustUSD},
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

// ThresholdView is the stored threshold row. The RPC URL is not included.
type ThresholdView struct {
	ID                       string `json:"id"`
	GasReadinessThresholdRaw string `json:"gas_readiness_threshold_raw"`
	DustThresholdNativeRaw   string `json:"dust_threshold_native_raw"`
	DustThresholdUSD         string `json:"dust_threshold_usd"`
}

// Thresholds edits the three sweep columns S1.4.4 keeps on the chain row.
type Thresholds struct {
	store    ThresholdStore
	admins   PlatformAdmins
	activity activitylog.Writer
}

// NewThresholds builds the platform chain-threshold editor. The store, the
// platform-admin lookup and the activity log are required.
func NewThresholds(store ThresholdStore, admins PlatformAdmins, activity activitylog.Writer) *Thresholds {
	if store == nil {
		panic("chain thresholds: store is required")
	}
	if admins == nil {
		panic("chain thresholds: platform admins are required")
	}
	if activity == nil {
		panic("chain thresholds: activity log is required")
	}
	return &Thresholds{store: store, admins: admins, activity: activity}
}

// Update writes the threshold fields present in body. An unknown chain is
// ErrNotFound before the platform-admin check. A caller who is not a platform
// admin is ErrPlatformForbidden. S1.4.4 names chains.update; this branch has
// no platform permission catalog, so the gate is the platform_admins row.
// The columns declare sweep.view and sweep.update. A platform_admins row
// stands in for that pair. Holding settings.update is not sweep.update.
// A negative amount or a negative confirmation count is a ValidationError and
// is not stored. An omitted field leaves that column unchanged. An empty gas
// string is stored only for a bitcoin chain, where it is the sentinel.
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
	if err := requireSweepThresholdPair(ChainThresholdCatalog()); err != nil {
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
	fields := make([]string, 0, 3)
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
			if message := parseConfirmations(raw); message != "" {
				invalid.add(key, message)
			}
		default:
			invalid.add(key, "unknown field")
		}
	}
	return write, fields, invalid
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

func parseConfirmations(raw json.RawMessage) string {
	text, kind, message := jsonScalar(raw, "must be an integer")
	if message != "" || kind == kindNull {
		return "must be an integer"
	}
	count, err := strconv.Atoi(text)
	if err != nil {
		if strings.HasPrefix(text, "-") {
			return "must not be negative"
		}
		return "must be an integer"
	}
	if count < 0 {
		return "must not be negative"
	}
	return ""
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
	}, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
