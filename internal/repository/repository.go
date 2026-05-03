package repository

import "errors"

var (
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrDuplicateID       = errors.New("duplicate id")
	ErrUnknownID         = errors.New("unknown id")
	ErrIncompleteReorder = errors.New("incomplete reorder; all ids must be supplied")
)
