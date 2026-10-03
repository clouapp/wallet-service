package withdraw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

const (
	blankSpendingLimit = "{}"
	spendingLimitField = "daily_usd"
	spendingLimitTTL   = 24 * time.Hour
	usdCentScale       = 100
)

// dailyUSDPattern is a non-negative decimal. A leading minus is rejected
// before this pattern runs, so a negative amount is never stored.
var dailyUSDPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

var (
	// ErrNegativeSpendingLimit is a create-time refusal. The row is not written.
	ErrNegativeSpendingLimit = errors.New("spending limit amount must be greater than or equal to 0")
	// ErrSpendingLimitInvalid means the stored JSON cannot be enforced. The
	// withdrawal stops instead of ignoring the cap.
	ErrSpendingLimitInvalid = errors.New("spending_limit_invalid")
	// ErrSpendingLimitExceeded is the daily USD cap for this access token.
	ErrSpendingLimitExceeded = errors.New("spending_limit_exceeded")
	// ErrSpendingQuoteUnavailable means a set cap could not be priced in USD.
	ErrSpendingQuoteUnavailable = errors.New("spending_limit_quote_unavailable")
)

// USDQuote prices one asset amount in USD. A nil quote blocks a set cap and
// is never called when the cap is blank.
type USDQuote interface {
	ConvertToUSD(ctx context.Context, code string, amount float64) (float64, error)
}

// StoreSpendingLimit turns the create-token object into the column value.
// A nil or empty object, or a blank daily_usd, is the blank cap "{}".
// A negative amount anywhere in the object is refused and nothing is stored.
func StoreSpendingLimit(value map[string]any) (string, error) {
	if len(value) == 0 {
		return blankSpendingLimit, nil
	}
	if amountIsNegative(value) {
		return "", ErrNegativeSpendingLimit
	}
	raw, exists := value[spendingLimitField]
	if !exists || raw == nil {
		return blankSpendingLimit, nil
	}
	text, present, err := dailyUSDText(raw)
	if err != nil {
		return "", err
	}
	if !present {
		return blankSpendingLimit, nil
	}
	canonical, err := canonicalDailyUSD(text)
	if err != nil {
		return "", err
	}
	if canonical == "" {
		return blankSpendingLimit, nil
	}
	encoded, err := json.Marshal(struct {
		DailyUSD string `json:"daily_usd"`
	}{DailyUSD: canonical})
	if err != nil {
		return "", ErrSpendingLimitInvalid
	}
	return string(encoded), nil
}

// WithAPIToken copies the access token's cap onto the withdrawal. A blank
// cap stays a blank cap: Request does not price the amount or touch Redis.
func (r WithdrawRequest) WithAPIToken(token *models.AccessToken, humanAmount string) WithdrawRequest {
	if token == nil {
		return r
	}
	r.AccessTokenID = token.ID
	r.SpendingLimit = token.SpendingLimit
	r.QuoteAmount = strings.TrimSpace(humanAmount)
	return r
}

// UseUSDQuote installs the price source used when a cap is set.
func (s *Service) UseUSDQuote(quote USDQuote) {
	if s == nil {
		return
	}
	s.usdQuote = quote
}

// enforceTokenSpendingLimit applies a per-token daily USD cap. A blank cap
// returns nil before any quote or Redis call, matching a withdrawal with no
// cap. The counter is an INCRBY of USD cents on a UTC-day key, with a 24h TTL
// on the first increment, the same shape as the consolidate daily quota.
// A rejected spend is removed from the counter so it does not consume the cap.
func (s *Service) enforceTokenSpendingLimit(ctx context.Context, req WithdrawRequest) error {
	limitCents, set, err := dailyLimitCents(req.SpendingLimit)
	if err != nil {
		return err
	}
	if !set {
		return nil
	}
	if req.AccessTokenID == uuid.Nil {
		return ErrSpendingLimitInvalid
	}
	spendCents, err := s.quoteSpendCents(ctx, req)
	if err != nil {
		return err
	}
	if s == nil || s.locker == nil {
		return fmt.Errorf("redis lock: redis is not configured")
	}
	key := spendingLimitKey(req.AccessTokenID, time.Now().UTC())
	total, err := s.locker.IncrBy(ctx, key, spendCents, spendingLimitTTL)
	if err != nil {
		return fmt.Errorf("incr token spending limit: %w", err)
	}
	if total > limitCents {
		_ = s.locker.DecrBy(ctx, key, spendCents)
		return ErrSpendingLimitExceeded
	}
	return nil
}

func (s *Service) quoteSpendCents(ctx context.Context, req WithdrawRequest) (int64, error) {
	asset := strings.TrimSpace(req.Asset)
	if asset == "" || strings.TrimSpace(req.QuoteAmount) == "" {
		return 0, ErrSpendingQuoteUnavailable
	}
	if s == nil || s.usdQuote == nil {
		return 0, ErrSpendingQuoteUnavailable
	}
	human, ok := new(big.Rat).SetString(strings.TrimSpace(req.QuoteAmount))
	if !ok || human.Sign() <= 0 {
		return 0, ErrSpendingQuoteUnavailable
	}
	amount, _ := human.Float64()
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0, ErrSpendingQuoteUnavailable
	}
	usd, err := s.usdQuote.ConvertToUSD(ctx, asset, amount)
	if err != nil || math.IsNaN(usd) || math.IsInf(usd, 0) || usd <= 0 {
		return 0, ErrSpendingQuoteUnavailable
	}
	cents, err := usdFloatToCents(usd)
	if err != nil || cents <= 0 {
		return 0, ErrSpendingQuoteUnavailable
	}
	return cents, nil
}

func spendingLimitKey(tokenID uuid.UUID, day time.Time) string {
	return fmt.Sprintf("vault:quota:spend:%s:%s", tokenID.String(), day.UTC().Format("2006-01-02"))
}

func dailyLimitCents(stored string) (int64, bool, error) {
	trimmed := strings.TrimSpace(stored)
	if trimmed == "" || trimmed == blankSpendingLimit || trimmed == "null" {
		return 0, false, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return 0, false, ErrSpendingLimitInvalid
	}
	raw, ok := payload[spendingLimitField]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return 0, false, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return 0, false, ErrSpendingLimitInvalid
	}
	if amountIsNegative(decoded) {
		return 0, false, ErrSpendingLimitInvalid
	}
	text, present, err := dailyUSDText(decoded)
	if err != nil {
		return 0, false, ErrSpendingLimitInvalid
	}
	if !present {
		return 0, false, nil
	}
	canonical, err := canonicalDailyUSD(text)
	if err != nil {
		return 0, false, err
	}
	if canonical == "" {
		return 0, false, nil
	}
	cents, err := decimalToCents(canonical)
	if err != nil {
		return 0, false, ErrSpendingLimitInvalid
	}
	return cents, true, nil
}

func dailyUSDText(value any) (string, bool, error) {
	switch typed := value.(type) {
	case nil:
		return "", false, nil
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return "", false, nil
		}
		return text, true, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return "", true, ErrSpendingLimitInvalid
		}
		return strconv.FormatFloat(typed, 'f', -1, 64), true, nil
	case json.Number:
		text := strings.TrimSpace(typed.String())
		if text == "" {
			return "", false, nil
		}
		return text, true, nil
	default:
		text := strings.TrimSpace(fmt.Sprint(typed))
		if text == "" || text == "<nil>" {
			return "", false, nil
		}
		return text, true, nil
	}
}

func canonicalDailyUSD(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if negativeDecimalString(text) {
		return "", ErrNegativeSpendingLimit
	}
	if !dailyUSDPattern.MatchString(text) {
		return "", ErrSpendingLimitInvalid
	}
	return text, nil
}

func decimalToCents(text string) (int64, error) {
	amount, ok := new(big.Rat).SetString(text)
	if !ok || amount.Sign() < 0 {
		return 0, ErrSpendingLimitInvalid
	}
	scaled := new(big.Rat).Mul(amount, big.NewRat(usdCentScale, 1))
	cents := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	remainder := new(big.Int).Rem(scaled.Num(), scaled.Denom())
	if remainder.Sign() > 0 {
		cents.Add(cents, big.NewInt(1))
	}
	if !cents.IsInt64() {
		return 0, ErrSpendingLimitInvalid
	}
	return cents.Int64(), nil
}

func usdFloatToCents(amount float64) (int64, error) {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return 0, ErrSpendingLimitInvalid
	}
	return decimalToCents(strconv.FormatFloat(amount, 'f', 8, 64))
}

func amountIsNegative(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return negativeDecimalString(typed)
	case map[string]any:
		for _, child := range typed {
			if amountIsNegative(child) {
				return true
			}
		}
		return false
	case []any:
		for _, child := range typed {
			if amountIsNegative(child) {
				return true
			}
		}
		return false
	default:
		return negativeDecimalString(fmt.Sprint(typed))
	}
}

func negativeDecimalString(text string) bool {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "-") {
		return false
	}
	body := strings.TrimPrefix(text, "-")
	return dailyUSDPattern.MatchString(body)
}
