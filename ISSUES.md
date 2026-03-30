# Open Issues

Issues requiring a decision or further research before the spec can be finalised.

---

## Missing features








### F11 — Draft + attachment flow (§5.3)
There is no `POST /api/v1/drafts-with-attachments` equivalent. The spec says "Attachments in the send flow are handled via a separate endpoint" but only defines that endpoint for `/messages/send`. Specify how attachments are saved with drafts (e.g. multipart draft creation, or a separate `POST /api/v1/drafts/{id}/attachments` endpoint).

---

## Ambiguous / under-specified behaviour

### A1 — §5.7 is missing
The spec jumps from §5.6 (Spam Filter) to §5.8 (Thread View). Either a section was removed without renumbering, or the numbering was incremented by mistake. Verify and correct.

### A2 — BCC in sent copies (§9)
`sendmail -t` reads recipients from headers, so if the `Bcc` header is included in the piped message it will be delivered to BCC recipients correctly. However, the raw BLOB stored in the Sent folder will contain the `Bcc` header, exposing recipients if the raw message is later inspected. Decide: strip `Bcc` from the raw BLOB before storage (as most MUAs do), or preserve it.

### A3 — Message-ID hostname source (§9)
The spec says generate `<uuid@hostname>` but does not specify how `hostname` is determined (`os.Hostname()`? a configurable `-hostname` flag?). This matters for uniqueness and for message threading interoperability. Specify.

### A4 — `match_to` filter UI label (§4.6, §13)
The `match_to` column matches both the `To` **and** `Cc` headers (as documented in the SQL comment), but the column name and any auto-generated UI label would read as "To" only. Specify that the filter management UI must label this field "To / Cc" to avoid user confusion.
