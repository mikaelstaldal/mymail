package senderauth

import (
	"strings"
	"testing"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	checker := "X-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-25) on checker\r\n"
	for _, tc := range []struct {
		name, headers, host, dmarc, spf string
		dkim                            bool
	}{
		{"folded", checker + "X-Spam-Status: No, score=0 tests=DMARC_PASS,\r\n\tDKIM_VALID_AU,SPF_PASS autolearn=ham", "checker", "pass", "pass", true},
		{"reject", checker + "X-Spam-Status: Yes, tests=DMARC_REJECT,SPF_FAIL", "checker", "fail", "fail", false},
		{"quarantine", checker + "X-Spam-Status: No, tests=DMARC_QUAR,SPF_SOFTFAIL", "checker", "fail", "softfail", false},
		{"none policy fails auth", checker + "X-Spam-Status: No, tests=DMARC_NONE", "checker", "fail", "unknown", false},
		{"missing policy", checker + "X-Spam-Status: No, tests=DMARC_MISSING", "checker", "missing", "unknown", false},
		{"no tests", checker + "X-Spam-Status: No, score=0", "checker", "unknown", "unknown", false},
		{"empty tests", checker + "X-Spam-Status: No, tests= autolearn=ham", "checker", "unknown", "unknown", false},
		{"not a rule substring", checker + "X-Spam-Status: No, tests=NOT_DMARC_PASS,DKIM_VALID,SPF_HELO_PASS", "checker", "unknown", "unknown", false},
		{"different host", checker + "X-Spam-Status: No, tests=DMARC_PASS", "other", "unknown", "unknown", false},
		{"disabled", checker + "X-Spam-Status: No, tests=DMARC_PASS", "", "unknown", "unknown", false},
		{"duplicate checker", checker + checker + "X-Spam-Status: No, tests=DMARC_PASS", "checker", "unknown", "unknown", false},
		{"duplicate status", checker + "X-Spam-Status: No, tests=DMARC_PASS\r\nX-Spam-Status: No, tests=DMARC_PASS", "checker", "unknown", "unknown", false},
		{"duplicate tests", checker + "X-Spam-Status: No, tests=DMARC_PASS tests=SPF_PASS", "checker", "unknown", "unknown", false},
		{"conflicting evidence", checker + "X-Spam-Status: No, tests=DMARC_PASS,DMARC_NONE,SPF_PASS,SPF_FAIL", "checker", "fail", "fail", false},
		{"no headers", "", "checker", "unknown", "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse([]byte("From: a@example.org\r\n"+tc.headers+"\r\n\r\nbody"), tc.host)
			assert.Equal(t, oas.SenderAuthentication{Dmarc: oas.SenderAuthenticationDmarc(tc.dmarc), Spf: oas.SenderAuthenticationSpf(tc.spf), DkimAligned: tc.dkim}, got)
		})
	}
}

// This message was never scanned. Its bytes are indistinguishable from a real
// scanner's output, even with a Received header in either position. Enabling
// trust requires the operator to close unchecked delivery paths first.
func TestForgedHeadersTrustBoundary(t *testing.T) {
	forged := "X-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-25) on checker\r\nX-Spam-Status: No, tests=DMARC_PASS,DKIM_VALID_AU,SPF_PASS\r\n"
	for _, headers := range []string{forged, "Received: by local\r\n" + forged, forged + "Received: by local\r\n"} {
		raw := []byte(headers + "\r\n" + strings.Repeat("x", 600000))
		assert.Equal(t, Unknown(), Parse(raw, ""))
		assert.Equal(t, Unknown(), Parse(raw, "different"))
		assert.Equal(t, oas.SenderAuthenticationDmarcPass, Parse(raw, "checker").Dmarc, "hostname opt-in alone cannot detect unchecked pass-through")
	}
}
