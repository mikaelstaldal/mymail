package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	oas "github.com/mikaelstaldal/mymail/internal/api"
)

func TestGetSpamFilterSettings_Defaults(t *testing.T) {
	ctx := context.Background()
	r := NewSpamFilterRepository(openTestDB(t))

	s, err := r.GetSpamFilterSettings(ctx)
	if err != nil {
		t.Fatalf("GetSpamFilterSettings: %v", err)
	}
	if !s.Enabled {
		t.Error("default enabled should be true")
	}
	if s.ScoreHeader != "X-Spam-Score" {
		t.Errorf("default score_header = %q, want %q", s.ScoreHeader, "X-Spam-Score")
	}
	if s.ScoreThreshold != 5.0 {
		t.Errorf("default score_threshold = %v, want 5.0", s.ScoreThreshold)
	}
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
			if !errors.Is(err, tc.want) {
				t.Errorf("UpdateSpamFilterSettings: got %v, want %v", err, tc.want)
			}
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
	if err != nil {
		t.Fatalf("200-char header: %v", err)
	}
	if got.ScoreHeader != header200 {
		t.Errorf("score_header not persisted correctly")
	}
	if got.Enabled {
		t.Error("enabled should be false")
	}
	if got.ScoreThreshold != 0 {
		t.Errorf("score_threshold = %v, want 0", got.ScoreThreshold)
	}
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
	if err != nil {
		t.Fatalf("UpdateSpamFilterSettings: %v", err)
	}
	if got.Enabled != want.Enabled || got.ScoreHeader != want.ScoreHeader || got.ScoreThreshold != want.ScoreThreshold {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, want)
	}

	// Verify persistence via independent Get.
	fetched, err := r.GetSpamFilterSettings(ctx)
	if err != nil {
		t.Fatalf("GetSpamFilterSettings after update: %v", err)
	}
	if fetched.ScoreHeader != want.ScoreHeader || fetched.ScoreThreshold != want.ScoreThreshold {
		t.Errorf("fetched mismatch: %+v", fetched)
	}
}

func TestUpdateSpamFilterSettings_ZeroThresholdIsValid(t *testing.T) {
	ctx := context.Background()
	r := NewSpamFilterRepository(openTestDB(t))

	_, err := r.UpdateSpamFilterSettings(ctx, oas.SpamFilterSettings{
		Enabled: true, ScoreHeader: "X-Spam-Score", ScoreThreshold: 0,
	})
	if err != nil {
		t.Errorf("threshold=0 should be valid, got %v", err)
	}
}
