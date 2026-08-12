# MyMail — Web UI

Instructions for working under `web/`: the TypeScript sources in `web/ts/` and the embedded assets in
`web/static/`. The repo-root `AGENTS.md` is always loaded as well and covers everything else — project
overview, architecture, the Go backend, the database, the REST API, demo mode and the sanitization
policies. File paths here are written relative to the repo root, as they are there.

**Parts of this directory are governed from outside the repo**, by the `mysuite` contracts, and each
gets a `##` section below of its own. They exist for the same reason — they name the
ordinary-looking edits that break a cross-repo contract while changing nothing you can see in
MyMail alone.

**What a test catches is recorded with the test, not here.** Each of the three sections points at a
coverage map in the spec file that holds its half of the contract; those maps say, item by item,
which case goes red and which items nothing anywhere can see. They are deliberately not duplicated
into this file — a copy is a second source of truth that goes stale without anyone editing it
(`../mysuite/AGENTS.md` §4). A green suite bounds what was checked, not what is correct, so read the
map before assuming an edit below is safe.

## The sidebar footer is governed from outside this repo

The rule for `.sidebar-theme-toggle, .sidebar-settings-link` in `web/static/app.css` looks like
ordinary CSS with a verbose comment. Every declaration in it is load-bearing, and the contract is
`../mysuite/spec/sidebar-footer.md`. Each of the following is a plausible tidy-up that breaks the
three apps' consistency:

- **Normalising `0.80rem` to `0.8rem`.** The trailing zero is the convention that makes one grep find
  the value in all three repos. The rest of this stylesheet uses `0.8rem`, so the canonical spelling
  looks like the odd one out — and any formatter would "fix" it unprompted.
- **Deleting `flex-shrink: 0`, `text-align: center`, `font-weight: 400` or `font-style: normal` as
  redundant.** They are no-ops *today*, pinned because the two controls reach the same values by
  different routes: the toggle is a `<button>` taking them from the UA stylesheet, Settings is an
  `<a>` inheriting them from `body`.
- **Adding `font-weight`, `font-style` or `font` to a base `button` rule.** That would move the
  toggle and not the anchor — a divergence inside one app, between two controls 6px apart. The class
  selector outranks a bare `button` rule, so the pin above is what makes this harmless: this item is
  the reason that one exists, not a separate risk.
- **Restoring `outline: none` on their `:focus-visible`,** or adding a
  `@media (forced-colors: active)` block containing `outline: revert`. Both silently undo a WCAG
  1.4.11 fix; the second looks like it is protecting it.
- **Removing `--focus-ring` as unused.** These two controls no longer use it, but many other rules
  still do.
- **Re-adding `class="folder-icon"` to the two footer icons.** It dims them to `opacity: 0.85`; the
  contract wants full opacity.
- **Changing `.sidebar-footer`'s padding.** It is not spacing — it *is* the buttons' (8, 8) position
  on screen, and below 4px it starts cropping the focus outline.
- **Folding the pair into a generic icon-button class**, or renaming either selector.
- **Retuning `--surface`.** It looks like an ordinary theme colour — it also feeds `--topbar-bg`,
  `--surface-bg` and `.btn-ghost` — but the sidebar paints it, so it is the colour behind these two
  controls, and the contract records that colour per app as a literal (`#ffffff` light, `#1f2937`
  dark). Moving it is a spec change, and the margins are thin; the shade-by-shade figures are
  recorded beside the contrast cases in the spec.

**`e2e/tests/sidebar-footer.spec.ts` is this repo's whole half of the contract, and its header
carries the coverage map for the list above** — including the two items nothing here catches, one of
which is catchable by no test at all.

The suite runs in CI on every push to `main` (root `AGENTS.md` § E2E Tests has the evidence and the
dates). Run `./build.sh && ./test-e2e.sh` before you push anyway, and note what the gate does and
does not mean: the workflow triggers on `push`, so a breaking commit is already on `main` by the time
the suite is red. What it prevents is a broken contract reaching Pages or the rolling release — not
the commit landing.

Also: `web/static/` is embedded with `//go:embed`, so **a running server keeps serving the CSS it
started with**. Rebuilding does not change what an already-running server serves, and a stale
measurement looks exactly like a passing one. See `../mysuite/spec/measurement-protocol.md` before
measuring anything here — and run the suite with `./build.sh && ./test-e2e.sh`, which does the whole
sequence including the md5 served-vs-disk check.

## The app logo is governed from outside this repo

The badge in the top left — `.logo-icon` in `web/static/app.css`, drawn by
`<div class="logo-icon"><Icon name="mail" size={17} /></div>` in `web/ts/layout/Sidebar.tsx` — is a
three-repo contract, specified in **`../mysuite/spec/app-logo.md`**. Its box, radius, fill, glyph
size, the mark's minimum extent, the gap to the app name and the badge's decorative status are all
**values MyMail does not get to choose**. Read that document before changing any of them, and make
the change there first: **changing any of it is a change in MyCal and MyNotes too.** The values are
deliberately not repeated here (`../mysuite/AGENTS.md` §4).

**MyMail's half is `e2e/tests/logo.spec.ts`**, which holds this contract and the app-name-label one
below in the same file, because the two are one row and most of the edits move both. Its header
carries the coverage map: which case catches which of the edits below, and the several things
nothing catches. *(This section said nothing tested any of it until 2026-08-09, when
that suite was added with the `align-self: flex-start` fix. The false version is worth remembering
because it failed in the reassuring direction.)*

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
  `app-logo.md` §3.4, and load-bearing three times over: MyNotes' sidebar width was sized against it
  (`app-logo.md` §10.1), and it is a term in the label's `x`, which `app-name-label.md` §3.3 checks
  as a consequence of that pin rather than pinning separately. It is not spacing you may tune.
- **Looking for a selector for the app name and "fixing" its absence.** MyMail's app name is a
  **bare text node** inside `.sidebar-header`, with no element and no class. That is recorded in
  `app-logo.md` §1 on purpose, so a blank cell there does not read as an oversight. **The label's
  font, size and placement are governed by `../mysuite/spec/app-name-label.md`** — see the next
  section — and that contract deliberately mandates **no** selector, because two of the three apps
  have no element for the label and requiring one would make things worse (`app-name-label.md`
  §8.4). So the advice stands and its reason has changed: leave it a bare text node.
- **Changing `.sidebar-header`'s `padding`.** It *is* the badge's on-screen position, the same way
  `.sidebar-footer`'s padding is the footer buttons' (8, 8) — and it is the label's `x` and the top
  edge of the row the label is centred in as well. `app-logo.md` §4 pins the badge at `(16, 14)`
  from the window, so moving this **is** a contract violation. *(This bullet said §4 "deliberately
  does not pin window coordinates" until 2026-08-09. That was written before §4 existed in its
  current form and was simply false; it is the second thing in this file to have been falsified by
  an edit in another repository — `../mysuite/AGENTS.md` §3.5.)*
- **Wrapping the badge in a link, or giving it an `aria-label`.** It is specified as decorative
  (`app-logo.md` §8), and that ruling holds *because* the visible app name sits beside it. Removing
  the visible label is what would give the badge a naming obligation.

Two things worth knowing that are **not** MyMail's to fix, both recorded as open items in the
contract: the badge scrolls out of the window with the sidebar under content overflow (§9.1 — the
footer beside it does not, because it is sticky and the header is not), and the badge is `px` while
the label around it is `rem`, so it reads undersized at large browser font settings (§9.2). Both are
shared with MyCal. Do not fix either locally.

## The app name beside the logo is governed from outside this repo

The word **MyMail** to the right of the badge — a bare text node in `.sidebar-header`, written at
`web/ts/layout/Sidebar.tsx` — is a three-repo contract of its own, specified in
**`../mysuite/spec/app-name-label.md`**. Its font, its size and its placement are **values MyMail
does not get to choose**. Read that document before changing any of them, and make the change there
first: **changing any of it is a change in MyCal and MyNotes too.** The values are deliberately not
repeated here (`../mysuite/AGENTS.md` §4).

**This supersedes the ruling that used to put the label out of scope.** `app-logo.md` §2 recorded
the owner's decision to exclude the label because MyNotes' was smaller and its top-left was
crowded — with the condition *"for now, since space there is crowded"* attached. The owner has
reopened it: MyNotes bought the crowding out by widening its column, and the label is now specified
across all three. `app-name-label.md` §2.1 is the ruling. Nothing about MyMail's label changed.

**MyMail's half is `e2e/tests/logo.spec.ts`**, the same file as the badge; its coverage map covers
the list below too, and every item in it was demonstrated red before being claimed as caught.

### The edits that break it silently

- **Adding `line-height: 1` to `.sidebar-header`.** The likeliest edit on this list —
  `.sidebar-reload-btn` seventy lines below already has it, and it is the standard idiom for a
  header row. **It sits at the intersection of the two contracts and it moves both:**
  - it would **freeze the badge at `y = 14` at every root font size**, which is what
    `app-logo.md` §4.2 wants — but reached by accident rather than authored;
  - and it would **move the app-name label from `y = 14.797` to `19.203`**, breaking
    `app-name-label.md` §4.1. (The contract quotes `19.2`, which is the closed form
    `14 + (28 − 17.6)/2`; the page renders `19.203`. Same 1/128px LayoutUnit snapping §4.1.1
    records for `14.8047` against a measured `14.797`, and quoted here the way that section
    asks — what the mutation measured, not what the arithmetic says.)

  One edit, satisfying one contract and violating the other, and neither document can see that
  on its own — it is recorded in both. The badge's half is fixed properly instead, by
  `align-self: flex-start` on `.logo-icon`, the declaration MyCal and MyNotes already carried.
- **Padding or resizing `.sidebar-reload-btn`.** It is in the same flex row, and
  `app-name-label.md` §4.1 states the rule against **the row's tallest item** — not against the
  badge — precisely because MyCal's and MyMail's rows contain this button. Grow it past the badge
  and the label moves, in a rule that has nothing to do with the brand. **Measured here, not carried
  over from MyCal:** `.sidebar-reload-btn { padding: 20px }` takes the button from 26px to 56px, the
  header from 55 to 83, and **the label from `y = 14.797` to `28.797` — 14px — while the badge does
  not move at all.** `app-name-label.md` §4.2 has MyCal's own table, and it is the same 14px by
  coincidence of geometry rather than by transfer. The badge is protected from this by its
  `align-self`; **the label is not, deliberately** — §4.3 records the label's position rather than
  mandating it, because a declaration would move three working apps ~0.8px to defend a value that is
  currently correct everywhere.
- **Editing `body`'s font stack in `web/static/app.css`.** It **is** the label's font: nothing
  nearer the label declares one, and `app-name-label.md` §3.1 mandates the declared stack. A change
  made for message bodies is a change to this contract and will not look like one. Note that no test
  anywhere can hold the rendered *face* — `system-ui` resolves per machine (§3.1, §7.3).
- **Normalising `1.1rem` on `.sidebar-header`.** Three substitutions, and they are not equivalent.
  To `17.6px`: identical at a 16px root and frozen at every other, so it agrees with the siblings
  exactly where a test would look and diverges everywhere else. To `1.10rem`: identical computed
  **and** identical serialised. To `1.1em`: identical today, divergent the moment an ancestor sets a
  size. `app-name-label.md` §3.2 tabulates which mechanism catches which — and note its §7.1 is
  *"the only thing that compares the three apps"*, which is a claim about comparison and **not**
  about being the only thing that can see a given edit.
- **Giving the label a `<span>` with its own `font-size`.** Worth stating separately from the
  bullet above, because in **MyMail** it is the gap between the two guards: the cross-repo check
  reads the declaration on the row, so a declaration nearer the label beats it silently, and unlike
  MyCal there is no `.brand-name` for it to check instead.
- **Moving `font-size` or `font-weight` off `.sidebar-header` onto a wrapper around the label**, or
  the reverse. Renders identically today, which is the whole difficulty.
- **Removing `align-self: flex-start` from `.logo-icon`.** It is what stops the badge's `y` being a
  remainder of the label's line box. Deleting it looks like removing a redundant line: at the
  default root it changes nothing at all.

Three things worth knowing that are **not** MyMail's to fix: the label scrolls off the window with
the badge under content overflow (`app-name-label.md` §4.5 — the same open item as `app-logo.md`
§9.1); MyCal hides its label below 600px and MyMail has no width breakpoint at all, which is
recorded rather than harmonised (§4.4); and how the label degrades under a long name differs
between the three and is deliberately not mandated (§8.5). Do not fix any of them locally.

## Build & Development Commands

```bash
# Compile the demo-mode service worker (separate project — worker code, not DOM code)
tsc --project web/ts/demo/tsconfig.json

# Type-check TypeScript without emitting files
tsc --project web/ts/tsconfig.json --noEmit

# Run the frontend tests (needs web/static/*.js compiled first; unpack.sh is a
# no-op once web/ts/vendor/test/node_modules/ exists)
web/ts/vendor/test/unpack.sh
node --test web/ts/quotetext.test.mjs web/ts/wrap.test.mjs web/ts/address.test.mjs web/ts/signature.test.mjs web/ts/confirm.test.mjs web/ts/icslinks.test.mjs web/ts/demo.test.mjs web/ts/date.test.mjs

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

Plain `node --test` with `node:assert/strict` — no test framework, no package manager. Tests are
`web/ts/*.test.mjs` and import the **compiled** output from `web/static/`, not the `.ts` sources, so
`tsc` must have run first (`build.sh` orders it that way).

DOM-dependent code gets a DOM from jsdom, imported via `web/ts/vendor/test/jsdom.js` and installed
on `globalThis` *before* the module under test is imported — a module reading `DOMParser`/`Node` at
load time would otherwise see nothing. Use a dynamic `await import()` for that ordering.

jsdom is vendored as one deterministic `jsdom-node_modules.tar.gz` (it can't be bundled: it reads
data files from its own package dir at runtime). `web/ts/vendor/test/unpack.sh` extracts it with
`tar` alone and is idempotent; `web/ts/vendor/rebuild.sh` regenerates the tarball and is
maintainer-only (it is the only thing here that needs npm).

**Only logic reachable from a plain function is covered this way** — component rendering and Quill
interaction are not tested. That means a function worth testing generally belongs in `web/ts/util/`,
exported, rather than kept private inside a `.tsx` view (this is why `quoteHtmlToText` lives in
`web/ts/util/quotetext.ts` and not in `ComposeForm.tsx`).

**Each test file's header comment says what it covers, why that code was factored out to be testable
at all, and what it deliberately leaves unasserted.** Those explanations live with the tests rather
than here — read the header before the cases.

| Test | Under test | DOM |
|---|---|---|
| `quotetext.test.mjs` | `util/quotetext.ts` — the text/plain half of a reply or forward | yes |
| `icslinks.test.mjs` | `util/icslinks.ts` — which links in an incoming body get an "Import to Calendar" button | yes |
| `wrap.test.mjs` | `util/wrap.ts` — every line break a composed message gets | no |
| `address.test.mjs` | `util/address.ts` — whether the Send button is offered at all | no |
| `signature.test.mjs` | `util/signature.ts` — the identity signature as a region of the Quill document | no |
| `confirm.test.mjs` | `util/confirm.ts` — the store behind every confirmation the UI asks for | no |
| `date.test.mjs` | `util/date.ts` — the Date, Scheduled and Snoozed columns' formatters | no |
| `demo.test.mjs` | `web/ts/demo/*.ts` — the demo backend, evaluated as the worker scripts they are | no |

## Important Implementation Notes

- **Compose soft-break mark:** the editor wraps at the `wrapColumn` preference (default 80, `0` off) and marks its own breaks with a Quill block format rendered as `class="ql-softwrap-y"`, so a paragraph can be re-filled rather than only broken further. Two things keep it working: the sanitiser must never allow `class` (or the mark ships to recipients), and the format's attribute name must stay equal to the class prefix (or Quill's clipboard cannot map the class back when a draft is reopened). Enter inside a wrapped paragraph must clear the mark explicitly — splitting a paragraph copies it to both halves, and a marked break is one the wrapper may dissolve.
- **Compose signature mark:** the identity signature is found by a second block format, `class="ql-signature-y"` (`web/ts/util/signature.ts`), never by searching the editor's HTML for what `signatureToHtml` produced — Quill does not give that string back, and the swap that relied on it left the old identity's signature in the message. It carries the same two obligations as the soft-break mark, plus two of its own. A wrap break made inside the signature must carry the mark, or the half above it stops counting as signature and a swap leaves it behind (Quill puts the block format only on the half inheriting the old newline, so `autoWrapEditor` has to add it back). Enter at the end of the signature must clear it, or a paragraph written below the signature is deleted by the next swap.
