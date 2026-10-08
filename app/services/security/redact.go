package security

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// urlPattern matches a URL of any scheme inside free-form text. Database
// drivers and RPC clients embed those URLs, with the credential in the userinfo
// or the query, into the error string that a log line later prints.
var urlPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"']+`)

// secretAssign matches a secret-bearing key written as key=value or key: value
// inside a message. The names are the ones the logging guideline says must
// never be logged.
var secretAssign = regexp.MustCompile(`(?i)\b(password[-_]?reset[-_]?token|private[-_]?key|webhook[-_]?signing[-_]?secret|signing[-_]?secret|webhook[-_]?secret|api[-_]?key|refresh[-_]?token|api[-_]?token|reset[-_]?token|chain[-_]?code|x[-_]signature|mpc[-_]?share|customer[-_]?share|service[-_]?share|share[-_]?[ab]|passphrase|password|mnemonic|totp(?:[-_]?secret)?|seed|hmac(?:[-_]?key)?)\s*[:=]\s*(?:"[^"]*"|'[^']*'|\S+)`)

// pemPrivateKey matches a PEM block that carries a private key.
var pemPrivateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)

const redactedMark = "[redacted]"

// secretSegmentMin is the shortest path or query piece of a configured RPC URL
// that is treated as the key itself. Shorter pieces ("v2", "api") stay.
const secretSegmentMin = 16

var (
	redactionMu sync.RWMutex
	rpcHosts    = map[string]struct{}{}
	secretVals  []string
)

// ConfigureRedaction replaces the RPC hosts whose path is a secret and the
// configured secret values that must not survive in a log line. InstallLogRedaction
// calls it once, after config is loaded and before any channel is used.
func ConfigureRedaction(hosts, secrets []string) {
	nextHosts := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		nextHosts[host] = struct{}{}
	}
	nextSecrets := make([]string, 0, len(secrets))
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if !acceptableSecret(secret) {
			continue
		}
		if _, ok := seen[secret]; ok {
			continue
		}
		seen[secret] = struct{}{}
		nextSecrets = append(nextSecrets, secret)
	}
	sort.Slice(nextSecrets, func(i, j int) bool {
		return len(nextSecrets[i]) > len(nextSecrets[j])
	})

	redactionMu.Lock()
	rpcHosts = nextHosts
	secretVals = nextSecrets
	redactionMu.Unlock()
}

// RedactText returns a log-safe rendering of free-form text. URL userinfo and
// query strings are stripped; a configured RPC host also loses its path,
// because that path is where the provider key lives. Configured secret values
// and secret-shaped assignments are replaced as well.
func RedactText(text string) string {
	if text == "" {
		return ""
	}
	hosts, secrets := snapshotRedaction()
	return redactText(text, hosts, secrets)
}

// RedactError is RedactText over an error's message. A nil error yields "".
func RedactError(err error) string {
	if err == nil {
		return ""
	}
	return RedactText(err.Error())
}

func snapshotRedaction() (map[string]struct{}, []string) {
	redactionMu.RLock()
	defer redactionMu.RUnlock()
	hosts := make(map[string]struct{}, len(rpcHosts))
	for host := range rpcHosts {
		hosts[host] = struct{}{}
	}
	secrets := append([]string(nil), secretVals...)
	return hosts, secrets
}

func redactText(text string, hosts map[string]struct{}, secrets []string) string {
	text = pemPrivateKey.ReplaceAllString(text, redactedMark)
	text = urlPattern.ReplaceAllStringFunc(text, func(raw string) string {
		return redactURL(raw, hosts)
	})
	text = secretAssign.ReplaceAllString(text, "$1="+redactedMark)
	for _, secret := range secrets {
		if secret == "" || !strings.Contains(text, secret) {
			continue
		}
		text = strings.ReplaceAll(text, secret, redactedMark)
	}
	return text
}

func redactURL(raw string, hosts map[string]struct{}) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return redactedMark
	}
	path := u.Path
	if _, ok := hosts[strings.ToLower(u.Hostname())]; ok && path != "" && path != "/" {
		path = "/" + redactedMark
	}
	redacted := u.Scheme + "://" + u.Host + path
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		redacted += "?" + redactedMark
	}
	return redacted
}

func acceptableSecret(secret string) bool {
	if len(secret) < 8 || strings.EqualFold(secret, redactedMark) {
		return false
	}
	return true
}

// sensitiveName reports whether a structured log field's name is one the
// logging guideline says must never be logged.
func sensitiveName(key string) bool {
	k := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	compounds := []string{
		"private_key", "privatekey", "api_key", "share_a", "share_b",
		"mpc_share", "customer_share", "service_share", "chain_code",
		"webhook_secret", "signing_secret", "x_signature",
	}
	for _, compound := range compounds {
		if strings.Contains(k, compound) {
			return true
		}
	}
	for _, part := range strings.FieldsFunc(k, func(r rune) bool { return r == '_' || r == '.' }) {
		switch part {
		case "passphrase", "password", "mnemonic", "seed", "totp", "secret",
			"token", "hmac", "signature", "apikey", "share":
			return true
		}
	}
	return false
}
