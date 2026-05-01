# Open Issues

Issues requiring explicit decisions before implementation can proceed. All other issues from
the previous review have been resolved by updating REQUIREMENTS.md, IMPLEMENTATION.md, and
openapi.yaml.

---

## Critical — Would cause implementation bugs or contradictions

### 1. `align` attribute allowed on `div`, but `div` is not an allowed element

**REQUIREMENTS.md §HTML Sanitization, Allowed attributes table:** `align` is listed as allowed on `"table, tbody, td, tfoot, th, thead, tr, p, h1–h6, div"`.  
**REQUIREMENTS.md §HTML Sanitization, Allowed elements list:** `div` does not appear.

**Decision required:** Choose one:
- **Option A — Remove `div` from the `align` row.** Keeps the sanitizer strict; `div` is silently stripped along with any `align` on it. Simpler and safer for a strict email policy.
- **Option B — Add `div` to the allowed elements list** (and keep it in the `align` attribute row). `div` is widely used in HTML emails for layout; allowing it is more permissive but more compatible.

---

### 2. `identity_id` for drafts is not stored in the database schema

**openapi.yaml `DraftRequest`:** includes optional `identity_id: integer`.  
**IMPLEMENTATION.md §Send Draft Logic, step 3:** "Resolve the identity: use `identity_id` from the draft if set, otherwise the default identity."  
**IMPLEMENTATION.md §messages schema:** the `messages` table has no `identity_id` column — only `from_addr TEXT`.

The current design resolves `identity_id` on each PUT and writes the resulting address to `from_addr`, then discards `identity_id`. Step 3 of Send Draft Logic therefore cannot read back a stored `identity_id`; it can only match `from_addr` against identities.

**Decision required:** Choose one:
- **Option A — Add `identity_id INTEGER REFERENCES identities(id) ON DELETE SET NULL` to the `messages` table.** Preserves the user's explicit identity choice across renames. Requires a schema version bump and migration notes update.
- **Option B — Clarify step 3 to read "find the identity whose casefolded address matches `from_addr`; if no match, use the default identity."** No schema change needed; the stored `from_addr` acts as the identity reference. Breaks silently if the user renames the identity address after drafting.

---

## Missing details — Required to write correct implementation code

### 3. `source_message_id` error behaviour unspecified

**openapi.yaml `DraftRequest`:** "If present on POST, attachments from this source message are copied server-side into the new draft atomically. Used when forwarding a message."

No response code is specified when `source_message_id` references a non-existent message.

**Decision required:** Choose one:
- **404** — communicates clearly that the referenced message is gone.
- **400** — treats a bad ID as a client input error.
- **Silently ignore** — create the draft with no attachments and return 201.

---

### 4. Position default when `position` is omitted on entity creation

**IMPLEMENTATION.md / openapi.yaml:** `position` is optional in `POST /folders`, `POST /filters`, and `POST /identities`. The database schemas default to `0`, which places every newly created entity at position 0 (ties broken by `id` in list ordering, but the intent is unclear).

**Decision required:** Choose one:
- **Append semantics** — when `position` is omitted, the server sets `position = MAX(existing positions) + 1`, placing the new entity at the end of the ordered list. This is the expected UX behaviour.
- **Explicit required** — callers must always supply `position`; the field should be added to `required`. The database default of `0` is only a schema safety net.

---

### 5. FTS snippet is sourced only from `body_text`; matches in other columns produce no snippet

**IMPLEMENTATION.md §Search snippet:** `snippet(messages_fts, 4, '**', '**', '…', 15)` — column index 4 is `body_text`.

If the search term matches only in `from_addr`, `to_addr`, `cc_addr`, or `subject` (columns 0–3), the `snippet()` call returns an empty or irrelevant excerpt from `body_text`. This is a known SQLite FTS5 limitation.

**Decision required:** Choose one:
- **Accept the limitation** — document in IMPLEMENTATION.md that snippet may be empty for header-only matches; the UI shows an empty snippet in that case.
- **Try each column in order** — attempt `snippet(messages_fts, 0, …)` through `snippet(messages_fts, 4, …)` and return the first non-empty result. More expensive but more accurate.

---

## Design gaps — Potential behavioural ambiguities

### 6. `mark-junk` does not prevent marking Scheduled or Snoozed messages as junk

**openapi.yaml `POST /messages/{id}/mark-junk`:** only returns `400` "if the message is currently in the Junk folder."

A Scheduled message moved to Junk would have a dangling `send_at` (the scheduler checks `folder_id = 5`, so it won't resend, but the field remains set). A Snoozed message would have a dangling `snoozed_until`.

**Decision required:** Choose one:
- **Reject Scheduled and Snoozed with 400** — add them to the existing Junk check. Clean semantics; forces the user to cancel the schedule/snooze first.
- **Allow the move, clear the dangling fields** — add `send_at = NULL` / `snoozed_until = NULL` / `snooze_folder = NULL` to the UPDATE for `mark-junk`. Matches the behaviour already specified for folder deletion and single/bulk DELETE.
- **Allow the move, leave fields set** — acceptable because the scheduler already gates on `folder_id`; the dangling values are harmless. Document as a known minor inconsistency.

---

### 7. Thread algorithm has no stated complexity bound or limit

**IMPLEMENTATION.md §Thread Algorithm:** describes a transitive-closure graph traversal across all stored messages.

For a large mailbox with a very long reply chain, this traversal could require loading many rows into memory in Go. The spec is silent on whether this is acceptable or whether a limit should be imposed.

**Decision required:** Choose one:
- **Unbounded** — confirm that unbounded traversal is acceptable for a single-user personal-mail use case (typical threads are short; pathological cases are the user's problem).
- **Capped at N** — specify an upper bound (e.g. 500 messages per thread result). Truncated threads would show a "thread too long" indicator in the UI.
