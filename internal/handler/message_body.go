package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ogen-go/ogen/middleware"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/phishing"
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

// MessageBodyFetchMetadata blocks browser-driven access outside the app's body
// iframe/subresource flow. Missing metadata remains supported for API clients
// and older browsers; the response CSP sandbox applies to those requests too.
func MessageBodyFetchMetadata(req middleware.Request, next middleware.Next) (middleware.Response, error) {
	if req.OperationName == api.MessagesIDBodyGetOperation &&
		(req.Raw.Header.Get("Sec-Fetch-Site") == "cross-site" || req.Raw.Header.Get("Sec-Fetch-Dest") == "document") {
		return middleware.Response{}, &api.DefaultStatusCode{
			StatusCode: 403, Response: api.Error{Error: "message bodies require an in-app request"},
		}
	}
	return next(req)
}

func (h *Handler) MessagesIDBodyGet(ctx context.Context, params api.MessagesIDBodyGetParams) (api.MessagesIDBodyGetRes, error) {
	ctx, release, err := h.messages.DetailReadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	msg, err := h.messages.GetMessageDetail(ctx, int64(params.ID))
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "message not found"}, nil
	}
	if err != nil {
		return nil, err
	}

	warningContext, err := h.messages.WarningContext(ctx, msg.FromAddr)
	if err != nil {
		return nil, err
	}
	servedHTML := phishing.RewriteLinks(msg.BodyHTML, int64(params.ID), phishing.ComputeWithLinks(msg, nil, warningContext, phishing.Links(msg.BodyHTML)))

	imgSrc := "data:"
	if v, ok := params.External.Get(); ok && v == "1" {
		imgSrc = "https: data:"
	}
	csp := fmt.Sprintf("default-src 'none'; img-src %s; style-src 'unsafe-inline'; frame-ancestors 'self'; sandbox allow-popups allow-popups-to-escape-sandbox allow-downloads", imgSrc)

	body := fmt.Sprintf("<!DOCTYPE html>\n<html>\n<head><meta charset=\"utf-8\"><base target=\"_blank\"></head>\n<body>%s</body>\n</html>", servedHTML)
	return &api.MessagesIDBodyGetOKHeaders{
		ContentSecurityPolicy: api.NewOptString(csp),
		XFrameOptions:         api.NewOptString("SAMEORIGIN"),
		ReferrerPolicy:        api.NewOptString("no-referrer"),
		Response:              api.MessagesIDBodyGetOK{Data: strings.NewReader(body)},
	}, nil
}
