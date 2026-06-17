package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

func (h *Handler) MessagesIDHeadersGet(ctx context.Context, params api.MessagesIDHeadersGetParams) (api.MessagesIDHeadersGetRes, error) {
	id := int64(params.ID)

	raw, err := h.messages.GetRawMessage(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "message not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	if raw == nil {
		return &api.Error{Error: "no headers for draft"}, nil
	}

	headerBlock, _, ok := bytes.Cut(raw, []byte("\r\n\r\n"))
	if !ok {
		headerBlock, _, ok = bytes.Cut(raw, []byte("\n\n"))
		if !ok {
			headerBlock = raw
		}
	}

	return &api.MessagesIDHeadersGetOK{Data: bytes.NewReader(headerBlock)}, nil
}

func (h *Handler) MessagesIDBodyGet(ctx context.Context, params api.MessagesIDBodyGetParams) (api.MessagesIDBodyGetRes, error) {
	msg, err := h.messages.GetMessageDetail(ctx, int64(params.ID))
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "message not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	imgSrc := "data:"
	if v, ok := params.External.Get(); ok && v == "1" {
		imgSrc = "https: data:"
	}
	csp := fmt.Sprintf("default-src 'none'; img-src %s; style-src 'unsafe-inline'; frame-ancestors 'self'", imgSrc)

	body := fmt.Sprintf("<!DOCTYPE html>\n<html>\n<head><meta charset=\"utf-8\"><base target=\"_blank\"></head>\n<body>%s</body>\n</html>", msg.BodyHTML)
	return &api.MessagesIDBodyGetOKHeaders{
		ContentSecurityPolicy: api.NewOptString(csp),
		XFrameOptions:         api.NewOptString("SAMEORIGIN"),
		ReferrerPolicy:        api.NewOptString("no-referrer"),
		Response:              api.MessagesIDBodyGetOK{Data: strings.NewReader(body)},
	}, nil
}
