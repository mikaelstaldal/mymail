# MyMail — Web UI

Instructions for working under `web/`: the TypeScript sources in `web/ts/` and the embedded assets in
`web/static/`. The repo-root `AGENTS.md` is always loaded as well and covers everything else — project
overview, architecture, the Go backend, the database, the REST API, demo mode and the sanitization
policies. File paths here are written relative to the repo root, as they are there.

**Parts of this directory are governed from outside the repo**, by the `mysuite` contracts, and each
gets a `##` section below of its own. They exist for the same reason — they name the
ordinary-looking edits that break a cross-repo contract while changing nothing you can see in
MyMail alone.

## The sidebar footer is governed from outside this repo

The rule for `.sidebar-theme-toggle, .sidebar-settings-link` in `web/static/app.css` looks like ordinary CSS with
a verbose comment. Every declaration in it is load-bearing, and each of the following is a plausible tidy-up that
breaks the suite's consistency.

**`e2e/tests/sidebar-footer.spec.ts` catches most of them, and it now runs in CI** — the step executes on
every push to `main` (see the repo-root `AGENTS.md` § E2E Tests for the evidence and the dates). Still run
`./build.sh && ./test-e2e.sh` before you push: CI tells you afterwards.

And it does not catch you in the way you probably mean: the workflow triggers on `push` to `main`, so a
breaking commit is already on `main` by the time the suite is red. What the gate prevents is a broken
contract reaching Pages or the rolling release — not the commit landing.

It is not a licence to stop reading either: the first item below is catchable by no test at
all, and a green suite bounds what was checked rather than what is correct. Each item says where it stands.

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

## The app logo is governed from outside this repo

The badge in the top left — `.logo-icon` in `web/static/app.css`, drawn by
`<div class="logo-icon"><Icon name="mail" size={17} /></div>` in `web/ts/layout/Sidebar.tsx` — is a
three-repo contract, specified in **`../mysuite/spec/app-logo.md`**. Its box, radius, fill, glyph
size, the mark's minimum extent, the gap to the app name and the badge's decorative status are all
**values MyMail does not get to choose**. Read that document before changing any of them, and make
the change there first: **changing any of it is a change in MyCal and MyNotes too.** The values are
deliberately not repeated here (`../mysuite/AGENTS.md` §4) — a copy is a second source of truth that
goes stale without anyone editing it.

**Nothing in this repository tests any of it.** Two files mention the logo at all: the markup line
and the CSS rule. `e2e/tests/sidebar-footer.spec.ts` asserts nothing about `.logo-icon` or
`.sidebar-header`, the Go tests never render CSS, the `node --test` suites cover
`web/ts/util/` and the demo backend rather than components, and `../mysuite/tools/check-contract.py`
does not know the logo exists. So for every item below the answer to *"what catches this?"* is
**nothing** — and that is from reading the suites, not from a run that went red. Contrast the
sidebar footer above, where most items name a test. **This section is the only guard the logo has.**

### After upgrading the vendored Lucide bundle, re-measure the mark

**This is the one that will bite, and MyMail is the only repo it can bite.** Our mark is not ours:
it is Lucide's `mail`, vendored as `web/static/vendor/lucide/lucide-<version>.js` and regenerated by
the maintainer-only `web/ts/vendor/rebuild.sh` from `lucide-static`. **Icon sets get redrawn between
releases.** `app-logo.md` §3.3 imposes a floor on how much of the glyph box the mark's rendered ink
must span, and MyMail sits above it with room to spare — the floor was set deliberately low *to give
this vendored drawing room* (§3.3's box says so). **Headroom is not immunity.** A routine version
bump that trims the envelope by a few points puts MyMail in violation of a contract nobody touched,
inside a generated file reviewers skim, with no test anywhere to notice.

So, as a step of any Lucide upgrade — after `rebuild.sh`, before committing the regenerated bundle:

1. `./build.sh`, then start a **fresh** server (the binary embeds `web/static/`; see
   `../mysuite/spec/measurement-protocol.md`), and confirm the served bundle matches disk.
2. Screenshot `.logo-icon` at a high `deviceScaleFactor` and measure the **painted ink** bounding
   box — the extent of pixels that differ from the flat badge fill — as a fraction of the 17px glyph
   box, on the mark's **larger** axis.
3. Compare against §3.3's floor, and report the figure to the `mysuite` owner either way — §3.3's
   table records a per-app value that a bump makes stale. **Report it as *pixel-ink extent on the
   larger axis*, in those words**: §3.3 keeps ink and stroke-inclusive geometry as two statistics
   and requires every figure to name which box it is.

**Measure the ink, not `getBBox()`.** Two traps, both of which cost time here:

- Our mark is **stroke-only** (`fill: none` on every element), so the geometry box *understates*
  what a person sees — by 8.3 percentage points at `lucide-1.25.0`, the drawing this paragraph
  exists because somebody will replace. Re-derive the gap; do not carry that figure forward.
- **`getBBox({ stroke: true })` silently returns the geometry box** — a reading on Chromium 145 via
  Playwright 1.58.2, the version `e2e/package-lock.json` pins, not a guarantee about the API. It
  does not error and it does not warn; it just gives you the wrong number, which is why step 2
  counts pixels. Do not read "identical to `getBBox()`" as evidence the stroke adds nothing —
  check it against pixels whichever way it comes back.

Also: `<Icon>` renders **nothing** for a name absent from the bundle, so if `gen-lucide.mjs`'s
`ICONS` list ever loses `'mail'`, the badge silently becomes an empty blue square. `./build.sh`
stays green.

### The other edits that break it silently

- **Deleting `size={17}` because `<Icon>` has a default.** It does — 16 — so the glyph would shrink
  by 1px, which is invisible without a sibling app beside it. The contract mandates the *rendered*
  17, not the mechanism.
- **Adding a CSS rule that sizes the logo's SVG, to match MyCal.** MyCal does size its glyph in CSS
  and is right to; the contract sanctions both layers. But **MyMail has no CSS rule anywhere that
  sizes an SVG** (`.lucide` sets `vertical-align` and `flex-shrink` and no dimensions), and adding
  one creates a second source of truth that *outranks the attribute* — so the next edit to `size=`
  becomes a silent no-op. Change the prop, not the stylesheet.
- **Retuning `--primary`, e.g. to make the logo pop.** `.logo-icon` reads it through
  `--sidebar-badge`, an alias declared once in `:root`; the alias does not decouple the value today.
  `--primary` is also `.folder-badge`, `.settings-badge`, and — the one nobody will look for — the
  **focus-outline colour the sidebar-footer contract holds to WCAG 1.4.11 with under one point of
  headroom** (`app-logo.md` §7.3, `sidebar-footer.md` §6.2; the operands are recorded next to the
  rule in `web/static/app.css`). A change made for the logo lands on an accessibility obligation in
  a different contract. If you think the fill needs changing, raise it — do not change it.
- **Replacing `.logo-icon`'s `color: #fff` with `var(--on-color-fg)`**, which `.settings-badge`
  already uses on the same fill. Identical today, because `--on-color-fg` is defined once in `:root`
  and never theme-scoped. It becomes a divergence the day somebody scopes it — the harmlessness is
  conditional and the condition lives elsewhere (`../mysuite/AGENTS.md` §3.3).
- **Touching `gap` on `.sidebar-header`.** It is the badge-to-app-name gap, pinned by
  `app-logo.md` §3.4, and load-bearing twice: MyNotes' sidebar width was sized against it
  (`app-logo.md` §10.1). It is not spacing you may tune.
- **Looking for a selector for the app name and "fixing" its absence.** MyMail's app name is a
  **bare text node** inside `.sidebar-header`, with no element and no class. That is recorded in
  `app-logo.md` §1 on purpose, so a blank cell there does not read as an oversight — and the label's
  typography is out of scope for the contract by an explicit ruling (§2). Wrapping it in a `<span>`
  is a change to a row the contract measures.
- **Changing `.sidebar-header`'s `padding`.** It *is* the badge's on-screen position, the same way
  `.sidebar-footer`'s padding is the footer buttons' (8, 8). The difference is that moving the
  footer's fails fourteen of the e2e tests, and moving this one fails nothing. Note that `app-logo.md` §4 deliberately
  does **not** pin window coordinates — so this is not a contract violation, but it is the kind of
  change to make on purpose rather than while tidying.
- **Wrapping the badge in a link, or giving it an `aria-label`.** It is specified as decorative
  (`app-logo.md` §8), and that ruling holds *because* the visible app name sits beside it. Removing
  the visible label is what would give the badge a naming obligation.

Two things worth knowing that are **not** MyMail's to fix, both recorded as open items in the
contract: the badge scrolls out of the window with the sidebar under content overflow (§9.1 — the
footer beside it does not, because it is sticky and the header is not), and the badge is `px` while
the label around it is `rem`, so it reads undersized at large browser font settings (§9.2). Both are
shared with MyCal. Do not fix either locally.

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
