# Open Issues

The following issues require deliberate design decisions before they can be resolved.

---

## Consistency (SPEC.md ↔ openapi.yaml)

### CON-2: `send_failure_count` not exposed in `MessageSummary` or `MessageDetail`
SPEC.md documents `send_failure_count` in the `messages` table and uses it to drive the retry
logic (move to Drafts after 3 failures). The OpenAPI schemas surface only the derived boolean
`send_failed`, giving API consumers no visibility into how many attempts remain before the message
is abandoned.

**Decision needed:** Should `send_failure_count` (integer) be added to `MessageSummary` and
`MessageDetail`, replacing or supplementing the boolean `send_failed`? Adding the count allows
third-party consumers to display granular retry state ("2 of 3 attempts failed") but exposes an
implementation detail. Keeping only the boolean is simpler; the UI can distinguish the
"still retrying" vs "exhausted" states using `folder_id` context (Scheduled vs Drafts).

---

## API Design

### API-1: `POST /messages/{id}/mark-junk` and `mark-not-junk` should be `PATCH`
These endpoints update the `folder_id` of an existing resource — a state mutation, not a
creation. Using POST is semantically incorrect; PATCH (either on the dedicated path or merged
into `PATCH /messages/{id}`) is appropriate.

**Decision needed:** Change the HTTP method to PATCH, or keep POST (which is common for named
action endpoints, e.g. GitHub's star/unstar)? If changed to PATCH, consider whether these should
remain as distinct paths or be merged into `PATCH /messages/{id}`.

### API-2: Reorder endpoints use `POST` instead of `PATCH`
`POST /filters/reorder` and `POST /identities/reorder` are idempotent bulk updates. POST is
non-idempotent and signals resource creation. These should use PATCH.

**Decision needed:** Change to PATCH, or keep POST? Same trade-off as API-1.

### API-3: No `/folders/reorder` endpoint
Folders have a `position` field and the same ordering need as filters and identities, but there
is no reorder endpoint for folders. This is an inconsistency.

**Decision needed:** Add `POST /api/v1/folders/reorder` (or PATCH if API-2 is resolved first)?

### API-4: Bulk `PATCH /messages` returns `{"updated": n}`; single `PATCH /messages/{id}` returns full resource
The asymmetry makes client code inconsistent: after a bulk update the UI must re-fetch to learn
the new state, while after a single update it does not. Either both should return the updated
resource(s) or both should return only counts, with the choice documented explicitly.

**Decision needed:** Should bulk PATCH return an array of `MessageSummary` (consistent with
single, but potentially large), or should single PATCH be changed to return `{"updated": 1}`
(consistent with bulk, but requires a re-fetch)?

### API-5: `DELETE /messages` (bulk) returns `200` with body; `DELETE /messages/{id}` returns `204`
Inconsistent conventions for deletes. Both should follow the same pattern — either `204 No
Content` for all deletes or a body for all deletes.

**Decision needed:** Change bulk DELETE to return `204` (no body), or change single DELETE to
return `200` with `{"deleted": 1}`? Note that `DELETE /folders/{folder_id}/messages` also
returns `200` with a body, so a "204 for all deletes" choice would need to cover that endpoint too.

### API-6: `DELETE /messages/{id}/snooze` returns `{id, folder_id}`; `POST` returns `{id, folder_id, snoozed_until}`
The response shapes for the two snooze operations are different. The issue says both should
ideally return the updated `MessageSummary`.

**Decision needed:** Should both operations return the full `MessageSummary` (requires the
server to query the updated row), or should DELETE return `{id, folder_id, snoozed_until: null}`
(minimal change for consistency), or is the current asymmetry acceptable?
