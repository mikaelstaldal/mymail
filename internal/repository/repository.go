package repository

import "errors"

var (
	ErrNotFound              = errors.New("not found")
	ErrConflict              = errors.New("conflict")
	ErrUnknownIdentity       = errors.New("unknown identity")
	ErrTooManyIDs            = errors.New("too many ids")
	ErrForbiddenFolder       = errors.New("operation not permitted in this folder")
	ErrSnoozeTimeTooSoon     = errors.New("snooze time must be at least 60 seconds in the future")
	ErrDuplicateID           = errors.New("duplicate id")
	ErrUnknownID             = errors.New("unknown id")
	ErrIncompleteReorder     = errors.New("incomplete reorder; all ids must be supplied")
	ErrInvalidAddress        = errors.New("invalid address")
	ErrLastIdentity          = errors.New("cannot delete the last identity")
	ErrInvalidFilter         = errors.New("at least one of match_from, match_to, match_subject must be non-empty")
	ErrInvalidAction         = errors.New("action must be one of: move, trash, mark_read, drop")
	ErrInvalidFolderTarget   = errors.New("folder_id for move action must be 1 (Inbox), 4 (Trash), 7 (Junk), or a user folder (id >= 100)")
	ErrInvalidScoreHeader    = errors.New("score_header must be between 1 and 200 characters")
	ErrInvalidScoreThreshold = errors.New("score_threshold must be >= 0")
)
