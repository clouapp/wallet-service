package providers

import (
	"context"
	"errors"
	"strings"
)

const sealedKeyPrefix = "enc:v1:"

// KeySource returns this provider's credential for one call. An empty result
// means the credential is unavailable. Callers must not log or return it.
type KeySource func(ctx context.Context) string

var (
	errEmptyProviderKey = errors.New("empty provider key")
	errEmptySigningKey  = errors.New("empty signing key")
)

// credentialAtUse reads source when one is installed. A missing source keeps
// the key passed to the constructor, which unit tests still use. A source
// that returns a blank or still-sealed value fails closed: the ciphertext
// is not a credential and is not placed in the error.
func credentialAtUse(ctx context.Context, source KeySource, captured string) (string, error) {
	if source == nil {
		return strings.TrimSpace(captured), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resolved := strings.TrimSpace(source(ctx))
	if resolved == "" || strings.HasPrefix(resolved, sealedKeyPrefix) {
		return "", errEmptyProviderKey
	}
	return resolved, nil
}

// requireCredential is the outbound-call gate. A blank captured key with no
// source is the same failure the providers already returned before the call.
func requireCredential(ctx context.Context, source KeySource, captured string) (string, error) {
	key, err := credentialAtUse(ctx, source, captured)
	if err != nil {
		return "", err
	}
	if key == "" || strings.HasPrefix(key, sealedKeyPrefix) {
		return "", errEmptyProviderKey
	}
	return key, nil
}

// gateInboundKey fails closed when a KeySource is installed and this call
// resolves no usable credential. Providers constructed with a literal key
// and no source keep verifying with the subscription signing secret only.
func gateInboundKey(ctx context.Context, source KeySource) error {
	if source == nil {
		return nil
	}
	_, err := requireCredential(ctx, source, "")
	return err
}

func rejectBlankSigningKey(secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" || strings.HasPrefix(secret, sealedKeyPrefix) {
		return errEmptySigningKey
	}
	return nil
}
