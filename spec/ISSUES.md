# Open Issues

Issues are grouped by document/area. Each entry names the affected documents.

---

## Spec Inconsistencies

### 1. `send_at` not cleared when scheduler moves failed message to Drafts
**Affects:** REQUIREMENTS.md, IMPLEMENTATION.md, openapi.yaml

REQUIREMENTS.md (Background Scheduler) says: "After 3 consecutive failures, move to Drafts." IMPLEMENTATION.md repeats this without mentioning clearing `send_at`. But openapi.yaml MessageDetail says `send_at` is "Non-null only for messages in the Scheduled folder." If the scheduler moves a message from Scheduled to Drafts without clearing `send_at`, that invariant is violated, and the draft would appear schedulable at a time already in the past.

**Fix:** REQUIREMENTS.md and IMPLEMENTATION.md should explicitly state that `send_at` is cleared (set to NULL) when the scheduler moves a failed message to Drafts.

---

### 2. `DraftRequest.identity_id` description contradicts stored value
**Affects:** openapi.yaml, IMPLEMENTATION.md

The `DraftRequest` schema in openapi.yaml says for `identity_id`: "If absent, the default identity is used." This implies the default identity's ID is stored. IMPLEMENTATION.md says "If `identity_id` is absent in the PUT body, `identity_id` is set to NULL and `from_addr` is set to the default identity's address." The stored `identity_id` is NULL, not the default identity's ID.

**Fix:** The DraftRequest description should be revised to say something like: "If absent, `identity_id` is stored as NULL and `from_addr` is set to the current default identity's address."

---

### 3. `from_addr` on POST /drafts not specified
**Affects:** IMPLEMENTATION.md

IMPLEMENTATION.md (Draft Auto-Save Logic) specifies PUT handling: "the server resolves `identity_id` to its `address` and updates both the `identity_id` column and `from_addr`… If `identity_id` is absent in the PUT body, `identity_id` is set to NULL and `from_addr` is set to the default identity's address." For POST (new draft) it only says: "On POST (new draft), `identity_id` is stored directly from the request body (or NULL if absent)." It does not specify what `from_addr` is set to. Since `messages.from_addr TEXT NOT NULL` has no DEFAULT, an INSERT without an explicit `from_addr` will fail.

**Fix:** IMPLEMENTATION.md must specify POST /drafts sets `from_addr` to the specified identity's address, or to the default identity's address when `identity_id` is absent (mirroring the PUT rule).

---

### 4. Contacts upsert does not update `updated_at`
**Affects:** IMPLEMENTATION.md

The upsert SQL in IMPLEMENTATION.md is:
```sql
INSERT INTO contacts (...) ON CONFLICT(address) DO UPDATE SET name = excluded.name WHERE contacts.name = ''
```
This does not update `updated_at` when the name is changed. Additionally there is no trigger on the contacts table (unlike messages which has `messages_updated_at`), so manual PUT /contacts/{id} updates also need an explicit `updated_at = strftime(...)` in the UPDATE statement.

**Fix:** Update the upsert to also set `updated_at = excluded.updated_at` in the DO UPDATE clause, and document that PUT /contacts/{id} must explicitly set `updated_at`.

---

### 5. Filter secondary sort key not specified
**Affects:** IMPLEMENTATION.md, openapi.yaml

For identities, IMPLEMENTATION.md says "The server sorts by `position ASC, id ASC`." For filters, only "ordered by position" is stated, with no secondary sort key when two filters share the same position. Ties in position produce non-deterministic ordering.

**Fix:** Add a secondary sort key for filters (e.g., `position ASC, id ASC`), consistent with the identity sort order.

---

### 6. Contact upsert timing for scheduled sends not specified for `POST /messages/send`
**Affects:** REQUIREMENTS.md, IMPLEMENTATION.md

REQUIREMENTS.md says "On send: To, Cc, and Bcc addresses are upserted" without distinguishing immediate vs. scheduled. IMPLEMENTATION.md specifies this distinction only for `POST /drafts/{id}/send`: "Upsert recipients on immediate send only. For scheduled sends, upsert happens when the scheduler actually sends the message." The equivalent rule for `POST /messages/send` and `POST /messages/send-with-attachments` when they result in scheduling (202) is not stated.

**Fix:** IMPLEMENTATION.md should explicitly state that for all three send endpoints, contact upserts happen at actual send time (either immediately or when the scheduler fires), never at schedule-creation time.

---

## Missing Implementation Details

### 7. Thread algorithm SQL not provided
**Affects:** IMPLEMENTATION.md

IMPLEMENTATION.md describes the thread algorithm conceptually ("build a directed graph… take the transitive closure") but provides no SQL. For the primary (header-based) path, a recursive CTE is required. The References column stores multiple IDs (newline-separated), and matching them against other rows' `message_id` / `in_reply_to` columns requires careful handling to avoid partial-ID substring matches. Without concrete SQL the algorithm is ambiguous to implement.

**Fix:** IMPLEMENTATION.md should include a concrete SQL pattern (recursive CTE or equivalent Go query loop) for the transitive closure, including how to split and join the newline-separated References values.

---

### 8. `From ` separator line capture with go-mbox streaming API
**Affects:** IMPLEMENTATION.md

IMPLEMENTATION.md says to "parse and save the `From ` separator line timestamp **before** stripping it" and to use "the streaming `NextMessage()` API." The `go-mbox` `NextMessage()` call consumes the `From ` line internally before returning the message body reader, giving the caller no direct access to it. The spec does not explain how to capture the `From ` line timestamp when using the streaming API.

**Fix:** IMPLEMENTATION.md should document the concrete approach — e.g., wrapping the underlying `io.Reader` to intercept and record the `From ` line before it is consumed by `go-mbox`, or using a lower-level mbox parser for the From line.

---

### 9. Email address validation approach not specified
**Affects:** IMPLEMENTATION.md

Multiple places require "valid RFC 5322 addr-spec" validation (identity and contact address on create/update; `to_addr`/`cc_addr`/`bcc_addr`/`reply_to_addr` in send requests). IMPLEMENTATION.md specifies no Go library or function for this check. Using `net/mail.ParseAddress()` would accept display-name + addr-spec pairs (e.g. `"John Doe <john@example.com>"`), but for identity/contact storage only the bare addr-spec should be valid.

**Fix:** IMPLEMENTATION.md should specify the validation function (e.g., `net/mail.ParseAddress()` accepting only the addr-spec form — where the returned `Address.Name` is empty — or a dedicated regex). Also clarify whether `to_addr`/`cc_addr`/`bcc_addr` fields in send requests (which may contain comma-separated lists) are validated per-address or as a raw string.

---

### 10. Import mode folder lookup semantics not specified
**Affects:** REQUIREMENTS.md, IMPLEMENTATION.md

REQUIREMENTS.md says the `<folder>` part of an import mapping can be `inbox`, `sent`, `drafts`, `trash`, or any user-folder name, and the folder is "Created automatically if it does not exist." It does not specify:

- Whether the lookup is by slug (matching built-in slugs `inbox`, `sent`, etc.) or by name (`Inbox`, `Sent`, etc.)
- Whether the lookup is case-sensitive
- What happens if the same `<folder>` name appears in multiple mapping triplets (is the folder reused, or attempted to be created twice?)

**Fix:** REQUIREMENTS.md / IMPLEMENTATION.md should specify that the lookup is by slug for built-in folders and by name (case-sensitive or insensitive?) for user-created folders; and that duplicate folder names in mappings reuse the same target folder.

---

### 11. Contact list ordering SQL not specified
**Affects:** IMPLEMENTATION.md

openapi.yaml specifies contacts are "ordered by name (empty names last), then address." IMPLEMENTATION.md does not provide the corresponding SQL `ORDER BY` clause. The SQL to sort empty names last while ordering non-empty names alphabetically (e.g. `ORDER BY CASE WHEN name = '' THEN 1 ELSE 0 END, name, address`) is non-obvious and should be documented.

**Fix:** Add the SQL `ORDER BY` expression for contacts to IMPLEMENTATION.md.

---

### 12. Sendmail path resolution not mentioned in IMPLEMENTATION.md
**Affects:** IMPLEMENTATION.md, REQUIREMENTS.md

REQUIREMENTS.md says the server resolves the sendmail binary at startup using `PATH` lookup when the value is not absolute, and exits fatally if not found. IMPLEMENTATION.md has no corresponding implementation note. The Go function is `exec.LookPath()`.

**Fix:** Add a note in IMPLEMENTATION.md specifying `exec.LookPath()` for PATH resolution and `os.Access` / `os.Stat` (or attempting `exec.LookPath` which covers executability on Unix) to verify the binary is executable.

---

## Missing Requirements Details

### 13. Date filtering UI not described for Search view
**Affects:** REQUIREMENTS.md

The search API supports optional `date_from` and `date_to` parameters (documented in IMPLEMENTATION.md and openapi.yaml), but the REQUIREMENTS.md Web UI section for the Search view makes no mention of a date filter control. It is unclear whether the Web UI should expose date filtering to the user, and if so what the control looks like (date pickers, etc.).

**Fix:** REQUIREMENTS.md should either describe the date filter UI controls in the Search view, or explicitly state that date filtering is an API-only feature not exposed in the v1 UI.

---

### 14. `POST /messages/{id}/mark-junk` restriction for Drafts not specified
**Affects:** REQUIREMENTS.md, openapi.yaml

openapi.yaml for `POST /messages/{id}/mark-junk` returns 400 for Junk (id=7), Scheduled (id=5), and Snoozed (id=6). It does not restrict Drafts (id=3). Marking a draft as junk would move it from Drafts to Junk and mark it read, which conflicts with the dedicated draft lifecycle (drafts are created/deleted via `/drafts` endpoints). REQUIREMENTS.md does not address this either.

**Fix:** Specify whether Drafts should be rejected with 400 by mark-junk (consistent with the draft-lifecycle isolation principle already applied to Scheduled and Snoozed).

---

### 15. Global full-text search includes Trash and Junk
**Affects:** REQUIREMENTS.md, openapi.yaml

The search endpoint searches all folders by default (unless `folder_id` is specified). This means results from Trash and Junk appear in global searches. REQUIREMENTS.md and openapi.yaml do not state whether this is intentional or whether Trash/Junk should be excluded from default searches.

**Fix:** REQUIREMENTS.md should explicitly state that global search includes all folders including Trash and Junk, or specify exclusion rules and the corresponding SQL WHERE clause.

---

## Schema Issues

### 16. `messages.to_addr` missing DEFAULT clause
**Affects:** IMPLEMENTATION.md

The `messages` table schema in IMPLEMENTATION.md has:
```sql
from_addr  TEXT NOT NULL,
to_addr    TEXT NOT NULL,
```
But the other address fields (`cc_addr`, `bcc_addr`, `reply_to_addr`) all have `DEFAULT ''`. Drafts may have no `to_addr` at creation time. If the INSERT statement does not explicitly supply `to_addr`, SQLite will raise a constraint error. The same applies to `from_addr` (though that is always resolvable from the identity).

**Fix:** Add `DEFAULT ''` to `to_addr` (and potentially `from_addr`) in the schema, consistent with the other address columns, or document that the INSERT must always explicitly supply these values even for empty drafts.

---

## Typos / Minor

### 17. Typo in IMPLEMENTATION.md — stray `k` before "Database existence check"
**Affects:** IMPLEMENTATION.md

Line 218: `k**Database existence check:**` — the leading `k` is a typo and breaks the bold heading formatting.

**Fix:** Remove the leading `k`.
