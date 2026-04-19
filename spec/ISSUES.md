# Open Issues

Issues found by pre-implementation specification review, organized by area.
All issues below require a deliberate design decision before they can be resolved.

---

## 2. Database Schema and Data Model

### 2.1 `snooze_folder` has no foreign key constraint or ON DELETE action
Adding `REFERENCES folders(id) ON DELETE SET NULL` would be consistent with filter behavior. The SPEC prose already documents the COALESCE fallback, which handles the stale-ID case at snooze expiry. Decision: add the FK constraint for correctness, or leave it absent for simplicity (SQLite FK enforcement is opt-in and may interact with the pure-Go SQLite driver)?

### 2.2 `references` field space-separated storage is ambiguous for malformed Message-IDs
RFC 5322 prohibits spaces inside Message-IDs but malformed messages exist in the wild. Options: (a) define trimming/sanitization at parse time so malformed IDs are stored as-is and split on spaces, accepting possible incorrect splitting; (b) use a different separator (e.g. newline `\n`) that cannot appear inside a Message-ID. Decision needed on separator strategy.

### 2.3 `PRAGMA user_version` inside transaction may not be atomic in SQLite
The SPEC claims atomicity ("a crash mid-migration leaves the version unchanged"), but SQLite's behavior for `PRAGMA user_version` inside a DDL transaction may vary by version. Decision: accept the current approach with a documentation caveat, or set `PRAGMA user_version` after the transaction commits (sacrificing atomic rollback but gaining simpler reasoning)?

### 2.4 Unread count computation not specified
`unread_count` on the `Folder` response is not specified as computed on-the-fly (`COUNT(*)`) or via a denormalized counter. Decision: on-the-fly (simpler, correct under concurrent writes) vs. denormalized counter with triggers (faster for large folders, more complex)?

### 2.6 Contacts address lowercasing undefined for Unicode/IDN addresses
The spec does not define the lowercasing algorithm (byte-by-byte, Unicode simple casefolding, locale-sensitive). Decision: specify as Unicode simple casefolding (recommended) or ASCII-only lowercasing (simpler, covers almost all real addresses)?

### 2.8 Import batch rollback provides no way to identify failed batch
After a batch failure, there is no way to identify which messages were committed. Decision: accept as a documented limitation (simplest), or add a transient `import_batch_id` column (complex), or document that the user should re-run the import after fixing the source?

---

## 3. LDA and Mail Processing

### 3.1 MIME parsing strategy for deeply nested structures not specified
The search strategy for `text/plain` and `text/html` parts in nested MIME structures is not specified (depth-first vs. breadth-first, handling of `message/rfc822` attachments). Decision: specify as depth-first search of the primary body tree, skipping `message/rfc822` sub-parts?

### 3.3 Message-ID generation domain undefined in LDA mode
When generating a Message-ID for an incoming message that lacks one, the LDA has no sender identity. Decision: use the recipient's address domain (from the `To` header), the system hostname (`os.Hostname()`), or a configurable value?

### 3.4 Outgoing mail body structure when only one part is provided
If a user sends plain-text only, should the body be a single `text/plain` part or a `multipart/alternative` with one child? Decision: single part (more common, simpler) vs. always `multipart/alternative` (consistent structure)?

### 3.8 Definition of "unparseable" message not specified
The import spec says "A single unparseable message logs a warning and continues" without defining the threshold. Decision: define parse failure as any `net/mail.ReadMessage()` error (missing/malformed headers), with degraded-parse cases (missing optional fields like `Date`) treated as warnings rather than failures?

### 3.9 Maildir import ignores Flagged flag
The Maildir `F` (Flagged) flag is not mapped during import. Decision: map `F` → `flagged=1` (preserves user's starred messages from previous client) or explicitly document that Flagged is intentionally ignored?

---

## 5. REST API Design

### 5.2 `DELETE /folders/{folder_id}/messages` does not protect Drafts folder
The endpoint returns 400 for Scheduled and Snoozed but not Drafts. Decision: add Drafts (id=3) to the protected list (prevents accidental permanent deletion of in-progress drafts), or leave it unprotected (the endpoint description says "permanently delete", so the caller is informed)?

### 5.3 Junk folder shows no move controls but no alternative move path
Users can only move junk to Inbox via "Not junk". Decision: add normal move controls for Junk messages (allows moving directly to any folder), or document the two-step workaround (Not junk → move)?

### 5.5 No server-side "mark all as read" endpoint
Current design requires multiple round trips from the client. Decision: add `POST /api/v1/folders/{id}/mark-all-read` for efficiency and atomicity, or keep the current client-side batch approach?

### 5.6 Contact autocomplete pagination behavior unclear
Whether the autocomplete dropdown should paginate, what the default limit is, and how to signal "more results available" is unspecified. Decision: for autocomplete use, cap at `limit=10` (no pagination in the dropdown) and display a "type more to narrow" hint when results are truncated?

### 5.8 Message list view mentions snippets but API returns none for folder lists
The SPEC Web UI Layout mentions "snippet" in the message list but `MessageSummary` (returned by folder list) has no snippet field. Decision: add a `snippet` field to `MessageSummary` (requires FTS query on every folder page load), or remove "snippet" from the Web UI Layout description (folder lists show subject only; snippets only in search results)?

---

## 6. Security

### 6.5 Date range search uses UTC but users may expect local time
`date_from`/`date_to` are interpreted as UTC midnight boundaries. Users in non-UTC timezones may get unexpected results. Decision: document the UTC limitation explicitly (simplest), or add an optional `tz` parameter (IANA timezone name) to shift the boundaries?

---

## 7. Thread Algorithm

### 7.1 International subject prefix variants not handled
`Re:`, `Fwd:`, `Fw:` are stripped for subject-based thread grouping but international variants (`AW:`, `WG:`, `RES:`, `ENC:`, `VS:`, `SV:`) are not. Decision: add the common international prefixes to the strip list, or document the omission as a known limitation?
