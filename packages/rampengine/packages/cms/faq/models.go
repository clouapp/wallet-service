// Package faq is the CMS's first content type: sections of questions, each
// question published on its own, every text translated.
package faq

import (
	"time"

	"github.com/goravel/framework/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/packages/cms/i18n"
)

// Section groups questions under a translated title.
type Section struct {
	orm.Model
	Slug      string
	Title     i18n.Translations
	Position  int
	Questions []Question
}

func (Section) TableName() string { return "cms_faq_sections" }

// Question is one entry: a translated question, its translated markdown
// answer, and when it went live. A nil PublishedAt is a draft; one in the
// future is scheduled.
type Question struct {
	orm.Model
	SectionID   uint
	Slug        string
	Question    i18n.Translations
	Answer      i18n.Translations
	Position    int
	PublishedAt *time.Time
}

func (Question) TableName() string { return "cms_faq_questions" }

// IsPublished reports whether q is live at now.
func (q Question) IsPublished(now time.Time) bool {
	return q.PublishedAt != nil && !q.PublishedAt.After(now)
}

// LocalizedSection is a section as a reader gets it: one locale, live
// questions only.
type LocalizedSection struct {
	Slug      string
	Locale    string
	Title     string
	Questions []LocalizedQuestion
}

// LocalizedQuestion is a question and its answer in the one locale both have.
type LocalizedQuestion struct {
	Slug     string
	Locale   string
	Question string
	Answer   string
}

// localize resolves q along chain, question and answer together.
func (q Question) localize(chain []string) (LocalizedQuestion, bool) {
	locale, ok := i18n.Resolve(chain, q.Question, q.Answer)
	if !ok {
		return LocalizedQuestion{}, false
	}
	return LocalizedQuestion{Slug: q.Slug, Locale: locale, Question: q.Question[locale], Answer: q.Answer[locale]}, true
}

// localize is s as a reader of chain gets it at now. The title resolves on its
// own; a section left with no live question it can show is not shown at all.
func (s Section) localize(chain []string, now time.Time) (LocalizedSection, bool) {
	locale, ok := i18n.Resolve(chain, s.Title)
	if !ok {
		return LocalizedSection{}, false
	}
	questions := make([]LocalizedQuestion, 0, len(s.Questions))
	for _, question := range s.Questions {
		if !question.IsPublished(now) {
			continue
		}
		if localized, ok := question.localize(chain); ok {
			questions = append(questions, localized)
		}
	}
	if len(questions) == 0 {
		return LocalizedSection{}, false
	}
	return LocalizedSection{Slug: s.Slug, Locale: locale, Title: s.Title[locale], Questions: questions}, true
}
