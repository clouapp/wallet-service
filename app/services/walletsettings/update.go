// Package walletsettings parses and validates PATCH /v1/wallets/{id}/settings:
// explicit fields only, each absent (unchanged), null (reset to the default) or
// a value checked against its column and its named bounds.
package walletsettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

// Accepted fields.
const (
	FieldLabel             = "label"
	FieldFeeRateMin        = "fee_rate_min"
	FieldFeeRateMax        = "fee_rate_max"
	FieldFeeMultiplier     = "fee_multiplier"
	FieldRequiredApprovals = "required_approvals"
)

// frozenUntilField is refused with a pointer to POST /freeze, which enforces
// the wallet.freeze ability.
const frozenUntilField = "frozen_until"

const (
	// LabelMaxLength matches wallets.label varchar(255), in characters.
	LabelMaxLength = 255
	// RequiredApprovalsMin and RequiredApprovalsMax bound the approval threshold.
	RequiredApprovalsMin = 1
	RequiredApprovalsMax = 10
	// MaxBodyBytes bounds the request body.
	MaxBodyBytes = 4 << 10
)

var acceptedFields = []string{FieldLabel, FieldFeeRateMin, FieldFeeRateMax, FieldFeeMultiplier, FieldRequiredApprovals}

// ErrNoFields marks a body that changes nothing.
var ErrNoFields = errors.New("no settings to update")

// FieldError is a rejected field; the controller answers 422 with it.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

func fieldError(field, format string, args ...any) *FieldError {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Optional is a field of the body: Set when present, Null when sent as null.
type Optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// Update is a parsed body.
type Update struct {
	Label             Optional[string]
	FeeRateMin        Optional[int]
	FeeRateMax        Optional[int]
	FeeMultiplier     Optional[decimal.Decimal]
	RequiredApprovals Optional[int]
}

// Parse reads a JSON object of accepted fields. Numbers may be JSON numbers or
// decimal strings; unknown fields are rejected by name.
func Parse(body []byte) (Update, error) {
	if len(body) > MaxBodyBytes {
		return Update{}, fieldError("body", "must not exceed %d bytes", MaxBodyBytes)
	}
	raw := map[string]json.RawMessage{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return Update{}, fieldError("body", "must be a JSON object")
	}
	if err := rejectUnknownFields(raw); err != nil {
		return Update{}, err
	}

	var update Update
	var err error
	if update.Label, err = parseLabel(raw); err != nil {
		return Update{}, err
	}
	if update.FeeRateMin, err = parseInteger(raw, FieldFeeRateMin, true); err != nil {
		return Update{}, err
	}
	if update.FeeRateMax, err = parseInteger(raw, FieldFeeRateMax, true); err != nil {
		return Update{}, err
	}
	if update.RequiredApprovals, err = parseInteger(raw, FieldRequiredApprovals, false); err != nil {
		return Update{}, err
	}
	if update.FeeMultiplier, err = parseFeeMultiplier(raw); err != nil {
		return Update{}, err
	}
	if !update.changesAnything() {
		return Update{}, ErrNoFields
	}
	return update, nil
}

func rejectUnknownFields(raw map[string]json.RawMessage) error {
	if _, ok := raw[frozenUntilField]; ok {
		return fieldError(frozenUntilField, "is not a setting; use POST /v1/wallets/{walletId}/freeze")
	}
	unknown := make([]string, 0)
	for name := range raw {
		if !isAccepted(name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fieldError(unknown[0], "is not an accepted field (accepted: %s)", strings.Join(acceptedFields, ", "))
}

func isAccepted(name string) bool {
	for _, accepted := range acceptedFields {
		if name == accepted {
			return true
		}
	}
	return false
}

func isNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

// scalarText is a JSON number's literal or a JSON string's content.
func scalarText(field string, value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return "", fieldError(field, "must be a number")
		}
		return text, nil
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return "", fieldError(field, "must be a number")
	}
	return number.String(), nil
}

func parseLabel(raw map[string]json.RawMessage) (Optional[string], error) {
	value, ok := raw[FieldLabel]
	if !ok {
		return Optional[string]{}, nil
	}
	if isNull(value) {
		return Optional[string]{}, fieldError(FieldLabel, "must not be null")
	}
	var label string
	if err := json.Unmarshal(value, &label); err != nil {
		return Optional[string]{}, fieldError(FieldLabel, "must be a string")
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return Optional[string]{}, fieldError(FieldLabel, "must not be empty")
	}
	if utf8.RuneCountInString(label) > LabelMaxLength {
		return Optional[string]{}, fieldError(FieldLabel, "must not exceed %d characters", LabelMaxLength)
	}
	return Optional[string]{Set: true, Value: label}, nil
}

func parseInteger(raw map[string]json.RawMessage, field string, nullable bool) (Optional[int], error) {
	value, ok := raw[field]
	if !ok {
		return Optional[int]{}, nil
	}
	if isNull(value) {
		if !nullable {
			return Optional[int]{}, fieldError(field, "must not be null")
		}
		return Optional[int]{Set: true, Null: true}, nil
	}
	text, err := scalarText(field, value)
	if err != nil {
		return Optional[int]{}, err
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return Optional[int]{}, fieldError(field, "must be a whole number")
	}
	return Optional[int]{Set: true, Value: parsed}, nil
}

func parseFeeMultiplier(raw map[string]json.RawMessage) (Optional[decimal.Decimal], error) {
	value, ok := raw[FieldFeeMultiplier]
	if !ok {
		return Optional[decimal.Decimal]{}, nil
	}
	if isNull(value) {
		return Optional[decimal.Decimal]{Set: true, Null: true}, nil
	}
	text, err := scalarText(FieldFeeMultiplier, value)
	if err != nil {
		return Optional[decimal.Decimal]{}, err
	}
	multiplier, err := numeric.Parse(FieldFeeMultiplier, text)
	if err != nil {
		return Optional[decimal.Decimal]{}, fieldError(FieldFeeMultiplier, "must be a decimal number")
	}
	if err := models.ValidateFeeMultiplier(multiplier); err != nil {
		return Optional[decimal.Decimal]{}, fieldError(FieldFeeMultiplier,
			"must be between %s and %s with at most %d decimal places",
			models.FeeMultiplierMin.StringFixed(2), models.FeeMultiplierMax.StringFixed(2), models.FeeMultiplierColumn.Scale)
	}
	return Optional[decimal.Decimal]{Set: true, Value: multiplier}, nil
}

func (u Update) changesAnything() bool {
	return u.Label.Set || u.FeeRateMin.Set || u.FeeRateMax.Set || u.FeeMultiplier.Set || u.RequiredApprovals.Set
}

// Columns validates the update against the wallet's chain and current values,
// and returns the columns to write (nil values reset them to NULL).
func (u Update) Columns(wallet *models.Wallet, adapterType string) (map[string]any, error) {
	if wallet == nil {
		return nil, fmt.Errorf("wallet settings: wallet is required")
	}
	columns := map[string]any{}
	if u.Label.Set {
		columns[FieldLabel] = u.Label.Value
	}
	if err := u.feeMultiplierColumn(adapterType, columns); err != nil {
		return nil, err
	}
	if err := u.feeRateColumns(wallet, adapterType, columns); err != nil {
		return nil, err
	}
	if u.RequiredApprovals.Set {
		if u.RequiredApprovals.Value < RequiredApprovalsMin || u.RequiredApprovals.Value > RequiredApprovalsMax {
			return nil, fieldError(FieldRequiredApprovals, "must be between %d and %d", RequiredApprovalsMin, RequiredApprovalsMax)
		}
		columns[FieldRequiredApprovals] = u.RequiredApprovals.Value
	}
	return columns, nil
}

func (u Update) feeMultiplierColumn(adapterType string, columns map[string]any) error {
	if !u.FeeMultiplier.Set {
		return nil
	}
	if u.FeeMultiplier.Null {
		columns[FieldFeeMultiplier] = numeric.NullDecimal{}
		return nil
	}
	if !models.FeeMultiplierApplies(adapterType) {
		return fieldError(FieldFeeMultiplier, "does not apply to %s wallets (flat network fee)", adapterType)
	}
	columns[FieldFeeMultiplier] = numeric.NewNullDecimal(u.FeeMultiplier.Value)
	return nil
}

func (u Update) feeRateColumns(wallet *models.Wallet, adapterType string, columns map[string]any) error {
	if !u.FeeRateMin.Set && !u.FeeRateMax.Set {
		return nil
	}
	minimum := mergedBound(u.FeeRateMin, wallet.FeeRateMin)
	maximum := mergedBound(u.FeeRateMax, wallet.FeeRateMax)
	settingValue := (u.FeeRateMin.Set && !u.FeeRateMin.Null) || (u.FeeRateMax.Set && !u.FeeRateMax.Null)
	if settingValue && !models.FeeRateBoundsApply(adapterType) {
		return fieldError(FieldFeeRateMin, "fee rate bounds (sat/vB) apply only to bitcoin wallets")
	}
	if err := models.ValidateFeeRateBounds(minimum, maximum); err != nil {
		field := FieldFeeRateMin
		if errors.Is(err, models.ErrFeeRateOutOfRange) && maximum != nil &&
			(*maximum < models.FeeRateMinSatPerVByte || *maximum > models.FeeRateMaxSatPerVByte) {
			field = FieldFeeRateMax
		}
		return fieldError(field, "%s", err.Error())
	}
	if u.FeeRateMin.Set {
		columns[FieldFeeRateMin] = intOrNull(minimum)
	}
	if u.FeeRateMax.Set {
		columns[FieldFeeRateMax] = intOrNull(maximum)
	}
	return nil
}

// intOrNull is the column value of an optional integer: untyped nil writes NULL.
func intOrNull(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

// mergedBound is the bound after the update: the new value, NULL, or the current one.
func mergedBound(update Optional[int], current *int) *int {
	if !update.Set {
		return current
	}
	if update.Null {
		return nil
	}
	value := update.Value
	return &value
}
