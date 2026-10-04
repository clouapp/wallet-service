package models

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// ErrChainRPCUnusable is a stored endpoint that cannot be dialed. The error
// text never includes the URL.
var ErrChainRPCUnusable = errors.New("chain rpc is not usable")

// RPCURLEnvPrefix marks a stored rpc_url that names an environment variable
// ("env:SOLANA_RPC_URL") instead of holding the URL, so endpoints carrying an API
// key stay out of the database.
const RPCURLEnvPrefix = "env:"

var rpcURLEnvName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// IsValidRPCURLEnvReference reports whether value is a well-formed "env:NAME".
func IsValidRPCURLEnvReference(value string) bool {
	name, ok := strings.CutPrefix(value, RPCURLEnvPrefix)
	return ok && rpcURLEnvName.MatchString(name)
}

// ResolveRPCURL turns a decrypted rpc_url into the endpoint to call: the URL itself,
// or the value of the environment variable an "env:NAME" reference names.
func ResolveRPCURL(decrypted string) (string, error) {
	return resolveRPCURL(decrypted, os.LookupEnv)
}

func resolveRPCURL(decrypted string, lookupEnv func(string) (string, bool)) (string, error) {
	if !strings.HasPrefix(decrypted, RPCURLEnvPrefix) {
		return decrypted, nil
	}
	if !IsValidRPCURLEnvReference(decrypted) {
		return "", fmt.Errorf("rpc_url env reference %q is malformed", decrypted)
	}
	name := strings.TrimPrefix(decrypted, RPCURLEnvPrefix)
	value, ok := lookupEnv(name)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return "", fmt.Errorf("rpc_url names environment variable %s, which is not set", name)
	}
	return value, nil
}

// DialEndpoint is the URL a chain dialer calls after the seal is open.
// A stored http(s) URL is returned as itself and does not consult the
// environment. An env:NAME reference is resolved. The error never includes
// the URL.
func DialEndpoint(plaintext string) (string, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", ErrChainRPCUnusable
	}
	if strings.HasPrefix(plaintext, RPCURLEnvPrefix) {
		value, err := ResolveRPCURL(plaintext)
		if err != nil {
			return "", ErrChainRPCUnusable
		}
		value = strings.TrimSpace(value)
		if !usableHTTPURL(value) {
			return "", ErrChainRPCUnusable
		}
		return value, nil
	}
	if !usableHTTPURL(plaintext) {
		return "", ErrChainRPCUnusable
	}
	return plaintext, nil
}

func usableHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}
