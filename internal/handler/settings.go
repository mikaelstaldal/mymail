package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

// ── Health ────────────────────────────────────────────────────────────────────

func (h *Handler) HealthGet(_ context.Context) (*api.HealthGetOK, error) {
	return &api.HealthGetOK{Status: api.HealthGetOKStatusOk}, nil
}

// ── Filters ───────────────────────────────────────────────────────────────────

func (h *Handler) FiltersGet(ctx context.Context) (*api.FiltersGetOK, error) {
	filters, err := h.filters.ListFilters(ctx)
	if err != nil {
		return nil, err
	}
	return &api.FiltersGetOK{Total: len(filters), Items: filters}, nil
}

func (h *Handler) FiltersPost(ctx context.Context, req *api.FilterRequest) (api.FiltersPostRes, error) {
	f := filterFromRequest(req)
	created, err := h.filters.CreateFilter(ctx, f, req.Position)
	if err != nil {
		return &api.Error{Error: err.Error()}, nil
	}
	return &created, nil
}

func (h *Handler) FiltersIDPut(ctx context.Context, req *api.FilterRequest, params api.FiltersIDPutParams) (api.FiltersIDPutRes, error) {
	id := int64(params.ID)
	f := filterFromRequest(req)
	updated, err := h.filters.UpdateFilter(ctx, id, f)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.FiltersIDPutNotFound{Error: "filter not found"}, nil
	}
	if err != nil {
		return &api.FiltersIDPutBadRequest{Error: err.Error()}, nil
	}
	return &updated, nil
}

func (h *Handler) FiltersIDDelete(ctx context.Context, params api.FiltersIDDeleteParams) (api.FiltersIDDeleteRes, error) {
	id := int64(params.ID)
	err := h.filters.DeleteFilter(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "filter not found"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.FiltersIDDeleteNoContent{}, nil
}

func (h *Handler) FiltersReorderPatch(ctx context.Context, req *api.FiltersReorderPatchReq) (api.FiltersReorderPatchRes, error) {
	ids := make([]int64, len(req.Ids))
	for i, id := range req.Ids {
		ids[i] = int64(id)
	}
	n, err := h.filters.ReorderFilters(ctx, ids)
	if errors.Is(err, repository.ErrDuplicateID) {
		return &api.Error{Error: "duplicate id"}, nil
	}
	if errors.Is(err, repository.ErrUnknownID) {
		return &api.Error{Error: "unknown id"}, nil
	}
	if errors.Is(err, repository.ErrIncompleteReorder) {
		return &api.Error{Error: "incomplete reorder; all ids must be supplied"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.FiltersReorderPatchOK{Updated: n}, nil
}

// filterFromRequest converts a FilterRequest to a Filter model.
func filterFromRequest(req *api.FilterRequest) api.Filter {
	f := api.Filter{
		Action:   api.FilterAction(req.Action),
		FolderID: req.FolderID,
		Stop:     true, // default to true per DB default
	}
	if v, ok := req.Name.Get(); ok {
		f.Name = strings.TrimSpace(v)
	}
	if v, ok := req.MatchFrom.Get(); ok {
		f.MatchFrom = v
	}
	if v, ok := req.MatchTo.Get(); ok {
		f.MatchTo = v
	}
	if v, ok := req.MatchSubject.Get(); ok {
		f.MatchSubject = v
	}
	if v, ok := req.Position.Get(); ok {
		f.Position = v
	}
	if v, ok := req.Stop.Get(); ok {
		f.Stop = v
	}
	return f
}

// ── Spam filter ───────────────────────────────────────────────────────────────

func (h *Handler) SpamFilterGet(ctx context.Context) (*api.SpamFilterSettings, error) {
	s, err := h.spamFilter.GetSpamFilterSettings(ctx)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (h *Handler) SpamFilterPut(ctx context.Context, req *api.SpamFilterSettings) (api.SpamFilterPutRes, error) {
	updated, err := h.spamFilter.UpdateSpamFilterSettings(ctx, *req)
	if errors.Is(err, repository.ErrInvalidScoreHeader) {
		return &api.Error{Error: err.Error()}, nil
	}
	if errors.Is(err, repository.ErrInvalidScoreThreshold) {
		return &api.Error{Error: err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// ── Identities ────────────────────────────────────────────────────────────────

func (h *Handler) IdentitiesGet(ctx context.Context) (*api.IdentitiesGetOK, error) {
	identities, err := h.identities.ListIdentities(ctx)
	if err != nil {
		return nil, err
	}
	return &api.IdentitiesGetOK{Total: len(identities), Items: identities}, nil
}

func (h *Handler) IdentitiesPost(ctx context.Context, req *api.IdentityRequest) (api.IdentitiesPostRes, error) {
	identity := identityFromRequest(req)
	created, err := h.identities.CreateIdentity(ctx, identity)
	if errors.Is(err, repository.ErrInvalidAddress) {
		return &api.IdentitiesPostBadRequest{Error: "invalid address: must be a bare addr-spec (no display name)"}, nil
	}
	if errors.Is(err, repository.ErrConflict) {
		return &api.IdentitiesPostConflict{Error: "address already in use by another identity"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (h *Handler) IdentitiesIDPut(ctx context.Context, req *api.IdentityRequest, params api.IdentitiesIDPutParams) (api.IdentitiesIDPutRes, error) {
	id := int64(params.ID)
	identity := identityFromRequest(req)
	updated, err := h.identities.UpdateIdentity(ctx, id, identity)
	if errors.Is(err, repository.ErrInvalidAddress) {
		return &api.IdentitiesIDPutBadRequest{Error: "invalid address: must be a bare addr-spec (no display name)"}, nil
	}
	if errors.Is(err, repository.ErrNotFound) {
		return &api.IdentitiesIDPutNotFound{Error: "identity not found"}, nil
	}
	if errors.Is(err, repository.ErrConflict) {
		return &api.IdentitiesIDPutConflict{Error: "address already in use by another identity"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (h *Handler) IdentitiesIDDelete(ctx context.Context, params api.IdentitiesIDDeleteParams) (api.IdentitiesIDDeleteRes, error) {
	id := int64(params.ID)
	err := h.identities.DeleteIdentity(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.IdentitiesIDDeleteNotFound{Error: "identity not found"}, nil
	}
	if errors.Is(err, repository.ErrLastIdentity) {
		return &api.IdentitiesIDDeleteBadRequest{Error: "cannot delete the last identity"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.IdentitiesIDDeleteNoContent{}, nil
}

func (h *Handler) IdentitiesReorderPatch(ctx context.Context, req *api.IdentitiesReorderPatchReq) (api.IdentitiesReorderPatchRes, error) {
	ids := make([]int64, len(req.Ids))
	for i, id := range req.Ids {
		ids[i] = int64(id)
	}
	n, err := h.identities.ReorderIdentities(ctx, ids)
	if errors.Is(err, repository.ErrDuplicateID) {
		return &api.Error{Error: "duplicate id"}, nil
	}
	if errors.Is(err, repository.ErrUnknownID) {
		return &api.Error{Error: "unknown id"}, nil
	}
	if errors.Is(err, repository.ErrIncompleteReorder) {
		return &api.Error{Error: "incomplete reorder; all ids must be supplied"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.IdentitiesReorderPatchOK{Updated: n}, nil
}

// identityFromRequest converts an IdentityRequest to an Identity model.
func identityFromRequest(req *api.IdentityRequest) api.Identity {
	identity := api.Identity{
		Name:    strings.TrimSpace(req.Name),
		Address: req.Address,
	}
	if v, ok := req.IsDefault.Get(); ok {
		identity.IsDefault = v
	}
	if v, ok := req.Position.Get(); ok {
		identity.Position = v
	}
	if v, ok := req.Signature.Get(); ok {
		identity.Signature = v
	}
	return identity
}

// ── Contacts ──────────────────────────────────────────────────────────────────

func (h *Handler) ContactsGet(ctx context.Context, params api.ContactsGetParams) (*api.ContactsGetOK, error) {
	limit, offset := parsePagination(params.Limit, params.Offset)

	var q *string
	if v, ok := params.Q.Get(); ok {
		trimmed := strings.TrimSpace(v)
		q = &trimmed
	}

	contacts, total, err := h.contacts.ListContacts(ctx, q, limit, offset)
	if err != nil {
		return nil, err
	}
	return &api.ContactsGetOK{Total: total, Items: contacts}, nil
}

func (h *Handler) ContactsPost(ctx context.Context, req *api.ContactsPostReq) (api.ContactsPostRes, error) {
	name := ""
	if v, ok := req.Name.Get(); ok {
		name = strings.TrimSpace(v)
	}
	if len([]rune(name)) > 200 {
		return &api.ContactsPostBadRequest{Error: "name must not exceed 200 characters"}, nil
	}
	if len(req.Address) > 254 {
		return &api.ContactsPostBadRequest{Error: "address must not exceed 254 characters"}, nil
	}

	contact := api.Contact{
		Address: req.Address,
		Name:    name,
	}
	created, err := h.contacts.CreateContact(ctx, contact)
	if errors.Is(err, repository.ErrInvalidAddress) {
		return &api.ContactsPostBadRequest{Error: "invalid address: must be a bare addr-spec (no display name)"}, nil
	}
	if errors.Is(err, repository.ErrConflict) {
		return &api.ContactsPostConflict{Error: "address already exists"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (h *Handler) ContactsIDPut(ctx context.Context, req *api.ContactsIDPutReq, params api.ContactsIDPutParams) (api.ContactsIDPutRes, error) {
	id := int64(params.ID)

	name := ""
	if v, ok := req.Name.Get(); ok {
		name = strings.TrimSpace(v)
	}
	if len([]rune(name)) > 200 {
		return &api.ContactsIDPutBadRequest{Error: "name must not exceed 200 characters"}, nil
	}
	if len(req.Address) > 254 {
		return &api.ContactsIDPutBadRequest{Error: "address must not exceed 254 characters"}, nil
	}

	contact := api.Contact{
		Address: req.Address,
		Name:    name,
	}
	updated, err := h.contacts.UpdateContact(ctx, id, contact)
	if errors.Is(err, repository.ErrInvalidAddress) {
		return &api.ContactsIDPutBadRequest{Error: "invalid address: must be a bare addr-spec (no display name)"}, nil
	}
	if errors.Is(err, repository.ErrNotFound) {
		return &api.ContactsIDPutNotFound{Error: "contact not found"}, nil
	}
	if errors.Is(err, repository.ErrConflict) {
		return &api.ContactsIDPutConflict{Error: "address already exists"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (h *Handler) ContactsIDDelete(ctx context.Context, params api.ContactsIDDeleteParams) (api.ContactsIDDeleteRes, error) {
	id := int64(params.ID)
	err := h.contacts.DeleteContact(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.Error{Error: "contact not found"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.ContactsIDDeleteNoContent{}, nil
}
