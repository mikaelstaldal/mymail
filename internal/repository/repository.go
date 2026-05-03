package repository

import "errors"

var (
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrTooManyIDs        = errors.New("too many ids")
	ErrForbiddenFolder   = errors.New("operation not permitted in this folder")
	ErrSnoozeTimeTooSoon = errors.New("snooze time must be at least 60 seconds in the future")
	ErrDuplicateID       = errors.New("duplicate id")
	ErrUnknownID         = errors.New("unknown id")
	ErrIncompleteReorder = errors.New("incomplete reorder; all ids must be supplied")
	ErrInvalidAddress    = errors.New("invalid address")
	ErrLastIdentity      = errors.New("cannot delete the last identity")
)
