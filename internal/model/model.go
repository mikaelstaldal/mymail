// Package model defines data types for the database layer.
//
// Type inventory:
//
// ogen aliases — use the generated types from internal/api directly; the repository
// scans SQLite rows into these without an intermediate struct:
//
//	api.Folder            — all fields non-nullable; unread_count computed in SQL
//	api.Identity          — is_default scanned from INTEGER 0/1
//	api.Contact           — all fields non-nullable
//	api.SpamFilterSettings — enabled scanned from INTEGER 0/1
//
// ogen compatible with a thin scanning helper (repository scans via temporaries):
//
//	api.Filter            — folder_id scanned as sql.NullInt64, action as string,
//	                        then converted to api.OptNilInt / api.FilterAction
//
// DB-native types (defined here):
//
//	DBMessage    — full messages table row; ToOASMessage / ToOASMessageSummary convert
//	DBAttachment — full attachments row including data BLOB; ToOASAttachmentMeta converts
//	ParsedMessage — LDA / import pipeline intermediate; never serialised to JSON
package model

import (
	"database/sql"
	"strings"
	"time"

	oas "github.com/mikaelstaldal/mymail/internal/api"
)

// DBMessage is the full row from the messages table. Nullable columns use sql.Null*.
// Raw is nil for drafts (raw IS NULL in the DB).
type DBMessage struct {
	ID                int
	FolderID          int
	IdentityID        sql.NullInt64
	MessageID         sql.NullString
	InReplyTo         sql.NullString
	References        sql.NullString // newline-separated message-ids without angle brackets
	FromAddr          string
	ToAddr            string
	CcAddr            string
	BccAddr           string
	ReplyToAddr       string
	Subject           string
	Date              time.Time
	BodyText          string
	BodyHTML          string
	Raw               []byte // nil for drafts
	Read              bool
	Flagged           bool
	HasAttachments    bool
	HasExternalImages bool
	SendAt            sql.NullTime
	SnoozedUntil      sql.NullTime
	SnoozeFolder      sql.NullInt64 // exposed as snooze_folder_id in the API
	SendError         sql.NullString
	SendFailureCount  int
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ToOASMessageSummary converts a DB row to the API summary type.
func (m *DBMessage) ToOASMessageSummary() *oas.MessageSummary {
	return &oas.MessageSummary{
		ID:             m.ID,
		FolderID:       m.FolderID,
		MessageID:      nullStringToNilString(m.MessageID),
		FromAddr:       m.FromAddr,
		ToAddr:         m.ToAddr,
		Subject:        m.Subject,
		Date:           m.Date,
		Read:           m.Read,
		Flagged:        m.Flagged,
		HasAttachments: m.HasAttachments,
		SendFailed:     m.SendFailureCount > 0,
		CreatedAt:      m.CreatedAt,
	}
}

// ToOASMessage converts a DB row to the API detail type.
// The caller supplies the pre-fetched attachment metadata.
func (m *DBMessage) ToOASMessage(attachments []oas.AttachmentMeta) *oas.MessageDetail {
	if attachments == nil {
		attachments = []oas.AttachmentMeta{}
	}
	return &oas.MessageDetail{
		ID:                m.ID,
		FolderID:          m.FolderID,
		MessageID:         nullStringToNilString(m.MessageID),
		InReplyTo:         nullStringToNilString(m.InReplyTo),
		References:        splitReferences(m.References),
		FromAddr:          m.FromAddr,
		ToAddr:            m.ToAddr,
		CcAddr:            m.CcAddr,
		BccAddr:           m.BccAddr,
		ReplyToAddr:       m.ReplyToAddr,
		Subject:           m.Subject,
		Date:              m.Date,
		BodyText:          m.BodyText,
		BodyHTML:          m.BodyHTML,
		HasExternalImages: m.HasExternalImages,
		SendFailed:        m.SendFailureCount > 0,
		Read:              m.Read,
		Flagged:           m.Flagged,
		SendAt:            nullTimeToNilDateTime(m.SendAt),
		SendError:         nullStringToNilString(m.SendError),
		SnoozedUntil:      nullTimeToNilDateTime(m.SnoozedUntil),
		SnoozeFolderID:    nullInt64ToNilInt(m.SnoozeFolder),
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
		Attachments:       attachments,
	}
}

// DBAttachment is the full row from the attachments table, including the data BLOB.
// When building attachments inside ParsedMessage before DB insertion, ID and MessageID
// are zero-valued.
type DBAttachment struct {
	ID          int
	MessageID   int
	Filename    string
	ContentType string
	Size        int
	Data        []byte
}

// ToOASAttachmentMeta converts a DB row to the API metadata type (omitting Data).
func (a *DBAttachment) ToOASAttachmentMeta() *oas.AttachmentMeta {
	return &oas.AttachmentMeta{
		ID:          a.ID,
		Filename:    a.Filename,
		ContentType: a.ContentType,
		Size:        a.Size,
	}
}

// ParsedMessage is the intermediate representation produced by the LDA and import
// pipeline. It is never serialised to JSON. Attachments carry zero ID and MessageID
// values until the database insertion assigns them.
type ParsedMessage struct {
	FromAddr          string
	ToAddr            string
	CcAddr            string
	BccAddr           string
	ReplyToAddr       string
	Subject           string
	Date              *time.Time
	MessageID         *string
	InReplyTo         *string
	References        []string
	BodyText          string
	BodyHTML          string
	Attachments       []DBAttachment
	HasExternalImages bool
}

// nullStringToNilString converts sql.NullString to oas.NilString.
func nullStringToNilString(ns sql.NullString) oas.NilString {
	if ns.Valid {
		return oas.NewNilString(ns.String)
	}
	var n oas.NilString
	n.SetToNull()
	return n
}

// nullTimeToNilDateTime converts sql.NullTime to oas.NilDateTime.
func nullTimeToNilDateTime(nt sql.NullTime) oas.NilDateTime {
	if nt.Valid {
		var n oas.NilDateTime
		n.SetTo(nt.Time)
		return n
	}
	var n oas.NilDateTime
	n.SetToNull()
	return n
}

// nullInt64ToNilInt converts sql.NullInt64 to oas.NilInt.
func nullInt64ToNilInt(ni sql.NullInt64) oas.NilInt {
	if ni.Valid {
		return oas.NewNilInt(int(ni.Int64))
	}
	var n oas.NilInt
	n.SetToNull()
	return n
}

// splitReferences splits the newline-separated references column and re-adds angle
// brackets to each entry, matching the API serialisation contract.
func splitReferences(ns sql.NullString) []string {
	if !ns.Valid || ns.String == "" {
		return []string{}
	}
	parts := strings.Split(ns.String, "\n")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = "<" + p + ">"
	}
	return out
}
