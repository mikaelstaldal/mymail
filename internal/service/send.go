package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/sanitize"
)

// ResolveSendmailPath resolves the path to a sendmail-compatible binary via exec.LookPath.
// If configured is empty, it searches for "sendmail" in PATH.
func ResolveSendmailPath(configured string) (string, error) {
	if configured == "" {
		configured = "sendmail"
	}
	return exec.LookPath(configured)
}

// SendFields holds all compose fields and the resolved identity needed to build a MIME message.
type SendFields struct {
	FromName    string // identity display name
	FromAddr    string // identity email address (bare addr-spec)
	ToAddr      string
	CcAddr      string
	BccAddr     string
	ReplyToAddr string
	Subject     string
	BodyText    string
	BodyHTML    string
	InReplyTo   string   // without angle brackets
	References  []string // without angle brackets
}

// BuildMIMEMessage constructs a complete RFC 5322/MIME message.
// It sanitizes body_html with the email policy, then builds the MIME structure.
// Returns (rawMessage, hasExternalImages, messageID, error). messageID is the generated
// Message-ID value without surrounding angle brackets, suitable for DB storage.
func BuildMIMEMessage(fields SendFields, attachments []model.DBAttachment) ([]byte, bool, string, error) {
	// Sanitize outgoing HTML and detect external images. The outgoing policy is
	// the wider of the two (see sanitize.NewOutgoingPolicy); applying the
	// inbound one here would silently undo it, since callers have already
	// sanitized with the outgoing policy and the stricter pass would win.
	if fields.BodyHTML != "" {
		fields.BodyHTML = sanitize.OutgoingHTML(fields.BodyHTML)
	}
	hasExternalImages := sanitize.HasExternalImages(fields.BodyHTML)

	// Strip header-injection characters (CR, LF, NUL) from all user-supplied values
	fields.FromName = StripHeaderControls(fields.FromName)
	fields.ToAddr = StripHeaderControls(fields.ToAddr)
	fields.CcAddr = StripHeaderControls(fields.CcAddr)
	fields.BccAddr = StripHeaderControls(fields.BccAddr)
	fields.ReplyToAddr = StripHeaderControls(fields.ReplyToAddr)
	fields.Subject = StripHeaderControls(fields.Subject)
	fields.InReplyTo = StripHeaderControls(fields.InReplyTo)
	for i, ref := range fields.References {
		fields.References[i] = StripHeaderControls(ref)
	}

	// Derive Message-ID domain from sender address
	domain := "localhost"
	if addr, err := ParseAddress(fields.FromAddr); err == nil {
		if at := strings.LastIndex(addr.Address, "@"); at >= 0 {
			domain = addr.Address[at+1:]
		}
	}
	msgIDValue := fmt.Sprintf("%s@%s", uuid.New().String(), domain)
	msgID := "<" + msgIDValue + ">"

	// RFC 5322 date
	date := time.Now().Format(time.RFC1123Z)

	// Format the display name as an RFC 5322 address (including quotes for commas).
	var fromHeader string
	if fields.FromName != "" {
		fromHeader = (&mail.Address{Name: fields.FromName, Address: fields.FromAddr}).String()
	} else {
		fromHeader = fields.FromAddr
	}

	// Build body into a buffer; learn the top-level content type
	var bodyBuf bytes.Buffer
	topCT, topCTE, err := buildBody(&bodyBuf, fields, attachments)
	if err != nil {
		return nil, false, "", err
	}

	// Assemble the RFC 5322 message
	var out bytes.Buffer
	wh := func(name, value string) {
		fmt.Fprintf(&out, "%s: %s\r\n", name, value)
	}

	wh("Date", date)
	wh("Message-ID", msgID)
	wh("From", fromHeader)
	writeAddrList := func(header, value string) error {
		if value == "" {
			return nil
		}
		encoded, err := encodeAddrList(value)
		if err != nil {
			return fmt.Errorf("invalid %s address list: %w", header, err)
		}
		wh(header, encoded)
		return nil
	}
	if err := writeAddrList("To", fields.ToAddr); err != nil {
		return nil, false, "", err
	}
	if err := writeAddrList("Cc", fields.CcAddr); err != nil {
		return nil, false, "", err
	}
	// Bcc always written so sendmail -t picks up recipients; MTA strips it on delivery
	if err := writeAddrList("Bcc", fields.BccAddr); err != nil {
		return nil, false, "", err
	}
	if err := writeAddrList("Reply-To", fields.ReplyToAddr); err != nil {
		return nil, false, "", err
	}
	wh("Subject", mime.QEncoding.Encode("utf-8", fields.Subject))
	if fields.InReplyTo != "" {
		wh("In-Reply-To", "<"+fields.InReplyTo+">")
	}
	if len(fields.References) > 0 {
		refs := make([]string, len(fields.References))
		for i, r := range fields.References {
			refs[i] = "<" + r + ">"
		}
		wh("References", strings.Join(refs, " "))
	}
	wh("MIME-Version", "1.0")
	wh("Content-Type", topCT)
	if topCTE != "" {
		wh("Content-Transfer-Encoding", topCTE)
	}
	out.WriteString("\r\n")
	out.Write(bodyBuf.Bytes())

	return out.Bytes(), hasExternalImages, msgIDValue, nil
}

// buildBody writes the message body to w.
// Returns (Content-Type header value, Content-Transfer-Encoding header value or "").
// CTE is empty for multipart types.
func buildBody(w *bytes.Buffer, fields SendFields, attachments []model.DBAttachment) (ct, cte string, err error) {
	hasText := fields.BodyText != ""
	hasHTML := fields.BodyHTML != ""

	type part struct {
		ct   string
		cte  string
		body []byte
	}

	var inline part
	textCTE, textBody := encodePlainText(fields.BodyText)

	switch {
	case !hasText && !hasHTML:
		inline = part{ct: "text/plain; charset=utf-8", cte: textCTE, body: textBody}

	case hasText && !hasHTML:
		inline = part{ct: "text/plain; charset=utf-8", cte: textCTE, body: textBody}

	case !hasText && hasHTML:
		inline = part{ct: "text/html; charset=utf-8", cte: "quoted-printable", body: qpEncode(fields.BodyHTML)}

	default: // both text and html → multipart/alternative, text first
		var altBuf bytes.Buffer
		mw := multipart.NewWriter(&altBuf)

		pw, e := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {"text/plain; charset=utf-8"},
			"Content-Transfer-Encoding": {textCTE},
		})
		if e != nil {
			return "", "", e
		}
		if _, e = pw.Write(textBody); e != nil {
			return "", "", e
		}

		pw, e = mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {"text/html; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if e != nil {
			return "", "", e
		}
		if _, e = pw.Write(qpEncode(fields.BodyHTML)); e != nil {
			return "", "", e
		}

		if e = mw.Close(); e != nil {
			return "", "", e
		}
		inline = part{
			ct:   `multipart/alternative; boundary="` + mw.Boundary() + `"`,
			body: altBuf.Bytes(),
		}
	}

	if len(attachments) == 0 {
		w.Write(inline.body)
		return inline.ct, inline.cte, nil
	}

	// Wrap inline content and attachments in multipart/mixed
	mw := multipart.NewWriter(w)

	inlineHeaders := textproto.MIMEHeader{"Content-Type": {inline.ct}}
	if inline.cte != "" {
		inlineHeaders["Content-Transfer-Encoding"] = []string{inline.cte}
	}
	pw, e := mw.CreatePart(inlineHeaders)
	if e != nil {
		return "", "", e
	}
	if _, e = pw.Write(inline.body); e != nil {
		return "", "", e
	}

	for _, att := range attachments {
		attMediaType, attParams, pErr := mime.ParseMediaType(att.ContentType)
		if pErr != nil {
			attMediaType = "application/octet-stream"
			attParams = map[string]string{}
		}
		attParams["name"] = att.Filename
		attCTHeader := mime.FormatMediaType(attMediaType, attParams)
		if attCTHeader == "" {
			attCTHeader = "application/octet-stream"
		}
		attDispHeader := mime.FormatMediaType("attachment", map[string]string{"filename": att.Filename})
		if attDispHeader == "" {
			attDispHeader = "attachment"
		}

		pw, e = mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {attCTHeader},
			"Content-Disposition":       {attDispHeader},
			"Content-Transfer-Encoding": {"base64"},
		})
		if e != nil {
			return "", "", e
		}
		writeBase64Wrapped(pw, att.Data)
	}

	if e = mw.Close(); e != nil {
		return "", "", e
	}
	return `multipart/mixed; boundary="` + mw.Boundary() + `"`, "", nil
}

// SendMail pipes message to sendmail -t -oi with a 30-second timeout.
// On failure it returns the last 4 KB of stderr; on success returns ("", nil).
func SendMail(sendmailPath string, message []byte) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, sendmailPath, "-t", "-oi")
	cmd.Stdin = bytes.NewReader(message)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		stderrBytes := stderrBuf.Bytes()
		if len(stderrBytes) > 4096 {
			stderrBytes = stderrBytes[len(stderrBytes)-4096:]
		}
		return string(stderrBytes), err
	}
	return "", nil
}

// StripHeaderControls removes CR, LF, and NUL from a header value to prevent injection.
func StripHeaderControls(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, s)
}

// encodeAddrList reformats an RFC 5322 address list, quoting ASCII display
// names when needed and encoding non-ASCII names as RFC 2047 encoded words.
func encodeAddrList(s string) (string, error) {
	addrs, err := ParseAddressList(s)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(addrs))
	for i, a := range addrs {
		parts[i] = a.String()
	}
	return strings.Join(parts, ", "), nil
}

// qpEncode returns the quoted-printable encoding of s.
func qpEncode(s string) []byte {
	var buf bytes.Buffer
	w := quotedprintable.NewWriter(&buf)
	w.Write([]byte(s)) //nolint:errcheck
	w.Close()          //nolint:errcheck
	return buf.Bytes()
}

// ASCII text with transport-safe lines needs no transfer encoding. This keeps
// authored line breaks readable in raw mail instead of adding quoted-printable
// soft breaks inside words near its 76-character limit.
func encodePlainText(s string) (string, []byte) {
	normalized := strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		if len(line) > 998 || strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			return "quoted-printable", qpEncode(s)
		}
		for i := 0; i < len(line); i++ {
			if c := line[i]; c > 127 || c < 32 && c != '\t' {
				return "quoted-printable", qpEncode(s)
			}
		}
	}
	return "7bit", []byte(strings.ReplaceAll(normalized, "\n", "\r\n"))
}

// writeBase64Wrapped writes data base64-encoded with lines of at most 76 characters.
func writeBase64Wrapped(w io.Writer, data []byte) {
	const lineLen = 76
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > lineLen {
		fmt.Fprintf(w, "%s\r\n", encoded[:lineLen]) //nolint:errcheck
		encoded = encoded[lineLen:]
	}
	if len(encoded) > 0 {
		fmt.Fprintf(w, "%s\r\n", encoded) //nolint:errcheck
	}
}
