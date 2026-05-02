# Open Issues

Issues found by cross-referencing REQUIREMENTS.md, ARCHITECTURE.md, IMPLEMENTATION.md, and openapi.yaml.

---

## Critical — Would block or produce incorrect implementation

### 1. Draft save with no identities: 500 vs. 201

**Files:** REQUIREMENTS.md (First-Run Behaviour), IMPLEMENTATION.md (Draft Auto-Save Logic)

REQUIREMENTS says:
> `POST /api/v1/drafts` and `POST /api/v1/drafts-with-attachments` are permitted with no identities so that a partially composed message is not lost when the user navigates away

IMPLEMENTATION says:
> If no identities exist when the default identity is needed (i.e. `identity_id` is absent and there is no default), the server returns `500` with `{"error": "no identity configured"}` and `from_addr` is set to empty string — this is a malconfigured-server condition.

These directly contradict. During first-run there are zero identities, so a draft save with no `identity_id` would hit the 500 path, making first-run drafts impossible.

**Resolution needed:** Clarify that when `identity_id` is absent AND no identities exist at all, the draft is saved with `from_addr = ""` and `identity_id = NULL` returning 201 — not 500. The 500 should be reserved for the case where the server's internal invariant is violated (identities exist but none is marked default).

---

### 2. `references` field: client input format (angle brackets or not)

**Files:** IMPLEMENTATION.md (Threading-header storage format), openapi.yaml (SendRequest, DraftRequest)

The threading-header storage format states that entries in `messages.references` are stored **without** angle brackets. `MessageDetail.references` serialises them back **with** brackets. But `SendRequest` and `DraftRequest` only say `type: array, items: type: string` with no description of whether elements should include brackets.

A client building a reply reads `MessageDetail.references` (with brackets), then submits them in a `DraftRequest`. The server must therefore strip brackets on inbound input before storage, or the stored values will contain double brackets on the next round-trip.

**Resolution needed:** Add an explicit statement in openapi.yaml descriptions for `SendRequest.references` and `DraftRequest.references` that elements may include angle brackets and the server strips them before storage (matching the outbound serialisation behaviour).

---

## Significant — Ambiguous enough to cause divergent implementations

### 3. PATCH /folders/reorder routing ambiguity

**Files:** openapi.yaml

Both `PATCH /folders/{id}` and `PATCH /folders/reorder` are defined. A request for `PATCH /folders/reorder` could match `{id}=reorder` if the router doesn't give static segments priority. ogen uses chi internally which does prioritise static segments, but this is an implementation dependency that should be noted.

**Resolution needed:** Add a note in IMPLEMENTATION.md confirming that static path segments (`reorder`) take priority over path parameters (`{id}`) in the generated router, and verify this with the chosen code generator.

---

### 4. `PUT /contacts/{id}` — omitted `name` semantics

**Files:** openapi.yaml (PUT /contacts/{id})

`DraftRequest` explicitly documents PUT replacement semantics ("any field omitted is cleared"). The contact PUT has no equivalent statement. If `name` is omitted from a `PUT /contacts/{id}` request body, it is unclear whether the stored name is cleared (consistent with PUT semantics) or preserved.

**Resolution needed:** Add a description to the PUT /contacts/{id} endpoint (or a `ContactRequest` schema) stating that omitting `name` clears it (sets to empty string), consistent with standard PUT replace semantics.

---

### 5. `DraftRequest.identity_id` referencing a non-existent identity on PUT

**Files:** IMPLEMENTATION.md (Draft Auto-Save Logic), openapi.yaml (DraftRequest)

IMPLEMENTATION describes resolving `identity_id` to its address on PUT but does not specify what to return if the supplied `identity_id` does not match any identity row.

**Resolution needed:** Specify the error response (400 with a message such as "identity not found") when `identity_id` is provided but refers to a non-existent identity, for both `POST /drafts` and `PUT /drafts/{id}`.

---

### 6. `source_message_id` + multipart attachments: merge or replace?

**Files:** openapi.yaml (POST /drafts-with-attachments, DraftRequest)

`POST /drafts-with-attachments` accepts file parts via multipart AND `DraftRequest.source_message_id` for server-side attachment copying. If both are supplied simultaneously, the expected behaviour (merge copied attachments with uploaded attachments, or error, or one takes precedence) is not defined.

**Resolution needed:** Specify what happens when both are provided — most likely the uploaded attachments are added alongside the copied ones, but this should be stated explicitly.

---

### 7. `messages_updated_at` trigger — implicit reliance on non-recursive trigger default

**Files:** IMPLEMENTATION.md (Database schema, messages table)

The trigger:
```sql
CREATE TRIGGER IF NOT EXISTS messages_updated_at AFTER UPDATE ON messages BEGIN
    UPDATE messages SET updated_at = ... WHERE id = new.id;
END;
```
…fires on any UPDATE to `messages`, then itself issues an UPDATE to `messages`. This is safe only because SQLite's default `PRAGMA recursive_triggers = OFF` suppresses the re-entrant firing. If recursive triggers are ever enabled (e.g. for another purpose), this trigger loops infinitely.

**Resolution needed:** Either add `WHEN new.updated_at = old.updated_at` (or equivalent guard) to the trigger, or add an explicit note in IMPLEMENTATION.md that `PRAGMA recursive_triggers` must remain OFF.

---

### 8. 32 MiB request body limit — scope undefined

**Files:** IMPLEMENTATION.md (REST API Implementation)

IMPLEMENTATION states "Max request body: 32 MiB" but this is not reflected in openapi.yaml and its scope is ambiguous for `multipart/form-data` endpoints: is 32 MiB the total encoded body, or per file part?

**Resolution needed:** Clarify whether 32 MiB applies to the entire multipart body (all parts combined) and document this in openapi.yaml as a server-level constraint or in endpoint descriptions.

---

## Minor — Small gaps or inconsistencies with low implementation risk

### 9. `from_addr` stale after identity deletion

**Files:** IMPLEMENTATION.md (Database schema, messages table)

`messages.identity_id` has `ON DELETE SET NULL`, so deleting an identity sets that column to NULL on affected drafts. However, `from_addr` is not updated by the FK constraint — it retains the deleted identity's address. The next PUT will fix it, but the draft list may briefly show a stale address.

**Resolution needed:** Document that this is acceptable (eventually consistent on next PUT), or add an application-level step when deleting an identity to clear/update `from_addr` on associated draft rows.

---

### 10. `bcc_addr` and `reply_to_addr` not indexed in FTS — undocumented omission

**Files:** IMPLEMENTATION.md (messages_fts schema)

The FTS5 table indexes `from_addr`, `to_addr`, `cc_addr`, `subject`, `body_text` but not `bcc_addr` or `reply_to_addr`. This means BCC recipients of sent messages cannot be searched. This is likely intentional (BCC is private; reply-to is rarely searched) but is not explicitly stated.

**Resolution needed:** Add a note in IMPLEMENTATION.md confirming that `bcc_addr` and `reply_to_addr` are intentionally excluded from full-text search.

---

### 11. Contact list ordering — case sensitivity undocumented

**Files:** IMPLEMENTATION.md (contacts, Contact list ordering SQL)

The SQL `ORDER BY ... name, address` performs case-sensitive comparison in SQLite. Names starting with uppercase letters will sort before identical names with lowercase. Whether this is intentional is not stated.

**Resolution needed:** Either document that ordering is case-sensitive (SQLite default) or change to `LOWER(name), LOWER(address)` for case-insensitive ordering.

---

### 12. `send_failure_count` and `send_error` not cleared on move to Trash

**Files:** openapi.yaml (DELETE /messages/{id}, PATCH /messages/{id})

When a message is moved to Trash, the spec clears `snoozed_until`, `snooze_folder`, and `send_at`. It does not clear `send_failure_count` or `send_error`. If a Scheduled message exhausts retries (moved to Drafts), then the user deletes it (moved to Trash), the failure metadata persists in Trash.

**Resolution needed:** Clarify whether `send_failure_count` and `send_error` should be cleared when moving any message to Trash. This avoids showing a failure badge on a Trash message that is there for unrelated reasons.

---

### 13. `mark-junk` on Trash messages — not addressed

**Files:** openapi.yaml (POST /messages/{id}/mark-junk), REQUIREMENTS.md

`POST /messages/{id}/mark-junk` returns 400 for Junk, Drafts, Scheduled, and Snoozed but does not mention Trash. The UI REQUIREMENTS exclude the Mark as Junk button only for Snoozed, Scheduled, and Drafts. This implies marking a Trash or Sent message as Junk is permitted via both API and UI. If this is intentional, it should be stated explicitly.

**Resolution needed:** Confirm (and document in the API description) that marking Trash and Sent messages as junk is allowed, or add them to the 400 rejection list.

---

### 14. `go 1.26` in go.mod

**Files:** IMPLEMENTATION.md (go.mod)

The specified Go version is `1.26`, which as of the spec authoring date may not yet be released. This is a minor forward-compatibility note — the actual minimum version for the used language features should be confirmed.

**Resolution needed:** Verify that `go 1.26` is the intended target or adjust to the current stable release (e.g. `go 1.24`).
