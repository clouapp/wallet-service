package contract

import (
	"fmt"
	"strings"
)

// Exchange is one recorded request and its answer. Path is the route as the
// scenario wrote it, with placeholders, so it does not change between runs.
type Exchange struct {
	Step        string
	Method      string
	Path        string
	Status      int
	ContentType string
	Body        string
}

const exchangeMarker = "### "

// Render writes exchanges in the snapshot format: one section per exchange,
// the body kept byte for byte (after normalization) on the lines that follow
// its headers.
func Render(exchanges []Exchange) string {
	var builder strings.Builder
	builder.WriteString("# HTTP contract snapshot. Rewrite with: go test ./tests/contract -update-contract\n")
	builder.WriteString("# Bodies are raw bytes after normalization (uuids, timestamps, tokens).\n")
	for _, exchange := range exchanges {
		builder.WriteString("\n")
		fmt.Fprintf(&builder, "%s%s: %s %s\n", exchangeMarker, exchange.Step, exchange.Method, exchange.Path)
		fmt.Fprintf(&builder, "status: %d\n", exchange.Status)
		fmt.Fprintf(&builder, "content-type: %s\n", exchange.ContentType)
		builder.WriteString(exchange.Body)
		builder.WriteString("\n")
	}
	return builder.String()
}

// Sections splits a rendered snapshot into its exchanges, keyed by the
// "### step: METHOD path" line, in order.
func Sections(snapshot string) ([]string, map[string]string) {
	var order []string
	sections := map[string]string{}
	parts := strings.Split(snapshot, "\n"+exchangeMarker)
	for _, part := range parts[1:] {
		header, body, _ := strings.Cut(part, "\n")
		order = append(order, header)
		sections[header] = strings.TrimRight(body, "\n")
	}
	return order, sections
}

// Diff returns one line per exchange that was added, removed or changed
// between want and got, with both versions of a changed one.
func Diff(want, got string) []string {
	wantOrder, wantSections := Sections(want)
	gotOrder, gotSections := Sections(got)
	var differences []string
	for _, header := range wantOrder {
		gotSection, present := gotSections[header]
		switch {
		case !present:
			differences = append(differences, fmt.Sprintf("removed: %s", header))
		case gotSection != wantSections[header]:
			differences = append(differences, fmt.Sprintf("changed: %s\n--- want\n%s\n+++ got\n%s",
				header, wantSections[header], gotSection))
		}
	}
	for _, header := range gotOrder {
		if _, present := wantSections[header]; !present {
			differences = append(differences, fmt.Sprintf("added: %s", header))
		}
	}
	if len(differences) == 0 && want != got {
		differences = append(differences, "the snapshots differ outside any exchange (header or order)")
	}
	return differences
}

// RenderDiff joins Diff lines the way testdata/http_contract_base.diff stores them.
func RenderDiff(differences []string) string {
	if len(differences) == 0 {
		return ""
	}
	return strings.Join(differences, "\n") + "\n"
}
