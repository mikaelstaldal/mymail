# Open Issues

## API / Behavior Contradictions

### 1. `POST /drafts/{id}/send` — "moved" vs. delete+insert contradiction
**Documents:** openapi.yaml, IMPLEMENTATION.md  
openapi.yaml says the draft is "moved to the Scheduled folder." IMPLEMENTATION.md says "Insert the message and attachments into the Scheduled folder (do not call sendmail), then delete the draft" — i.e. a new row is created and the old draft deleted. These are structurally different: the response `id` in the 202 case is the ID of the *new* Scheduled row, not the original draft ID. The client must stop using the old draft ID after receiving 202.  
**Resolution:** Align the two documents. openapi.yaml should say "a new message is created in the Scheduled folder and the draft is deleted." Update the 202 response description accordingly.

---

### 2. `DELETE /messages/{id}` and `DELETE /messages` (bulk) — no source-folder restriction
**Documents:** openapi.yaml  
`POST /messages/move` explicitly rejects Scheduled (5), Snoozed (6), and Drafts (3) as source folders. `DELETE /messages/{id}` and the bulk `DELETE /messages` have no such restriction. A Scheduled message deleted via `DELETE /messages/{id}` would be moved to Trash (or permanently deleted if already in Trash/Junk) without going through the cancel-schedule flow, bypassing `send_at`/`snoozed_until` cleanup and the scheduler race guard.  
**Resolution:** Specify whether these delete endpoints restrict Scheduled/Snoozed/Drafts sources (mirroring `POST /messages/move`), or explicitly permit them with field-clearing semantics.

---

### 3. Identity deletion cleanup — contradictory explanation
**Documents:** IMPLEMENTATION.md  
The cleanup SQL after identity deletion is:
```sql
UPDATE messages SET from_addr = '' WHERE identity_id IS NULL AND folder_id = 3 AND from_addr = ?
```
After the FK `ON DELETE SET NULL` cascade fires, *all* draft rows that referenced this identity already have `identity_id IS NULL`. The SQL therefore affects drafts that used the default-identity convention (stored `identity_id=NULL, from_addr=identity_address`). But the immediately following sentence says "Drafts that used the default-identity convention (`identity_id IS NULL`) but were associated with a different identity via `from_addr` are left unchanged." This directly contradicts the SQL — those drafts are precisely what the SQL targets.  
**Resolution:** Rewrite the explanation. The intent is to clear `from_addr` on draft rows where `from_addr` matches the deleted address (regardless of how `identity_id` was stored, since the FK cascade has already set it to NULL). The "left unchanged" note should instead say: "Only drafts whose `from_addr` matches the deleted identity's address are updated; drafts with a different `from_addr` are left unchanged."

---

### 4. Scheduler — intermediate-failure SQL missing
**Documents:** IMPLEMENTATION.md  
Only the 3rd-failure SQL (move-to-Drafts) is specified:
```sql
UPDATE messages SET folder_id = 3, send_at = NULL, send_failure_count = send_failure_count + 1, send_error = ?
WHERE id = ? AND folder_id = 5 AND send_failure_count >= 2
```
The SQL for the 1st and 2nd failures (increment count + record error, no folder change) is absent.  
**Resolution:** Add the intermediate-failure SQL, e.g.:
```sql
UPDATE messages SET send_failure_count = send_failure_count + 1, send_error = ?
WHERE id = ? AND folder_id = 5 AND send_failure_count < 2
```

---

## Missing Implementation Details

### 5. `in_reply_to` bracket handling in `SendRequest` / `DraftRequest` not specified
**Documents:** openapi.yaml, IMPLEMENTATION.md  
For `references`, the spec explicitly states "Elements may include or omit angle brackets; the server strips surrounding angle brackets before storage." No equivalent statement exists for `in_reply_to`. Since the DB stores `in_reply_to` without brackets, the server must also strip them from client-supplied values, but this is not documented.  
**Resolution:** Add to the `in_reply_to` field description in both `SendRequest` and `DraftRequest`: "Surrounding angle brackets are stripped before storage (consistent with `references` handling)."

---

### 6. Snooze creation handler missing ≥1 minute validation
**Documents:** REQUIREMENTS.md, IMPLEMENTATION.md  
REQUIREMENTS.md and openapi.yaml both require `until` to be at least 1 minute ahead of current server time. IMPLEMENTATION.md's snooze-creation section (the SQL and two-case logic) does not mention this validation at all.  
**Resolution:** Add the validation step to the snooze handler description in IMPLEMENTATION.md: "Validate that `until > now + 60 seconds`; return 400 otherwise."

---

### 7. Missing COUNT query for search `total` field
**Documents:** IMPLEMENTATION.md  
The search SQL uses `LIMIT ? OFFSET ?` but no separate `SELECT COUNT(*)` query is specified. The `total` field in the response must reflect total matches before pagination.  
**Resolution:** Add a count query alongside the main search query:
```sql
SELECT COUNT(*) FROM messages_fts JOIN messages m ON messages_fts.rowid = m.id
WHERE messages_fts MATCH ? AND <same folder/date conditions>;
```

---

### 8. `PUT /drafts-with-attachments/{id}` with zero file parts — undefined behavior
**Documents:** openapi.yaml, IMPLEMENTATION.md  
IMPLEMENTATION.md says this endpoint "replaces attachments wholesale." If the request contains no file parts, it is unclear whether all existing attachments are deleted (true "replace wholesale") or left unchanged.  
**Resolution:** Explicitly state: "If no file parts are present, all existing attachments for the draft are deleted. An empty attachments array is a valid way to clear all attachments."

---

### 9. Attachment form-data: filename and content-type extraction not specified
**Documents:** openapi.yaml, IMPLEMENTATION.md  
For `POST /messages/send-with-attachments`, `POST /drafts-with-attachments`, and `PUT /drafts-with-attachments/{id}`, the spec does not state how the attachment filename and content-type are extracted from multipart form parts, or what defaults to use when the `Content-Disposition` filename or `Content-Type` header is absent.  
**Resolution:** Add to IMPLEMENTATION.md: "For each attachment file part, extract the filename from `Content-Disposition: form-data; filename=...` (or `filename*=` for RFC 5987 encoding); use `untitled` if absent. Extract `Content-Type` from the part header; use `application/octet-stream` if absent."

---

### 10. `source_message_id` referencing a draft message — undefined behavior
**Documents:** openapi.yaml, IMPLEMENTATION.md  
`DraftRequest.source_message_id` is documented as "Returns 400 if the referenced message does not exist." If the referenced message exists but is a draft (folder_id=3, raw=NULL), the behavior is unspecified. Forwarding from a draft is an edge case but a real one.  
**Resolution:** Clarify: `source_message_id` may reference any message including drafts; only existence is checked. Attachments are copied regardless of source folder.

---

### 11. Attachment order when `source_message_id` + uploaded files both present
**Documents:** openapi.yaml  
`POST /drafts-with-attachments` says "both are applied: source message attachments are copied and uploaded file parts are stored." The order of the resulting attachments is unspecified, which affects MIME construction order when the draft is sent.  
**Resolution:** Specify: "Source-message attachments are stored first (in their original order), followed by newly uploaded file parts."

---

### 12. `FilterRequest.name` — no `maxLength`, limit not listed in input-limits table
**Documents:** openapi.yaml, IMPLEMENTATION.md  
The IMPLEMENTATION.md input length limits table lists limits for contact name, identity name, and folder name (all 200 characters) but omits filter name. `FilterRequest.name` in openapi.yaml has no `maxLength`.  
**Resolution:** Add `maxLength: 200` to `FilterRequest.name` in openapi.yaml and add filter name to the input-limits table in IMPLEMENTATION.md.

---

### 13. `subject` has no `maxLength`
**Documents:** openapi.yaml  
`SendRequest.subject` and `DraftRequest.subject` have no `maxLength`. RFC 5322 imposes a 998-character per-line limit (before folding); an unbounded subject could produce malformed headers.  
**Resolution:** Add `maxLength: 998` to `subject` in both schemas.

---

### 14. Contact `address` has no `maxLength`
**Documents:** openapi.yaml, IMPLEMENTATION.md  
The IMPLEMENTATION.md input-limits table omits a maximum length for contact address. The `address` field in `POST /contacts` and `PUT /contacts/{id}` request bodies has no `maxLength` in openapi.yaml.  
**Resolution:** Add a reasonable `maxLength` (e.g., 254 characters, the RFC 5321 maximum for an email address) to contact address fields and document in the input-limits table.

---

### 15. Draft with both body fields empty → MIME message with no body
**Documents:** openapi.yaml, IMPLEMENTATION.md  
A draft may be saved and sent with both `body_text` and `body_html` empty. The MIME construction logic ("single text/plain or text/html part when one is provided; multipart/alternative when both are provided") does not cover the case where neither is provided.  
**Resolution:** Specify the behavior: e.g., "If both body fields are empty, the MIME message is constructed with a single empty `text/plain` part."

---

## Missing API Documentation

### 16. `send_failed` badge suppression for Trash not in openapi.yaml
**Documents:** openapi.yaml, IMPLEMENTATION.md  
IMPLEMENTATION.md notes that `send_failure_count` and `send_error` are intentionally not cleared when a message is moved to Trash, and "the UI must suppress the `send_failed` badge when the message is in Trash." This client requirement is not documented in the openapi.yaml `send_failed` field descriptions.  
**Resolution:** Add to the `send_failed` description in both `MessageSummary` and `MessageDetail`: "When `folder_id = 4` (Trash), the UI should suppress this badge — `send_failure_count` is intentionally preserved in Trash."

---

### 17. Zero-identity 400 response not documented for `/messages/send` endpoints
**Documents:** openapi.yaml, REQUIREMENTS.md  
REQUIREMENTS.md documents that `POST /messages/send`, `POST /messages/send-with-attachments`, and `POST /drafts/{id}/send` return `400 {"error": "no identity configured; create one in Settings → Identities first"}` when called with no identities. This is not mentioned in the openapi.yaml endpoint descriptions.  
**Resolution:** Add the zero-identity 400 case to the descriptions of all three endpoints in openapi.yaml.

---

### 18. `POST /messages/send` and `POST /messages/send-with-attachments` — `id` in responses not described
**Documents:** openapi.yaml  
The 201 and 202 response bodies return `{id}` and `{id, send_at}` respectively, with no description of what `id` refers to. `POST /drafts/{id}/send` correctly describes "ID of the newly created message in the Sent folder" (201) and "ID of the newly created message in the Scheduled folder" (202). The same description is absent from the other two send endpoints.  
**Resolution:** Add matching `description` fields to the `id` properties in the 201 and 202 responses of `POST /messages/send` and `POST /messages/send-with-attachments`.

---

### 19. `PATCH /folders/reorder` — built-in folders must be included, but not stated
**Documents:** openapi.yaml, IMPLEMENTATION.md  
The reorder endpoint requires "every existing folder exactly once." Built-in folders (ids 1–7) are returned by `GET /folders` and have positions, but it is not explicitly stated that they must be included in the `ids` array for reorder calls.  
**Resolution:** Add to the endpoint description: "The `ids` array must include all folders, including built-in folders (ids 1–7)."

---

### 20. `DELETE /messages/{id}/snooze` — read state on early cancel not specified
**Documents:** openapi.yaml, REQUIREMENTS.md, IMPLEMENTATION.md  
The scheduler marks a message unread (`read = 0`) when a snooze expires naturally. The cancel-snooze handler SQL in IMPLEMENTATION.md does not include `read = 0`. This asymmetry (natural expiry resets read; early cancel preserves read) is not documented as intentional anywhere.  
**Resolution:** Explicitly state whether early snooze cancellation preserves the current read state. If intentional, document it; if a bug in the SQL, add `read = 0` to the cancel-snooze UPDATE.

---

### 21. Import mode: built-in folder lookup is case-sensitive, but this is not warned
**Documents:** REQUIREMENTS.md, IMPLEMENTATION.md  
The import `<folder>` argument for built-in folders is matched case-sensitively against slugs (`inbox`, `sent`, etc.). A user supplying `Inbox` (capital I) would silently create a new user folder named "Inbox" rather than mapping to the built-in. This behavior is implicit but not warned in the spec.  
**Resolution:** Add an explicit warning to the import mode documentation: "Built-in folder names in mapping arguments are case-sensitive lowercase slugs. `Inbox` (capitalized) would create a new user folder, not target the built-in inbox."

---

### 22. mbox two-pass: mismatch condition covers only one direction
**Documents:** IMPLEMENTATION.md  
The spec says "if a mismatch is detected (more messages than timestamps), fall back to the file mtime for the affected messages." This covers one direction; if there are more timestamps than messages (due to unescaped `From ` lines in mboxo files being counted as separators by the first pass), the excess timestamps would cause index misalignment without triggering the stated fallback condition.  
**Resolution:** Change the fallback condition to: "if the number of timestamps collected in the first pass does not match the number of messages yielded by the second pass (in either direction), fall back to the file mtime for all messages in the file."

---

### 23. `PUT /identities/{id}` address change stale `from_addr` in drafts
**Documents:** IMPLEMENTATION.md  
`DELETE /identities/{id}` includes a cleanup step to clear `from_addr` on affected drafts. `PUT /identities/{id}` (which can change the identity's address) has no equivalent cleanup. Drafts with `identity_id = X` will retain the old `from_addr` until the next draft auto-save triggers a PUT that re-resolves `identity_id → address`.  
**Resolution:** Either add a cascading `UPDATE messages SET from_addr = new_address WHERE identity_id = ? AND folder_id = 3` step to the `PUT /identities/{id}` handler, or document this as a known transient inconsistency that resolves on the next draft save.

---

### 24. `PATCH /messages/{id}` moving to Junk — snooze/send_at fields not cleared
**Documents:** openapi.yaml, IMPLEMENTATION.md  
Moving to Trash (folder_id=4) via `PATCH /messages/{id}` clears `snoozed_until`, `snooze_folder`, and `send_at`. Moving to Junk (folder_id=7) has no such clearing specified. While the normal flow prevents a Snoozed message from being PATCHed to Junk directly (from-Snoozed PATCH is rejected), other paths could leave residual fields set.  
**Resolution:** Specify whether moving to Junk via PATCH also clears these scheduler/snooze fields. If yes, add the same clearing behavior as for Trash moves. If not, explain why Junk is exempt.

