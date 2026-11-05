package faq

import "errors"

// The FAQ's refusals, matched with errors.Is.
var (
	// ErrNotFound: no section or question has the id, or no live question the slug.
	ErrNotFound = errors.New("faq: not found")
	// ErrSlugTaken: another section, or another question, already has the slug.
	ErrSlugTaken = errors.New("faq: slug taken")
	// ErrSectionNotFound: a question names a section that does not exist.
	ErrSectionNotFound = errors.New("faq: section not found")
	// ErrSectionNotEmpty: a section that still has questions cannot be deleted.
	ErrSectionNotEmpty = errors.New("faq: section not empty")
	// ErrOrderMismatch: a reorder's ids are not exactly the ids it reorders.
	ErrOrderMismatch = errors.New("faq: order does not match the current rows")
)
