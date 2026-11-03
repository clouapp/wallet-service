// Package contracts is what the CMS facade hands out.
package contracts

import (
	"context"

	"github.com/macrowallets/waas/packages/rampengine/packages/cms/faq"
	"github.com/macrowallets/waas/packages/rampengine/packages/cms/i18n"
)

// CMS is the configured locales and each content type's service.
type CMS interface {
	Locales() *i18n.Locales
	Faq() Faq
}

// Faq is the FAQ: the public reads take a locale, the editor's calls take
// and fill models.
type Faq interface {
	Published(ctx context.Context, locale string) ([]faq.LocalizedSection, error)
	PublishedQuestion(ctx context.Context, locale, slug string) (faq.LocalizedQuestion, error)

	Sections(ctx context.Context) ([]faq.Section, error)
	CreateSection(ctx context.Context, section *faq.Section) error
	UpdateSection(ctx context.Context, section *faq.Section) error
	DeleteSection(ctx context.Context, id uint) error
	ReorderSections(ctx context.Context, ids []uint) error

	CreateQuestion(ctx context.Context, question *faq.Question) error
	UpdateQuestion(ctx context.Context, question *faq.Question) error
	DeleteQuestion(ctx context.Context, id uint) error
	ReorderQuestions(ctx context.Context, sectionID uint, ids []uint) error
}
