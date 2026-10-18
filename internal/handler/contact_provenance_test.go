package handler_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContactPromotionAPI(t *testing.T) {
	db := openTestDB(t)
	contacts := repository.NewContactRepository(db)
	require.NoError(t, contacts.UpsertContact(context.Background(), "alice@example.com", "Alice"))
	srv := newServer(t, db)
	res := do(srv, "POST", "/contacts", `{"address":"ALICE@example.com","name":""}`)
	require.Equal(t, 201, res.StatusCode)
	var contact api.Contact
	decodeJSON(t, res, &contact)
	assert.True(t, contact.Explicit)
	assert.Equal(t, "Alice", contact.Name)
	res = do(srv, "POST", "/contacts", `{"address":"alice@example.com"}`)
	assert.Equal(t, 409, res.StatusCode)
	require.NoError(t, contacts.UpsertContact(context.Background(), "bob@example.com", "Bob"))
	items, _, err := contacts.ListContacts(context.Background(), nil, 10, 0)
	require.NoError(t, err)
	found := false
	for _, c := range items {
		if c.Address != "bob@example.com" {
			continue
		}
		found = true
		body, err := json.Marshal(map[string]string{"address": c.Address, "name": c.Name})
		require.NoError(t, err)
		res = do(srv, "PUT", "/contacts/"+strconv.Itoa(c.ID), string(body))
		require.Equal(t, 200, res.StatusCode)
		decodeJSON(t, res, &contact)
		assert.True(t, contact.Explicit)
	}
	require.True(t, found, "the auto-collected contact must be listed and edited")
	require.NoError(t, contacts.UpsertContact(context.Background(), "named@example.com", "Collected"))
	res = do(srv, "POST", "/contacts", `{"address":"named@example.com","name":"Chosen"}`)
	require.Equal(t, 201, res.StatusCode)
	decodeJSON(t, res, &contact)
	assert.Equal(t, "Chosen", contact.Name)
	assert.True(t, contact.Explicit)
}

func TestSendPathsDoNotTrustCollectedContacts(t *testing.T) {
	for _, path := range []string{"send", "attachments", "draft"} {
		t.Run(path, func(t *testing.T) {
			db := openTestDB(t)
			_, err := db.Exec(`INSERT INTO identities(name,address,is_default,position) VALUES('Me','me@example.com',1,0)`)
			require.NoError(t, err)
			sendmail, err := exec.LookPath("true")
			require.NoError(t, err)
			contacts := repository.NewContactRepository(db)
			h := handler.New(repository.NewFolderRepository(db), repository.NewMessageRepository(db),
				repository.NewAttachmentRepository(db), repository.NewDraftRepository(db), contacts,
				repository.NewIdentityRepository(db), repository.NewFilterRepository(db),
				repository.NewSpamFilterRepository(db), sendmail)
			ctx := context.Background()
			saved, err := contacts.CreateContact(ctx, api.Contact{Address: "trusted@example.com"})
			require.NoError(t, err)
			send := api.SendRequest{ToAddr: api.NewOptString("new@example.com"), CcAddr: api.NewOptString("trusted@example.com"), BccAddr: api.NewOptString("bcc@example.com")}
			switch path {
			case "send":
				res, err := h.MessagesSendPost(ctx, &send)
				require.NoError(t, err)
				require.IsType(t, &api.MessagesSendPostCreated{}, res)
			case "attachments":
				res, err := h.MessagesSendWithAttachmentsPost(ctx, &api.MessagesSendWithAttachmentsPostReq{Message: api.NewOptSendRequest(send)})
				require.NoError(t, err)
				require.IsType(t, &api.MessagesSendWithAttachmentsPostCreated{}, res)
			case "draft":
				draft, err := h.DraftsPost(ctx, &api.DraftRequest{ToAddr: send.ToAddr, CcAddr: send.CcAddr, BccAddr: send.BccAddr})
				require.NoError(t, err)
				created, ok := draft.(*api.DraftsPostCreated)
				require.True(t, ok)
				res, err := h.DraftsIDSendPost(ctx, api.DraftsIDSendPostParams{ID: created.ID})
				require.NoError(t, err)
				require.IsType(t, &api.DraftsIDSendPostCreated{}, res)
			}
			items, total, err := contacts.ListContacts(ctx, nil, 10, 0)
			require.NoError(t, err)
			require.Equal(t, 3, total)
			for _, c := range items {
				assert.Equal(t, c.ID == saved.ID, c.Explicit, c.Address)
			}
		})
	}
}
