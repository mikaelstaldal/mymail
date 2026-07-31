package demo

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/mikaelstaldal/mymail/internal/repository"
)

// SeedFileName is the file the browser demo fetches its initial content from.
// It sits next to index.html, so the service worker resolves it relative to its
// own scope and the same file works at any deployment path.
const SeedFileName = "demo-data.json"

// Seed is the JSON document the demo service worker seeds its browser-side
// store from. It is the exact same content the -demo flag writes into SQLite:
// rather than duplicating it in JavaScript, BuildSeed runs the real seeding
// pipeline against a throwaway in-memory database and dumps the resulting rows.
//
// Field names are the camelCase of the database columns, not the snake_case of
// the REST API — this is the store's shape, and web/ts/demo/api.ts projects it
// into API responses exactly as internal/model does on the server.
type Seed struct {
	Folders     []SeedFolder     `json:"folders"`
	Identities  []SeedIdentity   `json:"identities"`
	Contacts    []SeedContact    `json:"contacts"`
	Filters     []SeedFilter     `json:"filters"`
	SpamFilter  SeedSpamFilter   `json:"spamFilter"`
	Messages    []SeedMessage    `json:"messages"`
	Attachments []SeedAttachment `json:"attachments"`
}

type SeedFolder struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Position  int    `json:"position"`
	CreatedAt string `json:"createdAt"`
}

type SeedIdentity struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	IsDefault bool   `json:"isDefault"`
	Position  int    `json:"position"`
	Signature string `json:"signature"`
}

type SeedContact struct {
	ID        int64  `json:"id"`
	Address   string `json:"address"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type SeedFilter struct {
	ID           int64  `json:"id"`
	Position     int    `json:"position"`
	Name         string `json:"name"`
	MatchFrom    string `json:"matchFrom"`
	MatchTo      string `json:"matchTo"`
	MatchSubject string `json:"matchSubject"`
	Action       string `json:"action"`
	FolderID     *int64 `json:"folderId"`
	Stop         bool   `json:"stop"`
}

type SeedSpamFilter struct {
	Enabled        bool    `json:"enabled"`
	ScoreHeader    string  `json:"scoreHeader"`
	ScoreThreshold float64 `json:"scoreThreshold"`
}

// SeedMessage is one messages row. Nullable columns are pointers so the store
// can tell "absent" from "empty string", which the folder and threading rules
// both depend on.
type SeedMessage struct {
	ID          int64   `json:"id"`
	FolderID    int64   `json:"folderId"`
	IdentityID  *int64  `json:"identityId"`
	MessageID   *string `json:"messageId"`
	InReplyTo   *string `json:"inReplyTo"`
	References  *string `json:"references"`
	FromAddr    string  `json:"fromAddr"`
	ToAddr      string  `json:"toAddr"`
	CcAddr      string  `json:"ccAddr"`
	BccAddr     string  `json:"bccAddr"`
	ReplyToAddr string  `json:"replyToAddr"`
	Subject     string  `json:"subject"`
	Date        string  `json:"date"`
	BodyText    string  `json:"bodyText"`
	BodyHTML    string  `json:"bodyHtml"`
	// Raw is the RFC 5322 source, base64-encoded, or null for a draft.
	Raw               *string `json:"raw"`
	Read              bool    `json:"read"`
	Flagged           bool    `json:"flagged"`
	HasAttachments    bool    `json:"hasAttachments"`
	HasExternalImages bool    `json:"hasExternalImages"`
	SendAt            *string `json:"sendAt"`
	SnoozedUntil      *string `json:"snoozedUntil"`
	SnoozeFolder      *int64  `json:"snoozeFolder"`
	SendError         *string `json:"sendError"`
	SendFailureCount  int     `json:"sendFailureCount"`
	CreatedAt         string  `json:"createdAt"`
	UpdatedAt         string  `json:"updatedAt"`
}

type SeedAttachment struct {
	ID          int64  `json:"id"`
	MessageID   int64  `json:"messageId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
	// Data is the attachment content, base64-encoded (standard alphabet, padded).
	Data string `json:"data"`
}

// BuildSeed produces the demo seed document. It opens a private in-memory
// SQLite database, seeds the built-in folders and runs the normal Run seeding,
// then reads the stored rows back out. Nothing touches the filesystem and the
// database is discarded on return.
//
// The result is deliberately *not* reproducible: buildContent dates every
// message relative to time.Now and picks its extras at random, and Run mints
// fresh Message-IDs. Two builds of the same commit therefore differ. That is
// the point — a bundle pinned to build-time dates would show a mailbox that
// visibly rots ("received 8 months ago") the longer it stays published, and the
// seed is sample content rather than a build input, so nothing downstream
// depends on its bytes. Everything the build genuinely has to pin — the vendor
// bundles, the jsdom tarball — is committed instead.
func BuildSeed(ctx context.Context) (*Seed, error) {
	db, err := openSeedDB()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	if err := Run(ctx, db, io.Discard); err != nil {
		return nil, err
	}

	seed := &Seed{}
	if seed.Folders, err = exportFolders(ctx, db); err != nil {
		return nil, err
	}
	if seed.Identities, err = exportIdentities(ctx, db); err != nil {
		return nil, err
	}
	if seed.Contacts, err = exportContacts(ctx, db); err != nil {
		return nil, err
	}
	if seed.Filters, err = exportFilters(ctx, db); err != nil {
		return nil, err
	}
	if seed.SpamFilter, err = exportSpamFilter(ctx, db); err != nil {
		return nil, err
	}
	if seed.Messages, err = exportMessages(ctx, db); err != nil {
		return nil, err
	}
	if seed.Attachments, err = exportAttachments(ctx, db); err != nil {
		return nil, err
	}
	return seed, nil
}

// BuildSeedJSON is BuildSeed marshalled to the bytes served as demo-data.json.
func BuildSeedJSON(ctx context.Context) ([]byte, error) {
	seed, err := BuildSeed(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(seed)
}

// seedDBCounter makes each in-memory database name unique, so two concurrent
// BuildSeed calls (e.g. parallel tests) never share the `cache=shared` store.
var seedDBCounter atomic.Uint64

// openSeedDB opens a private, empty, fully-migrated in-memory database with the
// built-in folders already in place. The connection pool is pinned to one
// connection: a `cache=shared` in-memory database lives only as long as at
// least one connection is open.
func openSeedDB() (*sql.DB, error) {
	dsn := fmt.Sprintf("file:demoseed%d?mode=memory&cache=shared", seedDBCounter.Add(1))

	db, err := repository.OpenDB(dsn, 0)
	if err != nil {
		return nil, fmt.Errorf("open seed database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := repository.SeedBuiltinFolders(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("seed built-in folders: %w", err)
	}
	return db, nil
}

func exportFolders(ctx context.Context, db *sql.DB) ([]SeedFolder, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, slug, position, created_at FROM folders ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]SeedFolder, 0)
	for rows.Next() {
		var f SeedFolder
		if err := rows.Scan(&f.ID, &f.Name, &f.Slug, &f.Position, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func exportIdentities(ctx context.Context, db *sql.DB) ([]SeedIdentity, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, address, is_default, position, signature FROM identities ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]SeedIdentity, 0)
	for rows.Next() {
		var (
			i         SeedIdentity
			isDefault int
		)
		if err := rows.Scan(&i.ID, &i.Name, &i.Address, &isDefault, &i.Position, &i.Signature); err != nil {
			return nil, err
		}
		i.IsDefault = isDefault != 0
		out = append(out, i)
	}
	return out, rows.Err()
}

func exportContacts(ctx context.Context, db *sql.DB) ([]SeedContact, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, address, name, created_at, updated_at FROM contacts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]SeedContact, 0)
	for rows.Next() {
		var c SeedContact
		if err := rows.Scan(&c.ID, &c.Address, &c.Name, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func exportFilters(ctx context.Context, db *sql.DB) ([]SeedFilter, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, position, name, match_from, match_to, match_subject, action, folder_id, stop
		 FROM filters ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]SeedFilter, 0)
	for rows.Next() {
		var (
			f        SeedFilter
			folderID sql.NullInt64
			stop     int
		)
		if err := rows.Scan(&f.ID, &f.Position, &f.Name, &f.MatchFrom, &f.MatchTo,
			&f.MatchSubject, &f.Action, &folderID, &stop); err != nil {
			return nil, err
		}
		f.FolderID = nullInt64(folderID)
		f.Stop = stop != 0
		out = append(out, f)
	}
	return out, rows.Err()
}

func exportSpamFilter(ctx context.Context, db *sql.DB) (SeedSpamFilter, error) {
	var (
		s       SeedSpamFilter
		enabled int
	)
	err := db.QueryRowContext(ctx,
		`SELECT enabled, score_header, score_threshold FROM spam_filter_settings WHERE id = 1`,
	).Scan(&enabled, &s.ScoreHeader, &s.ScoreThreshold)
	if err != nil {
		return SeedSpamFilter{}, err
	}
	s.Enabled = enabled != 0
	return s, nil
}

func exportMessages(ctx context.Context, db *sql.DB) ([]SeedMessage, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, folder_id, identity_id, message_id, in_reply_to, "references",
		       from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject,
		       date, body_text, body_html, raw, read, flagged,
		       has_attachments, has_external_images,
		       send_at, snoozed_until, snooze_folder, send_error, send_failure_count,
		       created_at, updated_at
		FROM messages ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]SeedMessage, 0)
	for rows.Next() {
		var (
			m            SeedMessage
			identityID   sql.NullInt64
			messageID    sql.NullString
			inReplyTo    sql.NullString
			references   sql.NullString
			raw          []byte
			read         int
			flagged      int
			hasAttach    int
			hasExtImages int
			sendAt       sql.NullString
			snoozedUntil sql.NullString
			snoozeFolder sql.NullInt64
			sendError    sql.NullString
		)
		if err := rows.Scan(
			&m.ID, &m.FolderID, &identityID, &messageID, &inReplyTo, &references,
			&m.FromAddr, &m.ToAddr, &m.CcAddr, &m.BccAddr, &m.ReplyToAddr, &m.Subject,
			&m.Date, &m.BodyText, &m.BodyHTML, &raw, &read, &flagged,
			&hasAttach, &hasExtImages,
			&sendAt, &snoozedUntil, &snoozeFolder, &sendError, &m.SendFailureCount,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		m.IdentityID = nullInt64(identityID)
		m.MessageID = nullString(messageID)
		m.InReplyTo = nullString(inReplyTo)
		m.References = nullString(references)
		if raw != nil {
			encoded := base64.StdEncoding.EncodeToString(raw)
			m.Raw = &encoded
		}
		m.Read = read != 0
		m.Flagged = flagged != 0
		m.HasAttachments = hasAttach != 0
		m.HasExternalImages = hasExtImages != 0
		m.SendAt = nullString(sendAt)
		m.SnoozedUntil = nullString(snoozedUntil)
		m.SnoozeFolder = nullInt64(snoozeFolder)
		m.SendError = nullString(sendError)
		out = append(out, m)
	}
	return out, rows.Err()
}

func exportAttachments(ctx context.Context, db *sql.DB) ([]SeedAttachment, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, message_id, filename, content_type, size, data FROM attachments ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]SeedAttachment, 0)
	for rows.Next() {
		var (
			a    SeedAttachment
			data []byte
		)
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.Size, &data); err != nil {
			return nil, err
		}
		a.Data = base64.StdEncoding.EncodeToString(data)
		out = append(out, a)
	}
	return out, rows.Err()
}

func nullString(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

func nullInt64(ni sql.NullInt64) *int64 {
	if !ni.Valid {
		return nil
	}
	v := ni.Int64
	return &v
}
