package faq

import (
	"context"
	"fmt"

	contractsorm "github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/packages/cms/i18n"
)

// Sections is the FAQ as the panel edits it: every section with every
// question, drafts included, in order, each text cut down to the configured
// locales so a locale dropped from the config leaves with the next save.
func (s *Service) Sections(ctx context.Context) ([]Section, error) {
	sections, err := s.tree(ctx)
	if err != nil {
		return nil, err
	}
	supported := s.locales.Supported()
	for i := range sections {
		sections[i].Title = sections[i].Title.Only(supported)
		for j := range sections[i].Questions {
			question := &sections[i].Questions[j]
			question.Question = question.Question.Only(supported)
			question.Answer = question.Answer.Only(supported)
		}
	}
	return sections, nil
}

// CreateSection validates section, appends it after the last section and
// fills it with what was stored.
func (s *Service) CreateSection(ctx context.Context, section *Section) error {
	if err := s.validateSection(section); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx contractsorm.Query) error {
		position, err := nextPosition(tx.Model(&Section{}))
		if err != nil {
			return err
		}
		section.ID = 0
		section.Position = position
		section.Questions = nil
		if err := tx.Create(section); err != nil {
			return sectionWriteError(err)
		}
		return reload(tx, section, section.ID)
	})
}

// UpdateSection replaces the slug and the title of the section section.ID
// names, keeps its position and fills section with what was stored.
func (s *Service) UpdateSection(ctx context.Context, section *Section) error {
	if err := s.validateSection(section); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx contractsorm.Query) error {
		result, err := tx.Model(&Section{}).Where("id", section.ID).Update(map[string]any{
			"slug":  section.Slug,
			"title": section.Title,
		})
		if err != nil {
			return sectionWriteError(err)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		section.Questions = nil
		return reload(tx, section, section.ID)
	})
}

// DeleteSection deletes the section id names. The foreign key refuses it
// while the section still has questions.
func (s *Service) DeleteSection(ctx context.Context, id uint) error {
	result, err := s.query(ctx).Where("id", id).Delete(&Section{})
	if violated(err, foreignKeyViolation, questionSectionForeign) {
		return ErrSectionNotEmpty
	}
	if err != nil {
		return fmt.Errorf("faq: deleting section %d: %w", id, err)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderSections puts the sections in the order ids lists them.
func (s *Service) ReorderSections(ctx context.Context, ids []uint) error {
	return s.transaction(ctx, func(tx contractsorm.Query) error {
		return reorder(tx, &Section{}, func(query contractsorm.Query) contractsorm.Query { return query }, ids)
	})
}

func (s *Service) validateSection(section *Section) error {
	return s.locales.Validate(i18n.Field{Name: "title", Value: section.Title})
}

func sectionWriteError(err error) error {
	if violated(err, uniqueViolation, sectionSlugUnique) {
		return ErrSlugTaken
	}
	return fmt.Errorf("faq: writing section: %w", err)
}
