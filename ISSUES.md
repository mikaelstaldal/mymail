# Open Issues

## REST API — missing request/response shapes

The SPEC defers all payload details to `openapi.yaml`. Verify or add to the OpenAPI spec:

- Bulk update `PATCH /api/v1/messages` — request body shape (array of IDs + fields to update?)
- Bulk delete `DELETE /api/v1/messages` — request body shape
- `POST /api/v1/messages/{id}/snooze` — request body (`snoozed_until`? `snooze_folder`?)
- `POST /api/v1/drafts` / `PUT /api/v1/drafts/{id}` — which fields are required vs optional?
- `GET /api/v1/folders` response — does it include `unread_count` per folder? (implied by notification polling but not defined in the API section)
- `GET /api/v1/messages/search` — query parameter name, field-scoped search, response envelope

## REST API — pagination unspecified

Message list (`GET /api/v1/folders/{folder_id}/messages`), search results, and contact list (`GET /api/v1/contacts`) need a defined pagination strategy: page size, cursor vs offset, response envelope shape (e.g. `{ "items": [...], "total": N, "next_cursor": "..." }`).

## REST API — thread algorithm unspecified

`GET /api/v1/messages/{id}/thread` — define what constitutes a thread. Options:
- Transitive closure of `References` / `In-Reply-To` headers
- All messages sharing the same root `Message-ID`
- Subject-based grouping as fallback when headers are absent

## REST API — GET with side effect

`GET /api/v1/messages/{id}` auto-marks the message as read. Clarify:
- Is this always applied, or only when the caller does not pass a flag to suppress it?
- Consider adding `?mark_read=false` to allow clients to fetch without side effects (e.g. for print/export).

## REST API — import endpoint semantics

`POST /api/v1/messages/import` — specify:
- Request content type (`message/rfc822`? `multipart/form-data` with a folder field?)
- Whether spam detection and user filters are applied (like LDA) or bypassed (like batch import)

## REST API — message list ordering

`GET /api/v1/folders/{folder_id}/messages` — specify sort order. Always `date DESC`? Any `sort` parameter?

## Frontend — URL/routing strategy

No routing scheme is specified. Decide before implementation:
- Hash-based (`/#/inbox`, `/#/message/123`)
- History API (`/inbox`, `/message/123`) — requires server to serve `index.html` on all paths

## Frontend — compose HTML body editor

The spec mentions an "optional HTML body toggle" in the compose view but does not define what it means. Decide:
- Raw HTML textarea (power-user only)
- Rich-text WYSIWYG editor (e.g. using `contenteditable`)
- No HTML compose at all (plain text only sent as `text/plain`)

## Frontend — timezone and date display format

Messages are stored as UTC timestamps. Specify:
- Whether dates are displayed in browser local time or a user-configurable timezone
- Display format: relative ("2 hours ago"), absolute ("Apr 3, 14:14"), or adaptive (relative for recent, absolute for older)

## Frontend — thread display UI

The message detail view "shows thread if references chain exists" but the visual treatment is not defined. Specify:
- Collapsed conversation list below the current message (click to expand each)
- Expanded conversation (all messages shown in sequence)
- Flat list of links to related messages

## Frontend — settings navigation structure

Eleven settings-like views are described (filter management, identity management, spam filter settings, preferences, contact management, folder management) but their placement in the UI is not defined. Specify:
- Which live behind a settings icon/modal
- Which are top-level sidebar entries
- Whether there is a settings page with tabs

## Frontend — error handling UX

No guidance on how API errors are presented to the user. Specify:
- Toast/snackbar notifications for transient errors
- Inline error messages on forms
- Retry behaviour on network failure
