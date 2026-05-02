# Open Issues

## Decisions Required

### 1. `DELETE /messages/{id}` and `DELETE /messages` (bulk) — no source-folder restriction
**Documents:** openapi.yaml  
`POST /messages/move` explicitly rejects Scheduled (5), Snoozed (6), and Drafts (3) as source folders. `DELETE /messages/{id}` and the bulk `DELETE /messages` have no such restriction. A Scheduled message deleted via `DELETE /messages/{id}` would be moved to Trash (or permanently deleted if already in Trash/Junk) without going through the cancel-schedule flow, bypassing the scheduler race guard.  
**Decision needed:** Should these delete endpoints restrict Scheduled/Snoozed/Drafts as source folders (mirroring `POST /messages/move`), or explicitly permit them? (Note: the field-clearing semantics — snoozed_until, snooze_folder, send_at are already cleared when moving to Trash — are already specified.)

---

### 2. `DELETE /messages/{id}/snooze` — read state on early cancel not specified
**Documents:** openapi.yaml, REQUIREMENTS.md, IMPLEMENTATION.md  
The scheduler marks a message unread (`read = 0`) when a snooze expires naturally. The cancel-snooze handler SQL in IMPLEMENTATION.md does not include `read = 0`. This asymmetry (natural expiry resets read; early cancel preserves read) is not documented as intentional anywhere.  
**Decision needed:** Should early snooze cancellation also mark the message unread (matching natural expiry), or preserve the current read state? If intentional, add a note; if a bug in the SQL, add `read = 0` to the cancel-snooze UPDATE.

---

### 3. `PUT /identities/{id}` address change — stale `from_addr` in drafts
**Documents:** IMPLEMENTATION.md  
`DELETE /identities/{id}` includes a cleanup step to clear `from_addr` on affected drafts. `PUT /identities/{id}` (which can change the identity's address) has no equivalent cleanup. Drafts with `identity_id = X` will retain the old `from_addr` until the next draft auto-save triggers a PUT that re-resolves `identity_id → address`.  
**Decision needed:** Either add `UPDATE messages SET from_addr = new_address WHERE identity_id = ? AND folder_id = 3` to the `PUT /identities/{id}` handler, or document this as a known transient inconsistency that resolves on the next draft save.

---

### 4. `PATCH /messages/{id}` moving to Junk — snooze/send_at fields not cleared
**Documents:** openapi.yaml, IMPLEMENTATION.md  
Moving to Trash (folder_id=4) via `PATCH /messages/{id}` clears `snoozed_until`, `snooze_folder`, and `send_at`. Moving to Junk (folder_id=7) has no such clearing specified. While the normal flow prevents a Snoozed or Scheduled message from being PATCHed to Junk directly (from-Snoozed PATCH is rejected; from-Scheduled PATCH is rejected), other paths could leave residual fields.  
**Decision needed:** Should moving to Junk via PATCH also clear `snoozed_until`, `snooze_folder`, and `send_at`? If yes, add the same clearing behavior as for Trash moves. If not, explain why Junk is exempt.
