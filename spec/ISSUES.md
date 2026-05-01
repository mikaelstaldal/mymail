# Open Issues

Issues are grouped by severity. Each issue cites the conflicting or incomplete locations.

---

## Critical — Would cause implementation bugs or contradictions

### 1. LDA "creating if necessary" contradicts database-must-exist requirement

**REQUIREMENTS.md §LDA mode, step 1:** "Opens the database (creating it if necessary)."  
**REQUIREMENTS.md §Init mode (end of section):** "The server, LDA, and import modes require the database to already exist (created by `mymail -init`). They exit with a fatal error if the database file is absent."

These are contradictory. Decide: does LDA create the database if absent, or does it exit fatally? The rest of the spec (init mode, deployment model) implies the latter. Step 1 should read "Opens the database (exits with a fatal error if absent)."

---

### 2. `align` attribute allowed on `div`, but `div` is not an allowed element

**REQUIREMENTS.md §HTML Sanitization, Allowed attributes table:** `align` is listed as allowed on `"table, tbody, td, tfoot, th, thead, tr, p, h1–h6, div"`.  
**REQUIREMENTS.md §HTML Sanitization, Allowed elements list:** `div` does not appear.

If `div` is stripped by the sanitizer, there is no element to carry the `align` attribute. Either add `div` to the allowed elements list, or remove `div` from the `align` row in the attribute table.

---

### 3. `identity_id` for drafts is not stored in the database schema

**openapi.yaml `DraftRequest`:** includes optional `identity_id: integer`.  
**IMPLEMENTATION.md §Send Draft Logic, step 3:** "Resolve the identity: use `identity_id` from the draft if set, otherwise the default identity."  
**IMPLEMENTATION.md §messages schema:** the `messages` table has no `identity_id` column — only `from_addr TEXT`.

The send-draft logic references a stored `identity_id` that does not exist. Either:

- Add `identity_id INTEGER REFERENCES identities(id) ON DELETE SET NULL` to the `messages` table, and update the schema version / migration notes accordingly; or
- Clarify that "use `identity_id` from the draft" means "find the identity whose casefolded address matches `from_addr`", and remove the phrase "if set."

The first option is safer (handles identity renames; preserves the user's explicit choice).

---

## Missing details — Required to write correct implementation code

### 4. Single-message and bulk DELETE do not specify clearing scheduler/snooze fields

**openapi.yaml `DELETE /folders/{folder_id}` description:** explicitly states that `snoozed_until`, `snooze_folder`, and `send_at` are cleared when messages are moved to Trash.  
**openapi.yaml `DELETE /messages/{id}` and `DELETE /messages`:** silent on this side effect.

Without clearing these fields, a deleted (Trashed) message could still be acted on by the background scheduler (attempted send or snooze expiry restore). Both single and bulk delete endpoints should specify the same clearing behaviour as folder deletion.

---

### 5. `GET /messages/search` date filtering not described in IMPLEMENTATION.md

**openapi.yaml `GET /messages/search`:** documents `date_from` and `date_to` query parameters (inclusive/exclusive RFC 3339 bounds).  
**IMPLEMENTATION.md §REST API / Endpoint Summary and §FTS Search Input Sanitization:** describes the FTS5 phrase-match transformation but says nothing about how to combine FTS5 results with a date filter.

The implementation needs a concrete SQL pattern, e.g., joining `messages_fts` with `messages` and adding `AND m.date >= ?` / `AND m.date < ?` clauses. This should be specified, including whether the date comparison uses the stored RFC 3339 string lexicographic ordering (which works for UTC-normalised values).

---

### 6. `stop` field default in `FilterRequest` not documented in openapi.yaml

**IMPLEMENTATION.md §filters schema:** `stop INTEGER NOT NULL DEFAULT 1` — so the database default is `true` (stop after first match).  
**openapi.yaml `FilterRequest`:** `stop` is listed but not in `required`, and no default value is stated.

The API contract should state explicitly what value is assumed when `stop` is omitted from a POST or PUT body.

---

### 7. `source_message_id` error behaviour unspecified

**openapi.yaml `DraftRequest`:** "If present on POST, attachments from this source message are copied server-side into the new draft atomically."

No error response is specified when `source_message_id` references a non-existent message. Should the endpoint return `404`, silently create the draft with no attachments, or return `400`? This must be defined before implementation.

---

### 8. Position default when `position` is omitted on entity creation

**IMPLEMENTATION.md / openapi.yaml:** `position` is optional in `POST /folders`, `POST /filters`, and `POST /identities`. The database schemas default to `0`, which would place every newly created entity at the same position.

The spec should state the intended behaviour: either the entity is appended to the end of the list (position = max existing position + 1), or the caller is required to supply a position for correct ordering.

---

### 9. `is_default` default for subsequent identity creation not specified

**REQUIREMENTS.md §Identities:** "The first identity created is automatically marked as default."  
**openapi.yaml `POST /identities`:** "If `is_default` is true, all other identities are set to is_default=false." — no statement about what happens when `is_default` is omitted on a non-first identity.

Clarify: when `is_default` is absent or `false` on a POST and at least one identity already exists, the new identity is created as non-default (existing default is unchanged).

---

### 10. FTS snippet is sourced only from `body_text`; matches in other columns produce no snippet

**IMPLEMENTATION.md §Search snippet:** `snippet(messages_fts, 4, '**', '**', '…', 15)` — column index 4 is `body_text`.

If the search term matches only in `from_addr`, `to_addr`, `cc_addr`, or `subject` (columns 0–3), the `snippet()` call will return an empty or unrelated excerpt from `body_text`. This is a known SQLite FTS5 limitation (snippet only covers one column). The spec should acknowledge this and either accept it or specify fallback behaviour (e.g., try each column in order until a non-empty snippet is found).

---

## Inconsistencies between documents

### 11. `mark-not-junk` marks message as unread — not stated in REQUIREMENTS.md

**openapi.yaml `POST /messages/{id}/mark-not-junk`:** "Moves the message from Junk to Inbox and marks it as unread."  
**REQUIREMENTS.md §Web UI, Junk folder:** "Message detail shows a **Not junk** button (moves to Inbox)" — no mention of marking as unread.

Update REQUIREMENTS.md to match the API spec behaviour (marking as unread is intentional: mirrors the snooze-expiry behaviour and ensures the message appears as new on return to Inbox).

---

### 12. Snooze source-folder restrictions only in openapi.yaml

**openapi.yaml `POST /messages/{id}/snooze`:** "The message must be in Inbox, a user folder, or already in Snoozed. System folders (Drafts, Sent, Trash, Junk, Scheduled) are not allowed."  
**REQUIREMENTS.md:** no such restriction stated.

The restriction and its rationale should be added to REQUIREMENTS.md.

---

### 13. `DELETE /folders/{folder_id}/messages` folder restrictions only in openapi.yaml

**openapi.yaml:** returns `400` for Scheduled (id=5), Snoozed (id=6), and Drafts (id=3).  
**REQUIREMENTS.md §Web UI, Empty folder button:** only mentions Trash and Junk having the Empty button; no mention of protected folders.

Add the restriction and its rationale (these folders have dedicated lifecycle management) to REQUIREMENTS.md.

---

### 14. 60-second scheduling threshold not stated in REQUIREMENTS.md

**openapi.yaml `SendRequest.send_at`:** "Scheduled when `send_at > now + 60 seconds`; immediate otherwise."  
**IMPLEMENTATION.md §Send Draft Logic:** same threshold.  
**REQUIREMENTS.md:** describes deferred send and the Scheduled folder but never mentions the 60-second window.

Add the threshold and its rationale (prevents race between immediate path and scheduler) to REQUIREMENTS.md.

---

## Design gaps — Potential behavioural ambiguities

### 15. `mark-junk` does not prevent marking Scheduled or Snoozed messages as junk

**openapi.yaml `POST /messages/{id}/mark-junk`:** only returns `400` "if the message is currently in the Junk folder."

A message in the Scheduled folder can be moved to Junk by this endpoint, stranding a `send_at` value in the database (the scheduler checks `folder_id = 5` so it won't resend, but the field remains set). Similarly a Snoozed message would have a dangling `snoozed_until`. Specify whether Scheduled and Snoozed are also rejected with 400, and whether the relevant fields are cleared if the move is allowed.

---

### 16. Cancel-snooze response does not document field clearing

**openapi.yaml `DELETE /messages/{id}/snooze`:** response body is `{id, folder_id}`.

The endpoint description says the message is "returned to its original folder immediately" but does not explicitly state that `snoozed_until` and `snooze_folder` are cleared in the database. Add this to the description, matching the folder-deletion and scheduler-expiry behaviour.

---

### 17. Thread algorithm has no stated complexity bound or limit

**IMPLEMENTATION.md §Thread Algorithm:** describes a transitive-closure graph traversal across all stored messages.

For a very large mailbox with a long reply chain (thousands of messages in one thread), this traversal could require loading all `message_id`, `in_reply_to`, and `references` rows into memory in Go. The spec should acknowledge this and state an upper bound (e.g., cap at N messages) or confirm unbounded traversal is acceptable for the single-user personal-mail use case.

---

### 18. FTS search relevance ordering delegates entirely to FTS5 defaults without documentation

**openapi.yaml `GET /messages/search`:** "Results ordered by relevance."  
**IMPLEMENTATION.md:** silent on ranking.

SQLite FTS5 returns rows in an unspecified order unless `ORDER BY rank` is used. The spec should state explicitly that results are ordered by `rank` (BM25 default) and that no custom weighting is applied, so the implementation and future maintainers have a clear contract.
