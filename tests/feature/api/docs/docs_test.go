package docs

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
)

// globalCSP is what every route but the Swagger UI answers with.
const globalCSP = "default-src 'none'; frame-ancestors 'none'"

type DocsTestSuite struct {
	ctltestutil.HTTPSuite
}

func TestDocs_Suite(t *testing.T) {
	ctltestutil.RunSuite(t, new(DocsTestSuite))
}

// The UI page loads swagger-ui from unpkg and boots it with an inline script,
// so the global default-src 'none' policy leaves it blank.
func (s *DocsTestSuite) TestSwaggerUI_Page_CarriesAPolicyItCanRunUnder() {
	resp := s.Get("/swagger/index.html", ctltestutil.Session{})
	resp.AssertOk()

	page, err := resp.Content()
	s.Require().NoError(err)
	csp := resp.Headers().Get("Content-Security-Policy")

	s.NotEqual(globalCSP, csp)
	s.Contains(csp, "script-src https://unpkg.com ")
	s.Contains(csp, "style-src https://unpkg.com")
	s.Contains(csp, "connect-src 'self'")
	s.Contains(csp, "frame-ancestors 'none'")
	s.NotContains(csp, "'unsafe-inline'", "the inline boot script is allowed by hash")
	s.NotContains(csp, "'unsafe-eval'")

	inline := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindStringSubmatch(page)
	s.Require().Len(inline, 2, "the page has one inline script")
	sum := sha256.Sum256([]byte(inline[1]))
	s.Contains(csp, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")

	s.Equal("DENY", resp.Headers().Get("X-Frame-Options"))
	s.Equal("nosniff", resp.Headers().Get("X-Content-Type-Options"))
	s.Equal("no-store", resp.Headers().Get("Cache-Control"))
}

func (s *DocsTestSuite) TestOtherRoutes_KeepTheGlobalPolicy() {
	for _, path := range []string{"/health", "/swagger/doc.json", "/v1/chains"} {
		resp := s.Get(path, ctltestutil.Session{})
		s.Equal(globalCSP, resp.Headers().Get("Content-Security-Policy"), path)
		s.False(strings.Contains(resp.Headers().Get("Content-Security-Policy"), "unpkg"), path)
	}
}
