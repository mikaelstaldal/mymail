# Open Issues

Issues found by cross-referencing REQUIREMENTS.md, ARCHITECTURE.md, IMPLEMENTATION.md, and openapi.yaml.

---

## 1. `messages.raw NOT NULL` — no content specified for drafts

The `messages` table has `raw BLOB NOT NULL`, but drafts are created via `POST /api/v1/drafts` by
supplying individual fields (`to_addr`, `body_text`, etc.), not a raw RFC 5322 message. The spec
does not state what bytes go into the `raw` column when a draft is saved or updated.

**Affected:** IMPLEMENTATION.md (schema), openapi.yaml (`/messages/{id}/raw`).

*Decision:* Change the schema to make raw nullable, and set it to NULL for drafts, `GET /messages/{id}/raw` should
return an empty JSON object if the column is set to NULL.

---

## 2. User-created folder IDs ≥ 100 — enforcement mechanism missing

IMPLEMENTATION.md states "user-created folders have `id >= 100`" but the schema uses plain
`AUTOINCREMENT`. After seeding built-in folder rows 1–7, the next auto-assigned ID would be 8.
No mechanism is described to start the sequence at 100.

**Affected:** IMPLEMENTATION.md (schema, folder service).

*Decision:* Change the DB schema to not AUTOINCREMENT the folder id, generate id in the backend before insert instead.
Creating new folders is not a frequent operation, so it is acceptable if it requires some more DB roundtrips and takes 
a bit more time, prioritize correctness over performance for this operation.

---

## 3. LDA mode does not set `PRAGMA journal_mode=WAL`

IMPLEMENTATION.md specifies:
- Server: `PRAGMA journal_mode=WAL`, 5-second busy timeout.
- LDA: 30-second busy timeout only — `journal_mode` is not mentioned.

In a fresh install the LDA typically runs before the server has ever started. If the LDA creates
the database, it will be in the default DELETE journal mode. REQUIREMENTS.md explicitly states that
concurrent LDA + server is safe *because of WAL mode*; without it the guarantee does not hold.

**Affected:** IMPLEMENTATION.md (LDA implementation, SQLite configuration).

*Decision:* Add a new operational mode `mymail -init -data <dir>` which will create the database, set journal mode, initialize the schema, set `user_version`, 
and seed the database with built-in folders and any other data which needs to be there from start. The other operational modes (server, LDA, import) 
should check if the database already exists and immediately exit with an error if not. The user is expected to run `mymail -init` once as part of installation.   

---

## 4. Schema migration version numbering is self-contradictory

IMPLEMENTATION.md says both:
- `user_version 1 → (reserved for first future migration)` — implies version 1 is not yet used.
- `**Current schema version: 1** (initial schema applied; no further migrations yet)` — implies
  version 1 is the active post-initial-setup version.

The wording needs to be corrected so implementers know which `PRAGMA user_version` value to write
after applying the initial schema.

**Affected:** IMPLEMENTATION.md (schema migrations section).

*Decision:* The intended semantics appear to be:
- version 0 = uninitialized; apply v0 DDL and advance to version 1.
- version 1 = initial schema in place; the label "reserved for first future migration" is wrong
  (that migration would bump the version to 2).

---

## 5. Snooze endpoint response: `snooze_folder_id` is `required` but can be null

`POST /messages/{id}/snooze` response schema lists `snooze_folder_id` in `required` (no
`nullable: true`). However, the `snooze_folder` column has `ON DELETE SET NULL`. If the original
return folder is deleted between the first snooze and a subsequent re-snooze, the column will be
`NULL` and the response will violate its own `required` contract.

**Affected:** openapi.yaml (`/messages/{id}/snooze` response schema).

*Desicion:* `MessageDetail` correctly marks `snooze_folder_id` as `nullable: true`. The snooze-endpoint
response schema should match.

---

## 6. Import mode: contacts upsert not specified

LDA step 7 (REQUIREMENTS.md) explicitly upserts the `From` address into contacts. The import
description says "the full LDA parsing pipeline runs" but only enumerates:
> HTML sanitization, `cid:` resolution, charset decoding, `body_text` derivation, attachment
> extraction.

**Affected:** REQUIREMENTS.md (Batch Import), IMPLEMENTATION.md (Batch Import, LDA implementation).

*Decision:* Batch `-import` should upsert `From` addresses into contacts 

---

## 7. Import mode date fallback contradicts "missing optional fields are warnings, not failures"

REQUIREMENTS.md (import section):
> "A message is considered unparseable when `net/mail.ReadMessage()` returns an error; missing
> optional fields (e.g. absent `Date`) are warnings, not failures."

IMPLEMENTATION.md (batch import):
> "If the fallback [mtime or mbox From-line timestamp] is also unavailable, log a warning and
> **skip the message**."

These are contradictory for the edge case where a message has no `Date` header and no usable
timestamp fallback: the requirements say it is a warning (message continues to be imported), the
implementation says skip the message. One document must yield to the other.

**Affected:** REQUIREMENTS.md (Batch Import error handling), IMPLEMENTATION.md (Batch Import
implementation notes).

*Decision:* log a warning and skip the message.

---

## 8. "Empty Junk" via `DELETE /folders/{folder_id}/messages` silently moves to Trash

The endpoint uses two-step semantics: messages not already in Trash are **moved to Trash**, not
permanently deleted. For Trash (id=4) this is the expected outcome (messages already there are
deleted). For Junk (id=7) it means the "Empty Junk" button moves spam to Trash instead of deleting
it outright, which contradicts the typical expectation of this action and the word "Empty."

**Affected:** REQUIREMENTS.md (Web UI "Empty folder button"), openapi.yaml
(`DELETE /folders/{folder_id}/messages`).

*Decision:* Permanently delete messages from Junk.

---

## 9. Plain-text signature not specified for conversion into the HTML compose body

REQUIREMENTS.md:
- Signature field stores plain text (plain-text textarea in settings).
- On Reply/Reply-All/Forward the signature is placed at the top of the compose body.
- The compose body uses a Quill rich-text HTML editor.

There is no specification for how the plain-text signature (including the `\n-- \n` delimiter) is
converted to HTML before insertion into Quill's content model. The implementation needs to decide:
- Whether newlines become `<br>` tags or paragraph breaks.
- How special characters (`<`, `>`, `&`) are escaped.
- Whether the delimiter line gets distinct styling.

Without this, implementations will diverge on what the signature looks like in HTML replies.

**Affected:** REQUIREMENTS.md (Compose / Signature placement), IMPLEMENTATION.md (Web UI section).

*Decision:* newline becomes <br>, escape < > & with &lt; &gt; &amp;, use <hr> for delimiter line. 

---

## 10. Folder-scoped search (`folder_id` parameter) present in API but absent from requirements

`openapi.yaml` defines an optional `folder_id` query parameter on `GET /messages/search`. This
parameter does not appear anywhere in REQUIREMENTS.md, which describes only "Global full-text
search results as a message list."

It is unclear whether:
- This is an intentional v1 feature that was omitted from the requirements by accident.
- The UI should expose folder-scoped search (e.g. a folder selector in the search form).

**Affected:** REQUIREMENTS.md (Search view), openapi.yaml (`GET /messages/search`).

*Decision:* Keep this feature and add it to REQUIREMENTS.md and expose it in web UI.

---

## 11. Draft `from_addr` storage when `identity_id` is changed via PUT

The `messages` table has `from_addr TEXT NOT NULL`. When a draft is created, `from_addr` must be
populated from the selected (or default) identity. When a user subsequently changes the identity
via `PUT /api/v1/drafts/{id}` (which replaces the stored `identity_id`), the spec does not state
whether `from_addr` in the messages row is also updated.

If `from_addr` is not updated, the Drafts message list (`GET /folders/3/messages`) will show the
old from address. If it is updated, the update logic must resolve the identity at every PUT.

The spec should explicitly define this mapping.

**Affected:** IMPLEMENTATION.md (draft save/update logic, messages schema).

*Decision:* Update `from_addr`.

---

## 12. Bulk delete response format inconsistent with folder-empty response

`DELETE /messages` (bulk) returns `{"deleted": integer}` — a single count.  
`DELETE /folders/{folder_id}/messages` returns `{"moved_to_trash": integer, "permanently_deleted": integer}`.

Both endpoints share the same two-step delete semantics (move to Trash or permanently delete
depending on current folder). The bulk endpoint discards useful information (caller cannot tell how
many were trashed vs. permanently deleted). The formats should be consistent.

**Affected:** openapi.yaml (`DELETE /messages`, `DELETE /folders/{folder_id}/messages`).

*Decision:* Return {"moved_to_trash": integer, "permanently_deleted": integer}` from both endpoints. 

---

## 13. `PATCH /folders/{id}` rename of built-in folder — error message not specified

The spec says built-in folders (ids 1–7) may only have their `position` updated, and `PATCH`
returns `400` if a rename is attempted. No error message string is specified. Without a canonical
message, implementations will return inconsistent error text.

**Affected:** openapi.yaml (`PATCH /folders/{id}`).

*Decision:* `{"error": "built-in folders cannot be renamed"}`.

---

## 14. Outgoing contact upsert: name from address field not specified

REQUIREMENTS.md says outgoing mail upserts To/Cc/Bcc addresses into contacts. For incoming mail
the name from the `From` header is used (only when the stored name is empty). The equivalent rule
for outgoing contacts (whether the display name in the address string, e.g. "John Doe
<john@example.com>", is extracted and stored as the contact name) is not stated.

**Affected:** REQUIREMENTS.md (Contacts), IMPLEMENTATION.md (Contacts Upsert, Outgoing Mail).

*Decision:* Make the outgoing mail behavior consistent with the incoming mail behavior.
