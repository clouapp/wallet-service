package models

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
)

const (
	APIPermWalletsRead      = "wallets:read"
	APIPermWalletsWrite     = "wallets:write"
	APIPermTransactionsRead = "transactions:read"
	APIPermWithdrawalsWrite = "withdrawals:write"
	APIPermWebhooksRead     = "webhooks:read"
	APIPermWebhooksWrite    = "webhooks:write"
)

var (
	ErrAPIPermissionDenied = errors.New("api token is missing the required scope")
	ErrSpendingLimit       = errors.New("api token spending limit exceeded")
	ErrUnpricedSpend       = errors.New("spending limit cannot price this asset")
)

// APIPermissionCatalog is the scope vocabulary the dashboard already sends.
func APIPermissionCatalog() []string {
	return []string{
		APIPermWalletsRead,
		APIPermWalletsWrite,
		APIPermTransactionsRead,
		APIPermWithdrawalsWrite,
		APIPermWebhooksRead,
		APIPermWebhooksWrite,
	}
}

// AllAPIPermissionGrants is what a test token stores so it can call every
// external route. A blank permissions column is the omitted grant: the
// S3.4.6 scope check allows it. The colon catalog above is not that check.
func AllAPIPermissionGrants() string {
	raw, err := json.Marshal([]string{
		"wallets.read",
		"wallets.create",
		"addresses.create",
		"withdrawals.create",
		"sweep.execute",
		"webhooks.read",
		"webhooks.write",
		"transactions.read",
	})
	if err != nil {
		return ""
	}
	return string(raw)
}

func APITokenAllows(grants []string, permission string) bool {
	if permission == "" {
		return false
	}
	for _, grant := range grants {
		if grant == permission {
			return true
		}
	}
	return false
}

// RoleMayMintAPIPermissions reports whether role may hand a token exactly these scopes.
// The set must be non-empty and stay inside both the catalog and the role's own scopes.
func RoleMayMintAPIPermissions(role string, requested []string) bool {
	if len(requested) == 0 {
		return false
	}
	held := apiPermissionsForRole(role)
	for _, permission := range requested {
		if !APITokenAllows(held, permission) {
			return false
		}
	}
	return true
}

func apiPermissionsForRole(role string) []string {
	if role == RetiredAccountRoleViewer {
		role = AccountRoleAuditor
	}
	switch role {
	case AccountRoleOwner, AccountRoleAdmin:
		return APIPermissionCatalog()
	case AccountRoleUser, AccountRoleAuditor:
		return []string{APIPermWalletsRead, APIPermTransactionsRead, APIPermWebhooksRead}
	default:
		return nil
	}
}

// ParseStoredAPIPermissions reads the JSON array saved on access_tokens.permissions.
// An empty value is a token with no scopes, not an error.
func ParseStoredAPIPermissions(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var grants []string
	if err := json.Unmarshal([]byte(raw), &grants); err != nil {
		return nil, err
	}
	for _, grant := range grants {
		if !APITokenAllows(APIPermissionCatalog(), grant) {
			return nil, ErrAPIPermissionDenied
		}
	}
	return grants, nil
}

func MarshalAPIPermissions(grants []string) (string, error) {
	if len(grants) == 0 {
		return "", ErrAPIPermissionDenied
	}
	for _, grant := range grants {
		if !APITokenAllows(APIPermissionCatalog(), grant) {
			return "", ErrAPIPermissionDenied
		}
	}
	raw, err := json.Marshal(grants)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ClientIPAllowed reports whether clientIP satisfies a stored cidr.
// An empty restriction allows every address. A single IP is a host match.
// An unparseable restriction fails closed.
func ClientIPAllowed(cidr, clientIP string) bool {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return true
	}
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if ip == nil {
		return false
	}
	if !strings.Contains(cidr, "/") {
		return ip.Equal(net.ParseIP(cidr))
	}
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	return network.Contains(ip)
}

// CanonicalIPCidr accepts an empty value, a single IP, or a CIDR.
func CanonicalIPCidr(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.Contains(raw, "/") {
		ip := net.ParseIP(raw)
		if ip == nil {
			return "", errors.New("invalid ip")
		}
		return ip.String(), nil
	}
	_, network, err := net.ParseCIDR(raw)
	if err != nil {
		return "", err
	}
	return network.String(), nil
}

// TokenDailyUSDLimit reports whether the stored spending limit caps daily USD.
func TokenDailyUSDLimit(raw string) (float64, bool, error) {
	limit, err := dailyUSDLimit(raw)
	if err != nil || limit == nil {
		return 0, false, err
	}
	return *limit, true, nil
}

// AuthorizeTokenSpend applies a daily USD cap. An empty limit does not change the spent total.
// alreadySpent is the total before this withdrawal. The returned total includes this withdrawal.
func AuthorizeTokenSpend(limitJSON, asset, amount string, alreadySpent float64) (float64, error) {
	limit, err := dailyUSDLimit(limitJSON)
	if err != nil {
		return 0, err
	}
	if limit == nil {
		return alreadySpent, nil
	}
	add, err := TokenUSDAmount(asset, amount)
	if err != nil {
		return 0, err
	}
	if alreadySpent < 0 || add < 0 {
		return 0, ErrSpendingLimit
	}
	next := alreadySpent + add
	if next > *limit {
		return 0, ErrSpendingLimit
	}
	return next, nil
}

func dailyUSDLimit(raw string) (*float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil, nil
	}
	var parsed struct {
		DailyUSD *float64 `json:"daily_usd"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return nil, err
	}
	if parsed.DailyUSD == nil {
		return nil, nil
	}
	if *parsed.DailyUSD < 0 {
		return nil, ErrSpendingLimit
	}
	return parsed.DailyUSD, nil
}

// TokenUSDAmount is the USD value counted against a daily cap.
func TokenUSDAmount(asset, amount string) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(asset)) {
	case "usd", "usdt", "usdc":
	default:
		return 0, ErrUnpricedSpend
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(amount), 64)
	if err != nil || value < 0 {
		return 0, ErrSpendingLimit
	}
	return value, nil
}

// CanonicalSpendingLimit stores either {} or {"daily_usd":n}.
func CanonicalSpendingLimit(raw string) (string, error) {
	limit, err := dailyUSDLimit(raw)
	if err != nil {
		return "", err
	}
	if limit == nil {
		return "{}", nil
	}
	encoded, err := json.Marshal(struct {
		DailyUSD float64 `json:"daily_usd"`
	}{DailyUSD: *limit})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// NewAPITokenSecret returns a random secret and its sha256 hex digest.
// The digest is what access_tokens.token_hash stores. The secret rides in the JWT.
func NewAPITokenSecret() (secret, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	secret = hex.EncodeToString(buf)
	return secret, HashAPITokenSecret(secret), nil
}

func HashAPITokenSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// APITokenSecretMatches reports whether a bearer secret matches the stored digest.
// A digest that is not sha256 hex is a legacy row and is not checked here.
func APITokenSecretMatches(storedHash, secret string) bool {
	if !isSHA256Hex(storedHash) {
		return false
	}
	want, err := hex.DecodeString(storedHash)
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(HashAPITokenSecret(secret))
	if err != nil || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func IsSHA256Hex(value string) bool {
	return isSHA256Hex(value)
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
