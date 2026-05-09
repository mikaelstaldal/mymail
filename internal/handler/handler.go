package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

type Handler struct {
	api.UnimplementedHandler
	folders      *repository.FolderRepository
	messages     *repository.MessageRepository
	attachments  *repository.AttachmentRepository
	drafts       *repository.DraftRepository
	contacts     *repository.ContactRepository
	identities   *repository.IdentityRepository
	filters      *repository.FilterRepository
	spamFilter   *repository.SpamFilterRepository
	sendmailPath string
}

func New(
	folders *repository.FolderRepository,
	messages *repository.MessageRepository,
	attachments *repository.AttachmentRepository,
	drafts *repository.DraftRepository,
	contacts *repository.ContactRepository,
	identities *repository.IdentityRepository,
	filters *repository.FilterRepository,
	spamFilter *repository.SpamFilterRepository,
	sendmailPath string,
) *Handler {
	return &Handler{
		folders:      folders,
		messages:     messages,
		attachments:  attachments,
		drafts:       drafts,
		contacts:     contacts,
		identities:   identities,
		filters:      filters,
		spamFilter:   spamFilter,
		sendmailPath: sendmailPath,
	}
}

var _ api.Handler = (*Handler)(nil)

func (h *Handler) NewError(_ context.Context, err error) *api.DefaultStatusCode {
	return &api.DefaultStatusCode{StatusCode: 500, Response: api.Error{Error: err.Error()}}
}

// badRequest wraps a 400 message as *DefaultStatusCode so ogen's error path encodes it correctly.
func badRequest(msg string) error {
	return &api.DefaultStatusCode{StatusCode: 400, Response: api.Error{Error: msg}}
}

func (h *Handler) FoldersGet(ctx context.Context) (*api.FoldersGetOK, error) {
	folders, err := h.folders.ListFolders(ctx)
	if err != nil {
		return nil, err
	}
	return &api.FoldersGetOK{Total: len(folders), Items: folders}, nil
}

func (h *Handler) FoldersPost(ctx context.Context, req *api.FoldersPostReq) (api.FoldersPostRes, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, badRequest("name is required")
	}
	if len([]rune(name)) > 200 {
		return nil, badRequest("name must not exceed 200 characters")
	}

	var pos *int
	if v, ok := req.Position.Get(); ok {
		pos = &v
	}

	folder, err := h.folders.CreateFolder(ctx, name, pos)
	if errors.Is(err, repository.ErrConflict) {
		return &api.Error{Error: "folder name already exists"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (h *Handler) FoldersIDPatch(ctx context.Context, req *api.FoldersIDPatchReq, params api.FoldersIDPatchParams) (api.FoldersIDPatchRes, error) {
	id := int64(params.ID)

	if id < 100 && req.Name.IsSet() {
		return &api.FoldersIDPatchBadRequest{Error: "built-in folders cannot be renamed"}, nil
	}

	var name *string
	if v, ok := req.Name.Get(); ok {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return &api.FoldersIDPatchBadRequest{Error: "name must not be empty"}, nil
		}
		if len([]rune(trimmed)) > 200 {
			return &api.FoldersIDPatchBadRequest{Error: "name must not exceed 200 characters"}, nil
		}
		name = &trimmed
	}

	var position *int
	if v, ok := req.Position.Get(); ok {
		position = &v
	}

	folder, err := h.folders.UpdateFolder(ctx, id, name, position)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.FoldersIDPatchNotFound{Error: "folder not found"}, nil
	}
	if errors.Is(err, repository.ErrConflict) {
		return &api.FoldersIDPatchConflict{Error: "folder name already exists"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (h *Handler) FoldersIDDelete(ctx context.Context, params api.FoldersIDDeleteParams) (api.FoldersIDDeleteRes, error) {
	id := int64(params.ID)
	if id < 100 {
		return &api.FoldersIDDeleteBadRequest{Error: "cannot delete built-in folder"}, nil
	}

	err := h.folders.DeleteFolder(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return &api.FoldersIDDeleteNotFound{Error: "folder not found"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.FoldersIDDeleteNoContent{}, nil
}

func (h *Handler) FoldersReorderPatch(ctx context.Context, req *api.FoldersReorderPatchReq) (api.FoldersReorderPatchRes, error) {
	ids := make([]int64, len(req.Ids))
	for i, id := range req.Ids {
		ids[i] = int64(id)
	}

	n, err := h.folders.ReorderFolders(ctx, ids)
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
	return &api.FoldersReorderPatchOK{Updated: n}, nil
}

func (h *Handler) FoldersFolderIDMessagesDelete(ctx context.Context, params api.FoldersFolderIDMessagesDeleteParams) (api.FoldersFolderIDMessagesDeleteRes, error) {
	folderID := int64(params.FolderID)

	if folderID == 3 || folderID == 5 || folderID == 6 { // Drafts, Scheduled, Snoozed
		return &api.FoldersFolderIDMessagesDeleteBadRequest{Error: "cannot delete all messages in this folder"}, nil
	}

	if _, err := h.folders.GetFolderByID(ctx, folderID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &api.FoldersFolderIDMessagesDeleteNotFound{Error: "folder not found"}, nil
		}
		return nil, err
	}

	moved, deleted, err := h.folders.DeleteAllMessagesInFolder(ctx, folderID)
	if err != nil {
		return nil, err
	}
	return &api.FoldersFolderIDMessagesDeleteOK{MovedToTrash: moved, PermanentlyDeleted: deleted}, nil
}

func (h *Handler) FoldersFolderIDMarkAllReadPost(ctx context.Context, params api.FoldersFolderIDMarkAllReadPostParams) (api.FoldersFolderIDMarkAllReadPostRes, error) {
	folderID := int64(params.FolderID)

	if _, err := h.folders.GetFolderByID(ctx, folderID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &api.Error{Error: "folder not found"}, nil
		}
		return nil, err
	}

	n, err := h.folders.MarkAllRead(ctx, folderID)
	if err != nil {
		return nil, err
	}
	return &api.FoldersFolderIDMarkAllReadPostOK{Updated: n}, nil
}
