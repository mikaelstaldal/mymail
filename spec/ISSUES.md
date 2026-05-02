# Open Issues

## I-01 — `DELETE /messages/{id}`: missing 400 response in openapi.yaml

**Source:** openapi.yaml vs. the endpoint description

The description of `DELETE /messages/{id}` states that messages currently in Scheduled (5),
Snoozed (6), or Drafts (3) are rejected with `400`. However, the `responses` object only
lists `204` and `404` — the `400` status code is absent.

**Fix:** Add `'400': $ref: '#/components/responses/BadRequest'` to the responses for
`DELETE /messages/{id}`.

---

## I-02 — `DELETE /messages/{id}`: wrong cancel-schedule endpoint path in description

**Source:** openapi.yaml

The description of `DELETE /messages/{id}` says:
> "Use the dedicated endpoints to cancel a schedule (`DELETE /messages/{id}/scheduled`)"

The correct endpoint is `DELETE /scheduled/{id}` (base path `/api/v1`), not
`DELETE /messages/{id}/scheduled`. This path does not exist in the API.

**Fix:** Change the reference in the description to `DELETE /scheduled/{id}`.

---

## I-03 — `PUT /identities/{id}`: undefined behavior when `is_default: false` on the current default

**Source:** REQUIREMENTS.md, IMPLEMENTATION.md, openapi.yaml

The spec requires "exactly one identity is default at all times." `PUT /identities/{id}` accepts
an optional `is_default` boolean. Neither REQUIREMENTS.md nor IMPLEMENTATION.md addresses the
case where `is_default: false` is supplied for the identity that is currently the default,
leaving no default identity.

Two reasonable interpretations exist:
1. Reject with 400 ("cannot remove default flag without designating a new default").
2. Treat an absent or `false` `is_default` as "do not change the current default status" (i.e.
   the field only acts when `true`).

**Fix:** Pick one interpretation and document it consistently in REQUIREMENTS.md, IMPLEMENTATION.md,
and openapi.yaml.

---

## I-04 — Thread algorithm: `truncated` flag when loop terminates at the 1000-row cap

**Source:** IMPLEMENTATION.md

The iterative transitive-closure loop stops when `len(foundIDs) >= 1000`. At that point, the
full transitive closure has not been computed, so it is unknown whether the actual thread
contains more than 1000 messages. Yet the spec defines `truncated: true` as meaning the thread
exceeds 1000 messages — a fact that cannot be verified once the loop is stopped early.

**Fix:** Clarify that `truncated` should be set to `true` whenever the loop terminated because
the cap was reached (i.e. `len(foundIDs) == 1000` at termination), not only when the full
closure is known to exceed 1000. This is the only practical heuristic given the early exit.

---

## I-05 — Snooze `until` validation: "at least 1 minute" vs. "> now + 60 seconds"

**Source:** REQUIREMENTS.md vs. IMPLEMENTATION.md

REQUIREMENTS.md §Web UI says:
> "The snooze `until` time must be at least 1 minute ahead of the current server time"
  (≥ 60 seconds, inclusive)

IMPLEMENTATION.md §Background Scheduler says:
> "Validate that `until > now + 60 seconds`; return 400 otherwise"
  (strictly greater than 60 seconds, exclusive)

A value of exactly `now + 60s` is valid per REQUIREMENTS but invalid per IMPLEMENTATION.

**Fix:** Pick one and update both documents to match. The `POST /messages/{id}/snooze`
description in openapi.yaml also says "at least 1 minute" — all three must agree.

---

## I-06 — Reply-All: "all own identity addresses" not defined

**Source:** REQUIREMENTS.md

The compose pre-population table says the To and Cc fields for Reply-All exclude
"all own identity addresses," but the term is not defined. It is ambiguous whether this means:
- Only the address of the selected From identity, or
- The addresses of **all** identities in the system.

The standard email client behaviour is to exclude all own addresses (all identities), but this
should be stated explicitly.

**Fix:** Add a definition in REQUIREMENTS.md §Compose stating that "all own identity addresses"
means the set of `address` values from all identity rows, not just the selected From identity.

---

## I-07 — `POST /messages/send` and `POST /messages/send-with-attachments`: undefined behavior for non-existent `identity_id`

**Source:** IMPLEMENTATION.md, openapi.yaml

IMPLEMENTATION.md §Draft Auto-Save Logic defines that `POST /drafts` and `PUT /drafts/{id}`
return `400 {"error": "identity not found"}` when `identity_id` does not match any existing
identity. No equivalent rule is stated for `POST /messages/send` or
`POST /messages/send-with-attachments`.

**Fix:** Add an explicit rule for the send endpoints: if `identity_id` is supplied but does not
match any existing identity, return `400 {"error": "identity not found"}` (consistent with the
draft endpoints). Document this in IMPLEMENTATION.md and in the openapi.yaml description.

---

## I-08 — Navigate-away draft save before first auto-save tick (Reply / Reply-All / new compose)

**Source:** IMPLEMENTATION.md §Draft Auto-Save Logic

IMPLEMENTATION.md states that for Reply, Reply-All, and new compose the first `POST /drafts`
is deferred until the first 30-second tick. REQUIREMENTS.md says navigate-away triggers an
immediate draft save. If the user navigates away before the first tick, no draft ID exists yet.

The spec does not address how the navigate-away save should behave in this case: should it
create a new draft via `POST /drafts` (same as the deferred tick would do), and if that
POST fails, what should happen?

**Fix:** Add a clause to IMPLEMENTATION.md: on navigate-away, if no draft ID exists yet,
perform a `POST /drafts` immediately (same as the first tick would). If this POST fails, show
a brief warning but do not block navigation (consistent with the documented failure behaviour
for the navigate-away save).

---

## I-09 — `PATCH /messages` (bulk): no-op case when neither `read` nor `flagged` is present

**Source:** openapi.yaml, IMPLEMENTATION.md

`PATCH /messages/{id}` (single) is documented as accepting an empty body `{}` as a valid no-op
returning 200. `PATCH /messages` (bulk) requires `ids` but makes `read` and `flagged` optional.
The spec does not state what happens when `ids` is supplied but neither `read` nor `flagged` is
in the body (the bulk equivalent of a no-op).

**Fix:** Add a clarification in the openapi.yaml description: when neither `read` nor `flagged`
is supplied, all specified messages are considered already matching, `updated` is 0, and 200 is
returned. This mirrors the single-message no-op behaviour.

---

## I-10 — `PATCH /folders/{id}` on built-in folders: behavior when both `name` and `position` are supplied

**Source:** openapi.yaml

The description says supplying `name` for a built-in folder returns `400`. But it does not
specify whether a request supplying both `name` and `position` for a built-in folder:
(a) rejects entirely with 400, or
(b) updates `position` and then returns 400.

**Fix:** Clarify in the description that the entire request is rejected with 400 when `name` is
supplied for a built-in folder, regardless of whether `position` is also present.

---

## I-11 — Identity `address` casefolding not documented in openapi.yaml

**Source:** openapi.yaml vs. REQUIREMENTS.md

REQUIREMENTS.md states that identity addresses are stored in Unicode-simple-casefolded form.
The `POST /contacts` description in openapi.yaml mentions "The `address` is stored in
Unicode-simple-casefolded form." The equivalent `POST /identities` and `PUT /identities/{id}`
descriptions do not mention this normalization.

**Fix:** Add the same casefolding note to the `POST /identities` and `PUT /identities/{id}`
descriptions in openapi.yaml.

---

## I-12 — mbox `From ` timestamp: timezone variants not handled

**Source:** IMPLEMENTATION.md §Batch Import

IMPLEMENTATION.md specifies the Go layout `"Mon Jan _2 15:04:05 2006"` for parsing mbox
`From ` separator timestamps. Many real-world mbox files include a three-letter timezone
abbreviation (e.g. `Mon Jan  2 15:04:05 CST 2006`) or a numeric offset. This format will fail
to parse with the specified layout, triggering the mtime fallback for the entire file. This
means all messages in the file get the file's mtime as their date rather than their actual
send time.

**Fix:** Try additional layouts in order before falling back to mtime, e.g.:
1. `"Mon Jan _2 15:04:05 2006"` (no timezone)
2. `"Mon Jan _2 15:04:05 MST 2006"` (three-letter timezone abbreviation)
3. `"Mon Jan _2 15:04:05 -0700 2006"` (numeric offset)

Document the ordered list of tried layouts in IMPLEMENTATION.md.

---

## I-13 — Maildir import: message ordering within a directory not specified

**Source:** REQUIREMENTS.md §Batch Import

REQUIREMENTS.md says "Messages are imported in source order (oldest first within each
file/directory)." For Maildir, files within `new/` and `cur/` have no guaranteed filesystem
ordering. The spec does not define what "source order" means for Maildir.

**Fix:** Specify the ordering explicitly in IMPLEMENTATION.md, e.g.: sort Maildir keys
lexicographically (which approximates delivery order since standard Maildir filenames begin
with a Unix timestamp), or sort by file mtime ascending.

---

## I-14 — `DraftRequest.identity_id`: no-identities-exist case not covered in IMPLEMENTATION.md

**Source:** openapi.yaml vs. IMPLEMENTATION.md

`DraftRequest.identity_id` in openapi.yaml says: "If no identities exist at all (first-run
state), `from_addr` is stored as empty string and 201 is returned."

IMPLEMENTATION.md §Draft Auto-Save Logic only documents the case where identities exist but
none is marked default (→ 500). The no-identities-at-all case is not mentioned.

**Fix:** Add an explicit clause to IMPLEMENTATION.md: when no identity rows exist in the
database, `identity_id` is stored as NULL and `from_addr` as empty string; 201 is returned
without error.

---

## I-15 — `send_at` and snooze `until`: timezone normalization not stated

**Source:** IMPLEMENTATION.md, openapi.yaml

The `send_at` field in `SendRequest`/`DraftRequest` and the `until` field in the snooze
request are typed `format: date-time`. Clients may send values with non-UTC offsets (e.g.
`2025-06-01T15:00:00+02:00`). IMPLEMENTATION.md states all timestamps are stored as UTC RFC
3339 strings but does not explicitly state that incoming `date-time` values are normalized to
UTC before storage and before arithmetic comparisons (`> now + 60s`, `> now + 60s`).

**Fix:** Add a clause to IMPLEMENTATION.md: all incoming `date-time` fields are normalized to
UTC before storage and before any threshold comparison.

---

## I-16 — Signature conversion: Windows-style line endings not addressed

**Source:** IMPLEMENTATION.md §Signature Plain-Text → HTML Conversion

The conversion algorithm checks for lines whose exact content is `-- ` (two hyphens, one
space) and replaces `\n` with `<br>`. If the stored signature contains Windows-style line
endings (`\r\n`), the delimiter detection matches `-- \r` rather than `-- `, and `\r`
characters appear literally in the HTML output.

**Fix:** Normalize line endings to `\n` before applying the conversion algorithm, or
explicitly strip `\r` from each line before the delimiter check.
