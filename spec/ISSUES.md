# Open Issues

Issues found by cross-reviewing REQUIREMENTS.md, ARCHITECTURE.md, IMPLEMENTATION.md, and openapi.yaml.

---

## Inconsistencies Between Documents

### 1. `mark-junk` availability in Snoozed folder

**REQUIREMENTS.md** says "All other views show a **Mark as junk** button" (the only exception being the Junk folder view itself, which shows **Not junk** instead). This implies mark-junk is available when the message is in the Snoozed folder.

**openapi.yaml** (`POST /messages/{id}/mark-junk`) returns 400 for Snoozed (id=6) and states "The schedule or snooze must be cancelled before marking as junk."

**Resolution needed:** Either update REQUIREMENTS.md to document that mark-junk is unavailable for Snoozed/Scheduled/Drafts messages, or relax the openapi.yaml restriction for Snoozed (which is the only arguably useful case — snoozed messages are incoming mail, not outgoing drafts).

---

### 2. `in_reply_to` and `message_id` bracket format not documented in openapi.yaml

**IMPLEMENTATION.md** specifies:
- `MessageDetail.message_id` is serialized **without** angle brackets.
- `MessageDetail.in_reply_to` is serialized **without** angle brackets.
- `MessageDetail.references` elements **re-add** angle brackets on serialization ("as it appears in the header, including angle brackets").

**openapi.yaml** documents the `references` bracket behaviour in a description comment but says nothing about `message_id` or `in_reply_to` being bracket-free. API consumers reading only the OpenAPI spec will not know about this asymmetry and may produce incorrect threading headers when building clients.

**Resolution needed:** Add field-level descriptions to `MessageDetail.message_id` and `MessageDetail.in_reply_to` in openapi.yaml stating the bracket-stripped format.

---

### 3. Re-snoozing not covered in REQUIREMENTS.md

**openapi.yaml** documents re-snoozing: "if the message is already in Snoozed, the request updates `snoozed_until` and returns 200 with `folder_id=6`; `snooze_folder` is not changed."

**REQUIREMENTS.md** mentions the Snooze button is available when the message is in Snoozed but does not define the re-snooze semantics (what changes, what is preserved).

**Resolution needed:** Add a re-snooze paragraph to REQUIREMENTS.md or explicitly reference the openapi.yaml behaviour.

---

### 4. Snooze minimum `until` duration only in openapi.yaml

**openapi.yaml** requires `until` to be "at least 1 minute ahead of the current server time" and returns 400 otherwise.

**REQUIREMENTS.md** mentions the Snooze button and the expiry behaviour but does not state any minimum snooze duration.

**Resolution needed:** Add the 1-minute minimum to REQUIREMENTS.md.

---

### 5. `message_id` presence in `MessageSummary` vs `MessageDetail`

In **openapi.yaml**, `MessageSummary.message_id` is **not** in the `required` array (the field may be absent from the response). `MessageDetail.message_id` **is** in `required` (present but nullable).

This asymmetry is undocumented and may surprise client implementors who encounter absent vs. null `message_id` across the two response shapes.

**Resolution needed:** Decide whether `message_id` should be required (nullable) in `MessageSummary` as well, or explicitly document why it is optional there.

---

## Missing Details

### 6. `cancel snooze` fallback when `snooze_folder` is deleted

**openapi.yaml** (`DELETE /messages/{id}/snooze`) says the message is returned to "its original folder immediately" and that `snoozed_until` and `snooze_folder` are cleared. It does not specify what happens when the original `snooze_folder` was deleted after the snooze was created.

**IMPLEMENTATION.md** documents this fallback only for the scheduler: "the scheduler uses `COALESCE(snooze_folder, 1)` so the message returns to Inbox."

**Resolution needed:** Document in openapi.yaml and IMPLEMENTATION.md (cancel-snooze handler) that the same `COALESCE(snooze_folder, 1)` fallback applies. The response `folder_id` should reflect the actual folder the message landed in.

---

### 7. `PATCH /messages/{id}` moving to Trash does not document clearing scheduler fields

**openapi.yaml** (`DELETE /messages/{id}`) explicitly states: "When moving to Trash, `snoozed_until`, `snooze_folder`, and `send_at` are cleared in the same UPDATE."

**openapi.yaml** (`PATCH /messages/{id}`) says nothing about clearing these fields when `folder_id` is set to 4 (Trash). An implementation following only the PATCH description would leave stale scheduler fields.

**Resolution needed:** Add the same clearing clause to the `PATCH /messages/{id}` description.

---

### 8. Bulk PATCH — "from" folder restriction not stated

**openapi.yaml** (`PATCH /messages`) says "The same target restrictions as the single PATCH apply to `folder_id`: Scheduled (5), Snoozed (6), and Drafts (3) are rejected with 400." This describes the *destination* restriction.

It does not state whether messages *currently* in one of those folders can be included in a bulk PATCH (matching single-PATCH behavior where "moving to/from" those folders is forbidden).

**Resolution needed:** Clarify whether messages currently in Scheduled/Snoozed/Drafts can be included in a bulk PATCH and what happens if they are.

---

### 9. `PUT /drafts/{id}` behavior when no identities exist

**IMPLEMENTATION.md**: "If `identity_id` is absent in the PUT body, `identity_id` is set to NULL and `from_addr` is set to the default identity's address."

**REQUIREMENTS.md** permits saving drafts with no identities, but neither document specifies what happens when `PUT /drafts/{id}` is called without `identity_id` and no identities exist (there is no default to fall back to).

**Resolution needed:** Define the behavior — either permit the PUT with `from_addr` set to empty string when no identities exist, or return 400.

---

### 10. Global search includes Drafts, Scheduled, and Snoozed

**openapi.yaml** (`GET /messages/search`) excludes only Junk (id=7) from global search. Drafts (3), Scheduled (5), and Snoozed (6) are all included. Whether user-composed draft and scheduled messages should surface in global search results is unspecified in REQUIREMENTS.md.

**Resolution needed:** Confirm intent. If Drafts/Scheduled/Snoozed should be excluded, add them to the exclusion list in both the API spec and REQUIREMENTS.md.

---

### 11. `has_external_images` computation missing from LDA pipeline step list

**IMPLEMENTATION.md** (LDA Implementation) lists the parsing pipeline steps but omits computing and persisting `has_external_images`. The flag is described only under the "Web UI" subsection of IMPLEMENTATION.md.

**IMPLEMENTATION.md** (Batch Import) says "run the full LDA parsing pipeline" without calling out this step either.

**Resolution needed:** Add `has_external_images` computation explicitly to the LDA pipeline step list and the import pipeline description.

---

### 12. Reorder endpoint empty `ids` array behavior missing from openapi.yaml

**IMPLEMENTATION.md**: "An empty `ids` array is rejected with the same 'incomplete reorder' 400 (unless no entities exist at all, in which case the call is a no-op returning `updated: 0`)."

**openapi.yaml** (`PATCH /folders/reorder`, `/filters/reorder`, `/identities/reorder`) does not document this edge case.

**Resolution needed:** Add descriptions to the three reorder endpoints documenting the empty-array and no-entities-exist behaviors.

---

### 13. `send_failure_count` and `send_error` not cleared on schedule cancellation

**openapi.yaml** (`DELETE /scheduled/{id}`) moves the message to Drafts. If the message had already recorded one or two failed send attempts, `send_failure_count > 0` and `send_error` is non-null after cancellation. A cancelled-then-re-drafted message retaining `send_failed=true` in the Drafts list would be confusing.

**Resolution needed:** Specify whether `send_failure_count` and `send_error` are reset to 0/NULL when `DELETE /scheduled/{id}` cancels the message.

---

### 14. Contact autocomplete SQL (`q` parameter) not specified

**REQUIREMENTS.md** and **IMPLEMENTATION.md** document the contact list ordering SQL and the upsert SQL, but neither provides the SQL fragment for the `q` substring filter used by autocomplete.

**Resolution needed:** Add the autocomplete SQL (e.g., `WHERE LOWER(name) LIKE '%'||LOWER(?)||'%' OR LOWER(address) LIKE '%'||LOWER(?)||'%'`) to IMPLEMENTATION.md.

---

### 15. `PATCH /messages/{id}` with empty body unspecified

Neither **openapi.yaml** nor **IMPLEMENTATION.md** defines whether an empty PATCH body (`{}`) is valid (no-op returning 200) or an error (400).

**Resolution needed:** Document the expected behavior.

---

### 16. `go 1.26` in `go.mod` does not exist

**IMPLEMENTATION.md** specifies `go 1.26` in the go.mod template. Go 1.26 has not been released; the current stable release is Go 1.24.

**Resolution needed:** Update to the current stable Go version (e.g., `go 1.24`) or note that this is the minimum version required and will be updated when 1.26 ships.
