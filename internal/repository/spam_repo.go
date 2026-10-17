package repository

import (
	"context"
	"database/sql"
	"regexp"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/senderauth"
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

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return oas.SpamFilterSettings{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	// Revoke cached evidence atomically when the configured trust root changes.
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET sender_authentication = ?
 WHERE EXISTS (SELECT 1 FROM spam_filter_settings WHERE id = 1 AND trusted_checker_host <> ?)`,
		senderauth.Encode(senderauth.Unknown()), s.TrustedCheckerHost.Value); err != nil {
		return oas.SpamFilterSettings{}, err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE spam_filter_settings SET enabled = ?, score_header = ?, score_threshold = ?, trusted_checker_host = ? WHERE id = 1`,
		boolToInt(s.Enabled), s.ScoreHeader, s.ScoreThreshold, s.TrustedCheckerHost.Value,
	)
	if err != nil {
		return oas.SpamFilterSettings{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return oas.SpamFilterSettings{}, ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return oas.SpamFilterSettings{}, err
	}
	return r.GetSpamFilterSettings(ctx)
}
