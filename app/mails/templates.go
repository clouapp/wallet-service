package mails

import (
	"bytes"
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templateFS embed.FS

// mailTemplates are the messages and layout.html, the frame they share.
var mailTemplates = template.Must(template.New("mails").Funcs(template.FuncMap{
	"button": func(link template.URL, label string) buttonData { return buttonData{Link: link, Label: label} },
}).ParseFS(templateFS, "templates/*.html"))

// buttonData is what layout.html's mail_button draws: the call to action and
// the same link written out for a client that does not follow the button.
type buttonData struct {
	Link  template.URL
	Label string
}

// render executes a presentational template. data is built by the mailable.
// A failure names the template only.
func render(name string, data any) string {
	var buf bytes.Buffer
	if err := mailTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		panic("mail template " + name)
	}
	return buf.String()
}
