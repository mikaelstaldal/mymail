# Open Issues

Issues that had obvious solutions have been addressed in SPEC.md and openapi.yaml. The following
issues remain open because they require non-trivial design decisions or are intentional scope
limitations.

## Security

---

## Backend / Data Model

### BE-10: No "re-run filters" endpoint
Filters are applied at LDA delivery time only. If a user adds a new filter, existing inbox messages
that match it are not moved automatically. This is a deliberate design decision (mirrors the
"simple client" philosophy), but should be documented explicitly in the Key Design Decisions section.

### BE-12: Bulk operations return 404 if any ID is missing — no partial success
If the UI holds a stale reference to a deleted message, an entire bulk operation fails with 404.
For a single-user app with a single active session this is unlikely, but the spec should document
this as a known limitation and indicate that the UI should refresh the folder view after a 404 on
a bulk operation.

### BE-16: Search lacks date-range filtering
`GET /api/v1/messages/search` has no `date_from` / `date_to` parameters. This significantly limits
search utility for large mailboxes. Should be documented as a known limitation or added as a future
enhancement.

---

## Frontend / API Usability

### FE-2: Thread endpoint causes N+1 fetches to expand all messages
`GET /api/v1/messages/{id}/thread` returns `MessageSummary` objects with no body. Expanding all
entries in a long thread requires N individual `GET /api/v1/messages/{entryId}` calls. This is
an accepted design trade-off (threads are uncommon in personal email), but should be documented.

### FE-3: No way to remove a single attachment from a draft
The draft update endpoints replace all attachments wholesale. Removing one attachment from a large
draft requires re-uploading all remaining ones. This is an architectural limitation of the current
multipart design. A future `DELETE /api/v1/drafts/{id}/attachments/{attachment_id}` endpoint would
address this.

---

## Usability / UX

### UX-2: No rich-text (WYSIWYG) compose editor
The compose form provides only a raw HTML `<textarea>`. A WYSIWYG editor is out of scope for the
initial version. This should be noted explicitly in the spec as a known limitation.

### UX-3: No mobile / responsive layout
The layout is a fixed three-pane desktop design. Mobile support is out of scope for the initial
version. Document this explicitly.

### UX-5: No inline attachment preview
Only attachment download is specified. Inline preview (images, PDFs) is out of scope for the
initial version.

### UX-8: No cross-folder "Starred" virtual folder
Flagged messages can only be filtered within a single folder. A cross-folder Starred view is a
potential future enhancement; document as a known limitation.
