# Open Issues

## LDA / Background Scheduler

### 12. `fts5_tokenize()` is not a query-sanitization helper
SPEC.md (REST API section, FTS search input): "the raw query string is wrapped using FTS5's
`fts5_tokenize()` helper". `fts5_tokenize` is an FTS5 auxiliary function for inspecting
tokenization results, not a query-string sanitization API. It cannot be used to "wrap" a user
query. The correct fallback approach (wrapping the entire input in double-quotes with internal
quotes escaped) is mentioned but not described as the primary path. The `fts5_tokenize()`
reference should be removed; the actual approach needs to be specified.

### 13. No sendmail timeout in background scheduler
The Background Scheduler section pipes to `sendmail -t -oi` and waits for exit, but specifies
no timeout. If sendmail hangs indefinitely, the scheduler goroutine is blocked. Because the
spec uses a mutex to prevent re-entrance, a permanently hung sendmail process prevents all
future scheduler ticks from executing (no scheduled messages will ever be sent again). A
maximum wait duration (e.g. 60 seconds) must be specified for the sendmail invocation.
