package lda

import (
	"testing"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The owner has a trusted-sender filter: match_from "boss@trusted.com" -> move
// to a "VIP" folder, stop. Every message below is flagged as spam
// (X-Spam-Flag: YES), so spam detection picks Junk (7). A message from the real
// boss@trusted.com should still be moved to VIP (documented: move overrides
// spam detection). A message from an address the attacker controls must not
// match the filter, so it must stay in Junk.
func TestMatchFromDoesNotMatchAttackerControlledSender(t *testing.T) {
	cases := []struct {
		name, from, msgID string
		wantFolder        int
	}{
		// Legitimate sender: the filter must keep working.
		{"real sender", `Boss <boss@trusted.com>`, "ok1@trusted.com", 100},
		{"real sender, case differs", `BOSS@Trusted.COM`, "ok2@trusted.com", 100},

		// Attacker mail from evil.com; SPF/DKIM/DMARC can all pass for evil.com.
		{"quoted display name", `"boss@trusted.com" <attacker@evil.com>`, "r1@evil.com", 7},
		{"RFC 2047 display name", `=?UTF-8?B?Ym9zc0B0cnVzdGVkLmNvbQ==?= <attacker@evil.com>`, "r2@evil.com", 7},
		{"malformed addr-spec fallback", `"boss@trusted.com" <attacker x@evil.com>`, "r3@evil.com", 7},
		{"look-alike subdomain", `boss@trusted.com.evil.com`, "r4@evil.com", 7},
		{"quoted local part", `<"boss@trusted.com"@evil.com>`, "r5@evil.com", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openLDATestDB(t)
			_, err := db.Exec(`INSERT INTO folders(id, name, slug, position) VALUES (100, 'VIP', 'vip', 10)`)
			require.NoError(t, err)
			createFilter(t, db, oas.Filter{
				Name:      "VIP",
				MatchFrom: "boss@trusted.com",
				Action:    oas.FilterActionMove,
				FolderID:  oas.NewOptNilInt(100),
				Stop:      true,
			})
			raw := []byte("From: " + tc.from + "\r\n" +
				"To: me@example.com\r\n" +
				"Subject: Urgent wire transfer\r\n" +
				"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
				"Message-Id: <" + tc.msgID + ">\r\n" +
				"X-Spam-Flag: YES\r\n" +
				"\r\n" +
				"Please pay the attached invoice today.\r\n")
			require.Equal(t, 0, runCore(db, raw))

			var fromAddr string
			require.NoError(t, db.QueryRow(`SELECT from_addr FROM messages WHERE message_id = ?`, tc.msgID).Scan(&fromAddr))
			folderID, _ := queryMessage(t, db, tc.msgID)
			t.Logf("From: %s -> from_addr=%q folder_id=%d", tc.from, fromAddr, folderID)
			assert.Equal(t, tc.wantFolder, folderID)
		})
	}
}

func TestSenderFilterBoundaries(t *testing.T) {
	for _, tc := range []struct {
		from, criterion string
		want            bool
	}{
		{"user@trusted.com", "@trusted.com", true},
		{"user@sub.trusted.com", "trusted.com", true},
		{"user@trusted.com.evil.com", "@trusted.com", false},
		{"user@nottrusted.com", "trusted.com", false},
		{"user@evil.com", "trusted.com", false},
		{"boss@trusted.com", "oss@trusted.com", false},
		{"boss@trusted.com", "boss", true},
		{"Other <user@evil.com>", "Other", false},
		{"boss@trusted.com\r\nFrom: other@evil.com", "boss@trusted.com", false},
		{"", "boss", false},
		{"attacker@evil.com, boss@trusted.com", "boss@trusted.com", false},
		{"boss@Key.com", "boss@key.com", false},
		{"boss@İ.com", "boss@i.com", false},
	} {
		t.Run(tc.from+"/"+tc.criterion, func(t *testing.T) {
			pm, err := ParseMessage([]byte("From: " + tc.from + "\r\n\r\nbody"))
			require.NoError(t, err)
			assert.Equal(t, tc.want, filterMatches(pm, oas.Filter{MatchFrom: tc.criterion}))
		})
	}
}
