package rules

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A bank statement is downloaded from the bank's app as a PDF or photographed,
// so the field takes either. start_with takes a single prefix, which is why
// the image fields can use it and this one cannot.
func TestKycDocumentFileRule(t *testing.T) {
	rule := &KycDocumentFileRule{}
	require.Equal(t, "kyc_document_file", rule.Signature())

	cases := []struct {
		name string
		val  any
		want bool
	}{
		{"a pdf", "data:application/pdf;base64,JVBERi0=", true},
		{"a jpeg", "data:image/jpeg;base64,/9j/", true},
		{"a png", "data:image/png;base64,iVBO", true},
		{"a mime in capitals", "data:Application/PDF;base64,JVBERi0=", true},
		{"a word document", "data:application/msword;base64,AAAA", false},
		{"plain text", "data:text/plain;base64,AAAA", false},
		{"not a data uri", "https://example.com/statement.pdf", false},
		{"a bare base64 payload", "JVBERi0=", false},
		{"not a string", 42, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, rule.Passes(context.Background(), mapData{}, c.val))
		})
	}
}
