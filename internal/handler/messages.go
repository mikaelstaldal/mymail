package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

func (h *Handler) FoldersFolderIDMessagesGet(ctx context.Context, params api.FoldersFolderIDMessagesGetParams) (api.FoldersFolderIDMessagesGetRes, error) {
	folderID := int64(params.FolderID)

	if err := h.folders.FolderExists(ctx, folderID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &api.Error{Error: "folder not found"}, nil
		}
		return nil, err
	}

	limit, offset := parsePagination(params.Limit, params.Offset)

	var unread, flagged *bool
	if v, ok := params.Unread.Get(); ok {
		unread = &v
	}
	if v, ok := params.Flagged.Get(); ok {
		flagged = &v
	}

	items, total, err := h.messages.ListMessages(ctx, folderID, limit, offset, unread, flagged)
	if err != nil {
		return nil, err
	}
	return &api.FoldersFolderIDMessagesGetOK{Total: total, Items: items}, nil
}

func (h *Handler) MessagesSearchGet(ctx context.Context, params api.MessagesSearchGetParams) (api.MessagesSearchGetRes, error) {
	q := strings.TrimSpace(params.Q)
	if q == "" {
		return &api.Error{Error: "q must contain at least one non-whitespace character"}, nil
	}
	if len([]rune(q)) > 500 {
		return &api.Error{Error: "q must not exceed 500 characters"}, nil
	}

	var folderID *int64
	if v, ok := params.FolderID.Get(); ok {
		id := int64(v)
		folderID = &id
	}

	var dateFrom, dateTo *time.Time
	if v, ok := params.DateFrom.Get(); ok {
		dateFrom = &v
	}
	if v, ok := params.DateTo.Get(); ok {
		dateTo = &v
	}

	limit, offset := parsePagination(params.Limit, params.Offset)

	items, total, err := h.messages.SearchMessages(ctx, q, folderID, dateFrom, dateTo, limit, offset)
	if err != nil {
		// A search that exceeds the repository's internal timeout returns a
		// deadline error; surface it as a clean client error rather than a
		// generic 500. (Caller-cancelled requests use a different context error.)
		if errors.Is(err, context.DeadlineExceeded) {
			return &api.Error{Error: "search timed out; please use a more specific query"}, nil
		}
		return nil, err
	}
	return &api.MessagesSearchGetOK{Total: total, Items: items}, nil
}

func (h *Handler) MessagesIDGet(ctx context.Context, params api.MessagesIDGetParams) (api.MessagesIDGetRes, error) {
	id := int64(params.ID)

	msg, err := h.messages.GetMessageDetail(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "message not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	attachments, err := h.attachments.ListAttachments(ctx, id)
	if err != nil {
		return nil, err
	}

	return msg.ToOASMessage(attachments), nil
}

func (h *Handler) MessagesIDRawGet(ctx context.Context, params api.MessagesIDRawGetParams) (api.MessagesIDRawGetRes, error) {
	id := int64(params.ID)

	raw, err := h.messages.GetRawMessage(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "message not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	if raw == nil {
		return &api.MessagesIDRawGetOKApplicationJSONHeaders{
			Response: api.MessagesIDRawGetOKApplicationJSON{},
		}, nil
	}

	disp := fmt.Sprintf("attachment; filename=%d.eml", id)
	return &api.MessagesIDRawGetOKMessageRfc822Headers{
		ContentDisposition: api.NewOptString(disp),
		Response:           api.MessagesIDRawGetOKMessageRfc822{Data: bytes.NewReader(raw)},
	}, nil
}

func (h *Handler) MessagesIDThreadGet(ctx context.Context, params api.MessagesIDThreadGetParams) (api.MessagesIDThreadGetRes, error) {
	id := int64(params.ID)

	items, truncated, err := h.messages.GetMessageThread(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "message not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	return &api.MessagesIDThreadGetOK{Total: len(items), Truncated: truncated, Items: items}, nil
}

func (h *Handler) MessagesIDPatch(ctx context.Context, req *api.MessagesIDPatchReq, params api.MessagesIDPatchParams) (api.MessagesIDPatchRes, error) {
	id := int64(params.ID)

	fields := make(map[string]any)

	if newFolderID, ok := req.FolderID.Get(); ok {
		if newFolderID == 3 || newFolderID == 5 || newFolderID == 6 {
			return &api.MessagesIDPatchBadRequest{Error: "cannot move to this folder"}, nil
		}
		msg, err := h.messages.GetMessageDetail(ctx, id)
		if errors.Is(err, repository.ErrNotFound) {
			return &api.MessagesIDPatchNotFound{Error: "message not found"}, nil
		}
		if err != nil {
			return nil, err
		}
		if msg.FolderID == 3 || msg.FolderID == 5 || msg.FolderID == 6 {
			return &api.MessagesIDPatchBadRequest{Error: "cannot move from this folder"}, nil
		}
		fields["folder_id"] = int64(newFolderID)
	}

	if v, ok := req.Read.Get(); ok {
		fields["read"] = v
	}
	if v, ok := req.Flagged.Get(); ok {
		fields["flagged"] = v
	}

	msg, err := h.messages.UpdateMessage(ctx, id, fields)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.MessagesIDPatchNotFound{Error: "message not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	return msg.ToOASMessageSummary(), nil
}

func (h *Handler) MessagesPatch(ctx context.Context, req *api.MessagesPatchReq) (api.MessagesPatchRes, error) {
	if len(req.Ids) == 0 {
		return &api.MessagesPatchBadRequest{Error: "ids must contain at least one id"}, nil
	}
	if len(req.Ids) > 1000 {
		return &api.MessagesPatchBadRequest{Error: "ids must not exceed 1000"}, nil
	}

	var read, flagged *bool
	if v, ok := req.Read.Get(); ok {
		read = &v
	}
	if v, ok := req.Flagged.Get(); ok {
		flagged = &v
	}

	n, err := h.messages.BulkUpdateMessages(ctx, toInt64IDs(req.Ids), read, flagged)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.MessagesPatchNotFound{Error: "message not found"}, nil
	}
	if errors.Is(err, repository.ErrTooManyIDs) {
		return &api.MessagesPatchBadRequest{Error: "ids must not exceed 1000"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.MessagesPatchOK{Updated: n}, nil
}

func (h *Handler) MessagesIDDelete(ctx context.Context, params api.MessagesIDDeleteParams) (api.MessagesIDDeleteRes, error) {
	id := int64(params.ID)

	err := h.messages.DeleteMessage(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.MessagesIDDeleteNotFound{Error: "message not found"}, nil
	}
	if errors.Is(err, repository.ErrForbiddenFolder) {
		return &api.MessagesIDDeleteBadRequest{Error: "cannot delete a message from this folder"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.MessagesIDDeleteNoContent{}, nil
}

func (h *Handler) MessagesDelete(ctx context.Context, req *api.MessagesDeleteReq) (api.MessagesDeleteRes, error) {
	if len(req.Ids) == 0 {
		return &api.MessagesDeleteBadRequest{Error: "ids must contain at least one id"}, nil
	}
	if len(req.Ids) > 1000 {
		return &api.MessagesDeleteBadRequest{Error: "ids must not exceed 1000"}, nil
	}

	moved, perm, err := h.messages.BulkDeleteMessages(ctx, toInt64IDs(req.Ids))
	if errors.Is(err, repository.ErrNotFound) {
		return &api.MessagesDeleteNotFound{Error: "message not found"}, nil
	}
	if errors.Is(err, repository.ErrForbiddenFolder) {
		return &api.MessagesDeleteBadRequest{Error: "cannot delete a message from this folder"}, nil
	}
	if errors.Is(err, repository.ErrTooManyIDs) {
		return &api.MessagesDeleteBadRequest{Error: "ids must not exceed 1000"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.MessagesDeleteOK{MovedToTrash: moved, PermanentlyDeleted: perm}, nil
}

func (h *Handler) MessagesMovePost(ctx context.Context, req *api.MessagesMovePostReq) (api.MessagesMovePostRes, error) {
	if len(req.Ids) == 0 {
		return &api.MessagesMovePostBadRequest{Error: "ids must contain at least one id"}, nil
	}
	if len(req.Ids) > 1000 {
		return &api.MessagesMovePostBadRequest{Error: "ids must not exceed 1000"}, nil
	}

	targetFolderID := int64(req.FolderID)
	if targetFolderID == 3 || targetFolderID == 5 || targetFolderID == 6 {
		return &api.MessagesMovePostBadRequest{Error: "cannot move to this folder"}, nil
	}

	if err := h.folders.FolderExists(ctx, targetFolderID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &api.MessagesMovePostBadRequest{Error: "target folder not found"}, nil
		}
		return nil, err
	}

	n, err := h.messages.MoveMessages(ctx, toInt64IDs(req.Ids), targetFolderID)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.MessagesMovePostNotFound{Error: "message not found"}, nil
	}
	if errors.Is(err, repository.ErrForbiddenFolder) {
		return &api.MessagesMovePostBadRequest{Error: "cannot move a message from this folder"}, nil
	}
	if errors.Is(err, repository.ErrTooManyIDs) {
		return &api.MessagesMovePostBadRequest{Error: "ids must not exceed 1000"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.MessagesMovePostOK{Updated: n}, nil
}

func (h *Handler) MessagesIDSnoozePost(ctx context.Context, req *api.MessagesIDSnoozePostReq, params api.MessagesIDSnoozePostParams) (api.MessagesIDSnoozePostRes, error) {
	id := int64(params.ID)

	msg, err := h.messages.SnoozeMessage(ctx, id, req.Until)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.MessagesIDSnoozePostNotFound{Error: "message not found"}, nil
	}
	if errors.Is(err, repository.ErrSnoozeTimeTooSoon) {
		return &api.MessagesIDSnoozePostBadRequest{Error: err.Error()}, nil
	}
	if errors.Is(err, repository.ErrForbiddenFolder) {
		return &api.MessagesIDSnoozePostBadRequest{Error: "cannot snooze a message in this folder"}, nil
	}
	if err != nil {
		return nil, err
	}

	var snoozeFolderID api.NilInt
	if msg.SnoozeFolder.Valid {
		snoozeFolderID = api.NewNilInt(int(msg.SnoozeFolder.Int64))
	} else {
		snoozeFolderID.SetToNull()
	}

	return &api.MessagesIDSnoozePostOK{
		ID:             msg.ID,
		FolderID:       msg.FolderID,
		SnoozedUntil:   msg.SnoozedUntil.Time,
		SnoozeFolderID: snoozeFolderID,
	}, nil
}

func (h *Handler) MessagesIDSnoozeDelete(ctx context.Context, params api.MessagesIDSnoozeDeleteParams) (api.MessagesIDSnoozeDeleteRes, error) {
	id := int64(params.ID)

	msg, err := h.messages.CancelSnooze(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.MessagesIDSnoozeDeleteNotFound{Error: "message not found"}, nil
	}
	if errors.Is(err, repository.ErrForbiddenFolder) {
		return &api.MessagesIDSnoozeDeleteBadRequest{Error: "message is not snoozed"}, nil
	}
	if err != nil {
		return nil, err
	}

	return &api.MessagesIDSnoozeDeleteOK{ID: msg.ID, FolderID: msg.FolderID}, nil
}

func (h *Handler) MessagesIDMarkJunkPost(ctx context.Context, params api.MessagesIDMarkJunkPostParams) (api.MessagesIDMarkJunkPostRes, error) {
	id := int64(params.ID)

	msg, err := h.messages.MarkJunk(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.MessagesIDMarkJunkPostNotFound{Error: "message not found"}, nil
	}
	if errors.Is(err, repository.ErrForbiddenFolder) {
		return &api.MessagesIDMarkJunkPostBadRequest{Error: "cannot mark junk from this folder"}, nil
	}
	if err != nil {
		return nil, err
	}

	return &api.MessagesIDMarkJunkPostOK{ID: msg.ID, FolderID: msg.FolderID}, nil
}

func (h *Handler) MessagesIDMarkNotJunkPost(ctx context.Context, params api.MessagesIDMarkNotJunkPostParams) (api.MessagesIDMarkNotJunkPostRes, error) {
	id := int64(params.ID)

	msg, err := h.messages.MarkNotJunk(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.MessagesIDMarkNotJunkPostNotFound{Error: "message not found"}, nil
	}
	if errors.Is(err, repository.ErrForbiddenFolder) {
		return &api.MessagesIDMarkNotJunkPostBadRequest{Error: "message is not in junk folder"}, nil
	}
	if err != nil {
		return nil, err
	}

	return &api.MessagesIDMarkNotJunkPostOK{ID: msg.ID, FolderID: msg.FolderID}, nil
}

func (h *Handler) AttachmentsIDGet(ctx context.Context, params api.AttachmentsIDGetParams) (api.AttachmentsIDGetRes, error) {
	id := int64(params.ID)

	att, err := h.attachments.GetAttachment(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "attachment not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	disp := contentDispositionFilename(att.Filename)
	return &api.AttachmentsIDGetOKHeaders{
		ContentDisposition: api.NewOptString(disp),
		Response:           api.AttachmentsIDGetOK{Data: bytes.NewReader(att.Data)},
	}, nil
}

// parsePagination extracts limit (default 50, max 200) and offset from optional query params.
func parsePagination(limitParam, offsetParam api.OptInt) (limit, offset int) {
	limit = 50
	if v, ok := limitParam.Get(); ok && v > 0 {
		if v > 200 {
			v = 200
		}
		limit = v
	}
	if v, ok := offsetParam.Get(); ok && v > 0 {
		offset = v
	}
	return
}

// toInt64IDs converts a []int slice (from generated request types) to []int64.
func toInt64IDs(ids []int) []int64 {
	out := make([]int64, len(ids))
	for i, id := range ids {
		out[i] = int64(id)
	}
	return out
}

// contentDispositionFilename builds a Content-Disposition header value.
// CR, LF, NUL, and " are stripped in a single pass; non-ASCII triggers RFC 8187 encoding.
func contentDispositionFilename(name string) string {
	var b strings.Builder
	hasNonASCII := false
	for _, r := range name {
		if r == '\r' || r == '\n' || r == '\x00' || r == '"' {
			continue
		}
		if r > 127 {
			hasNonASCII = true
		}
		b.WriteRune(r)
	}
	clean := b.String()
	if hasNonASCII {
		return "attachment; filename*=UTF-8''" + rfc8187Encode(clean)
	}
	return fmt.Sprintf(`attachment; filename="%s"`, clean)
}

// rfc8187Encode percent-encodes a string per RFC 8187 (attr-char set preserved).
func rfc8187Encode(s string) string {
	var b strings.Builder
	var buf [utf8.UTFMax]byte
	for _, r := range s {
		if isRFC8187AttrChar(r) {
			b.WriteRune(r)
		} else {
			n := utf8.EncodeRune(buf[:], r)
			for _, c := range buf[:n] {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
	}
	return b.String()
}

func isRFC8187AttrChar(r rune) bool {
	return (r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') ||
		r == '!' || r == '#' || r == '$' || r == '&' ||
		r == '+' || r == '-' || r == '.' || r == '^' ||
		r == '_' || r == '`' || r == '|' || r == '~'
}
