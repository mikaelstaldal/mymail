// Package senderauth interprets SpamAssassin results, conditional on an
// operator-secured scanning path. Headers alone cannot prove a scan occurred.
package senderauth

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/mail"
	"net/textproto"
	"regexp"
	"slices"
	"strings"

	oas "github.com/mikaelstaldal/mymail/internal/api"
)

var checker = regexp.MustCompile(`^SpamAssassin\s+[^\s]+\s+\([^\r\n]*\)\s+on\s+([A-Za-z0-9._-]+)$`)
var testsStart = regexp.MustCompile(`(?:^|\s)tests=`)
var tests = regexp.MustCompile(`(?:^|\s)tests=(.*?)(?:\s+[A-Za-z_]+=|$)`)
var rule = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// Unknown is also the verdict for drafts, outbound mail and absent evidence.
func Unknown() oas.SenderAuthentication {
	return oas.SenderAuthentication{Dmarc: "unknown", Spf: "unknown"}
}

// Fields returns every value of the named header field, including fields in
// RFC 5322 obsolete syntax with whitespace before the colon (`From : x`).
// net/textproto accepts those but files them under a key such as "From ", so
// Header.Get never sees them, while SpamAssassin strips the whitespace and
// counts them. Canonical fields come first; the relative order of differently
// spelled keys is not recorded by net/textproto.
func Fields(h mail.Header, name string) []string {
	name = textproto.CanonicalMIMEHeaderKey(name)
	out := slices.Clone(h[name])
	var keys []string
	for k := range h {
		if k != name && strings.EqualFold(strings.TrimRight(k, " \t"), name) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		out = append(out, h[k]...)
	}
	return out
}

// singleMailbox reports whether the message has exactly one From field and it
// holds exactly one mailbox. Encoded words are not decoded, so an unknown
// charset in a display name does not matter: only the structure is counted.
func singleMailbox(h mail.Header) bool {
	from := Fields(h, "From")
	if len(from) != 1 {
		return false
	}
	parser := mail.AddressParser{WordDecoder: &mime.WordDecoder{
		CharsetReader: func(_ string, input io.Reader) (io.Reader, error) { return input, nil },
	}}
	addrs, err := parser.ParseList(from[0])
	return err == nil && len(addrs) == 1
}

// Parse requires exactly one checker and status header. It deliberately makes
// no inference from Received positions: neither their provenance nor the scan
// insertion point is known. See REQUIREMENTS.md for the trust boundary.
//
// The verdict is also unknown unless the message has exactly one From field
// holding exactly one mailbox. A checker's DMARC result names no domain, so it
// is only attributable to the From MyMail stores when no other From exists —
// an obsolete-syntax `From :` line or a second mailbox could be the one DMARC
// evaluated, and a pass for it would exempt a forged own-identity sender.
func Parse(raw []byte, host string) oas.SenderAuthentication {
	v := Unknown()
	if host == "" {
		return v
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return v
	}
	if !singleMailbox(msg.Header) {
		return v
	}
	c, s := Fields(msg.Header, "X-Spam-Checker-Version"), Fields(msg.Header, "X-Spam-Status")
	if len(c) != 1 || len(s) != 1 {
		return v
	}
	m := checker.FindStringSubmatch(strings.Join(strings.Fields(c[0]), " "))
	if len(m) != 2 || m[1] != host {
		return v
	}
	status := strings.Join(strings.Fields(s[0]), " ")
	if len(testsStart.FindAllStringIndex(status, -1)) != 1 {
		return v
	}
	matches := tests.FindAllStringSubmatch(status, -1)
	if len(matches) != 1 {
		return v
	}
	hits := map[string]bool{}
	for _, name := range strings.Split(matches[0][1], ",") {
		name = strings.TrimSpace(name)
		if !rule.MatchString(name) {
			return v
		}
		hits[name] = true
	}
	// Negative evidence takes precedence over contradictory positive rules.
	switch {
	case hits["DMARC_REJECT"] || hits["DMARC_QUAR"] || hits["DMARC_NONE"]:
		v.Dmarc = "fail"
	case hits["DMARC_MISSING"]:
		v.Dmarc = "missing"
	case hits["DMARC_PASS"]:
		v.Dmarc = "pass"
	}
	v.DkimAligned = hits["DKIM_VALID_AU"]
	switch {
	case hits["SPF_FAIL"]:
		v.Spf = "fail"
	case hits["SPF_SOFTFAIL"]:
		v.Spf = "softfail"
	case hits["SPF_PASS"]:
		v.Spf = "pass"
	}
	return v
}

// Encode returns the JSON representation persisted in messages.
func Encode(v oas.SenderAuthentication) string {
	b, _ := json.Marshal(v) // fixed struct containing only strings and a boolean
	return string(b)
}

// Decode supplies unknown for legacy/unset values as well as invalid JSON.
func Decode(s string) oas.SenderAuthentication {
	v := Unknown()
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return Unknown()
	}
	return v
}
