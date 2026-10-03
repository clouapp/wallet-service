package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/macrowallets/waas/pkg/pyjson"
)

// CustomIDTag is the moto tag that makes CreateSecret reuse an ARN suffix.
const CustomIDTag = "_custom_id_"

// Tag is a Secrets Manager resource tag.
type Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// Entry is one secret as stored in a snapshot. Exactly one of SecretBinaryB64 and
// SecretString normally holds the value; both are kept as pointers because the
// snapshot format distinguishes null from "".
type Entry struct {
	Name            string  `json:"name"`
	ARN             string  `json:"arn"`
	VersionID       *string `json:"version_id"`
	Description     *string `json:"description"`
	Tags            []Tag   `json:"tags"`
	SecretBinaryB64 *string `json:"secret_binary_b64"`
	SecretString    *string `json:"secret_string"`
}

// SameMaterial reports whether both entries hold the same secret value.
func (e Entry) SameMaterial(other Entry) bool {
	return equalOptional(e.SecretBinaryB64, other.SecretBinaryB64) && equalOptional(e.SecretString, other.SecretString)
}

func equalOptional(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (e Entry) pythonObject() pyjson.Object {
	tags := make([]any, len(e.Tags))
	for index, tag := range e.Tags {
		tags[index] = pyjson.Object{{Key: "Key", Value: tag.Key}, {Key: "Value", Value: tag.Value}}
	}
	return pyjson.Object{
		{Key: "name", Value: e.Name},
		{Key: "arn", Value: e.ARN},
		{Key: "version_id", Value: e.VersionID},
		{Key: "description", Value: e.Description},
		{Key: "tags", Value: tags},
		{Key: "secret_binary_b64", Value: e.SecretBinaryB64},
		{Key: "secret_string", Value: e.SecretString},
	}
}

// Digest is sha256 of json.dumps(sorted([name, arn, binary_b64, string] ...),
// separators=(",", ":")), the value every meta file records.
func Digest(entries []Entry) (string, error) {
	sorted := sortedByName(entries)
	rows := make([]any, len(sorted))
	for index, entry := range sorted {
		rows[index] = []any{entry.Name, entry.ARN, entry.SecretBinaryB64, entry.SecretString}
	}
	canonical, err := pyjson.Dumps(rows, pyjson.Compact)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

// sortedByName returns a copy of entries ordered like Python's sorted() over
// [name, arn, ...] lists: names are unique, ARNs break any tie.
func sortedByName(entries []Entry) []Entry {
	sorted := append([]Entry(nil), entries...)
	sort.SliceStable(sorted, func(left, right int) bool {
		if sorted[left].Name != sorted[right].Name {
			return sorted[left].Name < sorted[right].Name
		}
		return sorted[left].ARN < sorted[right].ARN
	})
	return sorted
}

// The snapshot files and their digests were first written by a Python tool with
// json.dumps; pyjson keeps that byte layout so old snapshots stay valid.

// withoutCustomID drops the moto ARN-suffix tag, which only exists between a
// restore's CreateSecret and its UntagResource.
func withoutCustomID(tags []Tag) []Tag {
	kept := make([]Tag, 0, len(tags))
	for _, tag := range tags {
		if tag.Key != CustomIDTag {
			kept = append(kept, tag)
		}
	}
	return kept
}
