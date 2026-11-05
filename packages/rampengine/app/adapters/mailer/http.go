package mailer

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Deliver performs the one POST an HTTP driver makes and reports a failure as
// "<driver>: post: <cause>" or "<driver>: <status>: <body>". Those strings reach
// the admin's test-send answer, and the body is the actionable half: it names
// the unverified domain or the rejected address.
func Deliver(client *http.Client, driver string, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: post: %w", driver, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Best effort: a body that cannot be read still leaves the status.
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s: %d: %s", driver, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
