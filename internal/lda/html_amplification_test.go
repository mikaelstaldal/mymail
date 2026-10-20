package lda

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// amplifyingHTMLMessage builds a single-part text/html message: "<p>", then
// `formatters` <b> elements that differ only in their `attr` attribute (so the
// HTML5 "Noah's Ark" limit of three identical entries does not apply), then
// `repeats` copies of "<p>x". Each "<p>" makes the HTML5 tree builder
// reconstruct every active formatting element, cloning `formatters` nodes per
// 4-byte repeat.
func amplifyingHTMLMessage(attr string, formatters, repeats int) []byte {
	var b strings.Builder
	b.WriteString("From: attacker@example.net\r\nTo: victim@example.org\r\nSubject: hi\r\n" +
		"Message-ID: <amp@example.net>\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n")
	b.WriteString("<p>")
	for i := 0; i < formatters; i++ {
		fmt.Fprintf(&b, "<b "+attr+">", i)
	}
	b.WriteString(strings.Repeat("<p>x", repeats))
	b.WriteString("\r\n")
	return []byte(b.String())
}

// TestParseMessage_HTMLAmplificationIsBounded asserts that what ParseMessage
// stores stays within a small multiple of the raw message, so that inbound
// HTML is never parsed and re-rendered without a node or output bound.
func TestParseMessage_HTMLAmplificationIsBounded(t *testing.T) {
	for _, tc := range []struct {
		attr                string
		formatters, repeats int
	}{
		{"id=%d", 400, 1000}, {"id=%d", 400, 4000},
		// The sanitizer keeps style, so this survives it.
		{"style=color:#%06x", 400, 1000}, {"style=color:#%06x", 400, 4000},
	} {
		raw := amplifyingHTMLMessage(tc.attr, tc.formatters, tc.repeats)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		pm, err := ParseMessage(raw)
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		require.NoError(t, err)
		t.Logf("raw=%d bytes  elapsed=%s  allocated=%d MB  body_html=%d bytes (%.0fx)  body_text=%d bytes",
			len(raw), elapsed.Round(time.Millisecond), (after.TotalAlloc-before.TotalAlloc)>>20,
			len(pm.BodyHTML), float64(len(pm.BodyHTML))/float64(len(raw)), len(pm.BodyText))
		assert.LessOrEqual(t, len(pm.BodyHTML), 10*len(raw), "body_html amplified beyond 10x the raw message")
		assert.LessOrEqual(t, len(pm.BodyText), 10*len(raw), "body_text amplified beyond 10x the raw message")
	}
}
