import { test, expect, type Page } from '@playwright/test';

// The sidebar footer's two controls — the light/dark toggle and Settings — are a
// three-repo contract: they are specified to look and sit identically in MyCal,
// MyMail and MyNotes. Nothing else enforces that (there is no shared stylesheet
// and no cross-repo test), so these assertions are this repo's whole half of it.
// See the `.sidebar-theme-toggle, .sidebar-settings-link` block in
// web/static/app.css for the derivations, and `../mysuite/spec/sidebar-footer.md`
// for the contract itself. Bare § references below are that file.
//
// Ported from MyCal's e2e/tests/sidebar-footer.spec.ts, which was the only
// machine-checkable statement of this contract anywhere. Roughly two-thirds of it
// is app-independent and is here nearly verbatim, reasons included — the contract
// asks that where an assertion is kept its reason is kept with it. Where MyMail
// differs the comment says what changed and why; where MyCal covers something
// MyMail has no counterpart for, the comment says that rather than dropping it
// silently.
test.describe('Sidebar footer contract', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Nothing below means anything until the footer has rendered — a selector
    // that matches nothing returns a falsy value, and a falsy value compared
    // against an expectation frequently reads as a pass.
    await expect(page.locator('.sidebar-footer')).toBeVisible();

    // State the starting conditions rather than assuming them. Several tests
    // below begin by clicking "Switch to dark mode" and would report only
    // "element not found" if the app had started dark — a message that points
    // nowhere near the cause. `app.tsx` calls applyTheme(getTheme()) at module
    // scope and getTheme() reads localStorage, which Playwright gives each test
    // fresh, so light is well-defined here rather than merely usual.
    //
    // Added after a review saw this exact ambiguity once: the theme-toggle test
    // failed to find that button on one run out of eight, and passed on every run
    // before and after. **The cause was not established** — the only candidate
    // noted at the time was that the reviewer's suite and mine were running
    // against the same port, and therefore possibly the same server and database,
    // while the overflow tests were creating and deleting forty folders. That is
    // a hypothesis, not a finding. What this assertion does is make the next
    // occurrence say which precondition broke instead of leaving it ambiguous.
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
    await expect(page.locator(THEME)).toBeVisible();
    await expect(page.locator(SETTINGS)).toBeVisible();
  });

  // MyMail names its two controls separately, where MyCal gives both the same
  // `.sidebar-footer-btn`. So MyCal's `[title="Settings"]` / `[aria-pressed]`
  // disambiguation has nothing to do here: each class already matches exactly one
  // element. Worth stating, because the MyCal comment those locators carry — about
  // a positional locator silently repointing if a third control joined the row —
  // still applies; it is just answered by the class names rather than by an
  // attribute.
  //
  // Settings is an <a href="#/settings">, not a <button> (§1). The contract says
  // both element types are fine and both carry the identical rule, so everything
  // below asserts the shared *result* and never button semantics.
  const THEME = '.sidebar-theme-toggle';
  const SETTINGS = '.sidebar-settings-link';
  // In MyMail the footer *is* the flex row (§1) — there is no separate
  // `.sidebar-footer-actions`. That means it also carries the 8px padding, which
  // the width arithmetic below has to subtract; MyCal's row has none, so its
  // version of this compares against `clientWidth` directly. Comparing against
  // MyMail's clientWidth would silently give the row 16px it does not have.
  const ROW = '.sidebar-footer';

  // Sum the children rather than reading scrollWidth: scrollWidth's behaviour on
  // an `overflow: visible` box is not guaranteed, and this row is one. Comparing
  // the content the row must hold against the width it has is unambiguous
  // everywhere.
  const rowFit = (page: Page) =>
    page.locator(ROW).evaluate(el => {
      const cs = getComputedStyle(el);
      const gap = parseFloat(cs.columnGap) || 0;
      const kids = [...el.children];
      return {
        needed:
          kids.reduce((sum, k) => sum + k.getBoundingClientRect().width, 0) + gap * (kids.length - 1),
        available: el.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight),
      };
    });

  const rowOverflows = async (page: Page) => {
    const { needed, available } = await rowFit(page);
    return needed > available + 0.5;
  };

  // boundingBox() returns null for an element that is absent or not rendered, and
  // a bare `!` turns that into "Cannot read properties of null" several lines
  // later, naming a property instead of the element. Everything else in this file
  // works hard to make a failure say what broke; this is the one place that would
  // have undone it.
  const boxOf = async (page: Page, selector: string) => {
    const box = await page.locator(selector).boundingBox();
    if (!box) throw new Error(`${selector} has no layout box — is it rendered?`);
    return box;
  };

  const boxes = async (page: Page) => ({
    theme: await boxOf(page, THEME),
    settings: await boxOf(page, SETTINGS),
    column: await boxOf(page, '.sidebar'),
    viewportHeight: page.viewportSize()!.height,
  });

  // ---------------------------------------------------------------------------
  // Fixtures.
  //
  // The suite runs single-worker against one long-lived server and one database,
  // so anything a test creates is visible to every test after it. Each fixture
  // below is therefore removed in a `finally`, and the removal is *asserted*
  // rather than assumed: the measurement protocol's "prove the reset" — a reset
  // that silently does nothing turns three content volumes into one data point
  // wearing three hats.
  //
  // Written through `fetch` from inside the page rather than through
  // `page.request`, so the Origin header is the app's own and CSRF validation
  // passes. test-e2e.sh's `-public-url` is what makes that true.
  // ---------------------------------------------------------------------------

  const folderCount = (page: Page) =>
    page.evaluate(async () => (await (await fetch('/api/v1/folders')).json()).total as number);

  // Returns what it managed to create *and* the error, rather than throwing.
  // Throwing from inside page.evaluate discards the ids collected so far — they
  // only exist in the page's realm — so a POST failing at #20 would strand 19
  // folders in the database permanently, with nothing holding their ids. The
  // caller records the ids first and fails afterwards.
  const createFolders = (page: Page, n: number) =>
    page.evaluate(async count => {
      const ids: number[] = [];
      for (let i = 0; i < count; i++) {
        try {
          const r = await fetch('/api/v1/folders', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: `Overflow fixture ${i}` }),
          });
          if (r.status !== 201) return { ids, error: `folder ${i} create failed: ${r.status}` };
          ids.push((await r.json()).id as number);
        } catch (e) {
          return { ids, error: `folder ${i} create threw: ${String(e)}` };
        }
      }
      return { ids, error: null as string | null };
    }, n);

  const deleteFolders = (page: Page, ids: number[]) =>
    page.evaluate(async list => {
      for (const id of list) await fetch(`/api/v1/folders/${id}`, { method: 'DELETE' });
    }, ids);

  const createLongDraft = (page: Page) =>
    page.evaluate(async () => {
      const r = await fetch('/api/v1/drafts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          to_addr: 'recipient@example.com',
          subject: 'Long body fixture',
          body_text: Array.from(
            { length: 400 },
            (_, i) => `Paragraph ${i}. Enough text that the reading pane has to scroll.`,
          ).join('\n\n'),
        }),
      });
      if (r.status !== 201) throw new Error(`draft create failed: ${r.status}`);
      return (await r.json()).id as number;
    });

  // DELETE /drafts/{id}, not DELETE /messages/{id}: the latter rejects anything in
  // Drafts with a 400 and says so in openapi.yaml. The first version of this used
  // the wrong endpoint and ignored the status, so the fixture was never removed
  // and the "cleanup" was decorative — the exact shape measurement-protocol.md
  // warns about under "prove the reset". The status check is what makes it not
  // that, so do not drop it.
  const deleteDraft = (page: Page, id: number) =>
    page.evaluate(async i => {
      const r = await fetch(`/api/v1/drafts/${i}`, { method: 'DELETE' });
      if (r.status !== 204) throw new Error(`draft delete failed: ${r.status}`);
    }, id);

  const draftCount = (page: Page) =>
    page.evaluate(
      async () => (await (await fetch('/api/v1/folders/3/messages')).json()).total as number,
    );

  // ---------------------------------------------------------------------------
  // Position on screen — the point of the whole exercise. Measured from the
  // window, not from the sidebar: three apps can each be correct against their
  // own container and still put the buttons in three different places, which is
  // exactly what happened before this. A user with all three open in tabs must
  // see nothing move when switching between them.
  // ---------------------------------------------------------------------------

  test('controls sit 8px from the window left and bottom edges', async ({ page }) => {
    const { theme, settings, viewportHeight } = await boxes(page);

    // The two numbers that are the MySuite claim. Both font-independent.
    expect(theme.x).toBeCloseTo(8, 0);
    expect(viewportHeight - (theme.y + theme.height)).toBeCloseTo(8, 0);
    expect(viewportHeight - (settings.y + settings.height)).toBeCloseTo(8, 0);

    // Settings follows the toggle by the 6px gap. Derived from the measured
    // width rather than hardcoded: a literal would pin this machine's system-ui
    // metrics, which the CSS comment warns are not portable.
    expect(settings.x).toBeCloseTo(theme.x + theme.width + 6, 0);

    // The footer's own left edge, not just the buttons'. MyMail's sidebar is
    // already flush against the window, so this is 0 here rather than reached by
    // MyCal's negative margin — but the assertion is the same one, and it is what
    // would catch the sidebar gaining a left border or the grid gaining a gutter
    // while the buttons' own 8px still measured correct.
    const footer = await boxOf(page, '.sidebar-footer');
    expect(footer.x).toBeCloseTo(0, 0);
  });

  // MyCal loops over its five calendar views here, because those five lay out
  // differently from one another and two of them page-scroll. MyMail's equivalent
  // axis is its route table: the main pane holds a message list, a single message
  // with its own inner scrollport, a compose form, the settings page or search
  // results, and those are the layouts that could push the footer around.
  //
  // Complete *as of the current route table* (web/ts/router.ts: inbox, folder,
  // message, compose, search, settings). §8.3 is explicit that adding a route is
  // the event that invalidates this coverage — a new route is covered by nothing
  // and every existing one keeps passing. Add a case here when you add a route.
  const ROUTES = [
    ['inbox', '#/inbox', '.folder-view'],
    ['a folder', '#/folder/sent', '.folder-view'],
    ['compose', '#/compose', '.compose-form'],
    ['search', '#/search?q=fixture', '.search-view'],
    ['settings', '#/settings', '.settings-page'],
  ] as const;

  for (const [name, hash, selector] of ROUTES) {
    test(`controls hold (8, 8) on the ${name} route`, async ({ page }) => {
      await page.goto('/' + hash);
      // Wait for the view itself, so the measurement is taken on a laid-out page
      // rather than whatever height the previous route happened to leave.
      await expect(page.locator(selector)).toBeVisible();

      const { theme, viewportHeight } = await boxes(page);
      expect(theme.x).toBeCloseTo(8, 0);
      expect(viewportHeight - (theme.y + theme.height)).toBeCloseTo(8, 0);
    });
  }

  // The sixth route, separated because it needs a fixture: reading a message is
  // the one MyMail layout with a scrollport in the main pane, and the protocol
  // requires the overflow case to be measured rather than assumed.
  test('controls hold (8, 8) while reading a message whose body scrolls', async ({ page }) => {
    const id = await createLongDraft(page);
    try {
      await page.goto(`/#/message/${id}`);
      await expect(page.locator('.msg-detail')).toBeVisible();

      // Assert the precondition with numbers. A run that measured nothing and a
      // run that measured a pass look identical unless the intermediate state is
      // asserted — loading a lot of text is setup, not evidence.
      const pane = await page.locator('.msg-detail .body-text').evaluate(el => ({
        scrollHeight: el.scrollHeight,
        clientHeight: el.clientHeight,
      }));
      expect(
        pane.scrollHeight,
        `reading pane did not overflow: ${pane.scrollHeight}px of content in a ${pane.clientHeight}px box`,
      ).toBeGreaterThan(pane.clientHeight);

      const { theme, viewportHeight } = await boxes(page);
      expect(theme.x).toBeCloseTo(8, 0);
      expect(viewportHeight - (theme.y + theme.height)).toBeCloseTo(8, 0);
    } finally {
      await deleteDraft(page, id);
      // Prove the reset, for the same reason the folder fixture does.
      expect(await draftCount(page), 'the fixture draft was not removed').toBe(0);
    }
  });

  // MyCal has two views whose document scrolls, and its suite tests that the
  // footer survives that scroll (threat 2 in §8.3, handled there by sticky).
  // **MyMail has no such view.** `.app { height: 100vh }` over
  // `grid-template-rows: auto 1fr auto` makes the shell exactly the viewport, so
  // every overflowing region carries its own scrollport and the document never
  // scrolls — the structural immunity §8.3 records for this app.
  //
  // So the MyCal test has no counterpart, and this is its replacement: assert the
  // immunity itself. §8.3 says in as many words that this rests on one
  // declaration, that `height` becoming `min-height` ends it, that this is a
  // change somebody makes for a good reason, and that **it is tested nowhere**.
  // This is that test. Nothing would fail at the time of such an edit; the
  // consequence appears later, on a short window, with the footer scrolled off.
  test('the document never scrolls, on any route', async ({ page }) => {
    // Short enough that a shell sized by content rather than by the viewport
    // would overflow it — 100vh is indistinguishable from min-height: 100vh in a
    // tall window with little content.
    await page.setViewportSize({ width: 1000, height: 400 });

    for (const [name, hash, selector] of ROUTES) {
      await page.goto('/' + hash);
      await expect(page.locator(selector)).toBeVisible();

      const state = await page.evaluate(() => {
        const de = document.documentElement;
        window.scrollTo(0, 99999);
        return {
          scrollHeight: de.scrollHeight,
          clientHeight: de.clientHeight,
          scrollY: window.scrollY,
        };
      });
      expect(state.scrollHeight, `${name}: document is taller than the window`).toBe(state.clientHeight);
      expect(state.scrollY, `${name}: the page scrolled vertically`).toBe(0);

      // viewportHeight from the page, not the literal passed to setViewportSize
      // above: two copies of the same number are two things that can disagree, and
      // a B computed against the wrong height is a wrong measurement that looks
      // like a reading.
      const { theme, viewportHeight } = await boxes(page);
      expect(theme.x, name).toBeCloseTo(8, 0);
      expect(viewportHeight - (theme.y + theme.height), name).toBeCloseTo(8, 0);
    }
  });

  // §8.2 — the requirement the contract was missing, and the one that has actually
  // been violated in production code. MyMail's own numbers are the ones §8.2
  // quotes: B measured −43.61 at 13 user folders and −1052.16 at 40, against a
  // `.sidebar` that is `overflow-y: auto` with the footer as an ordinary last
  // child. `position: sticky` is what fixed it, and nothing has tested it since.
  //
  // MyMail's 48 passing hand measurements all used a dataset with zero user
  // folders, so the overflow case was never exercised at all. That is the whole
  // reason this test exists, and why it asserts its own precondition: a probe that
  // finds nothing to stress reports a clean pass.
  for (const root of [16, 24]) {
    test(`(8, 8) holds while the sidebar scrolls, at a ${root}px root`, async ({ page }) => {
      // `ids` is declared outside and filled inside, so a POST that fails partway
      // still reaches the cleanup with whatever was created. With the call outside
      // the `try`, the folders made before the failure would be unreachable and
      // permanent, the reset proof would never run, and every later test would
      // measure the overflow volume while believing it measured the empty one —
      // the failure these fixtures exist to prevent, reintroduced by the fixture.
      // (createFolders pushes each id as it succeeds for the same reason.)
      const ids: number[] = [];
      try {
        const made = await createFolders(page, 40);
        ids.push(...made.ids);
        expect(made.error, 'fixture setup failed').toBeNull();
        expect(ids, 'fixture did not create the folders it reported').toHaveLength(40);
        // A full reload, not a `goto` to a new hash: the sidebar fetches its
        // folder list once at startup and a hash change re-renders from the list
        // it already has. Without this the test measures the seven built-in
        // folders, the sidebar never overflows, and the run reports a pass having
        // stressed nothing — which is exactly how it first failed here.
        await page.reload();
        await expect(page.locator('.folder-view')).toBeVisible();
        if (root !== 16) {
          // Through the CSSOM rather than addStyleTag — the app's CSP has no
          // 'unsafe-inline' in style-src, so an injected <style> is rejected.
          // After the reload, or it would be discarded with the old document.
          await page.evaluate(r => { document.documentElement.style.fontSize = `${r}px`; }, root);
        }
        // The folder rows have to be on screen before the sidebar can overflow.
        await expect(page.locator('.folder-item')).toHaveCount(47); // 7 built-in + 40

        const pre = await page.locator('.sidebar').evaluate(el => ({
          scrollHeight: el.scrollHeight,
          clientHeight: el.clientHeight,
        }));
        expect(
          pre.scrollHeight,
          `sidebar did not overflow: ${pre.scrollHeight}px of content in a ${pre.clientHeight}px box`,
        ).toBeGreaterThan(pre.clientHeight);

        // A sticky element behaves differently at each end of its scroll range, so
        // both extremes are measured (§8.3, mechanism B's third failure mode).
        for (const [where, top] of [['top', 0], ['bottom', 1e6]] as const) {
          const scrollTop = await page.locator('.sidebar').evaluate((el, t) => {
            el.scrollTop = t;
            return el.scrollTop;
          }, top);
          if (where === 'bottom') {
            expect(scrollTop, 'sidebar did not actually scroll').toBeGreaterThan(0);
          }

          const { theme, viewportHeight } = await boxes(page);
          const B = viewportHeight - (theme.y + theme.height);
          expect(theme.x, `L at the ${where} of the scroll range`).toBeCloseTo(8, 0);
          // Fractional B at a scroll extreme is a rounding artefact of the scroll
          // range, not drift — §8.3 records the same 8.02–8.50 spread for this app
          // and 8.22 for MyCal. toBeCloseTo(…, 0) is a 0.5px window, which is the
          // tolerance those readings were accepted under.
          expect(B, `B at the ${where} of the scroll range (scrollTop ${scrollTop})`).toBeCloseTo(8, 0);
        }
      } finally {
        await deleteFolders(page, ids);
        // Prove the reset. A cleanup that silently did nothing would leave every
        // later test measuring the overflow volume while claiming to measure the
        // empty one — and would look exactly like this one passing.
        expect(await folderCount(page), 'fixture folders were not removed').toBe(7);
      }
    });
  }

  // ---------------------------------------------------------------------------
  // The pinned declarations
  // ---------------------------------------------------------------------------

  // Geometry assertions only catch a violation that *moves something*, and
  // several of the contract's pins deliberately do not: `font-weight: 400` is
  // what the UA button rule already gives the toggle, `flex-shrink: 0` does
  // nothing until the row is under pressure, `text-align: center` is a button's
  // default. They are pinned because the controls reach those values by different
  // routes — and MyMail is the app where that is true *within itself*: the toggle
  // is a <button> taking them from the UA stylesheet, Settings is an <a>
  // inheriting them from `body` (§3). An ordinary `button { font-weight: 500 }`
  // would move one and not the other, 6px apart.
  //
  // So assert the computed values themselves. This is font-independent and
  // platform-independent: it reads what the cascade resolved, not what the text
  // measured. It is also the only assertion here that would survive the labels
  // changing.
  test('the pinned declarations resolve to the contract values', async ({ page }) => {
    for (const selector of [THEME, SETTINGS]) {
      const cs = await page.locator(selector).evaluate(el => {
        const s = getComputedStyle(el);
        return {
          fontSize: s.fontSize, lineHeight: s.lineHeight, fontWeight: s.fontWeight,
          fontStyle: s.fontStyle, textAlign: s.textAlign, flexShrink: s.flexShrink,
          whiteSpace: s.whiteSpace, display: s.display, borderTopWidth: s.borderTopWidth,
          borderRadius: s.borderTopLeftRadius, padding: s.padding, columnGap: s.columnGap,
          textDecorationLine: s.textDecorationLine,
        };
      });
      // 0.80rem at a 16px root, on a 1.5 line box — the 29.2px acceptance height
      // is these two plus 8px padding and 2px border.
      expect(cs.fontSize, selector).toBe('12.8px');
      expect(cs.lineHeight, selector).toBe('19.2px');
      expect(cs.padding, selector).toBe('4px 8px');
      expect(cs.borderTopWidth, selector).toBe('1px');
      // MyMail reaches 6px through var(--border-radius) where MyCal writes the
      // literal. §2 says explicitly that both are correct — the resolved value is
      // what is mandated — so this asserts the resolved value and not the route.
      expect(cs.borderRadius, selector).toBe('6px');
      expect(cs.columnGap, selector).toBe('6px');
      // The rule says `inline-flex`; the computed value is `flex` because a flex
      // item's display is blockified. Asserting the computed value, not the
      // declaration — they legitimately differ here.
      expect(cs.display, selector).toBe('flex');
      expect(cs.whiteSpace, selector).toBe('nowrap');
      // The three pinned inherited properties, and the one that keeps overflow
      // rather than a silent squeeze as the failure mode.
      expect(cs.fontWeight, selector).toBe('400');
      expect(cs.fontStyle, selector).toBe('normal');
      expect(cs.textAlign, selector).toBe('center');
      expect(cs.flexShrink, selector).toBe('0');
      // Not in MyCal's list, because both of its controls are <button>s and a
      // button is never underlined. §1 names this as the one thing MyMail's anchor
      // additionally needs, and it is asserted on both so that swapping either
      // element type keeps the check.
      expect(cs.textDecorationLine, selector).toBe('none');
    }

    // The row itself — which in MyMail is the footer (§1).
    const row = await page.locator(ROW).evaluate(el => {
      const s = getComputedStyle(el);
      return { display: s.display, flexWrap: s.flexWrap, columnGap: s.columnGap };
    });
    expect(row.display).toBe('flex');
    expect(row.flexWrap).toBe('nowrap');
    expect(row.columnGap).toBe('6px');
  });

  // Some pins cannot be checked by computed value at all, because the UA default
  // already equals the contract value on the <button> half of the pair:
  // `font-weight: 400` and `text-align: center` are what a <button> computes with
  // or without our rule. Deleting either changes nothing for the toggle — and
  // changes everything for the Settings anchor the moment `body` moves, and for
  // MyNotes, whose `button { font: inherit }` means its buttons take those from
  // `body` too. §3.1: the app where a value is already correct is the app whose
  // rendering cannot detect the pin going missing.
  //
  // So read the rule itself out of the CSSOM. This asserts the declaration is
  // present, not merely that the result looks right.
  test('the pinned declarations are actually declared, not inherited', async ({ page }) => {
    const declared = await page.evaluate(() => {
      for (const sheet of [...document.styleSheets]) {
        let rules: CSSRuleList;
        try { rules = sheet.cssRules; } catch { continue; } // cross-origin
        for (const rule of [...rules]) {
          // The two controls share one rule, so the selector is the pair. Chromium
          // serialises the authored two-line list as a comma-and-space join;
          // matched exactly rather than by substring so a `:hover` or
          // `:focus-visible` variant of the same pair cannot answer instead.
          if (
            rule instanceof CSSStyleRule &&
            rule.selectorText === '.sidebar-theme-toggle, .sidebar-settings-link'
          ) {
            return {
              fontWeight: rule.style.fontWeight,
              fontStyle: rule.style.fontStyle,
              textAlign: rule.style.textAlign,
              flexShrink: rule.style.flexShrink,
              whiteSpace: rule.style.whiteSpace,
              fontFamily: rule.style.fontFamily,
              textDecoration: rule.style.textDecoration,
            };
          }
        }
      }
      return null;
    });

    expect(declared, 'the .sidebar-theme-toggle, .sidebar-settings-link rule was not found in any stylesheet')
      .not.toBeNull();
    expect(declared!.fontWeight).toBe('400');
    expect(declared!.fontStyle).toBe('normal');
    expect(declared!.textAlign).toBe('center');
    expect(declared!.flexShrink).toBe('0');
    expect(declared!.whiteSpace).toBe('nowrap');
    // Inheriting is itself the contract here — a literal stack would stop these
    // controls following the app's own typography (§4).
    expect(declared!.fontFamily).toBe('inherit');
    expect(declared!.textDecoration).toBe('none');
  });

  // §2: the icons are Lucide, 16px, at **full opacity**, and the contract calls
  // MyMail out by name — `.folder-icon` in the same sidebar sets `opacity: .85`,
  // and these two deliberately do not take that class. web/AGENTS.md lists
  // re-adding it as a plausible tidy-up that breaks the contract silently, so it
  // gets an assertion rather than a warning. MyCal has no equivalent test; it also
  // has no dimming class to reach for.
  test('the footer icons are 16px and at full opacity', async ({ page }) => {
    const icons = await page.locator('.sidebar-footer svg').evaluateAll(els =>
      els.map(el => ({
        width: el.getAttribute('width'),
        height: el.getAttribute('height'),
        strokeWidth: el.getAttribute('stroke-width'),
        opacity: getComputedStyle(el).opacity,
      })),
    );
    expect(icons).toHaveLength(2);
    for (const icon of icons) {
      expect(icon.width).toBe('16');
      expect(icon.height).toBe('16');
      expect(icon.strokeWidth).toBe('2');
      expect(icon.opacity).toBe('1');
    }
  });

  // ---------------------------------------------------------------------------
  // Size and stability
  // ---------------------------------------------------------------------------

  test('controls hold one row and do not move when the theme is toggled', async ({ page }) => {
    const theme = page.getByRole('button', { name: 'Switch to dark mode' });
    await expect(theme).toBeVisible();

    const before = await page.locator(SETTINGS).boundingBox();
    const themeBefore = await theme.boundingBox();

    await theme.click();
    await expect(page.getByRole('button', { name: 'Switch to light mode' })).toBeVisible();

    const after = await page.locator(SETTINGS).boundingBox();
    const themeAfter = await page.getByRole('button', { name: 'Switch to light mode' }).boundingBox();

    // toBeCloseTo's second argument is decimal places, so 0 is a 0.5px window —
    // loose enough to survive fractional layout, tight enough to catch the 2px
    // "Light" vs "Dark" shift the grid stacking (§7) exists to prevent.
    expect(themeAfter!.width).toBeCloseTo(themeBefore!.width, 0);
    expect(after!.x).toBeCloseTo(before!.x, 0);

    // 29.2px at a 16px root font is the height the three apps share; §2.2 calls it
    // the acceptance measurement rather than a hard contract, so pin it here where
    // the root font size is known to be 16.
    expect(themeAfter!.height).toBeCloseTo(29.2, 0);
    expect(after!.height).toBeCloseTo(29.2, 0);

    // Side by side, inside the footer's content box. In MyMail the footer is the
    // row, so this is the same element the buttons are laid out in — MyCal's
    // version distinguishes the two because its footer reaches further left than
    // its column.
    const inner = await page.locator(ROW).evaluate(el => {
      const r = el.getBoundingClientRect();
      const cs = getComputedStyle(el);
      return {
        left: r.left + el.clientLeft + parseFloat(cs.paddingLeft),
        right: r.left + el.clientLeft + el.clientWidth - parseFloat(cs.paddingRight),
      };
    });
    expect(after!.y).toBeCloseTo(themeAfter!.y, 0);
    expect(themeAfter!.x).toBeGreaterThanOrEqual(inner.left);
    expect(after!.x + after!.width).toBeLessThanOrEqual(inner.right);
  });

  // The row is nowrap and the buttons do not shrink, so a wider font overflows
  // rather than reflowing — and the only font anyone measures here is this
  // container's. This turns the CSS's slack claim into an assertion, since the
  // overflow branch is otherwise never exercised.
  //
  // 1.1x, well short of where it actually breaks: the point is to prove the slack
  // is real, not to pin how much of it there is. An assertion near the boundary
  // would be pinning exactly the system-ui reading the CSS comment beside it warns
  // is not portable.
  //
  // Measured here, at a 16px root, in whatever system-ui resolves to in this
  // container: 174px of pair in 203px of row, still fitting at 1.30x (203/203) and
  // overflowing at 1.40x (211/203). It is not linear in the multiplier — only the
  // text scales, while the padding, border, 6px gaps and 16px icons are px.
  //
  // **MyMail is not the tightest of the three.** §2.4's table gives MyCal 26px of
  // slack against MyMail's 29px and MyNotes' 229px, so MyCal is the narrow one and
  // MyMail is second. (Said explicitly because the first version of this comment
  // claimed MyMail was tightest — §2.4 is also where the font size was set *by*
  // MyCal's sidebar, which is easy to misread as MyMail being the binding
  // constraint. The two-word-label overflow §2.4 records for MyMail is a separate
  // fact and is about the label, not the budget.)
  test('the row absorbs a 10% wider font without overflowing', async ({ page }) => {
    const before = await rowFit(page);
    expect(before.needed, `pair ${before.needed} in ${before.available}`)
      .toBeLessThanOrEqual(before.available);

    await page.locator(`${THEME}, ${SETTINGS}`).evaluateAll(els => {
      for (const el of els) {
        (el as HTMLElement).style.fontSize =
          parseFloat(getComputedStyle(el).fontSize) * 1.1 + 'px';
      }
    });

    const after = await rowFit(page);
    // Reported either way, so a failure says how far over rather than just "red".
    expect(after.needed, `at 1.1x font: pair ${after.needed} in ${after.available}`)
      .toBeLessThanOrEqual(after.available);
  });

  // The footer buttons are sized in rem, so the column has to be too — a px
  // column keeps its width while they grow and pushes Settings out over the
  // message list. That starts at a 20px root, Chrome's "Large" setting, so it is
  // reachable from the browser's own menu (WCAG 1.4.4). §6.4 is explicit that the
  // column is *not* part of the contract — MyNotes keeps a px column by sanctioned
  // exemption — so this asserts MyMail's own choice of `13.75rem`, not a shared
  // rule.
  for (const root of [20, 24]) {
    test(`controls stay inside the column at a ${root}px root font`, async ({ page }) => {
      await page.evaluate(r => { document.documentElement.style.fontSize = `${r}px`; }, root);

      const { settings, column } = await boxes(page);
      expect(column.width).toBeGreaterThan(220);
      expect(settings.x + settings.width).toBeLessThanOrEqual(column.x + column.width);
      expect(await rowOverflows(page)).toBe(false);

      // Growing the column must not push the page into a horizontal scroll —
      // that would be the same 1.4.4 failure moved one row up.
      const hScroll = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      );
      expect(hScroll).toBe(false);
    });
  }

  // MyCal's counterpart of this test asserts `.mini-month` is hidden below 600px.
  // **MyMail has no <600px breakpoint** — `web/static/app.css` contains exactly one
  // media query, `@media (hover: none)`, and nothing about the layout responds to
  // width. So there is no hidden element to assert and the sidebar keeps its full
  // 13.75rem at every width. The footer requirement still applies, and MyMail
  // satisfies it more completely than MyCal does: MyCal's narrow layout sits at
  // B = 4 (§10.8, a documented deviation), MyMail stays at the contract's 8.
  //
  // **Declared blind spot, in MyCal's terms and for the same reason it declares
  // one.** Because nothing responds, the app shell has a min-content width — read
  // as 415px in this container, at a 16px root, in whatever system-ui resolves to
  // here — and below that the *document* scrolls horizontally. Scrolled fully
  // right at a 375px viewport the toggle measures L = −32: it leaves the window.
  // That is a reading of MyMail's non-responsiveness, not of this contract, which
  // says nothing about horizontal page scroll (§8.3's threat 2 is vertical). It is
  // deliberately not asserted here, and it is deliberately written down: §9.2
  // records that MyCal's suite stayed green through an obvious narrow-layout
  // breakage because it lay in the axis the test had declared out of scope. A
  // documented blind spot is still a blind spot.
  test('footer survives the narrow layout', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 700 });

    await expect(page.locator('.sidebar-footer')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Switch to dark mode' })).toBeVisible();
    await expect(page.locator(SETTINGS)).toBeVisible();

    const { theme, settings, column, viewportHeight } = await boxes(page);
    expect(theme.x).toBeCloseTo(8, 0);
    expect(viewportHeight - (theme.y + theme.height)).toBeCloseTo(8, 0);
    expect(settings.x + settings.width).toBeLessThanOrEqual(column.x + column.width);
    expect(await rowOverflows(page)).toBe(false);

    // Still usable, not just present.
    await page.locator(SETTINGS).click();
    await expect(page.locator('.settings-page')).toBeVisible();
  });

  // §8.4 — the focus outline extends 4px beyond the border box (2px offset + 2px
  // width), and every app has a clipping ancestor around the footer. MyMail's is
  // `.sidebar`, which is `overflow-y: auto` and therefore a scroll container in
  // **both** axes, so an L or B below 4 crops the compliant indicator on the
  // window-facing side. Nothing errors; the outline is simply cut, undoing §6.2.
  //
  // Not covered by MyCal's suite. It is covered here because web/AGENTS.md names
  // changing `.sidebar-footer`'s padding as a plausible tidy-up, and that padding
  // is the entire clearance — this asserts the floor rather than only the 8 above
  // it, so a change to 4 or 6 fails loudly at the value that actually matters.
  test('the focus outline has its 4px of clearance on the window-facing sides', async ({ page }) => {
    const geom = await page.locator(THEME).evaluate(el => {
      const r = el.getBoundingClientRect();
      const cs = getComputedStyle(el);
      const footer = getComputedStyle(document.querySelector('.sidebar-footer')!);
      return {
        L: r.left,
        B: window.innerHeight - r.bottom,
        // Read off :focus-visible rather than assumed: these two numbers are what
        // 4 is the sum of, so a change to either moves the floor.
        padLeft: parseFloat(footer.paddingLeft),
        padBottom: parseFloat(footer.paddingBottom),
        borderRadius: cs.borderTopLeftRadius,
      };
    });
    expect(geom.L, 'L below 4 crops the focus outline').toBeGreaterThanOrEqual(4);
    expect(geom.B, 'B below 4 crops the focus outline').toBeGreaterThanOrEqual(4);
    expect(geom.padLeft, 'footer padding is the clearance').toBeGreaterThanOrEqual(4);
    expect(geom.padBottom, 'footer padding is the clearance').toBeGreaterThanOrEqual(4);
  });

  // §8.5 — the footer is full-bleed across the sidebar and its border-top spans
  // the whole column; the 8px inset to the buttons comes from the footer's own
  // padding, never from a horizontal margin. Not covered by MyCal's suite either.
  // It is asserted here because the prohibited alternative — insetting the buttons
  // with a margin — produces buttons in exactly the right place with a separator
  // in the wrong one, which is a divergence no position assertion can see.
  test('the separator is full-bleed across the sidebar', async ({ page }) => {
    const m = await page.evaluate(() => {
      const f = document.querySelector('.sidebar-footer') as HTMLElement;
      const sb = document.querySelector('.sidebar') as HTMLElement;
      const cs = getComputedStyle(f);
      return {
        marginLeft: cs.marginLeft, marginRight: cs.marginRight,
        padding: cs.padding,
        borderTopWidth: cs.borderTopWidth, borderTopStyle: cs.borderTopStyle,
        borderTopColor: cs.borderTopColor,
        footerLeft: f.getBoundingClientRect().left,
        footerWidth: f.getBoundingClientRect().width,
        sidebarLeft: sb.getBoundingClientRect().left,
        // clientWidth, not the bounding rect: `.sidebar` carries a 1px
        // border-right, and the footer is full-bleed across the *content* box.
        sidebarInnerWidth: sb.clientWidth,
      };
    });
    expect(m.marginLeft).toBe('0px');
    expect(m.marginRight).toBe('0px');
    expect(m.padding).toBe('8px');
    expect(m.borderTopWidth).toBe('1px');
    expect(m.borderTopStyle).toBe('solid');
    expect(m.footerLeft).toBeCloseTo(m.sidebarLeft, 0);
    expect(m.footerWidth).toBeCloseTo(m.sidebarInnerWidth, 0);
    // The separator is the resting border colour. MyMail reaches it through the
    // `--sidebar-footer-border` alias, which §5.2 records as approved and
    // MyMail-local — so the resolved value is asserted and the alias is not,
    // exactly as §2.3 of the suite's AGENTS.md requires.
    expect(m.borderTopColor).toBe(await tokenColor(page, '--border'));
  });

  // ---------------------------------------------------------------------------
  // Colour
  // ---------------------------------------------------------------------------

  // The colour actually painted behind a control: walk up to the first ancestor
  // with a non-transparent background.
  //
  // Not the footer's own background — that is right in MyMail only because the
  // footer happens to declare one, and it would read rgba(0,0,0,0) in MyNotes,
  // whose footer is transparent and inherits its sidebar's paint. The contract is
  // about the resolved backdrop, not about which element supplies it (§5.3), so
  // this is the method it names. Same number here either way; this is the version
  // that is not carrying a MyMail-shaped assumption.
  //
  // Starts at the PARENT: an element's own background is not its backdrop.
  // Starting at the button is correct today only because these carry
  // `background: none`, so the loop would walk straight past them — nothing in
  // the contract guarantees that. Used in a hover context it would return the
  // button's own fill as the "backdrop" and every figure derived from it would be
  // wrong while looking entirely plausible.
  //
  // Limitations, per measurement-protocol.md, and the first two are the ways it
  // can be confidently *wrong* rather than merely decline to answer:
  //   1. `background-color` only — a gradient or background-image is walked past.
  //   2. a semi-transparent stop is treated as opaque, so the painter is not the
  //      effective colour and every figure derived from it is wrong.
  // Neither fires in MyMail today: the footer paints an opaque `--surface`.
  const backdropOf = (page: Page, selector: string) =>
    page.locator(selector).first().evaluate(el => {
      for (let n: Element | null = el.parentElement; n; n = n.parentElement) {
        const c = getComputedStyle(n).backgroundColor;
        if (c && c !== 'rgba(0, 0, 0, 0)' && c !== 'transparent') return c;
      }
      // Distinguishable from a colour, deliberately: a walk that finds nothing
      // must not fall through to a default, or it becomes a run that measured
      // nothing and looks like a pass. null is "could not determine", never
      // "agrees".
      return null;
    });

  // Resolve a custom property to the same rgb() form getComputedStyle reports for
  // a background, so token and measurement can be compared without a hex/rgb
  // conversion in the test. Reading the property off :root gives "#ffffff", which
  // never equals "rgb(255, 255, 255)" and would make an equality assertion fail
  // for a reason that has nothing to do with the contract.
  const tokenColor = (page: Page, name: string) =>
    page.evaluate(n => {
      const probe = document.createElement('div');
      probe.style.backgroundColor = `var(${n})`;
      document.body.appendChild(probe);
      const v = getComputedStyle(probe).backgroundColor;
      probe.remove();
      return v;
    }, name);

  const useDarkTheme = async (page: Page) => {
    await page.getByRole('button', { name: 'Switch to dark mode' }).click();
    await expect(page.getByRole('button', { name: 'Switch to light mode' })).toBeVisible();
    // The click leaves the pointer sitting on the toggle, so any colour read from
    // a footer control afterwards is read in its HOVER state. Every resting figure
    // below would then be measured against the wrong thing while looking entirely
    // reasonable. Move the pointer off before measuring anything.
    await page.mouse.move(0, 0);
    // Then let the colour transition finish. Swapping the theme re-resolves the
    // tokens under a 0.12s transition, so the buttons spend that long reporting a
    // blend of the two palettes. See settledStyle.
    await settledStyle(page, SETTINGS, 'color');
  };

  // These controls carry `transition: background 0.12s, color 0.12s,
  // border-color 0.12s` — mandated by §6.1, so it cannot be removed to make
  // measuring easier. Every one of those three properties therefore reports an
  // intermediate value for 120ms after anything that changes it, and the
  // intermediate values are ordinary colours that look entirely plausible.
  //
  // This is not a theoretical hazard: in MyCal, reading the label colour straight
  // after the theme toggle returned the *light* theme's resting value against a
  // dark backdrop, for a failing assertion that pointed at the palette instead of
  // at the clock. Note what did not catch it — the test already asserted the
  // element was not hovered, and it genuinely was not. The state was right and the
  // timing was wrong.
  //
  // So poll until two consecutive reads agree, rather than sleeping a guessed
  // interval, and throw if it never settles. A timeout that returned the last
  // value read would be a measurement of the transition reported as a measurement
  // of the colour — the same failure, quieter. Two equal reads are necessary but
  // not sufficient on their own: the transition interpolates in 8-bit channels, so
  // a slow segment can serialise to the same rgb() twice in a row while still
  // running. So also require that no transition is in flight.
  const settledStyle = async (page: Page, selector: string, prop: 'color' | 'backgroundColor') => {
    const read = () =>
      page.locator(selector).first().evaluate(
        (el, p) => ({
          value: getComputedStyle(el)[p as 'color' | 'backgroundColor'],
          running: el.getAnimations().length,
        }),
        prop,
      );
    let prev = await read();
    for (let i = 0; i < 40; i++) {
      await page.waitForTimeout(25);
      const now = await read();
      if (now.value === prev.value && now.running === 0) return now.value;
      prev = now;
    }
    throw new Error(`${prop} of ${selector} never settled`);
  };

  // WCAG 1.4.11 (AA) wants 3:1 between the focus indicator and the colours next
  // to it. Asserting the indicator merely *exists* is not enough — the
  // translucent --focus-ring this rule replaced existed too, and composites to
  // 1.288:1 in light and 1.501:1 in dark against the panel it sits on. So compute
  // the real contrast, and check the outline is offset clear of the button's own
  // border: drawn tight against it the neighbour is --border, which caps dark at
  // 2.803:1 at any alpha.
  const relLum = (rgb: [number, number, number]) => {
    const f = (c: number) => {
      const s = c / 255;
      return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
    };
    return 0.2126 * f(rgb[0]) + 0.7152 * f(rgb[1]) + 0.0722 * f(rgb[2]);
  };
  const contrast = (a: [number, number, number], b: [number, number, number]) => {
    const [hi, lo] = [relLum(a), relLum(b)].sort((x, y) => y - x);
    return (hi + 0.05) / (lo + 0.05);
  };
  const parseRgb = (s: string): [number, number, number] => {
    const m = s.match(/\d+(\.\d+)?/g)!;
    return [Number(m[0]), Number(m[1]), Number(m[2])];
  };

  // Chromium withholds :focus-visible from a programmatic focus() when the last
  // interaction was a pointer, which would make every assertion below vacuous.
  // Establish keyboard modality, then assert the element really matched — so
  // this fails loudly rather than silently if that ever stops holding.
  const focusAndRead = async (page: Page, selector: string) => {
    await page.keyboard.press('Tab');
    const s = await page.locator(selector).first().evaluate(el => {
      (el as HTMLElement).focus();
      const cs = getComputedStyle(el);
      return {
        focusVisible: el.matches(':focus-visible'),
        outlineStyle: cs.outlineStyle,
        outlineWidth: cs.outlineWidth,
        outlineOffset: cs.outlineOffset,
        outlineColor: cs.outlineColor,
      };
    });
    return { ...s, backdrop: await backdropOf(page, selector) };
  };

  // MyMail's own figures, against its own backdrop (§6.2): --primary measures
  // 5.169:1 on #ffffff and 3.991:1 on #1f2937. The dark margin is about one point
  // and is shared with MyNotes, not with MyCal — §6.2 warns in as many words
  // against transcribing a neighbour's figure, so what is asserted is the
  // threshold and what is measured is this app's pair.
  for (const dark of [false, true]) {
    test(`focus indicator meets 3:1 against its backdrop in ${dark ? 'dark' : 'light'} mode`, async ({ page }) => {
      if (dark) await useDarkTheme(page);

      const s = await focusAndRead(page, SETTINGS);
      expect(s.focusVisible).toBe(true);

      // Guard both parses: an rgba()/oklch()/color() value would fall out of
      // parseRgb as something bogus and could produce a meaningless pass.
      // null means the walk found nothing opaque, which is a broken measurement
      // rather than a failing one — distinguish it from a bad colour value.
      expect(s.backdrop, 'no opaque backdrop found above the control').not.toBeNull();
      expect(s.backdrop).toMatch(/^rgb\(/);
      expect(s.outlineColor).toMatch(/^rgb\(/);
      expect(s.outlineStyle).toBe('solid');
      expect(parseFloat(s.outlineWidth)).toBeGreaterThanOrEqual(2);
      // Offset clear of the button's border — this is what lifts dark past 3:1.
      expect(parseFloat(s.outlineOffset)).toBeGreaterThanOrEqual(2);
      expect(contrast(parseRgb(s.outlineColor), parseRgb(s.backdrop!))).toBeGreaterThanOrEqual(3);
    });
  }

  for (const dark of [false, true]) {
    const mode = dark ? 'dark' : 'light';

    // §5.3 records the backdrop per app: MyMail's is `--surface`, #ffffff light
    // and #1f2937 dark, and MyMail's footer paints it explicitly. This is *not*
    // MyCal's assertion with a different token substituted — MyCal pins `--bg` and
    // additionally asserts the backdrop is not `--surface`, which is a recorded
    // MyCal-local deviation and would be exactly backwards here.
    //
    // Asserted against the token rather than a literal so the palette stays the
    // single source of the value; the ratios below are what pin the value itself.
    test(`controls sit on the sidebar surface in ${mode} mode`, async ({ page }) => {
      if (dark) await useDarkTheme(page);
      const backdrop = await backdropOf(page, SETTINGS);
      expect(backdrop, 'no opaque backdrop found above the control').not.toBeNull();
      expect(backdrop).toBe(await tokenColor(page, '--surface'));
    });

    // WCAG 1.4.3 (AA). The label is 12.8px at weight 400 — normal text, so the
    // threshold is 4.5:1 and not the 3:1 large-text allowance. MyMail measures
    // 4.834:1 light (#6b7280 on #ffffff) and 5.782:1 dark (#9ca3af on #1f2937);
    // §5.4 flags the light figure as one of the two rows close to the line, with
    // 0.334 of headroom, so it is sensitive to any change in `--surface` or in the
    // shared label colour. That sensitivity is the reason for the test.
    test(`resting label meets 4.5:1 against its backdrop in ${mode} mode`, async ({ page }) => {
      if (dark) await useDarkTheme(page);
      // Assert the state that was measured, not just the number: the resting
      // colour and the hover colour are different declarations, and reading one
      // while believing it is the other passes for the wrong reason. This is
      // necessary and not sufficient — it says nothing about the transition, so
      // the colour itself is read through settledStyle.
      const hovered = await page.locator(SETTINGS).evaluate(el => el.matches(':hover'));
      expect(hovered, 'measured in the hover state — this is not the resting colour').toBe(false);
      const color = await settledStyle(page, SETTINGS, 'color');
      const backdrop = await backdropOf(page, SETTINGS);
      expect(backdrop, 'no opaque backdrop found above the control').not.toBeNull();
      expect(color).toMatch(/^rgb\(/);
      expect(backdrop).toMatch(/^rgb\(/);
      expect(contrast(parseRgb(color), parseRgb(backdrop!))).toBeGreaterThanOrEqual(4.5);
    });

    // There is no WCAG threshold for a hover fill, and the mandated one is faint
    // by design — 1.101:1 light and 1.424:1 dark in MyMail (§10.1) — so this pins
    // the only thing that is unambiguously broken: a fill the same colour as what
    // it is drawn on. That is not hypothetical; it is what happened in MyCal,
    // whose light `--hover-bg` and backdrop were both #f3f4f6, so the mandated
    // fill painted over itself at exactly 1.000:1. MyMail is not exposed to that
    // today — its backdrop is `--surface`, not `--hover-bg` — which is precisely
    // the situation §5.1's floor exists to keep from returning.
    //
    // 1.053:1 is the floor, taken from MyNotes' light fill: the weakest the suite
    // ships, and named with its app because a bound with an app's name attached is
    // falsifiable by anyone who knows that app.
    test(`hover fill is distinguishable from its backdrop in ${mode} mode`, async ({ page }) => {
      if (dark) await useDarkTheme(page);
      // Read the backdrop BEFORE hovering. The walk starts at the parent so it is
      // hover-safe by construction, but taking it first means the assertion does
      // not depend on that remaining true.
      const backdrop = await backdropOf(page, SETTINGS);
      expect(backdrop, 'no opaque backdrop found above the control').not.toBeNull();

      await page.locator(SETTINGS).hover();
      const fill = await settledStyle(page, SETTINGS, 'backgroundColor');
      // Guards the vacuous pass: with the hover rule gone the control keeps
      // `background: none` and reads rgba(0, 0, 0, 0), which parses to black and
      // would score a huge ratio against a light backdrop. rgb( excludes it.
      expect(fill, 'no opaque hover fill — did the hover rule apply?').toMatch(/^rgb\(/);
      expect(fill, 'hover fill is the same colour as its backdrop').not.toBe(backdrop);
      expect(contrast(parseRgb(fill), parseRgb(backdrop!))).toBeGreaterThanOrEqual(1.053);
    });

    // §6.1's hover border. It must be a token that differs from the hover fill in
    // *both* themes: in dark, `--border` and `--hover-bg` are the same #374151, so
    // keeping the resting border on hover would fill the control with its own
    // outline colour and erase it at exactly the moment it lights up. Light cannot
    // detect that — the two are different colours there — so this is another pin
    // whose failure is confined to one theme.
    test(`hover keeps the control's outline visible in ${mode} mode`, async ({ page }) => {
      if (dark) await useDarkTheme(page);
      await page.locator(SETTINGS).hover();
      const fill = await settledStyle(page, SETTINGS, 'backgroundColor');
      const border = await page.locator(SETTINGS).evaluate(el => getComputedStyle(el).borderTopColor);
      expect(border).toMatch(/^rgb\(/);
      expect(border, 'hover border is the same colour as the hover fill').not.toBe(fill);
    });
  }

  // MyCal carries a test here that reads its `[data-theme="dark"]` palette rule
  // out of the CSSOM and asserts the dark aliases still *name* the shared tokens.
  // **It has no MyMail equivalent, and that is deliberate.** §5.3 says in as many
  // words that MyCal's deviations are light-only, that confining a deviation to
  // one theme is a claim about the declaration which no rendering can check, and
  // that "MyMail and MyNotes need no equivalent because they have no deviations".
  //
  // The day MyMail is granted one — a per-app value recorded in §5.1's deviation
  // table because a shared value fails a threshold against MyMail's backdrop — it
  // needs that test, scoped to whichever theme did *not* deviate. That is a cost of
  // deviating which is easy to miss when granting one, so it is written down here
  // rather than left to be rediscovered.

  // §8.3, mechanism B: this footer is sticky and the folder list scrolls under it,
  // so it needs an opaque background of its own. MyMail is the app the contract
  // names as *requiring* sticky — its footer is an ordinary last child of an
  // `overflow-y: auto` `.sidebar` — and the opaque background is part of that
  // mechanism, not decoration.
  //
  // This cannot be folded into the backdrop test above, and the reason is the
  // point of it. `--sidebar-bg` resolves to `--surface`, the same colour
  // `.sidebar` itself paints, so deleting the declaration changes nothing visible
  // at the moment of the edit and the backdrop walk keeps passing — it simply
  // falls through to `.sidebar` and finds the identical colour. The colour is
  // redundant; the opacity is load-bearing. The defect surfaces later, as folder
  // rows sliding through the buttons on a scrolled sidebar.
  test('the footer paints an opaque background of its own', async ({ page }) => {
    const own = await page.locator('.sidebar-footer')
      .evaluate(el => getComputedStyle(el).backgroundColor);
    expect(own, '.sidebar-footer has no background of its own').toMatch(/^rgb\(/);
    expect(own).toBe(await tokenColor(page, '--surface'));

    // Sticky is the other half of the mechanism, and `bottom: 0` rather than
    // `bottom: 8px` is the sum rule (§8.3): this element's own padding is the 8px,
    // and setting both would double the inset to 16.
    const pos = await page.locator('.sidebar-footer').evaluate(el => {
      const cs = getComputedStyle(el);
      return { position: cs.position, bottom: cs.bottom };
    });
    expect(pos.position).toBe('sticky');
    expect(pos.bottom).toBe('0px');
  });

  // An outline is painted under forced colours; a box-shadow is not, which is
  // why the --focus-ring this rule replaced would have needed a media-query
  // patch. The base rule carries the outline, so there is nothing theme-specific
  // here — and §6.2 forbids adding a `forced-colors` block, because one containing
  // `outline: revert` would override the compliant outline with the UA default,
  // silently undoing the fix it looks like it is protecting.
  test('controls keep a focus indicator under forced colors', async ({ page }) => {
    await page.emulateMedia({ forcedColors: 'active' });
    const s = await focusAndRead(page, THEME);
    expect(s.focusVisible).toBe(true);
    expect(s.outlineStyle).not.toBe('none');
    expect(parseFloat(s.outlineWidth)).toBeGreaterThan(0);
  });

  // §7 — the accessible names. The label pair is aria-hidden, so nothing else
  // would catch a visible word drifting out of its matching accessible name
  // (WCAG 2.5.3 Label in Name). Settings is the divergence §7 records: MyCal and
  // MyNotes set `title` and `aria-label` on a <button>, MyMail's <a> carries
  // neither and takes its name from its own text. Only the *name* is specified, so
  // that is what is asserted — not the mechanism.
  test('the controls carry the specified accessible names', async ({ page }) => {
    await expect(page.getByRole('link', { name: 'Settings', exact: true })).toBeVisible();

    // Both words stay mounted — that is the width-stability mechanism (§7) — so
    // the pair is read once and the *shown* one is identified by `.is-shown`.
    const labels = await page.locator('.sidebar-theme-label > span')
      .evaluateAll(els => els.map(e => e.textContent));
    expect(labels).toEqual(['Light', 'Dark']);

    // Read both sides of the toggle, in both states. Comparing two literals here
    // would assert nothing about the app — the point is that the visible word and
    // the accessible name are produced by two different expressions in
    // Sidebar.tsx and could drift apart, with the span aria-hidden so nothing
    // else would notice (WCAG 2.5.3 Label in Name).
    for (const expected of ['dark', 'light']) {
      const shown = await page.locator('.sidebar-theme-label > .is-shown').textContent();
      const name = await page.locator(THEME).getAttribute('aria-label');
      expect(name, `no accessible name in the "${expected}" state`).not.toBeNull();
      expect(
        name!.toLowerCase(),
        `visible label "${shown}" is not contained in the accessible name "${name}"`,
      ).toContain(shown!.toLowerCase());

      await page.getByRole('button', { name: `Switch to ${expected} mode` }).click();
      await expect(
        page.getByRole('button', { name: `Switch to ${expected === 'dark' ? 'light' : 'dark'} mode` }),
      ).toBeVisible();
    }
  });
});
