# Open Issues

## 1. `nullable: true` vs OpenAPI 3.1 syntax

`openapi.yaml` declares `openapi: 3.1.0` but uses `nullable: true` throughout (e.g. `MessageDetail.message_id`, `MessageDetail.send_at`, `Filter.folder_id`, etc.). In OAS 3.1, `nullable` is not a standard keyword; the correct syntax is `type: [string, "null"]`. This was a 3.0 extension.

**Impact:** Strict OAS 3.1 validators will reject these fields. ogen (the chosen code generator) may handle `nullable: true` as a compatibility shim or may not — this needs to be verified before code generation. If ogen does not support it, all nullable fields must be updated to OAS 3.1 syntax.

**Fields affected:** `MessageSummary.message_id`, `MessageDetail.message_id`, `MessageDetail.in_reply_to`, `MessageDetail.send_at`, `MessageDetail.send_error`, `MessageDetail.snoozed_until`, `MessageDetail.snooze_folder_id`, `SendRequest.send_at`, `Filter.folder_id`, `FilterRequest.folder_id`.

---

## 2. Bulk operations: behavior when some IDs are unknown

`PATCH /messages` (bulk update) and `DELETE /messages` (bulk delete) do not specify what happens when the request includes message IDs that do not exist in the database.

**Options:**
- **Silently skip** unknown IDs and return the count of actually affected rows (most REST-idiomatic).
- **Return 404** if any ID is not found (strict, but makes partial success impossible).

The single-message endpoints return 404 on unknown ID, but that behavior does not necessarily compose to bulk. A decision is needed so the service layer can be implemented consistently.

---

## 3. Snooze source folder restriction unclear

`POST /messages/{id}/snooze` states "The message must be in Inbox or a user folder." It is not specified why messages in Sent, Trash, Junk, Scheduled, or Snoozed folders cannot be snoozed.

**Questions to resolve:**
- Should Sent messages be snoozeable (e.g. snooze a sent message as a follow-up reminder)?
- Should Junk messages be snoozeable?
- Are Trash and Scheduled excluded for obvious reasons (Trash = being discarded, Scheduled = already deferred)?
- What error (400) message should be returned for disallowed source folders?

Clarify which folders are allowed/disallowed and document the rationale so the service layer can enforce it correctly.

---

## 4. Forward action: attachment copy mechanism not specified

The compose view spec (view 3) states that Forward pre-populates "copies of all original attachments as attachment rows (new rows referencing copied `attachments` records; the originals are not modified)."

There is no server-side API endpoint to copy attachments. The UI would need to:
1. Download each original attachment via `GET /api/v1/attachments/{id}`.
2. Re-upload each one with the new draft via `POST /api/v1/drafts-with-attachments` (or `PUT /drafts-with-attachments/{id}`).

This is workable but adds significant UI complexity and potentially large data transfers for large attachments. It should be confirmed as the intended approach, or a server-side copy endpoint (e.g. `POST /api/v1/messages/{id}/forward-draft`) should be added.
