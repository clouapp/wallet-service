package faq

import (
	"context"
	"errors"
	"fmt"

	frameworkerrors "github.com/goravel/framework/errors"
	"github.com/goravel/framework/support/carbon"
)

// Published is the FAQ a reader of locale gets right now: sections in order,
// each with its live questions, every entity resolved along locale's chain on
// its own. It is never nil. "Live" is IsPublished against the framework
// clock, the one rule both reads use.
func (s *Service) Published(ctx context.Context, locale string) ([]LocalizedSection, error) {
	sections, err := s.tree(ctx)
	if err != nil {
		return nil, err
	}
	chain := s.locales.Chain(locale)
	now := carbon.Now().StdTime()
	published := make([]LocalizedSection, 0, len(sections))
	for _, section := range sections {
		if localized, ok := section.localize(chain, now); ok {
			published = append(published, localized)
		}
	}
	return published, nil
}

// PublishedQuestion is the question slug names, as a reader of locale gets
// it. A draft, a scheduled question and an unknown slug are all ErrNotFound:
// a reader must not be able to tell them apart.
func (s *Service) PublishedQuestion(ctx context.Context, locale, slug string) (LocalizedQuestion, error) {
	var question Question
	err := s.query(ctx).Where("slug", slug).FirstOrFail(&question)
	if errors.Is(err, frameworkerrors.OrmRecordNotFound) {
		return LocalizedQuestion{}, ErrNotFound
	}
	if err != nil {
		return LocalizedQuestion{}, fmt.Errorf("faq: loading question %q: %w", slug, err)
	}
	if !question.IsPublished(carbon.Now().StdTime()) {
		return LocalizedQuestion{}, ErrNotFound
	}
	localized, ok := question.localize(s.locales.Chain(locale))
	if !ok {
		return LocalizedQuestion{}, ErrNotFound
	}
	return localized, nil
}
