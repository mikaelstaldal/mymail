# Open Issues

## I-01: Import mode slug lookup omits `junk` (and other built-in folders)

**Files:** REQUIREMENTS.md, IMPLEMENTATION.md

REQUIREMENTS.md and IMPLEMENTATION.md specify that `-import` performs slug-based folder lookup for only `inbox`, `sent`, `drafts`, and `trash`. The remaining built-in folders (`junk`, `scheduled`, `snoozed`) are not in the slug lookup list. If a user passes `junk` as the `<folder>` argument, the implementation falls through to the user-folder name search, which only covers `id >= 100`. Since no user folder named "junk" exists, a new user folder named "junk" would be created instead of targeting the built-in Junk folder (id=7).

`junk` is a legitimate and common import target (migrating a spam folder from Thunderbird). `scheduled` and `snoozed` are not valid import targets (they have semantic fields — `send_at`/`snoozed_until` — that won't be populated), so the spec should explicitly reject them with an error.

**Resolution needed:** Add `junk` to the slug-based lookup. Explicitly reject `scheduled` and `snoozed` as import folder targets with an error message.


## I-02: `has_external_images` computation not specified for outgoing/sent messages

**Files:** IMPLEMENTATION.md

IMPLEMENTATION.md states the `has_external_images` flag is computed in "both the LDA pipeline and the batch import pipeline." It is not mentioned for the outgoing mail pipeline (sent messages stored in the Sent folder). The flag controls the "Load external images" UI button. Since the Sent folder is viewable, a sent message containing external image URLs would display without a "Load external images" option if the flag is not computed at send time.

**Resolution needed:** Clarify whether `has_external_images` is also computed when storing a message in the Sent folder (and in the Scheduled folder). Given consistency with LDA/import, it should be.


## I-03: Reply-All "minus own address" is ambiguous

**Files:** REQUIREMENTS.md

The pre-population table for Reply-All states:

- **To:** Same as Reply, minus own address
- **Cc:** Original To + Cc minus own address

"Own address" is not defined. There are two interpretations:

1. Only the selected From identity's address is removed.
2. All identity addresses are removed (regardless of which is selected as From).

Most email clients remove all known sender addresses (interpretation 2) to avoid emailing oneself on any identity. But the spec is silent on this.

**Resolution needed:** Specify whether "own address" means the selected From identity's address only, or all identity addresses.


## I-04: Identity deletion cleanup query is overly broad

**Files:** IMPLEMENTATION.md

The cleanup query run after deleting an identity is:

```sql
UPDATE messages SET from_addr = '' WHERE identity_id IS NULL AND folder_id = 3
```

`identity_id` is NULL in two distinct cases for draft rows:
1. The draft was created with an explicit `identity_id` that was just deleted (set to NULL by `ON DELETE SET NULL`). Clearing `from_addr` here is correct.
2. The draft was created without specifying `identity_id` (defaulted to NULL), in which case `from_addr` was set to the *default* identity's address at creation time. Clearing `from_addr` here is wrong when the deleted identity is *not* the default — the draft's `from_addr` still refers to a valid identity.

The consequence is that deleting any non-default identity clears `from_addr` on all drafts that used the "use default identity" convention, even though those drafts are unaffected.

**Resolution needed:** The cleanup should distinguish the two cases. One approach: only clear `from_addr` when the deleted identity's address matches the existing `from_addr`: `UPDATE messages SET from_addr = '' WHERE identity_id IS NULL AND folder_id = 3 AND from_addr = ?` (binding the deleted identity's address).


## I-05: Thread algorithm claim about generated Message-IDs contradicts import mode

**Files:** IMPLEMENTATION.md

The thread algorithm section states: "Messages that initially lacked a `Message-ID` header are assigned a generated ID at storage time." This is accurate for LDA mode, which always generates a `<uuid@domain>` ID for messages missing one.

However, import mode explicitly allows NULL `message_id` values: REQUIREMENTS.md states "Messages without a `Message-ID` are always imported (null Message-IDs are never treated as duplicates of each other)." IMPLEMENTATION.md does not mention generating Message-IDs during import.

The result is a misleading claim in the thread algorithm section that does not hold for imported messages.

**Resolution needed:** Either (a) extend LDA-style Message-ID generation to import mode, or (b) correct the thread algorithm description to note that the generated-ID guarantee only applies to LDA mode, and that imported messages with NULL `message_id` fall through to subject-based threading.


## I-06: `PUT /api/v1/drafts/{id}` — folder_id check not specified

**Files:** IMPLEMENTATION.md, openapi.yaml

`POST /api/v1/drafts/{id}/send` explicitly checks that the message is in the Drafts folder (`folder_id = 3`) and returns 404 if not. No equivalent check is documented for `PUT /api/v1/drafts/{id}` or `DELETE /api/v1/drafts/{id}`.

Under normal operation a draft always has `folder_id = 3`, but a buggy client or race could call these endpoints with a non-draft message ID. The expected behavior (404, 400, or silent success) is unspecified.

**Resolution needed:** Specify that `PUT /api/v1/drafts/{id}` and `DELETE /api/v1/drafts/{id}` return 404 when the message exists but `folder_id ≠ 3`.


## I-07: Snooze creation SQL not documented in IMPLEMENTATION.md

**Files:** IMPLEMENTATION.md

IMPLEMENTATION.md provides explicit SQL for the cancel-snooze handler and the scheduler's snooze-expiry UPDATE, but does not document the SQL for `POST /messages/{id}/snooze`. This is the most complex snooze mutation because it must handle two paths differently:

- **First snooze** (message not in Snoozed): move to Snoozed, set `snoozed_until`, record `snooze_folder = current folder_id`.
- **Re-snooze** (message already in Snoozed): update `snoozed_until` only; do NOT change `snooze_folder`.

Without explicit SQL, an implementer might use a single UPDATE that overwrites `snooze_folder` on re-snooze, violating the "original return folder is preserved across reschedules" invariant.

**Resolution needed:** Add the snooze creation SQL to IMPLEMENTATION.md, with separate cases for first-snooze and re-snooze (or a conditional expression that preserves `snooze_folder` when `folder_id = 6`).


## I-08: Concurrent LDA `INSERT OR IGNORE` — exit code not specified for the duplicate-by-race case

**Files:** IMPLEMENTATION.md

The LDA duplicate detection sequence is:

1. `SELECT EXISTS(...)` — if true, exit 0 (skip duplicate).
2. Proceed with parsing, spam detection, filter evaluation.
3. `INSERT OR IGNORE` — silently skips if a concurrent LDA inserted the same message between steps 1 and 3.

After step 3, if the INSERT was silently ignored (zero rows changed), the message was not actually stored, but no error occurred. The spec does not state what exit code the LDA should use in this race case. Exit 0 is the correct choice (the message is effectively already delivered), but it is not documented.

**Resolution needed:** Specify that the LDA checks `changes()` (or equivalent) after the INSERT and treats 0-rows-changed as success (exit 0), since a concurrent LDA already stored the message.


## I-09: `DELETE /folders/{folder_id}/messages` UI exposure vs. API capability mismatch

**Files:** REQUIREMENTS.md, openapi.yaml

REQUIREMENTS.md states "Trash and Junk views show an **Empty** button," implying only those two folders expose bulk-delete in the UI. The API endpoint `DELETE /folders/{folder_id}/messages` accepts any folder except Scheduled (5), Snoozed (6), and Drafts (3). The UI never exposes the button for Inbox, Sent, or user-created folders — but the API is fully capable.

This isn't a bug per se, but the REQUIREMENTS.md section is ambiguous: it's unclear whether other folders *intentionally* lack the UI button (and the API is future-proofing) or whether the REQUIREMENTS simply forgot to address them.

**Resolution needed:** Explicitly state whether the Empty button is limited to Trash and Junk in v1 by design, or whether it should also appear in other folders. Clarify whether API-only clients may empty non-Trash/Junk folders and what the intended two-step semantics are (move to Trash) for those cases.


## I-10: `PATCH /messages/{id}` — moving a message to Junk doesn't mark it as read

**Files:** openapi.yaml, REQUIREMENTS.md

`POST /messages/{id}/mark-junk` moves to Junk **and** marks as read. `PATCH /messages/{id}` with `folder_id: 7` would also move to Junk but would not mark the message as read (read state is only changed if `read: true` is also included in the PATCH body).

The UI shows a "Mark as junk" button that calls the dedicated endpoint, so read-marking happens correctly via the UI. However, an API client using PATCH to move a message to Junk would produce a different result. The spec does not forbid using PATCH to set `folder_id = 7`.

**Resolution needed:** Either (a) document that PATCH to `folder_id = 7` does not auto-mark-read (and that API clients must use the dedicated endpoint for the full mark-junk semantics), or (b) make PATCH auto-mark-read when the destination is Junk.


## I-11: Search `q` whitespace trimming uses "ASCII whitespace" — inconsistent with other trim operations

**Files:** IMPLEMENTATION.md, openapi.yaml

The search endpoint returns 400 when `q` "contains only ASCII whitespace after trimming." All other field trimming in the spec (folder names, filter names, contact names, identity names) does not specify ASCII-only trimming — it uses generic "whitespace trimming" or `TRIM()`. SQLite's `TRIM()` removes ASCII whitespace only, but Go's `strings.TrimSpace` removes Unicode whitespace. The spec is inconsistent about which convention to use for search.

**Resolution needed:** Clarify whether the search `q` check (and other trim operations) should use ASCII-only whitespace trimming or Unicode whitespace trimming, and use a consistent term throughout the spec.
