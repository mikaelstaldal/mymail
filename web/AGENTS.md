# MyMail — Web UI

Instructions for working under `web/`: the TypeScript sources in `web/ts/` and the embedded assets in
`web/static/`. The repo-root `AGENTS.md` is always loaded as well and covers everything else — project
overview, architecture, the Go backend, the database, the REST API, demo mode and the sanitization
policies — including the `mysuite` cross-repo contract whose CSS half is described first below. File
paths here are written relative to the repo root, as they are there.

## Edits that silently break that contract

The rule for `.sidebar-theme-toggle, .sidebar-settings-link` in `web/static/app.css` looks like ordinary CSS with
a verbose comment. Every declaration in it is load-bearing, and each of the following is a plausible tidy-up that
breaks the suite's consistency.

**`e2e/tests/sidebar-footer.spec.ts` now catches most of them**, and gates publication in CI (see the repo-root
`AGENTS.md` § E2E Tests). It is a real change from the state this section was written in, and it is not a licence
to stop reading: the first item below is catchable by no test at all, and a green suite bounds what was checked
rather than what is correct. Each item says where it stands.

- **Normalising `0.80rem` to `0.8rem`.** The trailing zero is the convention that makes one grep find the value in
  all three repos. The rest of this file uses `0.8rem`, so the canonical spelling looks like the odd one out — and
  any formatter would "fix" it unprompted. **Not catchable by any test** — the computed *and* serialised values are
  identical, so nothing in the CSSOM or the rendering can distinguish them. Held by review alone; the only thing
  anywhere that sees it is `../mysuite/tools/check-contract.py`, which nobody's CI runs.
- **Deleting `flex-shrink: 0`, `text-align: center`, `font-weight: 400` or `font-style: normal` as redundant.**
  They are no-ops *today*, pinned because the two controls reach the same values by different routes: the toggle
  is a `<button>` taking them from the UA stylesheet, Settings is an `<a>` inheriting them from `body`. Caught by
  *the pinned declarations are actually declared, not inherited*, which reads the rule off the CSSOM — deleting
  any of them changes nothing the toggle renders, which is the whole reason a computed-value test cannot hold them.
- **Adding `font-weight`, `font-style` or `font` to a base `button` rule.** That would move the toggle and not the
  anchor — a divergence inside one app, between two controls 6px apart. The class selector outranks a bare `button`
  rule, so the pin above is what makes this harmless: this item is the reason that one exists, not a separate risk.
- **Restoring `outline: none` on their `:focus-visible`,** or adding a `@media (forced-colors: active)` block
  containing `outline: revert`. Both silently undo a WCAG 1.4.11 fix; the second looks like it is protecting it.
  Caught by the two *focus indicator meets 3:1* tests and by *controls keep a focus indicator under forced colors*.
- **Removing `--focus-ring` as unused.** These two controls no longer use it, but many other rules still do.
  **Not covered here** — this suite asserts nothing about the rules that still read it.
- **Re-adding `class="folder-icon"` to the two footer icons.** It dims them to `opacity: 0.85`; the contract wants
  full opacity. Caught by *the footer icons are 16px and at full opacity*.
- **Changing `.sidebar-footer`'s padding.** It is not spacing — it *is* the buttons' (8, 8) position on screen,
  and below 4px it starts cropping the focus outline. Caught by every (8, 8) test, and the floor itself by *the
  focus outline has its 4px of clearance on the window-facing sides*.
- **Folding the pair into a generic icon-button class**, or renaming either selector. Caught: the CSSOM test
  matches the pair's selector text exactly, and every locator in the spec is one of the two class names.
- **Retuning `--surface`.** It looks like an ordinary theme colour — it also feeds `--topbar-bg`,
  `--surface-bg` and `.btn-ghost` — but the sidebar paints it, so it is the colour behind these two
  controls, and the contract records that colour per app as a literal (`#ffffff` light, `#1f2937` dark).
  Moving it is a spec change, and the margins are thin. Light has 0.334 of headroom on WCAG 1.4.3 for
  the label: `#f9fafb` still passes at 4.626:1, `#f3f4f6` fails at 4.393:1 — which is why MyCal deviates
  from the shared label colour rather than us sharing its backdrop. Dark has room for about two shades
  before both gates go: lightening toward `#374151`, the label loses 1.4.3 around `#313b4a` and the
  outline loses 1.4.11 around `#333d4c`; at `#374151` — this file's own dark border colour, so a
  plausible pick — they are 4.060:1 and 2.803:1. Caught by *resting label meets 4.5:1* and *focus indicator meets
  3:1*, in both themes — they assert the thresholds, so a retune that stays inside them passes, correctly.

Also: `web/static/` is embedded with `//go:embed`, so **a running server keeps serving the CSS it started with**.
Rebuilding does not change what an already-running server serves, and a stale measurement looks exactly like a
passing one. See `../mysuite/spec/measurement-protocol.md` before measuring anything here — and run the suite with
`./build.sh && ./test-e2e.sh`, which does the whole sequence including the md5 served-vs-disk check.

## Build & Development Commands

```bash
# Compile the demo-mode service worker (separate project — worker code, not DOM code)
tsc --project web/ts/demo/tsconfig.json

# Type-check TypeScript without emitting files
tsc --project web/ts/tsconfig.json --noEmit

# Run the frontend tests (needs web/static/*.js compiled first; unpack.sh is a
# no-op once web/ts/vendor/test/node_modules/ exists)
web/ts/vendor/test/unpack.sh
node --test web/ts/quotetext.test.mjs web/ts/wrap.test.mjs web/ts/address.test.mjs web/ts/signature.test.mjs web/ts/confirm.test.mjs web/ts/demo.test.mjs

# Run a single frontend test
node --test --test-name-pattern 'depth cap' web/ts/quotetext.test.mjs
```

## Web UI (TypeScript + Preact)

- TypeScript sources in `web/ts/`; compiled to `web/static/` by `tsc` — no bundler.
- ES6 modules with import maps.
- Preact + JSX and Quill rich-text editor are vendored (no CDN) in `web/static/vendor/`.
- Hash-based routing (`/#/inbox`, `/#/compose`, `/#/settings/:tab`, etc.).
- `openapi-typescript` generates `web/ts/api/types.ts` from `openapi.yaml`.
- Unit-tested with `node --test` against the compiled `web/static/` output; see **Testing → Frontend**.

## Testing

### Frontend (TypeScript)

Plain `node --test` with `node:assert/strict` — no test framework, no package
manager. Tests are `web/ts/*.test.mjs` and import the **compiled** output from
`web/static/`, not the `.ts` sources, so `tsc` must have run first (`build.sh`
orders it that way).

DOM-dependent code gets a DOM from jsdom, imported via
`web/ts/vendor/test/jsdom.js` and installed on `globalThis` *before* the module
under test is imported — a module reading `DOMParser`/`Node` at load time would
otherwise see nothing. Use a dynamic `await import()` for that ordering.

jsdom is vendored as one deterministic `jsdom-node_modules.tar.gz` (it can't be
bundled: it reads data files from its own package dir at runtime).
`web/ts/vendor/test/unpack.sh` extracts it with `tar` alone and is idempotent;
`web/ts/vendor/rebuild.sh` regenerates the tarball and is maintainer-only (it is
the only thing here that needs npm).

Only logic reachable from a plain function is covered this way — component
rendering and Quill interaction are not tested. That means a function worth
testing generally belongs in `web/ts/util/`, exported, rather than kept private
inside a `.tsx` view (this is why `quoteHtmlToText` lives in
`web/ts/util/quotetext.ts` and not in `ComposeForm.tsx`).

`wrap.test.mjs` needs no DOM — `web/ts/util/wrap.ts` is pure string handling —
so it skips the jsdom install entirely. It is deliberately the whole of the
wrapping logic: `ComposeForm` only turns its edits into a Quill delta and
decides which breaks are the editor's own. Anything about *where* a line breaks
belongs in `wrap.ts`, where it can be tested; the Quill wiring cannot be.

`address.test.mjs` needs no DOM either. `web/ts/util/address.ts` decides whether
the Send button is offered at all — in ComposeForm and on a draft in
MessageDetail — by answering the question the server answers with a 400: is
there at least one recipient, and is every address list well-formed. It is a
pre-flight check, never the authority; the server's 400 still surfaces inline.
Its parser is a third copy of the same rules (`service.ParseAddressList`,
`demo/text.ts`), unavoidably so — the demo backend has no imports to share with
— and the three must move together.

`signature.test.mjs` needs no DOM either. `web/ts/util/signature.ts` is the
arithmetic behind the signature mark — ops in, a span or a set of ops out — kept
apart from ComposeForm's Quill wiring so it can be exercised without a Quill.
The ops in that test are shapes the vendored Quill actually produces, so a
change in what Quill does to a `<br>`, an `<hr>` or a split block shows up as a
test that no longer describes reality rather than as a silently wrong span.

`confirm.test.mjs` needs no DOM either. `web/ts/util/confirm.ts` is the store
behind every confirmation the UI asks for, holding the promise a caller is
awaiting; the dialog that renders it (`components/ConfirmDialog.tsx`) is not
reachable from here. What the test pins is the two ways an answer could go
missing — a question superseded before it was answered resolves `false` rather
than stranding its caller's `await`, and answering a stale id is ignored instead
of resolving the question that replaced it. A stranded promise is a button that
silently stops working, with nothing in the console to say so.

`demo.test.mjs` needs no DOM either, but it does something the other four do not:
the demo backend is a set of classic worker scripts sharing one global scope, so
the test evaluates them into *this* realm with `vm.runInThisContext`, exactly as
`importScripts` would, and reads the declarations back out. `store.js` is the
one file left out — it is nothing but IndexedDB — and a stub for its five entry
points is evaluated in its place. That is what makes `api.js` testable here:
everything above the store is real code answering real `Request` objects, so the
parity rules (which folders refuse a move, what deleting does where, how threads
close over References) are asserted against the code that implements them rather
than a paraphrase of it.

## Important Implementation Notes

- **Compose soft-break mark:** the editor wraps at the `wrapColumn` preference (default 80, `0` off) and marks its own breaks with a Quill block format rendered as `class="ql-softwrap-y"`, so a paragraph can be re-filled rather than only broken further. Two things keep it working: the sanitiser must never allow `class` (or the mark ships to recipients), and the format's attribute name must stay equal to the class prefix (or Quill's clipboard cannot map the class back when a draft is reopened). Enter inside a wrapped paragraph must clear the mark explicitly — splitting a paragraph copies it to both halves, and a marked break is one the wrapper may dissolve.
- **Compose signature mark:** the identity signature is found by a second block format, `class="ql-signature-y"` (`web/ts/util/signature.ts`), never by searching the editor's HTML for what `signatureToHtml` produced — Quill does not give that string back, and the swap that relied on it left the old identity's signature in the message. It carries the same two obligations as the soft-break mark, plus two of its own. A wrap break made inside the signature must carry the mark, or the half above it stops counting as signature and a swap leaves it behind (Quill puts the block format only on the half inheriting the old newline, so `autoWrapEditor` has to add it back). Enter at the end of the signature must clear it, or a paragraph written below the signature is deleted by the next swap.
