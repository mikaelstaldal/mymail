# Open Issues

Issues requiring a decision or further research before the spec can be finalised.

---

## Missing features

### No endpoint to update existing draft content

`POST /api/v1/drafts` creates a draft and `DELETE /api/v1/drafts/{id}` deletes one, but there is no `PUT /api/v1/drafts/{id}` (or equivalent) to update the subject, body, recipients, or attachments of an existing draft.

§5.3 says "The compose UI saves drafts via `PATCH /api/v1/messages/{id}`", but `PATCH /api/v1/messages/{id}` (§5.2) only accepts `folder_id`, `read`, and `flagged` — it cannot update body content. The auto-save loop on a 30-second timer (§13) therefore has no working endpoint to call after the initial draft is created.

Decision needed: add `PUT /api/v1/drafts/{id}` (full replace) and `PUT /api/v1/drafts-with-attachments/{id}`, or extend `PATCH /api/v1/messages/{id}` to accept content fields for messages in the Drafts folder.

### `Reply-To` header not constructed in outgoing messages

The send request body (§5.2 `POST /api/v1/messages/send`) includes a `reply_to_addr` field, and `reply_to_addr` is stored in the `messages` table. However, §9 (Outgoing Mail) lists the headers to construct (`Date`, `Message-ID`, `MIME-Version`, body) but never mentions adding a `Reply-To` header to the outgoing RFC 5322 message when `reply_to_addr` is non-empty.

---

## Ambiguous / under-specified behaviour

### Contradiction: first identity creation (§3 vs §4.5)

§3 states: "There is no CLI flag for the initial identity; the first identity is created via the web UI on first use."

§4.5 states: "The first identity is created interactively or via CLI (see §3)."

These two statements contradict each other. Decide which is correct and update the other.

### `drop` filter action missing from §4.7 schema description

§6.1 (LDA filter application) defines four actions: `move`, `trash`, `mark_read`, and `drop`. But §4.7 (`filters` table) only lists three in its **Actions** block (`move`, `trash`, `mark_read`). The `drop` action is missing from the schema section.

### PATCH rule for built-in folders only covers ids 1–4

§5.1 `PATCH /api/v1/folders/{id}` states: "Built-in folders (id 1–4) may have their `position` updated but not their `name`." There are 7 built-in folders (ids 1–7). No rule is stated for Scheduled (5), Snoozed (6), and Junk (7). Are they also name-protected? Can they be renamed at all?

### "Hidden folders" terminology inconsistency

§4.1 defines `hidden=1` as the flag for folders excluded from the normal listing. The built-in folder table shows every folder (including Scheduled and Snoozed) with `hidden=0`. Yet §5.2 `DELETE /api/v1/folders/{id}/messages` says "Returns `400` for hidden folders (Scheduled, Snoozed)." — treating Scheduled and Snoozed as "hidden" even though they have `hidden=0`.

Either Scheduled (id=5) and Snoozed (id=6) should have `hidden=1` in the schema table, or the endpoint should use a different criterion (e.g. "built-in system folders") and not call them hidden.

### Snooze validity for Junk folder not specified

§5.4 says snoozing is invalid for "Sent, Drafts, Trash, Scheduled, or Snoozed". The Junk folder is not mentioned. Is snoozing a Junk message valid? If so, what folder does it return to?

### Migration pseudocode applies future migrations on a fresh install

The pseudocode in §4:
```
if v == 0:
    -- create all tables; PRAGMA user_version = 0
if v < 1:
    -- future migration; PRAGMA user_version = 1
```
Both conditions are true when `v == 0`, so a fresh install would run the future migration immediately after the initial schema creation. The pseudocode should use `else if` / `elif` or a `while v < target` loop pattern to apply exactly one migration per version step.

### Wrong cross-reference in §5.2

`GET /api/v1/messages/{id}` says: "The HTML body is sanitized (see §7)." HTML sanitization is §10, not §7. §7 is the Background Scheduler.

### CLI synopsis missing `-import` mode

The opening CLI synopsis (§3) shows only:
```
mymail [flags]
mymail -lda [flags]
```
The `-import` mode is described immediately after but not in the synopsis. Add `mymail -import -data <dir> <mapping>...` to the synopsis for completeness.

### `DELETE /api/v1/folders/{id}/messages` response field name is misleading

The endpoint moves non-Trash messages to Trash and permanently deletes already-Trash messages. The response is `{"deleted": N}`. The count conflates "moved to Trash" with "permanently deleted", which is confusing. Consider separate counts: `{"moved_to_trash": M, "permanently_deleted": N}`, or clarify what `deleted` means in the docs.

### `bcc_addr` missing from `GET /api/v1/messages/{id}` response example

The list endpoint explicitly omits `bcc_addr` for efficiency (§5.2), implying the full message detail response includes it. But the example response for `GET /api/v1/messages/{id}` does not include `bcc_addr`. Clarify whether `bcc_addr` is returned in the full message detail.

### Draft recovery: server returns 404 for stored draft id

§13 Client-Side Storage: on page reload, if a draft `id` is stored in `localStorage` the UI fetches it from the server. The spec does not say what to do if the server returns `404` (e.g. the draft was sent or deleted in another tab). Specify the fallback behaviour (e.g. fall back to the `localStorage` state and clear the stale id).

### Contacts auto-upsert scope for incoming mail

§6 (LDA) step 7 upserts only the **sender** (`From` address) into contacts. §9 (Outgoing Mail) step 6 upserts all **recipients** (To, Cc, Bcc). No rule is given for whether the To/Cc recipients of *incoming* messages are also auto-added to contacts. Specify explicitly (likely "no", to avoid polluting contacts with every mailing list address, but worth stating).
