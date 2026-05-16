package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

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

	body := fmt.Sprintf("<!DOCTYPE html>\n<html>\n<head><meta charset=\"utf-8\"></head>\n<body>%s</body>\n</html>", msg.BodyHTML)
	return &api.MessagesIDBodyGetOKHeaders{
		ContentSecurityPolicy: api.NewOptString(csp),
		XFrameOptions:         api.NewOptString("SAMEORIGIN"),
		ReferrerPolicy:        api.NewOptString("no-referrer"),
		Response:              api.MessagesIDBodyGetOK{Data: strings.NewReader(body)},
	}, nil
}
