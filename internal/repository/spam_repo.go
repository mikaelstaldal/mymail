package repository

import (
	"context"
	"database/sql"

	oas "github.com/mikaelstaldal/mymail/internal/api"
)

// SpamFilterRepository provides spam filter settings operations backed by SQLite.
type SpamFilterRepository struct {
	db *sql.DB
}

// NewSpamFilterRepository creates a SpamFilterRepository.
func NewSpamFilterRepository(db *sql.DB) *SpamFilterRepository {
	return &SpamFilterRepository{db: db}
}

// GetSpamFilterSettings returns the singleton spam filter configuration.
func (r *SpamFilterRepository) GetSpamFilterSettings(ctx context.Context) (oas.SpamFilterSettings, error) {
	var s oas.SpamFilterSettings
	var enabled int
	err := r.db.QueryRowContext(ctx,
		`SELECT enabled, score_header, score_threshold FROM spam_filter_settings WHERE id = 1`,
	).Scan(&enabled, &s.ScoreHeader, &s.ScoreThreshold)
	if err != nil {
		return oas.SpamFilterSettings{}, err
	}
	s.Enabled = enabled != 0
	return s, nil
}

// UpdateSpamFilterSettings fully replaces the spam filter configuration.
// score_header must be non-empty and at most 200 characters; score_threshold must be >= 0.
func (r *SpamFilterRepository) UpdateSpamFilterSettings(ctx context.Context, s oas.SpamFilterSettings) (oas.SpamFilterSettings, error) {
	if len(s.ScoreHeader) == 0 || len(s.ScoreHeader) > 200 {
		return oas.SpamFilterSettings{}, ErrInvalidScoreHeader
	}
	if s.ScoreThreshold < 0 {
		return oas.SpamFilterSettings{}, ErrInvalidScoreThreshold
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE spam_filter_settings SET enabled = ?, score_header = ?, score_threshold = ? WHERE id = 1`,
		boolToInt(s.Enabled), s.ScoreHeader, s.ScoreThreshold,
	)
	if err != nil {
		return oas.SpamFilterSettings{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return oas.SpamFilterSettings{}, ErrNotFound
	}

	return r.GetSpamFilterSettings(ctx)
}
