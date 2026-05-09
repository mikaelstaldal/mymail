package lda

import (
	"strings"
	"testing"
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(pm.BodyText, "Hello, world!") {
		t.Errorf("BodyText = %q, want to contain 'Hello, world!'", pm.BodyText)
	}
	if pm.BodyHTML != "" {
		t.Errorf("BodyHTML = %q, want empty", pm.BodyHTML)
	}
	if pm.Date == nil {
		t.Error("Date is nil, want non-nil")
	}
	if pm.MessageID == nil || *pm.MessageID != "test123@example.com" {
		t.Errorf("MessageID = %v, want 'test123@example.com'", pm.MessageID)
	}
	if len(pm.Attachments) != 0 {
		t.Errorf("Attachments = %d, want 0", len(pm.Attachments))
	}
	if pm.FromAddr != "sender@example.com" {
		t.Errorf("FromAddr = %q, want 'sender@example.com'", pm.FromAddr)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(pm.BodyText, "Plain text body") {
		t.Errorf("BodyText = %q, want to contain 'Plain text body'", pm.BodyText)
	}
	if !strings.Contains(pm.BodyHTML, "HTML body") {
		t.Errorf("BodyHTML = %q, want to contain 'HTML body'", pm.BodyHTML)
	}
	if len(pm.Attachments) != 0 {
		t.Errorf("Attachments = %d, want 0", len(pm.Attachments))
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pm.Attachments) != 0 {
		t.Errorf("Attachments = %d, want 0 (inline image should not be an attachment)", len(pm.Attachments))
	}
	if !strings.Contains(pm.BodyHTML, "data:image/gif;base64,") {
		t.Errorf("BodyHTML does not contain data URI, got: %q", pm.BodyHTML)
	}
	if strings.Contains(pm.BodyHTML, "cid:") {
		t.Errorf("BodyHTML still contains cid: reference, got: %q", pm.BodyHTML)
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(pm.BodyText, "Café") {
		t.Errorf("BodyText = %q, want to contain 'Café'", pm.BodyText)
	}
}

func TestParseMessage_HTMLOnly_DerivedBodyText(t *testing.T) {
	raw := []byte(
		"From: sender@example.com\r\n" +
			"Content-Type: text/html\r\n" +
			"\r\n" +
			"<p>Hello, world!</p>\r\n",
	)

	pm, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pm.BodyText == "" {
		t.Error("BodyText is empty, want non-empty derived text")
	}
	if !strings.Contains(pm.BodyText, "Hello, world!") {
		t.Errorf("BodyText = %q, want to contain 'Hello, world!'", pm.BodyText)
	}
	if pm.BodyHTML == "" {
		t.Error("BodyHTML is empty, want non-empty")
	}
}

func TestParseMessage_HTMLOnly_BrBecomesNewline(t *testing.T) {
	raw := []byte(
		"From: sender@example.com\r\n" +
			"Content-Type: text/html\r\n" +
			"\r\n" +
			"<p>Line one<br>Line two</p>\r\n",
	)

	pm, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	idx1 := strings.Index(pm.BodyText, "Line one")
	idx2 := strings.Index(pm.BodyText, "Line two")
	if idx1 < 0 || idx2 < 0 {
		t.Fatalf("BodyText = %q, missing expected lines", pm.BodyText)
	}
	if idx2 <= idx1+len("Line one") {
		t.Fatalf("BodyText = %q, 'Line two' not after 'Line one'", pm.BodyText)
	}
	between := pm.BodyText[idx1+len("Line one") : idx2]
	if !strings.Contains(between, "\n") {
		t.Errorf("<br> did not produce newline between lines, got %q", between)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(pm.BodyText, "native plain text") {
		t.Errorf("BodyText = %q, want native plain text", pm.BodyText)
	}
	// If the derived path were used, html2text would produce "HTML only content".
	if strings.Contains(pm.BodyText, "HTML only content") {
		t.Errorf("BodyText = %q, derived path was used instead of native plain part", pm.BodyText)
	}
	if !strings.Contains(pm.BodyHTML, "HTML only content") {
		t.Errorf("BodyHTML = %q, want to contain 'HTML only content'", pm.BodyHTML)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pm.Date != nil {
		t.Errorf("Date = %v, want nil", pm.Date)
	}
}
