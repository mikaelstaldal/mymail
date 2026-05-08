package lda

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	"github.com/jaytaylor/html2text"
	"golang.org/x/net/html/charset"

	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/sanitize"
)

const maxRefsBytes = 16 * 1024

// ParseMessage parses a raw RFC 5322 message into a ParsedMessage.
// Returns an error only if net/mail.ReadMessage fails (hard failure).
func ParseMessage(raw []byte) (*model.ParsedMessage, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}

	dec := new(mime.WordDecoder)

	var date *time.Time
	if ds := msg.Header.Get("Date"); ds != "" {
		if t, err := mail.ParseDate(ds); err == nil {
			date = &t
		}
	}

	var messageID *string
	if mid := msg.Header.Get("Message-Id"); mid != "" {
		if fields := strings.Fields(mid); len(fields) > 0 {
			s := stripAngles(fields[0])
			messageID = &s
		}
	}

	var inReplyTo *string
	if irt := msg.Header.Get("In-Reply-To"); irt != "" {
		if fields := strings.Fields(irt); len(fields) > 0 {
			s := stripAngles(fields[0])
			inReplyTo = &s
		}
	}

	var refs []string
	if r := msg.Header.Get("References"); r != "" {
		for _, tok := range strings.Fields(r) {
			refs = append(refs, stripAngles(tok))
		}
	}
	refs = truncateRefs(refs)

	fromAddr := decodeAddrHeader(dec, msg.Header.Get("From"))
	toAddr := decodeAddrHeader(dec, msg.Header.Get("To"))
	ccAddr := decodeAddrHeader(dec, msg.Header.Get("Cc"))
	bccAddr := decodeAddrHeader(dec, msg.Header.Get("Bcc"))
	replyToAddr := decodeAddrHeader(dec, msg.Header.Get("Reply-To"))

	subject, _ := dec.DecodeHeader(msg.Header.Get("Subject"))

	bodyBytes, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, err
	}

	state := &mimeState{
		cidMap: make(map[string][]byte),
		cidCT:  make(map[string]string),
	}
	ct := msg.Header.Get("Content-Type")
	if ct == "" {
		ct = "text/plain"
	}
	traversePart(ct, msg.Header.Get("Content-Transfer-Encoding"), msg.Header, bodyBytes, state)

	rawHTML := ""
	if state.bodyHTML != nil {
		rawHTML = *state.bodyHTML
	}

	usedCIDs := extractCIDRefs(rawHTML)

	var attachments []model.DBAttachment
	for _, p := range state.pending {
		if p.contentID != "" && usedCIDs[strings.ToLower(p.contentID)] {
			continue
		}
		attachments = append(attachments, model.DBAttachment{
			Filename:    p.filename,
			ContentType: p.contentType,
			Size:        len(p.data),
			Data:        p.data,
		})
	}
	if attachments == nil {
		attachments = []model.DBAttachment{}
	}

	bodyHTML := sanitize.SanitizeHTML(sanitize.ResolveCID(rawHTML, state.cidMap, state.cidCT))

	bodyText := ""
	if state.bodyText != nil {
		bodyText = *state.bodyText
	} else if bodyHTML != "" {
		bodyText, _ = html2text.FromString(bodyHTML, html2text.Options{PrettyTables: false, OmitLinks: false})
	}

	return &model.ParsedMessage{
		FromAddr:          fromAddr,
		ToAddr:            toAddr,
		CcAddr:            ccAddr,
		BccAddr:           bccAddr,
		ReplyToAddr:       replyToAddr,
		Subject:           subject,
		Date:              date,
		MessageID:         messageID,
		InReplyTo:         inReplyTo,
		References:        refs,
		BodyText:          bodyText,
		BodyHTML:          bodyHTML,
		Attachments:       attachments,
		HasExternalImages: sanitize.HasExternalImages(bodyHTML),
	}, nil
}

// --- MIME traversal ---

type mimeState struct {
	bodyText *string
	bodyHTML *string
	cidMap   map[string][]byte
	cidCT    map[string]string
	pending  []pendingPart
}

type pendingPart struct {
	contentID   string
	filename    string
	contentType string
	data        []byte
}

type headerGetter interface {
	Get(key string) string
}

// traversePart processes one MIME part (leaf or multipart) depth-first.
func traversePart(rawCT, rawCTE string, headers headerGetter, rawBody []byte, state *mimeState) {
	mediaType, params, err := mime.ParseMediaType(rawCT)
	if err != nil {
		mediaType = "application/octet-stream"
		params = map[string]string{}
	}
	mediaType = strings.ToLower(mediaType)

	if mediaType == "message/rfc822" {
		return
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		mr := multipart.NewReader(bytes.NewReader(rawBody), boundary)
		if mediaType == "multipart/alternative" {
			traverseAlternative(mr, state)
		} else {
			traverseMultipartParts(mr, state)
		}
		return
	}

	body := decodeCTE(rawBody, rawCTE)

	disposition := ""
	dispFilename := ""
	if rawDisp := headers.Get("Content-Disposition"); rawDisp != "" {
		if disp, dispParams, e := mime.ParseMediaType(rawDisp); e == nil {
			disposition = strings.ToLower(disp)
			dispFilename = dispParams["filename"]
		}
	}

	filename := params["name"]
	if dispFilename != "" {
		filename = dispFilename
	}

	contentID := ""
	if rawCID := headers.Get("Content-Id"); rawCID != "" {
		contentID = stripAngles(strings.TrimSpace(rawCID))
	}

	switch mediaType {
	case "text/plain":
		if disposition != "attachment" && state.bodyText == nil {
			s := decodeCharset(body, rawCT)
			state.bodyText = &s
		} else {
			state.pending = append(state.pending, pendingPart{
				contentID: contentID, filename: filename,
				contentType: mediaType, data: body,
			})
		}
	case "text/html":
		if disposition != "attachment" && state.bodyHTML == nil {
			s := decodeCharset(body, rawCT)
			state.bodyHTML = &s
		} else {
			state.pending = append(state.pending, pendingPart{
				contentID: contentID, filename: filename,
				contentType: mediaType, data: body,
			})
		}
	default:
		if contentID != "" {
			state.cidMap[contentID] = body
			state.cidCT[contentID] = mediaType
		}
		state.pending = append(state.pending, pendingPart{
			contentID: contentID, filename: filename,
			contentType: mediaType, data: body,
		})
	}
}

func traverseMultipartParts(mr *multipart.Reader, state *mimeState) {
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		body, _ := io.ReadAll(p)
		ct := p.Header.Get("Content-Type")
		if ct == "" {
			ct = "text/plain"
		}
		traversePart(ct, p.Header.Get("Content-Transfer-Encoding"), p.Header, body, state)
	}
}

type rawPart struct {
	ct      string
	cte     string
	headers textproto.MIMEHeader
	body    []byte
}

// traverseAlternative picks the most-preferred sub-part per RFC 2046
// (last part is most preferred). HTML/multipart beats plain text.
func traverseAlternative(mr *multipart.Reader, state *mimeState) {
	var parts []rawPart
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		body, _ := io.ReadAll(p)
		ct := p.Header.Get("Content-Type")
		if ct == "" {
			ct = "text/plain"
		}
		parts = append(parts, rawPart{
			ct: ct, cte: p.Header.Get("Content-Transfer-Encoding"),
			headers: p.Header, body: body,
		})
	}

	htmlIdx, plainIdx := -1, -1
	for i, p := range parts {
		mt, _, _ := mime.ParseMediaType(p.ct)
		mt = strings.ToLower(mt)
		if mt == "text/html" || strings.HasPrefix(mt, "multipart/") {
			htmlIdx = i
		} else if mt == "text/plain" {
			plainIdx = i
		}
	}

	if htmlIdx >= 0 {
		p := parts[htmlIdx]
		traversePart(p.ct, p.cte, p.headers, p.body, state)
	}
	if plainIdx >= 0 && state.bodyText == nil {
		p := parts[plainIdx]
		traversePart(p.ct, p.cte, p.headers, p.body, state)
	}
}

// --- Decoding helpers ---

func decodeCTE(data []byte, cte string) []byte {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		decoded, _ := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
		return decoded
	case "base64":
		filtered := data[:0:0]
		for _, b := range data {
			if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
				filtered = append(filtered, b)
			}
		}
		out := make([]byte, base64.StdEncoding.DecodedLen(len(filtered)))
		n, err := base64.StdEncoding.Decode(out, filtered)
		if err != nil {
			n, _ = base64.RawStdEncoding.Decode(out, filtered)
		}
		return out[:n]
	default:
		return data
	}
}

func decodeCharset(data []byte, contentType string) string {
	r, err := charset.NewReader(bytes.NewReader(data), contentType)
	if err != nil {
		return strings.ToValidUTF8(string(data), "�")
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		return strings.ToValidUTF8(string(data), "�")
	}
	return string(decoded)
}

func decodeAddrHeader(dec *mime.WordDecoder, raw string) string {
	if raw == "" {
		return ""
	}
	decoded, err := dec.DecodeHeader(raw)
	if err != nil {
		decoded = raw
	}
	if _, err := mail.ParseAddressList(decoded); err != nil {
		return ""
	}
	return decoded
}

// --- Small utilities ---

func stripAngles(s string) string {
	if len(s) >= 2 && s[0] == '<' && s[len(s)-1] == '>' {
		return s[1 : len(s)-1]
	}
	return s
}

func truncateRefs(refs []string) []string {
	for len(refs) > 0 && len(strings.Join(refs, "\n")) > maxRefsBytes {
		refs = refs[1:]
	}
	return refs
}

// extractCIDRefs returns the set of cid: values (lowercase, no angle brackets)
// referenced in img src attributes within rawHTML.
func extractCIDRefs(rawHTML string) map[string]bool {
	refs := make(map[string]bool)
	lower := strings.ToLower(rawHTML)
	pos := 0
	for {
		idx := strings.Index(lower[pos:], "cid:")
		if idx < 0 {
			break
		}
		absIdx := pos + idx
		start := absIdx + 4
		end := start
		for end < len(rawHTML) {
			c := rawHTML[end]
			if c == '"' || c == '\'' || c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '>' {
				break
			}
			end++
		}
		if end > start {
			refs[strings.ToLower(rawHTML[start:end])] = true
		}
		pos = absIdx + 1
	}
	return refs
}
