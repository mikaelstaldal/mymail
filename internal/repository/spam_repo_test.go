package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/senderauth"
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

func TestTrustedCheckerHost(t *testing.T) {
	ctx := context.Background()
	r := NewSpamFilterRepository(openTestDB(t))
	s, err := r.GetSpamFilterSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, "", s.TrustedCheckerHost.Value)
	s.TrustedCheckerHost = oas.NewOptString("checker.example.org")
	got, err := r.UpdateSpamFilterSettings(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, "checker.example.org", got.TrustedCheckerHost.Value)
	for _, host := range []string{"invalid host", "checker\r\nInjected: yes", strings.Repeat("a", 254)} {
		s.TrustedCheckerHost = oas.NewOptString(host)
		_, err = r.UpdateSpamFilterSettings(ctx, s)
		require.ErrorIs(t, err, ErrInvalidCheckerHost)
	}
	// An older client's complete replacement omits the new field and disables it.
	s.TrustedCheckerHost = oas.OptString{}
	got, err = r.UpdateSpamFilterSettings(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, "", got.TrustedCheckerHost.Value)
}

func TestChangingCheckerRevokesStoredEvidenceWithoutWritingMail(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewSpamFilterRepository(db)
	s, err := r.GetSpamFilterSettings(ctx)
	require.NoError(t, err)
	s.TrustedCheckerHost = oas.NewOptString("checker")
	s, err = r.UpdateSpamFilterSettings(ctx, s)
	require.NoError(t, err)
	messages := NewMessageRepository(db)
	msg := makeMsg(1, "authenticated message")
	msg.Raw = []byte("From: a@example.org\r\nX-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-25) on checker\r\nX-Spam-Status: No, tests=DMARC_PASS,DKIM_VALID_AU,SPF_PASS\r\n\r\nbody")
	msg.SenderAuthentication = senderauth.Encode(oas.SenderAuthentication{Dmarc: "pass", Spf: "pass", DkimAligned: true})
	id, err := messages.InsertMessage(ctx, msg)
	require.NoError(t, err)
	var initialTimestamp string
	require.NoError(t, db.QueryRow(`SELECT updated_at FROM messages WHERE id = ?`, id).Scan(&initialTimestamp))
	// The old implementation fails here: saving rewrote every message, including
	// huge raw blobs in archives. No settings save may write a message row.
	_, err = db.Exec(`CREATE TRIGGER forbid_settings_mail_write BEFORE UPDATE ON messages BEGIN SELECT RAISE(ABORT, 'settings must not rewrite mail'); END`)
	require.NoError(t, err)
	s.Enabled = !s.Enabled
	_, err = r.UpdateSpamFilterSettings(ctx, s)
	require.NoError(t, err)
	got, err := messages.GetMessageDetail(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "pass", string(got.ToOASMessage(nil).SenderAuthentication.Dmarc))
	for _, host := range []string{"different", "", "checker"} {
		s.TrustedCheckerHost = oas.NewOptString(host)
		_, err = r.UpdateSpamFilterSettings(ctx, s)
		require.NoError(t, err)
		got, err = messages.GetMessageDetail(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, senderauth.Unknown(), got.ToOASMessage(nil).SenderAuthentication)
		full, err := messages.GetMessage(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, senderauth.Encode(senderauth.Unknown()), full.SenderAuthentication)
		var stored, timestamp string
		var raw []byte
		require.NoError(t, db.QueryRow(`SELECT sender_authentication, updated_at, raw FROM messages WHERE id=?`, id).Scan(&stored, &timestamp, &raw))
		assert.Equal(t, msg.SenderAuthentication, stored)
		assert.Equal(t, initialTimestamp, timestamp)
		assert.Equal(t, msg.Raw, raw)
	}
	// Re-selecting a previous hostname does not resurrect revoked evidence.
	// Only explicit backfill updates it to the new generation.
	_, err = db.Exec(`DROP TRIGGER forbid_settings_mail_write`)
	require.NoError(t, err)
	require.NoError(t, BackfillSenderAuthentication(ctx, db, "inbox"))
	got, err = messages.GetMessageDetail(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "pass", string(got.ToOASMessage(nil).SenderAuthentication.Dmarc))
}
