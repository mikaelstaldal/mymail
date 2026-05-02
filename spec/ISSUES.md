# Open Issues

## I-03: Reply-All "minus own address" is ambiguous

**Files:** REQUIREMENTS.md

The pre-population table for Reply-All states:

- **To:** Same as Reply, minus own address
- **Cc:** Original To + Cc minus own address

"Own address" is not defined. There are two interpretations:

1. Only the selected From identity's address is removed.
2. All identity addresses are removed (regardless of which is selected as From).

Most email clients remove all known sender addresses (interpretation 2) to avoid emailing oneself on any identity. But the spec is silent on this.

**Decision needed:** Specify whether "own address" means the selected From identity's address only, or all identity addresses.


## I-09: `DELETE /folders/{folder_id}/messages` UI exposure vs. API capability mismatch

**Files:** REQUIREMENTS.md, openapi.yaml

REQUIREMENTS.md states "Trash and Junk views show an **Empty** button," implying only those two folders expose bulk-delete in the UI. The API endpoint `DELETE /folders/{folder_id}/messages` accepts any folder except Scheduled (5), Snoozed (6), and Drafts (3). The UI never exposes the button for Inbox, Sent, or user-created folders — but the API is fully capable.

This isn't a bug per se, but the REQUIREMENTS.md section is ambiguous: it's unclear whether other folders *intentionally* lack the UI button (and the API is future-proofing) or whether the REQUIREMENTS simply forgot to address them.

**Decision needed:** Explicitly state whether the Empty button is limited to Trash and Junk in v1 by design, or whether it should also appear in other folders. Clarify whether API-only clients may empty non-Trash/Junk folders and what the intended two-step semantics are (move to Trash) for those cases.


## I-11: Search `q` whitespace trimming uses "ASCII whitespace" — inconsistent with other trim operations

**Files:** IMPLEMENTATION.md, openapi.yaml

The search endpoint returns 400 when `q` "contains only ASCII whitespace after trimming." All other field trimming in the spec (folder names, filter names, contact names, identity names) does not specify ASCII-only trimming — it uses generic "whitespace trimming" or `TRIM()`. SQLite's `TRIM()` removes ASCII whitespace only, but Go's `strings.TrimSpace` removes Unicode whitespace. The spec is inconsistent about which convention to use for search.

**Decision needed:** Clarify whether the search `q` check (and other trim operations) should use ASCII-only whitespace trimming or Unicode whitespace trimming, and use a consistent term throughout the spec.
