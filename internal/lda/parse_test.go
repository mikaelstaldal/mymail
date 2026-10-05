package lda

import (
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nestedMIME(depth int, kind string) []byte {
	part := "Content-Type: text/plain\r\n\r\nleaf"
	for i := depth - 1; i >= 0; i-- {
		boundary := fmt.Sprintf("b%d", i)
		part = "Content-Type: multipart/" + kind + "; boundary=" + boundary + "\r\n\r\n--" + boundary +
			"\r\n" + part + "\r\n--" + boundary + "--\r\n"
	}
	return []byte(part)
}

func TestParseMessage_MIMEDepthLimit(t *testing.T) {
	for _, kind := range []string{"mixed", "alternative"} {
		t.Run(kind, func(t *testing.T) {
			pm, err := ParseMessage(nestedMIME(maxMIMEDepth, kind))
			require.NoError(t, err)
			assert.Contains(t, pm.BodyText, "leaf")

			pm, err = ParseMessage(nestedMIME(maxMIMEDepth+1, kind))
			require.ErrorContains(t, err, "MIME nesting exceeds")
			assert.Nil(t, pm)
		})
	}
}

func TestParseMessage_MIMEPartLimit(t *testing.T) {
	for _, count := range []int{maxMIMEParts, maxMIMEParts + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var raw strings.Builder
			raw.WriteString("Content-Type: multipart/mixed; boundary=b\r\n\r\n")
			for i := 0; i < count; i++ {
				raw.WriteString("--b\r\nContent-Type: application/octet-stream\r\n\r\nx\r\n")
			}
			raw.WriteString("--b--\r\n")
			pm, err := ParseMessage([]byte(raw.String()))
			if count <= maxMIMEParts {
				require.NoError(t, err)
				assert.Len(t, pm.Attachments, count)
			} else {
				require.ErrorContains(t, err, "MIME part count exceeds")
				assert.Nil(t, pm)
			}
		})
	}
}

func TestParseMessage_MalformedMultipartKeepsParsedContent(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantText string
		wantHTML string
	}{
		{
			name:     "missing closing boundary",
			raw:      "Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nhello",
			wantText: "hello",
		},
		{
			name:     "bad later part header",
			raw:      "Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nhello\r\n--b\r\nbad header\r\n\r\nignored\r\n--b--\r\n",
			wantText: "hello",
		},
		{
			name:     "bad later alternative header",
			raw:      "Content-Type: multipart/alternative; boundary=b\r\n\r\n--b\r\nContent-Type: text/html\r\n\r\n<p>hello</p>\r\n--b\r\nbad header\r\n\r\nignored\r\n--b--\r\n",
			wantHTML: "hello",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pm, err := ParseMessage([]byte(tc.raw))
			require.NoError(t, err)
			assert.Contains(t, pm.BodyText, tc.wantText)
			assert.Contains(t, pm.BodyHTML, tc.wantHTML)
		})
	}
}

func TestParseMessage_MIMEByteBudgets(t *testing.T) {
	state := &mimeState{workBytes: maxMIMEWorkBytes - 1}
	mr := multipart.NewReader(strings.NewReader("--b\r\n\r\nxy\r\n--b--\r\n"), "b")
	p, err := mr.NextPart()
	require.NoError(t, err)
	_, err = state.readPart(p)
	require.ErrorContains(t, err, "MIME work exceeds")

	state = &mimeState{decodedBytes: maxMIMEDecodedBytes - 1}
	err = traversePart("text/plain", "base64", textproto.MIMEHeader{}, []byte("eHk="), state, 0)
	require.ErrorContains(t, err, "decoded MIME data exceeds")

	for _, tc := range []struct {
		name string
		cte  string
		data string
	}{
		{"plain", "", "xy"},
		{"quoted printable", "quoted-printable", "xy"},
		{"base64", "base64", "eHk="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := decodeCTE([]byte(tc.data), tc.cte, 2)
			require.NoError(t, err)
			assert.Equal(t, "xy", string(decoded))
			_, err = decodeCTE([]byte(tc.data), tc.cte, 1)
			require.ErrorContains(t, err, "decoded MIME data exceeds")
		})
	}
}

func TestParseMessage_PlainText(t *testing.T) {
	raw := []byte(
		"From: sender@example.com\r\n" +
			"To: recipient@example.com\r\n" +
			"Subject: Hello\r\n" +
			"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
			"Message-Id: <test123@example.com>\r\n" +
			"\r\n" +
			"Hello, world!\r\n",
	)

	pm, err := ParseMessage(raw)
	require.NoError(t, err)

	assert.Contains(t, pm.BodyText, "Hello, world!")
	assert.Empty(t, pm.BodyHTML)
	assert.NotNil(t, pm.Date)
	assert.NotNil(t, pm.MessageID)
	assert.Equal(t, "test123@example.com", *pm.MessageID)
	assert.Empty(t, pm.Attachments)
	assert.Equal(t, "sender@example.com", pm.FromAddr)
}

func TestParseMessage_MultipartAlternative_HTMLPreferred(t *testing.T) {
	raw := []byte(
		"From: sender@example.com\r\n" +
			"To: recipient@example.com\r\n" +
			"Subject: Multipart Test\r\n" +
			"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: multipart/alternative; boundary=\"boundary\"\r\n" +
			"\r\n" +
			"--boundary\r\n" +
			"Content-Type: text/plain\r\n" +
			"\r\n" +
			"Plain text body\r\n" +
			"--boundary\r\n" +
			"Content-Type: text/html\r\n" +
			"\r\n" +
			"<p>HTML body</p>\r\n" +
			"--boundary--\r\n",
	)

	pm, err := ParseMessage(raw)
	require.NoError(t, err)
	assert.Contains(t, pm.BodyText, "Plain text body")
	assert.Contains(t, pm.BodyHTML, "HTML body")
	assert.Empty(t, pm.Attachments)
}

func TestParseMessage_CIDInlineImage(t *testing.T) {
	// 1×1 transparent GIF
	const gifBase64 = "R0lGODlhAQABAIAAAP///wAAACH5BAAAAAAALAAAAAABAAEAAAICTAEAOw=="
	raw := []byte(
		"From: sender@example.com\r\n" +
			"Content-Type: multipart/related; boundary=\"rel\"\r\n" +
			"\r\n" +
			"--rel\r\n" +
			"Content-Type: text/html\r\n" +
			"\r\n" +
			`<img src="cid:img001@example.com"> Hello` + "\r\n" +
			"--rel\r\n" +
			"Content-Type: image/gif\r\n" +
			"Content-Id: <img001@example.com>\r\n" +
			"Content-Transfer-Encoding: base64\r\n" +
			"\r\n" +
			gifBase64 + "\r\n" +
			"--rel--\r\n",
	)

	pm, err := ParseMessage(raw)
	require.NoError(t, err)
	assert.Empty(t, pm.Attachments, "inline image should not be an attachment")
	assert.Contains(t, pm.BodyHTML, "data:image/gif;base64,")
	assert.NotContains(t, pm.BodyHTML, "cid:")
}

func TestParseMessage_CIDReferencesOnlyFromResolvableImages(t *testing.T) {
	const cid = "img001@example.com"
	cases := []struct {
		name        string
		html        string
		contentID   string
		contentType string
		data        string
	}{
		{"repeated CID text", strings.Repeat("cid:", 20_000) + cid, cid, "image/gif", "GIF"},
		{"CID in another attribute", `<div title="cid:` + cid + `">text</div>`, cid, "image/gif", "GIF"},
		{"malformed empty source", `<img src="cid:">`, cid, "image/gif", "GIF"},
		{"oversized image source", `<img src="cid:` + strings.Repeat("x", 1025) + `">`, strings.Repeat("x", 1025), "image/gif", "GIF"},
		{"too many images", strings.Repeat(`<img src="cid:`+cid+`">`, 65), cid, "image/gif", "GIF"},
		{"unsupported image type", `<img src="cid:` + cid + `">`, cid, "image/svg+xml", "GIF"},
		{"image in object", `<object><img src="cid:` + cid + `"></object>`, cid, "image/gif", "GIF"},
		{"image in template", `<template><img src="cid:` + cid + `"></template>`, cid, "image/gif", "GIF"},
		{"image too large", `<img src="cid:` + cid + `">`, cid, "image/gif", strings.Repeat("x", 1*1024*1024+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte("Content-Type: multipart/related; boundary=rel\r\n\r\n" +
				"--rel\r\nContent-Type: text/html\r\n\r\n" + tc.html + "\r\n" +
				"--rel\r\nContent-Type: " + tc.contentType + "\r\nContent-Id: <" + tc.contentID + ">\r\n\r\n" + tc.data + "\r\n" +
				"--rel--\r\n")
			pm, err := ParseMessage(raw)
			require.NoError(t, err)
			require.Len(t, pm.Attachments, 1)
			assert.Equal(t, tc.data, string(pm.Attachments[0].Data))
			assert.NotContains(t, pm.BodyHTML, "data:image/gif;base64,")
			assert.NotContains(t, pm.BodyHTML, `src="cid:`)
		})
	}
}

func TestParseMessage_CharsetISO8859(t *testing.T) {
	// 'é' in ISO-8859-1 is byte 0xe9
	body := []byte("Caf\xe9")
	raw := append(
		[]byte("From: sender@example.com\r\nContent-Type: text/plain; charset=ISO-8859-1\r\n\r\n"),
		body...,
	)

	pm, err := ParseMessage(raw)
	require.NoError(t, err)
	assert.Contains(t, pm.BodyText, "Café")
}

func TestParseMessage_HTMLOnly_DerivedBodyText(t *testing.T) {
	raw := []byte(
		"From: sender@example.com\r\n" +
			"Content-Type: text/html\r\n" +
			"\r\n" +
			"<p>Hello, world!</p>\r\n",
	)

	pm, err := ParseMessage(raw)
	require.NoError(t, err)
	assert.NotEmpty(t, pm.BodyText)
	assert.Contains(t, pm.BodyText, "Hello, world!")
	assert.NotEmpty(t, pm.BodyHTML)
}

func TestParseMessage_HTMLOnly_BrBecomesNewline(t *testing.T) {
	raw := []byte(
		"From: sender@example.com\r\n" +
			"Content-Type: text/html\r\n" +
			"\r\n" +
			"<p>Line one<br>Line two</p>\r\n",
	)

	pm, err := ParseMessage(raw)
	require.NoError(t, err)
	assert.Contains(t, pm.BodyText, "Line one")
	assert.Contains(t, pm.BodyText, "Line two")
	assert.Contains(t, pm.BodyText, "Line one\nLine two")
}

func TestParseMessage_PlainAndHTML_UsesNativePlain(t *testing.T) {
	raw := []byte(
		"From: sender@example.com\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: multipart/alternative; boundary=\"b\"\r\n" +
			"\r\n" +
			"--b\r\n" +
			"Content-Type: text/plain\r\n" +
			"\r\n" +
			"native plain text\r\n" +
			"--b\r\n" +
			"Content-Type: text/html\r\n" +
			"\r\n" +
			"<p>HTML only content</p>\r\n" +
			"--b--\r\n",
	)

	pm, err := ParseMessage(raw)
	require.NoError(t, err)
	assert.Contains(t, pm.BodyText, "native plain text")
	assert.NotContains(t, pm.BodyText, "HTML only content")
	assert.Contains(t, pm.BodyHTML, "HTML only content")
}

func TestParseMessage_NoDate(t *testing.T) {
	raw := []byte(
		"From: sender@example.com\r\n" +
			"To: recipient@example.com\r\n" +
			"Subject: No Date\r\n" +
			"\r\n" +
			"Hello\r\n",
	)

	pm, err := ParseMessage(raw)
	require.NoError(t, err)
	assert.Nil(t, pm.Date)
}

func TestDecodeHeader(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		decoded string
	}{
		{
			name:    "Only address",
			raw:     "sender@example.com",
			decoded: "sender@example.com",
		},
		{
			name:    "Name and address",
			raw:     "John Doe <sender@example.com>",
			decoded: "John Doe <sender@example.com>",
		},
		{
			name:    "Adjacent encoded-words with non-ASCII and comma",
			raw:     "=?utf-8?b?QWNtZSBDYWbDqSAtIEhhdXB0c3RyYcOfZSA0?= =?utf-8?b?MiwgWsO8cmljaA==?= <info@example.com>",
			decoded: "Acme Café - Hauptstraße 42, Zürich <info@example.com>",
		},
		{
			name:    "Encoded-word wrapped in quotes",
			raw:     `"=?utf-8?B?SsO2aG4gRMO4ZQ==?=" <sender@example.com>`,
			decoded: "Jöhn Døe <sender@example.com>",
		},
		{
			name:    "Malformed address with space salvages display name",
			raw:     "Ali_express <summer items@woowstars.shop>",
			decoded: "Ali_express",
		},
		{
			name:    "Malformed address with no name and no angle brackets",
			raw:     "summer items@woowstars.shop",
			decoded: "",
		},
		{
			name:    "Windows-1252 charset in header",
			raw:     "=?Windows-1252?Q?Max_G=E4rdet_=28gardet=40max=2Ese=29?= <gardet@max.se>",
			decoded: "Max Gärdet <gardet@max.se>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoded := service.DecodeAddressHeader(tc.raw)
			assert.Equal(t, tc.decoded, decoded)
		})
	}
}
