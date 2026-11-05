package faq

import (
	"context"
	"errors"
	"fmt"
	"time"

	contractsorm "github.com/goravel/framework/contracts/database/orm"
	frameworkerrors "github.com/goravel/framework/errors"

	"github.com/macrowallets/waas/packages/rampengine/packages/cms/i18n"
)

// CreateQuestion validates question, appends it to its section and fills it
// with what was stored. PublishedAt is stored in UTC.
func (s *Service) CreateQuestion(ctx context.Context, question *Question) error {
	if err := s.validateQuestion(question); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx contractsorm.Query) error {
		position, err := nextPosition(inSection(tx.Model(&Question{}), question.SectionID))
		if err != nil {
			return err
		}
		question.ID = 0
		question.Position = position
		question.PublishedAt = inUTC(question.PublishedAt)
		if err := tx.Create(question); err != nil {
			return questionWriteError(err)
		}
		return reload(tx, question, question.ID)
	})
}

// UpdateQuestion replaces every editable field of the question question.ID
// names: section, slug, texts and PublishedAt (nil unpublishes). It keeps its
// position, unless it moves to another section, where it goes to the end.
func (s *Service) UpdateQuestion(ctx context.Context, question *Question) error {
	if err := s.validateQuestion(question); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx contractsorm.Query) error {
		var current Question
		err := tx.Where("id", question.ID).LockForUpdate().FirstOrFail(&current)
		if errors.Is(err, frameworkerrors.OrmRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("faq: loading question %d: %w", question.ID, err)
		}
		position := current.Position
		if current.SectionID != question.SectionID {
			if position, err = nextPosition(inSection(tx.Model(&Question{}), question.SectionID)); err != nil {
				return err
			}
		}
		if _, err := tx.Model(&Question{}).Where("id", question.ID).Update(map[string]any{
			"section_id":   question.SectionID,
			"slug":         question.Slug,
			"question":     question.Question,
			"answer":       question.Answer,
			"position":     position,
			"published_at": inUTC(question.PublishedAt),
		}); err != nil {
			return questionWriteError(err)
		}
		return reload(tx, question, question.ID)
	})
}

// DeleteQuestion deletes the question id names.
func (s *Service) DeleteQuestion(ctx context.Context, id uint) error {
	result, err := s.query(ctx).Where("id", id).Delete(&Question{})
	if err != nil {
		return fmt.Errorf("faq: deleting question %d: %w", id, err)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderQuestions puts the questions of section sectionID in the order ids
// lists them.
func (s *Service) ReorderQuestions(ctx context.Context, sectionID uint, ids []uint) error {
	return s.transaction(ctx, func(tx contractsorm.Query) error {
		return reorder(tx, &Question{}, func(query contractsorm.Query) contractsorm.Query {
			return inSection(query, sectionID)
		}, ids)
	})
}

func (s *Service) validateQuestion(question *Question) error {
	return s.locales.Validate(
		i18n.Field{Name: "question", Value: question.Question},
		i18n.Field{Name: "answer", Value: question.Answer},
	)
}

func inSection(query contractsorm.Query, sectionID uint) contractsorm.Query {
	return query.Where("section_id", sectionID)
}

// inUTC is t as the same instant in UTC: the column has no zone.
func inUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}

func questionWriteError(err error) error {
	switch {
	case violated(err, uniqueViolation, questionSlugUnique):
		return ErrSlugTaken
	case violated(err, foreignKeyViolation, questionSectionForeign):
		return ErrSectionNotFound
	}
	return fmt.Errorf("faq: writing question: %w", err)
}
