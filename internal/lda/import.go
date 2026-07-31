package lda

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/emersion/go-maildir"
	"github.com/emersion/go-mbox"

	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

// builtinSlugs maps the built-in folder slugs (case-sensitive) to their IDs.
var builtinSlugs = map[string]int64{
	"inbox":  1,
	"sent":   2,
	"drafts": 3,
	"trash":  4,
	"junk":   7,
}

// fromLayouts are tried in order when parsing mbox From-separator timestamps.
var fromLayouts = []string{
	"Mon Jan _2 15:04:05 2006",
	"Mon Jan _2 15:04:05 MST 2006",
	"Mon Jan _2 15:04:05 -0700 2006",
}

// batchEntry is one message ready to be committed to the database.
type batchEntry struct {
	pm      *model.ParsedMessage
	raw     []byte
	date    string // RFC3339 UTC
	read    int
	flagged int
}

// AcquireImportLock creates/opens <dataDir>/mymail.lock and acquires an
// exclusive non-blocking flock. Returns the open *os.File; caller must close.
// If the lock is held, the error names the holding PID.
func AcquireImportLock(dataDir string) (*os.File, error) {
	lockPath := filepath.Join(dataDir, "mymail.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		buf := make([]byte, 32)
		n, _ := f.Read(buf)
		f.Close()
		pid := strings.TrimSpace(string(buf[:n]))
		if pid == "" {
			pid = "unknown"
		}
		return nil, fmt.Errorf("lock held by PID %s: %w", pid, err)
	}
	_ = f.Truncate(0)
	_, _ = fmt.Fprintf(f, "%d", os.Getpid())
	_ = f.Sync()
	return f, nil
}

// importMapping is one parsed <folder>:<format>:<path> triplet.
type importMapping struct {
	folder string
	format string
	path   string
}

// parseMappings splits "folder:format:path" arguments. The path may contain colons.
func parseMappings(args []string) ([]importMapping, error) {
	out := make([]importMapping, 0, len(args))
	for _, arg := range args {
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return nil, fmt.Errorf("invalid mapping %q: expected <folder>:<format>:<path>", arg)
		}
		if parts[1] != "mbox" && parts[1] != "maildir" && parts[1] != "mbx" {
			return nil, fmt.Errorf("invalid format %q in %q: must be mbox, maildir, or mbx", parts[1], arg)
		}
		out = append(out, importMapping{folder: parts[0], format: parts[1], path: parts[2]})
	}
	return out, nil
}

// resolveFolder maps a folder name to its DB id, creating a user folder if needed.
// nameCache prevents creating the same user folder twice across multiple mappings.
func resolveFolder(ctx context.Context, db *sql.DB, name string, nameCache map[string]int64) (int64, error) {
	if id, ok := builtinSlugs[name]; ok {
		return id, nil
	}
	lower := strings.ToLower(name)
	if lower == "scheduled" || lower == "snoozed" {
		return 0, fmt.Errorf("folder %q cannot be used as an import target", name)
	}
	if id, ok := nameCache[lower]; ok {
		return id, nil
	}
	// unicode_lower (see repository/sqlfunc.go), matched against the same folded
	// name the cache is keyed on. SQLite's built-in lower() folds ASCII only, so
	// an existing "Räkningar" would not be found for an import into "räkningar"
	// and a second folder differing from it only in case would be created.
	var id int64
	err := db.QueryRowContext(ctx, `SELECT id FROM folders WHERE unicode_lower(name) = ?`, lower).Scan(&id)
	if err == nil {
		nameCache[lower] = id
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("lookup folder %q: %w", name, err)
	}
	f, err := repository.NewFolderRepository(db).CreateFolder(ctx, name, nil)
	if err != nil {
		return 0, fmt.Errorf("create folder %q: %w", name, err)
	}
	nameCache[lower] = int64(f.ID)
	return int64(f.ID), nil
}

// RunImport is the entry point for -import mode.
// mappingArgs are positional arguments of the form "folder:format:path".
func RunImport(db *sql.DB, mappingArgs []string) int {
	if len(mappingArgs) == 0 {
		fmt.Fprintln(os.Stderr, "import: no mappings specified")
		return 1
	}
	mappings, err := parseMappings(mappingArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "import: %v\n", err)
		return 1
	}

	ctx := context.Background()
	nameCache := make(map[string]int64)

	type resolved struct {
		folderName string
		folderID   int64
		format     string
		path       string
	}
	rs := make([]resolved, 0, len(mappings))
	for _, m := range mappings {
		fid, err := resolveFolder(ctx, db, m.folder, nameCache)
		if err != nil {
			fmt.Fprintf(os.Stderr, "import: %v\n", err)
			return 1
		}
		rs = append(rs, resolved{m.folder, fid, m.format, m.path})
	}

	totalImported, totalSkipped := 0, 0
	exitCode := 0
	for _, r := range rs {
		var imp, skip int
		var e error
		switch r.format {
		case "mbox":
			imp, skip, e = importMbox(r.path, r.folderID, db)
		case "maildir":
			imp, skip, e = importMaildir(r.path, r.folderID, db)
		case "mbx":
			imp, skip, e = importMbx(r.path, r.folderID, db)
		}
		fmt.Printf("%s: %d imported, %d skipped\n", r.folderName, imp, skip)
		totalImported += imp
		totalSkipped += skip
		if e != nil {
			log.Printf("import: %s (%s %s): %v", r.folderName, r.format, r.path, e)
			exitCode = 1
		}
	}
	fmt.Printf("Total: %d imported, %d skipped\n", totalImported, totalSkipped)
	return exitCode
}

// commitBatch inserts a batch of messages and upserts contacts. Returns how
// many rows were actually inserted (race-condition duplicates count as 0).
func commitBatch(ctx context.Context, db *sql.DB, batch []batchEntry, folderID int64, contactRepo *repository.ContactRepository) (inserted int, err error) {
	if len(batch) == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	now := time.Now().UTC().Format(time.RFC3339)
	type insertedEntry struct {
		fromAddr string
	}
	var insertedRows []insertedEntry

	for _, e := range batch {
		pm := e.pm
		var inReplyTo, refs any
		if pm.InReplyTo != nil {
			inReplyTo = *pm.InReplyTo
		}
		if len(pm.References) > 0 {
			refs = strings.Join(pm.References, "\n")
		}
		res, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO messages (
				folder_id, message_id, in_reply_to, "references",
				from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject,
				date, body_text, body_html, raw, read, flagged,
				has_attachments, has_external_images,
				send_failure_count, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
			folderID, pm.MessageID, inReplyTo, refs,
			pm.FromAddr, pm.ToAddr, pm.CcAddr, pm.BccAddr, pm.ReplyToAddr, pm.Subject,
			e.date, pm.BodyText, pm.BodyHTML, e.raw, e.read, e.flagged,
			boolInt(len(pm.Attachments) > 0), boolInt(pm.HasExternalImages),
			now, now,
		)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue
		}
		msgID, _ := res.LastInsertId()
		for _, ref := range pm.References {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO message_references (message_id, ref_msg_id) VALUES (?, ?)`,
				msgID, ref,
			); err != nil {
				return 0, err
			}
		}
		for _, att := range pm.Attachments {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO attachments (message_id, filename, content_type, size, data) VALUES (?, ?, ?, ?, ?)`,
				msgID, att.Filename, att.ContentType, att.Size, att.Data,
			); err != nil {
				return 0, err
			}
		}
		insertedRows = append(insertedRows, insertedEntry{pm.FromAddr})
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	for _, row := range insertedRows {
		if row.fromAddr != "" {
			addr, name := parseFromAddr(row.fromAddr)
			if addr != "" {
				if err := contactRepo.UpsertContact(ctx, addr, name); err != nil {
					log.Printf("import: upsert contact: %v", err)
				}
			}
		}
	}
	return len(insertedRows), nil
}

// importMaildir imports all messages from a Maildir directory into folderID.
func importMaildir(dir string, folderID int64, db *sql.DB) (imported, skipped int, _ error) {
	type msgMeta struct {
		key      string
		flags    []maildir.Flag
		fromNew  bool
		filename string
	}

	var allMsgs []msgMeta

	// Collect files from new/ (no flags — unread, unflagged).
	newPath := filepath.Join(dir, "new")
	entries, err := os.ReadDir(newPath)
	if err != nil && !os.IsNotExist(err) {
		return 0, 0, fmt.Errorf("read %s/new: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		key, _, _ := strings.Cut(e.Name(), ":")
		allMsgs = append(allMsgs, msgMeta{
			key:      key,
			fromNew:  true,
			filename: filepath.Join(newPath, e.Name()),
		})
	}

	// Collect files from cur/ (may have flags).
	d := maildir.Dir(dir)
	curMsgs, curErr := d.Messages()
	for _, m := range curMsgs {
		allMsgs = append(allMsgs, msgMeta{
			key:      m.Key(),
			flags:    m.Flags(),
			fromNew:  false,
			filename: m.Filename(),
		})
	}
	if curErr != nil {
		log.Printf("import maildir %s: partial read of cur/: %v", dir, curErr)
	}

	// Sort lexicographically by key ascending.
	sort.Slice(allMsgs, func(i, j int) bool { return allMsgs[i].key < allMsgs[j].key })

	ctx := context.Background()
	contactRepo := repository.NewContactRepository(db)

	const batchSize = 50
	var batch []batchEntry
	var flushErr error

	flush := func() {
		n, err := commitBatch(ctx, db, batch, folderID, contactRepo)
		if err != nil {
			log.Printf("import maildir %s: commit batch: %v", dir, err)
			skipped += len(batch)
			if flushErr == nil {
				flushErr = err
			}
		} else {
			imported += n
			skipped += len(batch) - n
		}
		for i := range batch {
			batch[i] = batchEntry{}
		}
		batch = batch[:0]
	}

	for _, msg := range allMsgs {
		fi, statErr := os.Stat(msg.filename)
		raw, readErr := os.ReadFile(msg.filename)
		if readErr != nil {
			log.Printf("import maildir: read %s: %v", msg.filename, readErr)
			skipped++
			continue
		}
		pm, parseErr := ParseMessage(raw)
		if parseErr != nil {
			log.Printf("import maildir: parse %s: %v", msg.filename, parseErr)
			skipped++
			continue
		}

		// Date: header preferred, then mtime.
		var date time.Time
		if pm.Date != nil {
			date = pm.Date.UTC()
		} else if statErr == nil {
			date = fi.ModTime().UTC()
		} else {
			log.Printf("import maildir: no date or mtime for %s, skipping", msg.filename)
			skipped++
			continue
		}

		// Duplicate check.
		if pm.MessageID != nil {
			var exists bool
			if err := db.QueryRowContext(ctx,
				`SELECT EXISTS(SELECT 1 FROM messages WHERE message_id = ?)`, *pm.MessageID,
			).Scan(&exists); err != nil {
				log.Printf("import maildir: duplicate check %s: %v", msg.filename, err)
			} else if exists {
				skipped++
				continue
			}
		}

		readFlag, flaggedFlag := 0, 0
		if !msg.fromNew {
			for _, f := range msg.flags {
				switch f {
				case maildir.FlagSeen:
					readFlag = 1
				case maildir.FlagFlagged:
					flaggedFlag = 1
				}
			}
		}

		batch = append(batch, batchEntry{pm: pm, raw: raw, date: date.Format(time.RFC3339), read: readFlag, flagged: flaggedFlag})
		if len(batch) >= batchSize {
			flush()
		}
	}
	flush()
	return imported, skipped, flushErr
}

// importMbox imports all messages from an mbox file into folderID.
// Two passes: (1) scan for From-separator timestamps with a single bufio scan,
// (2) stream-process messages with batch commits. Separator/message count
// mismatch (mboxo unescaped body lines) is detected during pass 2.
func importMbox(path string, folderID int64, db *sql.DB) (imported, skipped int, _ error) {
	var fileMtime *time.Time
	if fi, err := os.Stat(path); err == nil {
		t := fi.ModTime().UTC()
		fileMtime = &t
	}

	// Pass 1: collect From-separator timestamps (and implicit separator count).
	fromTimestamps, err := scanMboxSeparators(path)
	if err != nil {
		return 0, 0, fmt.Errorf("scan %s: %w", path, err)
	}
	separatorCount := len(fromTimestamps)

	// Pass 2: stream-process messages.
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	ctx := context.Background()
	contactRepo := repository.NewContactRepository(db)

	const batchSize = 50
	var batch []batchEntry
	var flushErr error
	// useMtime is set to true when the processing pass finds more messages than
	// separators (mboxo unescaped body-From mismatch), disabling per-message timestamps.
	useMtime := false

	flush := func() {
		n, err := commitBatch(ctx, db, batch, folderID, contactRepo)
		if err != nil {
			log.Printf("import mbox %s: commit batch: %v", path, err)
			skipped += len(batch)
			if flushErr == nil {
				flushErr = err
			}
		} else {
			imported += n
			skipped += len(batch) - n
		}
		for i := range batch {
			batch[i] = batchEntry{}
		}
		batch = batch[:0]
	}

	msgIdx := 0
	reader := mbox.NewReader(f)
	for {
		msgReader, nextErr := reader.NextMessage()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			log.Printf("import mbox %s[%d]: %v", path, msgIdx, nextErr)
			skipped++
			msgIdx++
			continue
		}
		raw, readErr := io.ReadAll(msgReader)
		if readErr != nil {
			log.Printf("import mbox %s[%d]: read: %v", path, msgIdx, readErr)
			skipped++
			msgIdx++
			continue
		}
		pm, parseErr := ParseMessage(raw)
		if parseErr != nil {
			log.Printf("import mbox %s[%d]: parse: %v", path, msgIdx, parseErr)
			skipped++
			msgIdx++
			continue
		}

		// Detect mismatch early: more messages than separator lines found.
		if !useMtime && msgIdx >= separatorCount {
			useMtime = true
			log.Printf("import mbox %s: message count exceeds separator count (%d); using file mtime fallback",
				path, separatorCount)
		}

		// From-separator timestamp fallback.
		var fromTS *time.Time
		if !useMtime {
			fromTS = parseFromTimestamp(fromTimestamps[msgIdx])
		}

		// date: Date header preferred, then From-separator, then fileMtime.
		var date time.Time
		if pm.Date != nil {
			date = pm.Date.UTC()
		} else if fromTS != nil {
			date = fromTS.UTC()
		} else if fileMtime != nil {
			date = fileMtime.UTC()
		} else {
			log.Printf("import mbox %s[%d]: no date available, skipping", path, msgIdx)
			skipped++
			msgIdx++
			continue
		}

		// Duplicate check.
		if pm.MessageID != nil {
			var exists bool
			if err := db.QueryRowContext(ctx,
				`SELECT EXISTS(SELECT 1 FROM messages WHERE message_id = ?)`, *pm.MessageID,
			).Scan(&exists); err != nil {
				log.Printf("import mbox %s[%d]: duplicate check: %v", path, msgIdx, err)
			} else if exists {
				skipped++
				msgIdx++
				continue
			}
		}

		batch = append(batch, batchEntry{pm: pm, raw: raw, date: date.Format(time.RFC3339)})
		msgIdx++

		if len(batch) >= batchSize {
			flush()
		}
	}
	flush()

	// Post-loop: log if scanner overcounted separators (mboxo body-From lines).
	if !useMtime && separatorCount > msgIdx {
		log.Printf("import mbox %s: separator count (%d) > message count (%d); From-separator timestamps may be misaligned",
			path, separatorCount, msgIdx)
	}

	return imported, skipped, flushErr
}

// scanMboxSeparators scans an mbox file once with bufio and collects the
// timestamp suffix of every "From <addr> <timestamp>" separator line.
// The separator count equals len of the returned slice.
func scanMboxSeparators(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var ts []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "From ") || len(line) < 6 || line[5] == ' ' {
			continue
		}
		rest := line[5:]
		_, after, found := strings.Cut(rest, " ")
		if !found {
			ts = append(ts, "")
		} else {
			ts = append(ts, strings.TrimSpace(after))
		}
	}
	return ts, sc.Err()
}

// parseFromTimestamp tries the known layouts in order; returns nil on failure.
func parseFromTimestamp(s string) *time.Time {
	for _, layout := range fromLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// importMbx imports messages from a UW-IMAP mbx file into folderID.
//
// mbx file structure:
//   - 2048-byte file header starting with "*mbx*\r\n"
//   - Sequence of: per-message header line + message body bytes
//
// Per-message header: "<IMAP-date>,<size>;<8hex-uflags><4hex-sysflags>-<8hex-uid>\r\n"
// System flag bits: fSEEN=0x1, fFLAGGED=0x4, fEXPUNGED=0x8000.
func importMbx(path string, folderID int64, db *sql.DB) (imported, skipped int, _ error) {
	const (
		mbxHdrSize = 2048
		mbxMagic   = "*mbx*\r\n"
		fSEEN      = 0x0001
		fFLAGGED   = 0x0004
		fEXPUNGED  = 0x8000
	)

	var fileMtime *time.Time
	if fi, err := os.Stat(path); err == nil {
		t := fi.ModTime().UTC()
		fileMtime = &t
	}

	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	hdr := make([]byte, mbxHdrSize)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return 0, 0, fmt.Errorf("read mbx header %s: %w", path, err)
	}
	if string(hdr[:len(mbxMagic)]) != mbxMagic {
		return 0, 0, fmt.Errorf("%s: not an mbx file (invalid magic)", path)
	}

	ctx := context.Background()
	contactRepo := repository.NewContactRepository(db)

	const batchSize = 50
	var batch []batchEntry
	var flushErr error

	flush := func() {
		n, err := commitBatch(ctx, db, batch, folderID, contactRepo)
		if err != nil {
			log.Printf("import mbx %s: commit batch: %v", path, err)
			skipped += len(batch)
			if flushErr == nil {
				flushErr = err
			}
		} else {
			imported += n
			skipped += len(batch) - n
		}
		for i := range batch {
			batch[i] = batchEntry{}
		}
		batch = batch[:0]
	}

	br := bufio.NewReader(f)
	msgIdx := 0

	for {
		line, readErr := br.ReadString('\n')
		if readErr == io.EOF && len(line) == 0 {
			break
		}
		if readErr != nil && readErr != io.EOF {
			log.Printf("import mbx %s[%d]: read header: %v", path, msgIdx, readErr)
			if flushErr == nil {
				flushErr = readErr
			}
			break
		}
		if readErr == io.EOF {
			break // truncated header at end of file
		}
		line = strings.TrimRight(line, "\r\n")

		// Parse: <date>,<size>;<8hex-uflags><4hex-sysflags>-<8hex-uid>
		dateStr, rest, ok := strings.Cut(line, ",")
		if !ok {
			log.Printf("import mbx %s[%d]: malformed header (no comma): %q", path, msgIdx, line)
			if flushErr == nil {
				flushErr = fmt.Errorf("malformed mbx per-message header at message %d", msgIdx)
			}
			break
		}
		sizeStr, flagsStr, ok := strings.Cut(rest, ";")
		if !ok {
			log.Printf("import mbx %s[%d]: malformed header (no semicolon): %q", path, msgIdx, line)
			if flushErr == nil {
				flushErr = fmt.Errorf("malformed mbx per-message header at message %d", msgIdx)
			}
			break
		}

		msgSize, parseErr := strconv.ParseUint(sizeStr, 10, 64)
		if parseErr != nil || msgSize == 0 {
			log.Printf("import mbx %s[%d]: invalid message size %q", path, msgIdx, sizeStr)
			if flushErr == nil {
				flushErr = fmt.Errorf("invalid mbx message size at message %d", msgIdx)
			}
			break
		}

		// flagsStr layout: <8hex user flags><4hex sys flags>-<8hex uid>
		if len(flagsStr) < 21 || flagsStr[12] != '-' {
			log.Printf("import mbx %s[%d]: malformed flags field: %q", path, msgIdx, flagsStr)
			if flushErr == nil {
				flushErr = fmt.Errorf("malformed mbx flags field at message %d", msgIdx)
			}
			break
		}
		sysFlags, parseErr := strconv.ParseUint(flagsStr[8:12], 16, 64)
		if parseErr != nil {
			log.Printf("import mbx %s[%d]: parse sys flags %q: %v", path, msgIdx, flagsStr[8:12], parseErr)
			if flushErr == nil {
				flushErr = parseErr
			}
			break
		}

		// Skip messages marked as expunged without allocating a body buffer.
		if sysFlags&fEXPUNGED != 0 {
			if _, err := io.CopyN(io.Discard, br, int64(msgSize)); err != nil {
				log.Printf("import mbx %s[%d]: discard expunged body: %v", path, msgIdx, err)
				if flushErr == nil {
					flushErr = err
				}
				break
			}
			skipped++
			msgIdx++
			continue
		}

		raw := make([]byte, msgSize)
		if _, err := io.ReadFull(br, raw); err != nil {
			log.Printf("import mbx %s[%d]: read body: %v", path, msgIdx, err)
			if flushErr == nil {
				flushErr = err
			}
			break
		}

		pm, pmErr := ParseMessage(raw)
		if pmErr != nil {
			log.Printf("import mbx %s[%d]: parse: %v", path, msgIdx, pmErr)
			skipped++
			msgIdx++
			continue
		}

		// Date: header preferred, then mbx internal date, then file mtime.
		var date time.Time
		if pm.Date != nil {
			date = pm.Date.UTC()
		} else if t := parseMbxDate(dateStr); t != nil {
			date = *t
		} else if fileMtime != nil {
			date = *fileMtime
		} else {
			log.Printf("import mbx %s[%d]: no date available, skipping", path, msgIdx)
			skipped++
			msgIdx++
			continue
		}

		// Duplicate check.
		if pm.MessageID != nil {
			var exists bool
			if err := db.QueryRowContext(ctx,
				`SELECT EXISTS(SELECT 1 FROM messages WHERE message_id = ?)`, *pm.MessageID,
			).Scan(&exists); err != nil {
				log.Printf("import mbx %s[%d]: duplicate check: %v", path, msgIdx, err)
			} else if exists {
				skipped++
				msgIdx++
				continue
			}
		}

		readFlag, flaggedFlag := 0, 0
		if sysFlags&fSEEN != 0 {
			readFlag = 1
		}
		if sysFlags&fFLAGGED != 0 {
			flaggedFlag = 1
		}

		batch = append(batch, batchEntry{
			pm:      pm,
			raw:     raw,
			date:    date.Format(time.RFC3339),
			read:    readFlag,
			flagged: flaggedFlag,
		})
		msgIdx++

		if len(batch) >= batchSize {
			flush()
		}
	}
	flush()
	return imported, skipped, flushErr
}

// parseMbxDate parses an IMAP INTERNALDATE string from an mbx per-message header.
// Format: " 2-Jan-2006 15:04:05 -0700" (space-padded single-digit days).
func parseMbxDate(s string) *time.Time {
	t, err := time.Parse("_2-Jan-2006 15:04:05 -0700", s)
	if err != nil {
		return nil
	}
	u := t.UTC()
	return &u
}
