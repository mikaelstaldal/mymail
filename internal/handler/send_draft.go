package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"

	ht "github.com/ogen-go/ogen/http"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/mikaelstaldal/mymail/internal/sanitize"
	"github.com/mikaelstaldal/mymail/internal/service"
)

// ──────────────────────────────────────────────────────────────────────────────
// Input validation helpers
// ──────────────────────────────────────────────────────────────────────────────

// validateAndStripAddrList validates a comma-separated address list.
// Empty strings are always accepted. Returns the CR/LF/NUL-stripped value.
func validateAndStripAddrList(raw, field string) (string, error) {
	clean := service.StripHeaderControls(raw)
	if len(clean) > 8192 {
		return "", fmt.Errorf("%s must not exceed 8192 characters", field)
	}
	if clean == "" {
		return "", nil
	}
	addrs, err := mail.ParseAddressList(clean)
	if err != nil {
		return "", fmt.Errorf("invalid %s: %v", field, err)
	}
	for _, a := range addrs {
		if a.Address == "" {
			return "", fmt.Errorf("invalid %s: empty address", field)
		}
	}
	return clean, nil
}

// stripAngleBrackets strips a single pair of surrounding angle brackets if present.
func stripAngleBrackets(s string) string {
	if len(s) >= 2 && s[0] == '<' && s[len(s)-1] == '>' {
		return s[1 : len(s)-1]
	}
	return s
}

const maxRefsBytes = 16 * 1024

// normalizeReferences strips angle brackets and control chars from each element,
// joins with "\n", and truncates to 16 KiB by dropping oldest entries.
func normalizeReferences(refs []string) string {
	cleaned := make([]string, 0, len(refs))
	for _, r := range refs {
		r = service.StripHeaderControls(r)
		r = stripAngleBrackets(r)
		if r != "" {
			cleaned = append(cleaned, r)
		}
	}
	joined := strings.Join(cleaned, "\n")
	if len(joined) <= maxRefsBytes {
		return joined
	}
	for len(cleaned) > 0 {
		cleaned = cleaned[1:]
		joined = strings.Join(cleaned, "\n")
		if len(joined) <= maxRefsBytes {
			break
		}
	}
	return joined
}

// sendFieldsValidated holds all compose fields after validation and stripping.
type sendFieldsValidated struct {
	toAddr      string
	ccAddr      string
	bccAddr     string
	replyToAddr string
	subject     string
	inReplyTo   string
	references  string // newline-joined, angle brackets stripped
	bodyText    string
	bodyHTML    string
}

func validateSendFields(
	toAddrOpt, ccAddrOpt, bccAddrOpt, replyToAddrOpt, subjectOpt,
	bodyTextOpt, bodyHTMLOpt, inReplyToOpt api.OptString,
	refsSlice []string,
) (sendFieldsValidated, error) {
	var v sendFieldsValidated
	var err error

	if s, ok := toAddrOpt.Get(); ok {
		if v.toAddr, err = validateAndStripAddrList(s, "to_addr"); err != nil {
			return v, err
		}
	}
	if s, ok := ccAddrOpt.Get(); ok {
		if v.ccAddr, err = validateAndStripAddrList(s, "cc_addr"); err != nil {
			return v, err
		}
	}
	if s, ok := bccAddrOpt.Get(); ok {
		if v.bccAddr, err = validateAndStripAddrList(s, "bcc_addr"); err != nil {
			return v, err
		}
	}
	if s, ok := replyToAddrOpt.Get(); ok {
		if v.replyToAddr, err = validateAndStripAddrList(s, "reply_to_addr"); err != nil {
			return v, err
		}
	}
	if s, ok := subjectOpt.Get(); ok {
		s = service.StripHeaderControls(s)
		if len(s) > 998 {
			return v, fmt.Errorf("subject must not exceed 998 characters")
		}
		v.subject = s
	}
	if s, ok := bodyTextOpt.Get(); ok {
		v.bodyText = s
	}
	if s, ok := bodyHTMLOpt.Get(); ok {
		v.bodyHTML = s
	}
	if s, ok := inReplyToOpt.Get(); ok {
		s = service.StripHeaderControls(s)
		v.inReplyTo = stripAngleBrackets(s)
	}
	v.references = normalizeReferences(refsSlice)
	return v, nil
}

// splitNLToSlice splits a newline-separated string into a slice, dropping empty parts.
func splitNLToSlice(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ──────────────────────────────────────────────────────────────────────────────
// Identity resolution
// ──────────────────────────────────────────────────────────────────────────────

// resolveIdentityForSend resolves the From identity for a send operation.
// If identity_id is supplied, looks it up (400 if not found).
// If absent, uses the default identity (400 if no identities exist at all).
func (h *Handler) resolveIdentityForSend(ctx context.Context, identityIDOpt api.OptInt) (name, addr string, identityID sql.NullInt64, err error) {
	if v, ok := identityIDOpt.Get(); ok {
		identity, err := h.identities.GetIdentity(ctx, int64(v))
		if errors.Is(err, repository.ErrNotFound) {
			return "", "", sql.NullInt64{}, badRequest("identity not found")
		}
		if err != nil {
			return "", "", sql.NullInt64{}, err
		}
		return identity.Name, identity.Address, sql.NullInt64{Valid: true, Int64: int64(v)}, nil
	}
	identity, err := h.identities.GetDefaultIdentity(ctx)
	if errors.Is(err, repository.ErrNotFound) {
		return "", "", sql.NullInt64{}, badRequest("no identity configured; create one in Settings → Identities first")
	}
	if err != nil {
		return "", "", sql.NullInt64{}, err
	}
	return identity.Name, identity.Address, sql.NullInt64{Valid: true, Int64: int64(identity.ID)}, nil
}

// resolveIdentityForDraft resolves the identity for draft creation/update.
// If identity_id is supplied, validates it exists (400 if not). If absent, returns a null NullInt64
// (the draft repository will resolve from the default or use "" if no identities exist).
func (h *Handler) resolveIdentityForDraft(ctx context.Context, identityIDOpt api.OptInt) (sql.NullInt64, error) {
	if v, ok := identityIDOpt.Get(); ok {
		_, err := h.identities.GetIdentity(ctx, int64(v))
		if errors.Is(err, repository.ErrNotFound) {
			return sql.NullInt64{}, badRequest("identity not found")
		}
		if err != nil {
			return sql.NullInt64{}, err
		}
		return sql.NullInt64{Valid: true, Int64: int64(v)}, nil
	}
	return sql.NullInt64{}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Attachment helpers
// ──────────────────────────────────────────────────────────────────────────────

// readMultipartAttachments reads ht.MultipartFile list into model.DBAttachment list.
func readMultipartAttachments(files []ht.MultipartFile) ([]model.DBAttachment, error) {
	atts := make([]model.DBAttachment, 0, len(files))
	for _, f := range files {
		data, err := io.ReadAll(f.File)
		if err != nil {
			return nil, err
		}
		filename := f.Name
		if filename == "" {
			filename = "untitled"
		}
		contentType := f.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		atts = append(atts, model.DBAttachment{
			Filename:    filename,
			ContentType: contentType,
			Size:        len(data),
			Data:        data,
		})
	}
	return atts, nil
}

// upsertRecipients upserts To/Cc/Bcc addresses into the contacts table.
func (h *Handler) upsertRecipients(ctx context.Context, addrList string) {
	if addrList == "" {
		return
	}
	addrs, err := mail.ParseAddressList(addrList)
	if err != nil {
		return
	}
	for _, addr := range addrs {
		_ = h.contacts.UpsertContact(ctx, addr.Address, addr.Name)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Core send/schedule pipeline
// ──────────────────────────────────────────────────────────────────────────────

// isScheduled returns (true, sendAtTime) when sendAt is more than 60 seconds in the future.
func isScheduled(sendAt api.OptNilDateTime) (bool, time.Time) {
	t, ok := sendAt.Get()
	if !ok {
		return false, time.Time{}
	}
	t = t.UTC()
	return t.After(time.Now().UTC().Add(60 * time.Second)), t
}

// sanitizeForSend sanitizes body_html and computes has_external_images.
func sanitizeForSend(rawHTML string) (sanitizedHTML string, hasExtImg bool) {
	if rawHTML == "" {
		return "", false
	}
	sanitizedHTML = sanitize.SanitizeHTML(rawHTML)
	hasExtImg = sanitize.HasExternalImages(sanitizedHTML)
	return sanitizedHTML, hasExtImg
}

// executeSend builds a MIME message, pipes it to sendmail, and stores the sent message.
// Returns the new message ID or a 500-status error on sendmail failure.
func (h *Handler) executeSend(
	ctx context.Context,
	fromName, fromAddr string,
	identityID sql.NullInt64,
	v sendFieldsValidated,
	attachments []model.DBAttachment,
) (int64, error) {
	// Sanitize body_html once here for both MIME construction and DB storage.
	if v.bodyHTML != "" {
		v.bodyHTML = sanitize.SanitizeHTML(v.bodyHTML)
	}

	fields := service.SendFields{
		FromName:    fromName,
		FromAddr:    fromAddr,
		ToAddr:      v.toAddr,
		CcAddr:      v.ccAddr,
		BccAddr:     v.bccAddr,
		ReplyToAddr: v.replyToAddr,
		Subject:     v.subject,
		BodyText:    v.bodyText,
		BodyHTML:    v.bodyHTML,
		InReplyTo:   v.inReplyTo,
		References:  splitNLToSlice(v.references),
	}

	rawMsg, hasExtImg, msgIDValue, buildErr := service.BuildMIMEMessage(fields, attachments)
	if buildErr != nil {
		return 0, buildErr
	}

	stderr, sendErr := service.SendMail(h.sendmailPath, rawMsg)
	if sendErr != nil {
		errMsg := stderr
		if errMsg == "" {
			errMsg = sendErr.Error()
		}
		return 0, &api.DefaultStatusCode{StatusCode: 500, Response: api.Error{Error: errMsg}}
	}

	var refsNull sql.NullString
	if v.references != "" {
		refsNull = sql.NullString{Valid: true, String: v.references}
	}
	var inReplyToNull sql.NullString
	if v.inReplyTo != "" {
		inReplyToNull = sql.NullString{Valid: true, String: v.inReplyTo}
	}

	now := time.Now().UTC()
	msg := model.DBMessage{
		FolderID:          2, // Sent
		IdentityID:        identityID,
		MessageID:         sql.NullString{Valid: true, String: msgIDValue},
		FromAddr:          fromAddr,
		ToAddr:            v.toAddr,
		CcAddr:            v.ccAddr,
		BccAddr:           v.bccAddr,
		ReplyToAddr:       v.replyToAddr,
		Subject:           v.subject,
		Date:              now,
		BodyText:          v.bodyText,
		BodyHTML:          v.bodyHTML,
		Raw:               rawMsg,
		Read:              true,
		HasAttachments:    len(attachments) > 0,
		HasExternalImages: hasExtImg,
		InReplyTo:         inReplyToNull,
		References:        refsNull,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	sentID, err := h.messages.InsertMessage(ctx, msg)
	if err != nil {
		return 0, err
	}

	for _, att := range attachments {
		att.MessageID = int(sentID)
		if _, err := h.attachments.InsertAttachment(ctx, att); err != nil {
			return 0, err
		}
	}

	h.upsertRecipients(ctx, v.toAddr)
	h.upsertRecipients(ctx, v.ccAddr)
	h.upsertRecipients(ctx, v.bccAddr)

	return sentID, nil
}

// executeSchedule stores a message in the Scheduled folder (folder_id=5) for later delivery.
func (h *Handler) executeSchedule(
	ctx context.Context,
	fromName, fromAddr string,
	identityID sql.NullInt64,
	v sendFieldsValidated,
	sendAtTime time.Time,
	attachments []model.DBAttachment,
) (int64, error) {
	// Sanitize body_html and compute has_external_images for storage.
	sanitizedHTML, hasExtImg := sanitizeForSend(v.bodyHTML)

	var refsNull sql.NullString
	if v.references != "" {
		refsNull = sql.NullString{Valid: true, String: v.references}
	}
	var inReplyToNull sql.NullString
	if v.inReplyTo != "" {
		inReplyToNull = sql.NullString{Valid: true, String: v.inReplyTo}
	}

	now := time.Now().UTC()
	msg := model.DBMessage{
		FolderID:          5, // Scheduled
		IdentityID:        identityID,
		FromAddr:          fromAddr,
		ToAddr:            v.toAddr,
		CcAddr:            v.ccAddr,
		BccAddr:           v.bccAddr,
		ReplyToAddr:       v.replyToAddr,
		Subject:           v.subject,
		Date:              now,
		BodyText:          v.bodyText,
		BodyHTML:          sanitizedHTML,
		Raw:               nil,
		HasAttachments:    len(attachments) > 0,
		HasExternalImages: hasExtImg,
		SendAt:            sql.NullTime{Valid: true, Time: sendAtTime},
		InReplyTo:         inReplyToNull,
		References:        refsNull,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	_ = fromName // stored via identity_id reference

	scheduledID, err := h.messages.InsertMessage(ctx, msg)
	if err != nil {
		return 0, err
	}

	for _, att := range attachments {
		att.MessageID = int(scheduledID)
		if _, err := h.attachments.InsertAttachment(ctx, att); err != nil {
			return 0, err
		}
	}

	return scheduledID, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Send endpoints
// ──────────────────────────────────────────────────────────────────────────────

func (h *Handler) MessagesSendPost(ctx context.Context, req *api.SendRequest) (api.MessagesSendPostRes, error) {
	v, err := validateSendFields(
		req.ToAddr, req.CcAddr, req.BccAddr, req.ReplyToAddr,
		req.Subject, req.BodyText, req.BodyHTML, req.InReplyTo,
		req.References,
	)
	if err != nil {
		return &api.MessagesSendPostBadRequest{Error: err.Error()}, nil
	}
	if v.toAddr == "" && v.ccAddr == "" && v.bccAddr == "" {
		return &api.MessagesSendPostBadRequest{Error: "at least one of to_addr, cc_addr, bcc_addr must be non-empty"}, nil
	}

	fromName, fromAddr, identityID, err := h.resolveIdentityForSend(ctx, req.IdentityID)
	if err != nil {
		if e, ok := err.(*api.DefaultStatusCode); ok && e.StatusCode == 400 {
			return &api.MessagesSendPostBadRequest{Error: e.Response.Error}, nil
		}
		return nil, err
	}

	scheduled, sendAtTime := isScheduled(req.SendAt)
	if scheduled {
		id, err := h.executeSchedule(ctx, fromName, fromAddr, identityID, v, sendAtTime, nil)
		if err != nil {
			return nil, err
		}
		return &api.MessagesSendPostAccepted{ID: int(id), SendAt: sendAtTime}, nil
	}

	id, err := h.executeSend(ctx, fromName, fromAddr, identityID, v, nil)
	if err != nil {
		if e, ok := err.(*api.DefaultStatusCode); ok && e.StatusCode == 500 {
			return &api.MessagesSendPostInternalServerError{Error: e.Response.Error}, nil
		}
		return nil, err
	}
	return &api.MessagesSendPostCreated{ID: int(id)}, nil
}

func (h *Handler) MessagesSendWithAttachmentsPost(ctx context.Context, req *api.MessagesSendWithAttachmentsPostReq) (api.MessagesSendWithAttachmentsPostRes, error) {
	var sendReq api.SendRequest
	if sr, ok := req.Message.Get(); ok {
		sendReq = sr
	}

	v, err := validateSendFields(
		sendReq.ToAddr, sendReq.CcAddr, sendReq.BccAddr, sendReq.ReplyToAddr,
		sendReq.Subject, sendReq.BodyText, sendReq.BodyHTML, sendReq.InReplyTo,
		sendReq.References,
	)
	if err != nil {
		return &api.MessagesSendWithAttachmentsPostBadRequest{Error: err.Error()}, nil
	}
	if v.toAddr == "" && v.ccAddr == "" && v.bccAddr == "" {
		return &api.MessagesSendWithAttachmentsPostBadRequest{Error: "at least one of to_addr, cc_addr, bcc_addr must be non-empty"}, nil
	}

	fromName, fromAddr, identityID, err := h.resolveIdentityForSend(ctx, sendReq.IdentityID)
	if err != nil {
		if e, ok := err.(*api.DefaultStatusCode); ok && e.StatusCode == 400 {
			return &api.MessagesSendWithAttachmentsPostBadRequest{Error: e.Response.Error}, nil
		}
		return nil, err
	}

	attachments, err := readMultipartAttachments(req.Attachments)
	if err != nil {
		return nil, err
	}

	scheduled, sendAtTime := isScheduled(sendReq.SendAt)
	if scheduled {
		id, err := h.executeSchedule(ctx, fromName, fromAddr, identityID, v, sendAtTime, attachments)
		if err != nil {
			return nil, err
		}
		return &api.MessagesSendWithAttachmentsPostAccepted{ID: int(id), SendAt: sendAtTime}, nil
	}

	id, err := h.executeSend(ctx, fromName, fromAddr, identityID, v, attachments)
	if err != nil {
		if e, ok := err.(*api.DefaultStatusCode); ok && e.StatusCode == 500 {
			return &api.MessagesSendWithAttachmentsPostInternalServerError{Error: e.Response.Error}, nil
		}
		return nil, err
	}
	return &api.MessagesSendWithAttachmentsPostCreated{ID: int(id)}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Draft helpers
// ──────────────────────────────────────────────────────────────────────────────

// buildDraftDBMessage builds a model.DBMessage for draft creation/update.
func buildDraftDBMessage(identityID sql.NullInt64, v sendFieldsValidated, sendAt api.OptNilDateTime) model.DBMessage {
	now := time.Now().UTC()

	var sendAtVal sql.NullTime
	if t, ok := sendAt.Get(); ok {
		sendAtVal = sql.NullTime{Valid: true, Time: t.UTC()}
	}
	var refsNull sql.NullString
	if v.references != "" {
		refsNull = sql.NullString{Valid: true, String: v.references}
	}
	var inReplyToNull sql.NullString
	if v.inReplyTo != "" {
		inReplyToNull = sql.NullString{Valid: true, String: v.inReplyTo}
	}

	return model.DBMessage{
		IdentityID:  identityID,
		ToAddr:      v.toAddr,
		CcAddr:      v.ccAddr,
		BccAddr:     v.bccAddr,
		ReplyToAddr: v.replyToAddr,
		Subject:     v.subject,
		BodyText:    v.bodyText,
		BodyHTML:    v.bodyHTML,
		Date:        now,
		InReplyTo:   inReplyToNull,
		References:  refsNull,
		SendAt:      sendAtVal,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func validateDraftFields(req *api.DraftRequest) (sendFieldsValidated, error) {
	return validateSendFields(
		req.ToAddr, req.CcAddr, req.BccAddr, req.ReplyToAddr,
		req.Subject, req.BodyText, req.BodyHTML, req.InReplyTo,
		req.References,
	)
}

// ──────────────────────────────────────────────────────────────────────────────
// Draft endpoints
// ──────────────────────────────────────────────────────────────────────────────

func (h *Handler) DraftsPost(ctx context.Context, req *api.DraftRequest) (api.DraftsPostRes, error) {
	v, err := validateDraftFields(req)
	if err != nil {
		return &api.Error{Error: err.Error()}, nil
	}

	identityID, err := h.resolveIdentityForDraft(ctx, req.IdentityID)
	if err != nil {
		if e, ok := err.(*api.DefaultStatusCode); ok && e.StatusCode == 400 {
			return &api.Error{Error: e.Response.Error}, nil
		}
		return nil, err
	}

	msg := buildDraftDBMessage(identityID, v, req.SendAt)

	var sourceID *int64
	if smID, ok := req.SourceMessageID.Get(); ok {
		id := int64(smID)
		sourceID = &id
	}

	// CreateDraftCopying atomically inserts the draft and copies source attachments.
	draftID, err := h.drafts.CreateDraftCopying(ctx, msg, sourceID)
	if errors.Is(err, repository.ErrUnknownIdentity) {
		return &api.Error{Error: "identity not found"}, nil
	}
	if errors.Is(err, repository.ErrSourceNotFound) {
		return &api.Error{Error: "source_message_id references a message that does not exist"}, nil
	}
	if err != nil {
		return nil, err
	}

	draft, err := h.drafts.GetDraft(ctx, draftID)
	if err != nil {
		return nil, err
	}
	return &api.DraftsPostCreated{ID: draft.ID, UpdatedAt: draft.UpdatedAt}, nil
}

func (h *Handler) DraftsWithAttachmentsPost(ctx context.Context, req *api.DraftsWithAttachmentsPostReq) (api.DraftsWithAttachmentsPostRes, error) {
	var draftReq api.DraftRequest
	if dr, ok := req.Message.Get(); ok {
		draftReq = dr
	}

	v, err := validateDraftFields(&draftReq)
	if err != nil {
		return &api.Error{Error: err.Error()}, nil
	}

	identityID, err := h.resolveIdentityForDraft(ctx, draftReq.IdentityID)
	if err != nil {
		if e, ok := err.(*api.DefaultStatusCode); ok && e.StatusCode == 400 {
			return &api.Error{Error: e.Response.Error}, nil
		}
		return nil, err
	}

	msg := buildDraftDBMessage(identityID, v, draftReq.SendAt)

	var sourceID *int64
	if smID, ok := draftReq.SourceMessageID.Get(); ok {
		id := int64(smID)
		sourceID = &id
	}

	// CreateDraftCopying atomically inserts the draft and copies source attachments.
	draftID, err := h.drafts.CreateDraftCopying(ctx, msg, sourceID)
	if errors.Is(err, repository.ErrUnknownIdentity) {
		return &api.Error{Error: "identity not found"}, nil
	}
	if errors.Is(err, repository.ErrSourceNotFound) {
		return &api.Error{Error: "source_message_id references a message that does not exist"}, nil
	}
	if err != nil {
		return nil, err
	}

	uploadedAtts, err := readMultipartAttachments(req.Attachments)
	if err != nil {
		return nil, err
	}
	for i := range uploadedAtts {
		uploadedAtts[i].MessageID = int(draftID)
		if _, err := h.attachments.InsertAttachment(ctx, uploadedAtts[i]); err != nil {
			return nil, err
		}
	}

	draft, err := h.drafts.GetDraft(ctx, draftID)
	if err != nil {
		return nil, err
	}
	return &api.DraftsWithAttachmentsPostCreated{ID: draft.ID, UpdatedAt: draft.UpdatedAt}, nil
}

func (h *Handler) DraftsIDPut(ctx context.Context, req *api.DraftRequest, params api.DraftsIDPutParams) (api.DraftsIDPutRes, error) {
	id := int64(params.ID)

	v, err := validateDraftFields(req)
	if err != nil {
		return &api.DraftsIDPutBadRequest{Error: err.Error()}, nil
	}

	identityID, err := h.resolveIdentityForDraft(ctx, req.IdentityID)
	if err != nil {
		if e, ok := err.(*api.DefaultStatusCode); ok && e.StatusCode == 400 {
			return &api.DraftsIDPutBadRequest{Error: e.Response.Error}, nil
		}
		return nil, err
	}

	msg := buildDraftDBMessage(identityID, v, req.SendAt)

	err = h.drafts.UpdateDraft(ctx, id, msg)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.DraftsIDPutNotFound{Error: "draft not found"}, nil
	}
	if errors.Is(err, repository.ErrUnknownIdentity) {
		return &api.DraftsIDPutBadRequest{Error: "identity not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	draft, err := h.drafts.GetDraft(ctx, id)
	if err != nil {
		return nil, err
	}
	return &api.DraftsIDPutOK{ID: draft.ID, UpdatedAt: draft.UpdatedAt}, nil
}

func (h *Handler) DraftsWithAttachmentsIDPut(ctx context.Context, req *api.DraftsWithAttachmentsIDPutReq, params api.DraftsWithAttachmentsIDPutParams) (api.DraftsWithAttachmentsIDPutRes, error) {
	id := int64(params.ID)

	var draftReq api.DraftRequest
	if dr, ok := req.Message.Get(); ok {
		draftReq = dr
	}

	v, err := validateDraftFields(&draftReq)
	if err != nil {
		return &api.DraftsWithAttachmentsIDPutBadRequest{Error: err.Error()}, nil
	}

	identityID, err := h.resolveIdentityForDraft(ctx, draftReq.IdentityID)
	if err != nil {
		if e, ok := err.(*api.DefaultStatusCode); ok && e.StatusCode == 400 {
			return &api.DraftsWithAttachmentsIDPutBadRequest{Error: e.Response.Error}, nil
		}
		return nil, err
	}

	msg := buildDraftDBMessage(identityID, v, draftReq.SendAt)

	err = h.drafts.UpdateDraft(ctx, id, msg)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.DraftsWithAttachmentsIDPutNotFound{Error: "draft not found"}, nil
	}
	if errors.Is(err, repository.ErrUnknownIdentity) {
		return &api.DraftsWithAttachmentsIDPutBadRequest{Error: "identity not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	// Replace all attachments wholesale (atomic: delete + insert in one transaction).
	uploadedAtts, err := readMultipartAttachments(req.Attachments)
	if err != nil {
		return nil, err
	}
	if err := h.attachments.ReplaceAttachments(ctx, id, uploadedAtts); err != nil {
		return nil, err
	}

	draft, err := h.drafts.GetDraft(ctx, id)
	if err != nil {
		return nil, err
	}
	return &api.DraftsWithAttachmentsIDPutOK{ID: draft.ID, UpdatedAt: draft.UpdatedAt}, nil
}

func (h *Handler) DraftsIDDelete(ctx context.Context, params api.DraftsIDDeleteParams) (api.DraftsIDDeleteRes, error) {
	id := int64(params.ID)

	err := h.drafts.DeleteDraft(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "draft not found"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.DraftsIDDeleteNoContent{}, nil
}

func (h *Handler) DraftsIDAttachmentsAttachmentIDDelete(ctx context.Context, params api.DraftsIDAttachmentsAttachmentIDDeleteParams) (api.DraftsIDAttachmentsAttachmentIDDeleteRes, error) {
	draftID := int64(params.ID)
	attachmentID := int64(params.AttachmentID)

	// Verify the message exists and is in Drafts (folder_id=3).
	_, err := h.drafts.GetDraft(ctx, draftID)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.DraftsIDAttachmentsAttachmentIDDeleteNotFound{Error: "draft not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	err = h.attachments.DeleteAttachment(ctx, attachmentID, draftID)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.DraftsIDAttachmentsAttachmentIDDeleteNotFound{Error: "attachment not found"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.DraftsIDAttachmentsAttachmentIDDeleteNoContent{}, nil
}

func (h *Handler) DraftsIDSendPost(ctx context.Context, params api.DraftsIDSendPostParams) (api.DraftsIDSendPostRes, error) {
	id := int64(params.ID)

	draft, err := h.drafts.GetDraft(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.DraftsIDSendPostNotFound{Error: "draft not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	if draft.ToAddr == "" && draft.CcAddr == "" && draft.BccAddr == "" {
		return &api.DraftsIDSendPostBadRequest{Error: "at least one of to_addr, cc_addr, bcc_addr must be non-empty"}, nil
	}

	// Resolve identity: use draft's identity_id if it resolves; otherwise fall back to default.
	var fromName, fromAddr string
	var identityID sql.NullInt64
	if draft.IdentityID.Valid {
		identity, err := h.identities.GetIdentity(ctx, draft.IdentityID.Int64)
		if err == nil {
			fromName = identity.Name
			fromAddr = identity.Address
			identityID = draft.IdentityID
		} else {
			// Draft's identity was deleted; fall through to default.
			identity, err = h.identities.GetDefaultIdentity(ctx)
			if errors.Is(err, repository.ErrNotFound) {
				return &api.DraftsIDSendPostBadRequest{Error: "no identity configured; create one in Settings → Identities first"}, nil
			}
			if err != nil {
				return nil, err
			}
			fromName = identity.Name
			fromAddr = identity.Address
			identityID = sql.NullInt64{Valid: true, Int64: int64(identity.ID)}
		}
	} else {
		identity, err := h.identities.GetDefaultIdentity(ctx)
		if errors.Is(err, repository.ErrNotFound) {
			return &api.DraftsIDSendPostBadRequest{Error: "no identity configured; create one in Settings → Identities first"}, nil
		}
		if err != nil {
			return nil, err
		}
		fromName = identity.Name
		fromAddr = identity.Address
		identityID = sql.NullInt64{Valid: true, Int64: int64(identity.ID)}
	}

	// Load attachments with data.
	attachments, err := h.attachments.ListAttachmentsWithData(ctx, int64(draft.ID))
	if err != nil {
		return nil, err
	}

	var refsString string
	if draft.References.Valid {
		refsString = draft.References.String
	}
	inReplyTo := ""
	if draft.InReplyTo.Valid {
		inReplyTo = draft.InReplyTo.String
	}

	v := sendFieldsValidated{
		toAddr:      draft.ToAddr,
		ccAddr:      draft.CcAddr,
		bccAddr:     draft.BccAddr,
		replyToAddr: draft.ReplyToAddr,
		subject:     draft.Subject,
		bodyText:    draft.BodyText,
		bodyHTML:    draft.BodyHTML,
		inReplyTo:   inReplyTo,
		references:  refsString,
	}

	// Determine mode from draft's send_at.
	var sendAtOpt api.OptNilDateTime
	if draft.SendAt.Valid {
		sendAtOpt.SetTo(draft.SendAt.Time)
	}
	scheduled, sendAtTime := isScheduled(sendAtOpt)

	if scheduled {
		scheduledID, err := h.executeSchedule(ctx, fromName, fromAddr, identityID, v, sendAtTime, attachments)
		if err != nil {
			return nil, err
		}
		if err := h.drafts.DeleteDraft(ctx, id); err != nil {
			return &api.DraftsIDSendPostInternalServerError{Error: "message scheduled but draft could not be deleted: " + err.Error()}, nil
		}
		return &api.DraftsIDSendPostAccepted{ID: int(scheduledID), SendAt: sendAtTime}, nil
	}

	sentID, err := h.executeSend(ctx, fromName, fromAddr, identityID, v, attachments)
	if err != nil {
		if e, ok := err.(*api.DefaultStatusCode); ok && e.StatusCode == 500 {
			return &api.DraftsIDSendPostInternalServerError{Error: e.Response.Error}, nil
		}
		return nil, err
	}

	if err := h.drafts.DeleteDraft(ctx, id); err != nil {
		return &api.DraftsIDSendPostInternalServerError{Error: "message sent but draft could not be deleted: " + err.Error()}, nil
	}
	return &api.DraftsIDSendPostCreated{ID: int(sentID)}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Scheduled endpoints
// ──────────────────────────────────────────────────────────────────────────────

func (h *Handler) ScheduledIDDelete(ctx context.Context, params api.ScheduledIDDeleteParams) (api.ScheduledIDDeleteRes, error) {
	id := int64(params.ID)

	msg, err := h.drafts.CancelScheduled(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.ScheduledIDDeleteNotFound{Error: "scheduled message not found or already cancelled"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.ScheduledIDDeleteOK{ID: msg.ID, FolderID: msg.FolderID}, nil
}
