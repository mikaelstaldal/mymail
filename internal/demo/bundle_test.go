package demo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The browser demo is only as good as this document: it is the whole of its
// initial state, and the service worker has no way to fall back on the real
// server if something it needs is missing. So the assertions here are about the
// shape the store depends on, not about the exact prose of the sample messages.

func TestBuildSeedProducesTheDemoDataset(t *testing.T) {
	seed, err := BuildSeed(context.Background())
	require.NoError(t, err, "BuildSeed")

	// The seven built-in folders must all be there: the sidebar is built from
	// this list, not from anything hard-coded in the frontend.
	require.Len(t, seed.Folders, 7, "built-in folders")
	slugs := make([]string, len(seed.Folders))
	for i, f := range seed.Folders {
		slugs[i] = f.Slug
	}
	assert.Equal(t,
		[]string{"inbox", "sent", "drafts", "trash", "scheduled", "snoozed", "junk"},
		slugs, "folder ids 1–7 in order")

	require.Len(t, seed.Identities, 1, "demo identity")
	assert.True(t, seed.Identities[0].IsDefault, "the demo identity is the default")
	assert.Equal(t, "demo@example.com", seed.Identities[0].Address)

	assert.NotEmpty(t, seed.Contacts, "seeded contacts")
	assert.NotEmpty(t, seed.SpamFilter.ScoreHeader, "spam filter defaults")

	byFolder := map[int64]int{}
	for _, m := range seed.Messages {
		byFolder[m.FolderID]++
	}
	assert.Positive(t, byFolder[1], "inbox messages")
	assert.Positive(t, byFolder[2], "sent messages")
	assert.Positive(t, byFolder[3], "a draft")
	assert.Positive(t, byFolder[4], "a trashed message")
	assert.Positive(t, byFolder[7], "a junk message")
}

func TestBuildSeedCarriesRawAndAttachments(t *testing.T) {
	seed, err := BuildSeed(context.Background())
	require.NoError(t, err, "BuildSeed")

	require.NotEmpty(t, seed.Attachments, "the quote attachment")
	att := seed.Attachments[0]
	data, err := base64.StdEncoding.DecodeString(att.Data)
	require.NoError(t, err, "attachment data is base64")
	assert.Len(t, data, att.Size, "size matches the decoded bytes")

	byID := map[int64]SeedMessage{}
	for _, m := range seed.Messages {
		byID[m.ID] = m
	}
	carrier, ok := byID[att.MessageID]
	require.True(t, ok, "the attachment belongs to a seeded message")
	assert.True(t, carrier.HasAttachments, "has_attachments is set by the DB trigger")

	// Raw is what /messages/{id}/headers and the .eml download serve, and a
	// draft is exactly the row that must not have one.
	for _, m := range seed.Messages {
		if m.FolderID == 3 {
			assert.Nil(t, m.Raw, "draft %d has no raw source", m.ID)
			continue
		}
		require.NotNil(t, m.Raw, "message %d has a raw source", m.ID)
		raw, err := base64.StdEncoding.DecodeString(*m.Raw)
		require.NoError(t, err, "raw is base64")
		assert.True(t, strings.HasPrefix(string(raw), "Message-ID: <"),
			"raw starts with the header block")
	}
}

func TestBuildSeedThreadsResolve(t *testing.T) {
	seed, err := BuildSeed(context.Background())
	require.NoError(t, err, "BuildSeed")

	known := map[string]bool{}
	for _, m := range seed.Messages {
		if m.MessageID != nil {
			known[*m.MessageID] = true
		}
	}

	// The thread algorithm walks in_reply_to and references by Message-ID; a
	// dangling one would silently collapse the seeded thread into singletons.
	replies := 0
	for _, m := range seed.Messages {
		if m.InReplyTo == nil {
			continue
		}
		replies++
		assert.True(t, known[*m.InReplyTo],
			"in_reply_to %q of message %d names a seeded message", *m.InReplyTo, m.ID)
		require.NotNil(t, m.References, "a reply carries references")
		for ref := range strings.SplitSeq(*m.References, "\n") {
			assert.True(t, known[ref], "reference %q names a seeded message", ref)
		}
	}
	assert.GreaterOrEqual(t, replies, 2, "the seed contains a multi-message thread")
}

func TestBuildSeedJSONRoundTrips(t *testing.T) {
	data, err := BuildSeedJSON(context.Background())
	require.NoError(t, err, "BuildSeedJSON")

	var decoded Seed
	require.NoError(t, json.Unmarshal(data, &decoded), "unmarshal")
	assert.Len(t, decoded.Folders, 7)
	assert.NotEmpty(t, decoded.Messages)

	// Every array must serialise as [] rather than null: the worker indexes
	// straight into them without a nullish guard.
	var generic map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &generic))
	for _, key := range []string{"folders", "identities", "contacts", "filters", "messages", "attachments"} {
		raw, ok := generic[key]
		require.True(t, ok, "seed has a %q key", key)
		assert.True(t, strings.HasPrefix(string(raw), "["), "%s is an array, not null", key)
	}
}
