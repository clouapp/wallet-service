package mails

import (
	"bytes"
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templateFS embed.FS

var mailTemplates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// render executes a presentational template. data is built by the mailable.
// A failure names the template only.
func render(name string, data any) string {
	var buf bytes.Buffer
	if err := mailTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		panic("mail template " + name)
	}
	return buf.String()
}
