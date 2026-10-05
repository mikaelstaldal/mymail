package lda

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	"github.com/jaytaylor/html2text"
	"github.com/mikaelstaldal/mymail/internal/service"

	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/sanitize"
)

const (
	maxRefsBytes        = 16 * 1024
	maxRefsCount        = 1000
	maxRefsHeaderBytes  = 256 * 1024
	maxMIMEDepth        = 30
	maxMIMEParts        = 1000
	maxMIMEWorkBytes    = 256 << 20
	maxMIMEDecodedBytes = 64 << 20
)

// ParseMessage parses a raw RFC 5322 message into a ParsedMessage.
// Malformed messages and messages exceeding MIME resource limits return an error.
func ParseMessage(raw []byte) (*model.ParsedMessage, error) {
	if len(raw) > MaxMessageBytes {
		return nil, fmt.Errorf("message exceeds %d bytes", MaxMessageBytes)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}

	dec := service.NewWordDecoder()

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
		if len(r) > maxRefsHeaderBytes {
			return nil, fmt.Errorf("references header exceeds %d bytes", maxRefsHeaderBytes)
		}
		for _, tok := range strings.Fields(r) {
			if ref := stripAngles(tok); ref != "" {
				refs = append(refs, ref)
			}
		}
	}
	refs = truncateRefs(refs)

	fromAddr := service.DecodeAddressHeader(msg.Header.Get("From"))
	toAddr := service.DecodeAddressHeader(msg.Header.Get("To"))
	ccAddr := service.DecodeAddressHeader(msg.Header.Get("Cc"))
	bccAddr := service.DecodeAddressHeader(msg.Header.Get("Bcc"))
	replyToAddr := service.DecodeAddressHeader(msg.Header.Get("Reply-To"))

	subject, _ := dec.DecodeHeader(msg.Header.Get("Subject"))

	bodyBytes, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, err
	}

	state := &mimeState{
		cidMap:    make(map[string][]byte),
		cidCT:     make(map[string]string),
		workBytes: int64(len(bodyBytes)),
	}
	ct := msg.Header.Get("Content-Type")
	if ct == "" {
		ct = "text/plain"
	}
	if err := traversePart(ct, msg.Header.Get("Content-Transfer-Encoding"), msg.Header, bodyBytes, state, 0); err != nil {
		return nil, err
	}

	rawHTML := ""
	if state.bodyHTML != nil {
		rawHTML = *state.bodyHTML
	}

	bodyHTML, usedCIDs := sanitize.ResolveCID(rawHTML, state.cidMap, state.cidCT)

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
		Headers:           map[string][]string(msg.Header),
	}, nil
}

// --- MIME traversal ---

type mimeState struct {
	bodyText     *string
	bodyHTML     *string
	cidMap       map[string][]byte
	cidCT        map[string]string
	pending      []pendingPart
	parts        int
	workBytes    int64
	decodedBytes int64
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
func traversePart(rawCT, rawCTE string, headers headerGetter, rawBody []byte, state *mimeState, depth int) error {
	if depth > maxMIMEDepth {
		return fmt.Errorf("MIME nesting exceeds %d levels", maxMIMEDepth)
	}
	mediaType, params, err := mime.ParseMediaType(rawCT)
	if err != nil {
		mediaType = "application/octet-stream"
		params = map[string]string{}
	}
	mediaType = strings.ToLower(mediaType)

	if mediaType == "message/rfc822" {
		return nil
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil
		}
		mr := multipart.NewReader(bytes.NewReader(rawBody), boundary)
		if mediaType == "multipart/alternative" {
			return traverseAlternative(mr, state, depth)
		} else {
			return traverseMultipartParts(mr, state, depth)
		}
	}

	body, err := decodeCTE(rawBody, rawCTE, maxMIMEDecodedBytes-state.decodedBytes)
	if err != nil {
		return err
	}
	state.decodedBytes += int64(len(body))

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
			s := service.DecodeCharset(body, rawCT)
			state.bodyText = &s
		} else {
			state.pending = append(state.pending, pendingPart{
				contentID: contentID, filename: filename,
				contentType: mediaType, data: body,
			})
		}
	case "text/html":
		if disposition != "attachment" && state.bodyHTML == nil {
			s := service.DecodeCharset(body, rawCT)
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
	return nil
}

func traverseMultipartParts(mr *multipart.Reader, state *mimeState, depth int) error {
	for {
		p, err := mr.NextPart()
		if err != nil {
			return nil // Keep partial content from malformed multipart messages.
		}
		if err := state.countPart(); err != nil {
			return err
		}
		body, err := state.readPart(p)
		if err != nil {
			return err
		}
		ct := p.Header.Get("Content-Type")
		if ct == "" {
			ct = "text/plain"
		}
		if err := traversePart(ct, p.Header.Get("Content-Transfer-Encoding"), p.Header, body, state, depth+1); err != nil {
			return err
		}
	}
}

func (state *mimeState) countPart() error {
	state.parts++
	if state.parts > maxMIMEParts {
		return fmt.Errorf("MIME part count exceeds %d", maxMIMEParts)
	}
	return nil
}

func (state *mimeState) readPart(p *multipart.Part) ([]byte, error) {
	remaining := int64(maxMIMEWorkBytes) - state.workBytes
	if remaining < 0 {
		return nil, fmt.Errorf("MIME work exceeds %d bytes", maxMIMEWorkBytes)
	}
	body, _ := io.ReadAll(io.LimitReader(p, remaining+1))
	state.workBytes += int64(len(body))
	if state.workBytes > maxMIMEWorkBytes {
		return nil, fmt.Errorf("MIME work exceeds %d bytes", maxMIMEWorkBytes)
	}
	// A malformed part may end with unexpected EOF. Keep the bytes already read,
	// as the parser did before resource limits were added.
	return body, nil
}

type rawPart struct {
	ct      string
	cte     string
	headers textproto.MIMEHeader
	body    []byte
}

// traverseAlternative picks the most-preferred sub-part per RFC 2046
// (last part is most preferred). HTML/multipart beats plain text.
func traverseAlternative(mr *multipart.Reader, state *mimeState, depth int) error {
	var parts []rawPart
	for {
		p, err := mr.NextPart()
		if err != nil {
			break // Keep earlier alternatives from malformed messages.
		}
		if err := state.countPart(); err != nil {
			return err
		}
		body, err := state.readPart(p)
		if err != nil {
			return err
		}
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
		if err := traversePart(p.ct, p.cte, p.headers, p.body, state, depth+1); err != nil {
			return err
		}
	}
	if plainIdx >= 0 && state.bodyText == nil {
		p := parts[plainIdx]
		if err := traversePart(p.ct, p.cte, p.headers, p.body, state, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// --- Decoding helpers ---

func decodeCTE(data []byte, cte string, remaining int64) ([]byte, error) {
	if remaining < 0 {
		return nil, fmt.Errorf("decoded MIME data exceeds %d bytes", maxMIMEDecodedBytes)
	}
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		decoded, _ := io.ReadAll(io.LimitReader(quotedprintable.NewReader(bytes.NewReader(data)), remaining+1))
		if int64(len(decoded)) > remaining {
			return nil, fmt.Errorf("decoded MIME data exceeds %d bytes", maxMIMEDecodedBytes)
		}
		return decoded, nil
	case "base64":
		filtered := data[:0:0]
		for _, b := range data {
			if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
				filtered = append(filtered, b)
			}
		}
		if int64(base64.StdEncoding.DecodedLen(len(filtered))) > remaining+2 {
			return nil, fmt.Errorf("decoded MIME data exceeds %d bytes", maxMIMEDecodedBytes)
		}
		out := make([]byte, base64.StdEncoding.DecodedLen(len(filtered)))
		n, err := base64.StdEncoding.Decode(out, filtered)
		if err != nil {
			n, _ = base64.RawStdEncoding.Decode(out, filtered)
		}
		if int64(n) > remaining {
			return nil, fmt.Errorf("decoded MIME data exceeds %d bytes", maxMIMEDecodedBytes)
		}
		return out[:n], nil
	default:
		if int64(len(data)) > remaining {
			return nil, fmt.Errorf("decoded MIME data exceeds %d bytes", maxMIMEDecodedBytes)
		}
		return data, nil
	}
}

// --- Small utilities ---

func stripAngles(s string) string {
	if len(s) >= 2 && s[0] == '<' && s[len(s)-1] == '>' {
		return s[1 : len(s)-1]
	}
	return s
}

func truncateRefs(refs []string) []string {
	total := 0
	for i := len(refs) - 1; i >= 0; i-- {
		length := len(refs[i])
		if i < len(refs)-1 {
			length++ // newline separator
		}
		if total+length > maxRefsBytes || len(refs)-i > maxRefsCount {
			return refs[i+1:]
		}
		total += length
	}
	return refs
}
