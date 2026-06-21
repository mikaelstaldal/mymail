package lda

import (
	"mime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	}

	dec := new(mime.WordDecoder)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoded := decodeAddrHeader(dec, tc.raw)
			assert.Equal(t, tc.decoded, decoded)
		})
	}
}
