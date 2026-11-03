package faq

import (
	"context"
	"errors"
	"fmt"

	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/macrowallets/waas/packages/rampengine/packages/cms/i18n"
)

// Service is the FAQ: what readers get and what the panel edits. It queries
// through the ORM it is given and lets the database settle what a constraint
// can settle — a duplicate slug, a missing section, a section still in use —
// instead of reading first and racing the write.
type Service struct {
	db      contractsorm.Orm
	locales *i18n.Locales
}

// NewService returns the FAQ over db, writing and resolving text in locales.
func NewService(db contractsorm.Orm, locales *i18n.Locales) *Service {
	return &Service{db: db, locales: locales}
}

func (s *Service) query(ctx context.Context) contractsorm.Query {
	return s.db.WithContext(ctx).Query()
}

func (s *Service) transaction(ctx context.Context, work func(tx contractsorm.Query) error) error {
	query := s.db.WithContext(ctx).Query()
	if query.InTransaction() {
		return work(query)
	}
	return s.db.WithContext(ctx).Transaction(work)
}

// tree loads every section with every question, drafts included, in order.
func (s *Service) tree(ctx context.Context) ([]Section, error) {
	var sections []Section
	if err := ordered(s.query(ctx).With("Questions", ordered)).Get(&sections); err != nil {
		return nil, fmt.Errorf("faq: loading the sections: %w", err)
	}
	return sections, nil
}

// ordered is the order every list reads in. Two rows can share a position —
// two editors appending at once — and the id settles them.
func ordered(query contractsorm.Query) contractsorm.Query {
	return query.OrderBy("position").OrderBy("id")
}

// reload fills model with the row id names, as stored.
func reload(tx contractsorm.Query, model any, id uint) error {
	if err := tx.Where("id", id).FirstOrFail(model); err != nil {
		return fmt.Errorf("faq: reading back %d: %w", id, err)
	}
	return nil
}

// The constraints the migration names, and the SQLSTATEs they raise.
const (
	sectionSlugUnique      = "cms_faq_sections_slug_unique"
	questionSlugUnique     = "cms_faq_questions_slug_unique"
	questionSectionForeign = "cms_faq_questions_section_id_foreign"

	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

// violated reports whether err is Postgres refusing a write on constraint
// with code.
func violated(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
}
