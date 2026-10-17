package repository

import (
	"context"
	"database/sql"
	"regexp"

	oas "github.com/mikaelstaldal/mymail/internal/api"
)

var checkerHostPattern = regexp.MustCompile(`^[A-Za-z0-9._-]*$`)

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
	var host string
	err := r.db.QueryRowContext(ctx,
		`SELECT enabled, score_header, score_threshold, trusted_checker_host FROM spam_filter_settings WHERE id = 1`,
	).Scan(&enabled, &s.ScoreHeader, &s.ScoreThreshold, &host)
	if err != nil {
		return oas.SpamFilterSettings{}, err
	}
	s.Enabled = enabled != 0
	s.TrustedCheckerHost = oas.NewOptString(host)
	return s, nil
}

// UpdateSpamFilterSettings fully replaces the spam filter configuration.
// score_header must be non-empty and at most 200 characters; score_threshold must be >= 0.
func (r *SpamFilterRepository) UpdateSpamFilterSettings(ctx context.Context, s oas.SpamFilterSettings) (oas.SpamFilterSettings, error) {
	if len(s.TrustedCheckerHost.Value) > 253 || !checkerHostPattern.MatchString(s.TrustedCheckerHost.Value) {
		return oas.SpamFilterSettings{}, ErrInvalidCheckerHost
	}
	if len(s.ScoreHeader) == 0 || len(s.ScoreHeader) > 200 {
		return oas.SpamFilterSettings{}, ErrInvalidScoreHeader
	}
	if s.ScoreThreshold < 0 {
		return oas.SpamFilterSettings{}, ErrInvalidScoreThreshold
	}

	// Revocation changes only the singleton settings row. Detail reads suppress
	// evidence from older generations; never rewrite the mailbox when saving.
	res, err := r.db.ExecContext(ctx,
		`UPDATE spam_filter_settings SET enabled = ?, score_header = ?, score_threshold = ?,
 auth_generation = auth_generation + CASE WHEN trusted_checker_host <> ? THEN 1 ELSE 0 END,
 trusted_checker_host = ? WHERE id = 1`,
		boolToInt(s.Enabled), s.ScoreHeader, s.ScoreThreshold, s.TrustedCheckerHost.Value, s.TrustedCheckerHost.Value,
	)
	if err != nil {
		return oas.SpamFilterSettings{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return oas.SpamFilterSettings{}, ErrNotFound
	}

	return r.GetSpamFilterSettings(ctx)
}
