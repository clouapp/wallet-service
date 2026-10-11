package secretsmanager

import (
	"regexp"
	"strings"
)

var kmsKeyIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// KMSKeyID is the KMS key a MASTER_KEY_REF names (key ARN, alias, alias ARN or key
// id), or "" for a local reference ("local:…") or none, which leaves Secrets
// Manager on its default key.
func KMSKeyID(masterKeyRef string) string {
	ref := strings.TrimSpace(masterKeyRef)
	switch {
	case strings.HasPrefix(ref, "arn:aws:kms:"), strings.HasPrefix(ref, "alias/"), kmsKeyIDPattern.MatchString(ref):
		return ref
	default:
		return ""
	}
}
