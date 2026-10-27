package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

func (h *Handler) BlockedSendersGet(ctx context.Context, params api.BlockedSendersGetParams) (*api.BlockedSendersGetOK, error) {
	limit, offset := params.Limit.Or(50), params.Offset.Or(0)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	var q *string
	if v, ok := params.Q.Get(); ok {
		q = &v
	}
	items, total, err := h.blockedSenders.List(ctx, q, limit, offset)
	if err != nil {
		return nil, err
	}
	return &api.BlockedSendersGetOK{Items: items, Total: total}, nil
}
func (h *Handler) BlockedSendersPost(ctx context.Context, req *api.BlockedSendersPostReq) (api.BlockedSendersPostRes, error) {
	b, created, err := h.blockedSenders.Add(ctx, strings.TrimSpace(req.Address), nil)
	if errors.Is(err, repository.ErrInvalidAddress) {
		return &api.Error{Error: "invalid address or own identity address"}, nil
	}
	if err != nil {
		return nil, err
	}
	if created {
		return (*api.BlockedSendersPostCreated)(&b), nil
	}
	return (*api.BlockedSendersPostOK)(&b), nil
}
func (h *Handler) BlockedSendersIDDelete(ctx context.Context, params api.BlockedSendersIDDeleteParams) (api.BlockedSendersIDDeleteRes, error) {
	err := h.blockedSenders.Delete(ctx, int64(params.ID))
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "blocked sender not found"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.BlockedSendersIDDeleteNoContent{}, nil
}
