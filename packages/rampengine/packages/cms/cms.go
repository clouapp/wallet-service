// Package cms is the XIP content management system as a Goravel package: a
// shared base for translated, published content (i18n) and the content types
// built on it, starting with the FAQ.
package cms

import (
	"github.com/macrowallets/waas/packages/rampengine/packages/cms/contracts"
	"github.com/macrowallets/waas/packages/rampengine/packages/cms/faq"
	"github.com/macrowallets/waas/packages/rampengine/packages/cms/i18n"
)

var (
	_ contracts.CMS = (*Manager)(nil)
	_ contracts.Faq = (*faq.Service)(nil)
)

// Manager is what the CMS facade resolves.
type Manager struct {
	locales *i18n.Locales
	faq     contracts.Faq
}

// NewManager returns the CMS over locales and each content type's service.
func NewManager(locales *i18n.Locales, faqService contracts.Faq) *Manager {
	return &Manager{locales: locales, faq: faqService}
}

func (m *Manager) Locales() *i18n.Locales { return m.locales }

func (m *Manager) Faq() contracts.Faq { return m.faq }
