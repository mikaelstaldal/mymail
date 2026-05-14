package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	oas "github.com/mikaelstaldal/mymail/internal/api"
)

func TestGetSpamFilterSettings_Defaults(t *testing.T) {
	ctx := context.Background()
	r := NewSpamFilterRepository(openTestDB(t))

	s, err := r.GetSpamFilterSettings(ctx)
	require.NoError(t, err)
	assert.True(t, s.Enabled)
	assert.Equal(t, "X-Spam-Score", s.ScoreHeader)
	assert.Equal(t, 5.0, s.ScoreThreshold)
}

func TestUpdateSpamFilterSettings_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	r := NewSpamFilterRepository(openTestDB(t))

	cases := []struct {
		name     string
		settings oas.SpamFilterSettings
		want     error
	}{
		{
			name:     "empty score_header",
			settings: oas.SpamFilterSettings{Enabled: true, ScoreHeader: "", ScoreThreshold: 5.0},
			want:     ErrInvalidScoreHeader,
		},
		{
			name:     "score_header too long (201 chars)",
			settings: oas.SpamFilterSettings{Enabled: true, ScoreHeader: strings.Repeat("x", 201), ScoreThreshold: 5.0},
			want:     ErrInvalidScoreHeader,
		},
		{
			name:     "negative score_threshold",
			settings: oas.SpamFilterSettings{Enabled: true, ScoreHeader: "X-Spam-Score", ScoreThreshold: -0.1},
			want:     ErrInvalidScoreThreshold,
		},
		{
			name:     "negative integer threshold",
			settings: oas.SpamFilterSettings{Enabled: true, ScoreHeader: "X-Spam-Score", ScoreThreshold: -1},
			want:     ErrInvalidScoreThreshold,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.UpdateSpamFilterSettings(ctx, tc.settings)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestUpdateSpamFilterSettings_ValidBoundary(t *testing.T) {
	ctx := context.Background()
	r := NewSpamFilterRepository(openTestDB(t))

	// score_header exactly 200 chars is valid.
	header200 := strings.Repeat("X", 200)
	got, err := r.UpdateSpamFilterSettings(ctx, oas.SpamFilterSettings{
		Enabled: false, ScoreHeader: header200, ScoreThreshold: 0,
	})
	require.NoError(t, err)
	assert.Equal(t, header200, got.ScoreHeader)
	assert.False(t, got.Enabled)
	assert.Equal(t, 0.0, got.ScoreThreshold)
}

func TestUpdateSpamFilterSettings_Roundtrip(t *testing.T) {
	ctx := context.Background()
	r := NewSpamFilterRepository(openTestDB(t))

	want := oas.SpamFilterSettings{
		Enabled:        false,
		ScoreHeader:    "X-Custom-Spam",
		ScoreThreshold: 3.5,
	}
	got, err := r.UpdateSpamFilterSettings(ctx, want)
	require.NoError(t, err)
	assert.Equal(t, want.Enabled, got.Enabled)
	assert.Equal(t, want.ScoreHeader, got.ScoreHeader)
	assert.Equal(t, want.ScoreThreshold, got.ScoreThreshold)

	// Verify persistence via independent Get.
	fetched, err := r.GetSpamFilterSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, want.ScoreHeader, fetched.ScoreHeader)
	assert.Equal(t, want.ScoreThreshold, fetched.ScoreThreshold)
}

func TestUpdateSpamFilterSettings_ZeroThresholdIsValid(t *testing.T) {
	ctx := context.Background()
	r := NewSpamFilterRepository(openTestDB(t))

	_, err := r.UpdateSpamFilterSettings(ctx, oas.SpamFilterSettings{
		Enabled: true, ScoreHeader: "X-Spam-Score", ScoreThreshold: 0,
	})
	assert.NoError(t, err, "threshold=0 should be valid")
}
