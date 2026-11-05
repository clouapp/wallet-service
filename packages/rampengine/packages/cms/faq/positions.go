package faq

import (
	"fmt"

	contractsorm "github.com/goravel/framework/contracts/database/orm"
)

// nextPosition is one past the highest position among scope's rows, 0 when
// there are none.
func nextPosition(scope contractsorm.Query) (int, error) {
	var positions []int
	if err := scope.OrderByDesc("position").Limit(1).Pluck("position", &positions); err != nil {
		return 0, fmt.Errorf("faq: reading the last position: %w", err)
	}
	if len(positions) == 0 {
		return 0, nil
	}
	return positions[0] + 1, nil
}

// reorder writes positions 0..n-1 to the rows of scope, in the order ids
// lists them. ids must be exactly scope's ids: missing, extra or repeated
// means the editor's list was stale, and nothing is written. The rows stay
// locked until tx's transaction ends, so two reorders queue instead of mixing.
func reorder(tx contractsorm.Query, model any, scope func(contractsorm.Query) contractsorm.Query, ids []uint) error {
	var current []uint
	if err := scope(tx.Model(model)).LockForUpdate().Pluck("id", &current); err != nil {
		return fmt.Errorf("faq: locking the rows to reorder: %w", err)
	}
	if !sameSet(current, ids) {
		return ErrOrderMismatch
	}
	for position, id := range ids {
		if _, err := tx.Model(model).Where("id", id).Update("position", position); err != nil {
			return fmt.Errorf("faq: moving %d to %d: %w", id, position, err)
		}
	}
	return nil
}

// sameSet reports whether ids holds each of current exactly once, and nothing else.
func sameSet(current, ids []uint) bool {
	if len(current) != len(ids) {
		return false
	}
	left := make(map[uint]struct{}, len(current))
	for _, id := range current {
		left[id] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := left[id]; !ok {
			return false
		}
		delete(left, id)
	}
	return true
}
