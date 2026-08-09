import { test, expect, type Page } from '@playwright/test';

// The brand row at the top left of the sidebar — the badge and the app-name
// label beside it — is governed by **two** contracts in the sibling `mysuite`
// repository, and neither is defined here:
//
//   ../mysuite/spec/app-logo.md        the badge: box, mark, colour, placement
//   ../mysuite/spec/app-name-label.md  the label: font, size, placement
//
// **Every § reference below names its document.** Four documents in that repo
// now have a §2, §3 and §4, so a bare § is ambiguous by construction.
//
// See also the `.logo-icon` block in web/static/app.css and the two sections in
// web/AGENTS.md that name the ordinary-looking edits that break either contract.
//
// **The contracts are the source of truth for every number in this file.** They
// are repeated here because an assertion cannot reference a value, only hold it;
// when one of them moves it moves there first and this file follows.
//
// This is MyMail's half of both. Until it existed MyMail had **no** assertion
// about the badge or the label anywhere — `app-logo.md` §9.4 and
// `app-name-label.md` §8.2 both record that, and MyMail is the app those
// contracts were largely written from. Like sidebar-footer.spec.ts it cannot see
// the other two apps: it shows MyMail still satisfies its half and says nothing
// about whether the three still agree. `app-name-label.md` §7.2 is explicit that
// three per-app suites are not a cross-repo check and that adding more cannot
// make one.
//
// **Two things here are deliberately not asserted, because a rendered suite
// cannot hold them** (`app-name-label.md` §7.1, §7.3):
//   - `1.1rem` → `17.6px` is invisible at the root a test runs at. The two-root
//     assertion below narrows it and does not close it; `tools/check-contract.py`
//     is what compares the declaration text.
//   - which font actually renders. `system-ui` resolves per machine, and this
//     sandbox resolves it to a monospace face. Only the declared stack is
//     assertable, and `app-name-label.md` §3.1 mandates only that.
//
// Modelled on ../mynotes/e2e/tests/logo.spec.ts, the first of these. Where
// MyMail differs the comment says what changed; where MyNotes covers something
// MyMail has no counterpart for, the comment says so rather than dropping it
// silently.
//
// Everything here is MEASURED on a rendered page rather than read off an
// attribute or a prop — `app-logo.md` §3.2's wording is "renders at 17x17",
// never "the attribute says 17". That distinction is live in MyMail
// specifically: its glyph is sized by an SVG attribute from `<Icon size={17}>`,
// which any author CSS rule matching that SVG would silently outrank
// (`app-logo.md` §5).
test.describe('App logo and app-name label contracts', () => {
  const BADGE = '.sidebar-header .logo-icon';
  const HEADER = '.sidebar-header';

  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // A selector that matches nothing returns a falsy value, and a falsy value
    // compared against an expectation frequently reads as a pass.
    await expect(page.locator(BADGE)).toBeVisible();
    // State the starting theme rather than assuming it: the dark-mode tests
    // below click "Switch to dark mode" and would report only "element not
    // found" if the app had started dark.
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  });

  const darkMode = async (page: Page) => {
    await page.getByRole('button', { name: 'Switch to dark mode' }).click();
    await expect(page.locator('html[data-theme="dark"]')).toHaveCount(1);
  };

  // Sets the root unconditionally, including at 16 — an early return would make
  // the helper one-way, and a second call in a future test would silently keep
  // the first root instead of resetting it.
  //
  // **This emulates a reader's browser font setting; it is not that setting.**
  // The two were checked against each other by hand during the work that added
  // this file — CDP `Page.setFontSizes`, which *is* the browser's own control,
  // agreed to the last digit at 16 / 20 / 24 / 32px roots — but only the CSS
  // mechanism runs here, so nothing in this suite covers the real one.
  const setRoot = async (page: Page, root: number) => {
    await page.evaluate(r => { document.documentElement.style.fontSize = `${r}px`; }, root);
    await page.evaluate(() => new Promise(requestAnimationFrame));
  };

  const geometry = (page: Page) => page.evaluate(() => {
    const badge = document.querySelector('.sidebar-header .logo-icon') as HTMLElement;
    const svg = badge.querySelector('svg') as SVGSVGElement | null;
    const header = document.querySelector('.sidebar-header') as HTMLElement;
    const b = badge.getBoundingClientRect();
    const g = svg?.getBoundingClientRect();
    const bCS = getComputedStyle(badge);
    // `|| 0` normalises negative zero: `(-0.0002).toFixed(3)` is `-0`, and the
    // delta assertions below use toEqual, which distinguishes -0 from 0. A
    // probe reporting "the wrapper moved the label" for a sign bit would send
    // the next reader after a layout bug that does not exist.
    const r3 = (n: number) => +n.toFixed(3) || 0;

    // The label is a bare text node with no element of its own — MyMail's shape,
    // recorded in `app-logo.md` §1 and `app-name-label.md` §6.1, and NOT an
    // omission. `app-name-label.md` §8.4 says requiring an element would make
    // things worse, so nothing here should be read as asking for one.
    //
    // **Searched over descendants, not direct children, and that is a deliberate
    // departure from MyNotes' implementation of the same method.** §6.1 wants a
    // later `<span>` wrapper to break loudly on `labelParentIs…`; a
    // direct-children search cannot do that, because wrapping moves the node out
    // of the list and the run fails on "no label text node" instead — loud, but
    // pointing at a disappearance that did not happen. Measured: with the search
    // below, wrapping the label fails on the flag and its message; with a
    // direct-children search it failed on `labelFound` in 12 tests.
    // The badge's and the Reload button's subtrees are skipped so that a future
    // text child of either cannot be mistaken for the label.
    const walker = document.createTreeWalker(header, NodeFilter.SHOW_TEXT, {
      acceptNode: (n) => {
        if (n.textContent!.trim().length === 0) return NodeFilter.FILTER_REJECT;
        const el = n.parentElement;
        if (el?.closest('.logo-icon, .sidebar-reload-btn')) return NodeFilter.FILTER_REJECT;
        return NodeFilter.FILTER_ACCEPT;
      },
    });
    const labelNode = walker.nextNode() ?? undefined;

    // `app-name-label.md` §6.1: a bare text node has TWO boxes and they are not
    // interchangeable. The **ink box** (a Range) is the resolved face's ascent
    // and descent, so it is face-dependent and its numbers are not
    // transferable. The **flex-item box** is the line box — pure layout, and the
    // one the contract pins. Measure both and name both.
    let ink: DOMRect | null = null;
    let item: DOMRect | null = null;
    let wrapDelta: { dx: number; dy: number; dw: number; dh: number } | null = null;
    let unwrapDelta: { dx: number; dy: number; dw: number; dh: number } | null = null;

    if (labelNode) {
      const range = document.createRange();
      const inkRect = () => { range.selectNodeContents(labelNode); return range.getBoundingClientRect(); };
      const before = inkRect();

      // The flex-item box needs a temporary wrapper, because an anonymous flex
      // item has no node to query. `app-name-label.md` §6.1 requires the
      // wrapper's layout-neutrality to be **asserted on every run**, not assumed
      // once: a probe that changes what it measures is the failure
      // `measurement-protocol.md` exists to prevent.
      const wrap = document.createElement('span');
      labelNode.parentNode!.insertBefore(wrap, labelNode);
      wrap.appendChild(labelNode);
      const during = inkRect();
      const w = wrap.getBoundingClientRect();
      wrapDelta = {
        dx: r3(during.x - before.x), dy: r3(during.y - before.y),
        dw: r3(during.width - before.width), dh: r3(during.height - before.height),
      };
      item = new DOMRect(w.x, w.y, w.width, w.height);

      wrap.parentNode!.insertBefore(labelNode, wrap);
      wrap.remove();
      const after = inkRect();
      unwrapDelta = {
        dx: r3(after.x - before.x), dy: r3(after.y - before.y),
        dw: r3(after.width - before.width), dh: r3(after.height - before.height),
      };
      ink = new DOMRect(before.x, before.y, before.width, before.height);
    }

    return {
      badge: { x: r3(b.x), y: r3(b.y), w: r3(b.width), h: r3(b.height) },
      // null, not zero: "the mark is missing" and "the mark is 0px" are
      // different findings and only one of them is a layout bug. `<Icon>`
      // returns null for a name absent from the vendored bundle, so an empty
      // badge is a reachable state here, not a hypothetical
      // (`app-logo.md` §9.5).
      glyph: g ? { w: r3(g.width), h: r3(g.height) } : null,
      // Insets of the glyph box within the badge box, all four sides.
      inset: g ? {
        left: r3(g.left - b.left), top: r3(g.top - b.top),
        right: r3(b.right - g.right), bottom: r3(b.bottom - g.bottom),
      } : null,
      padding: bCS.padding,
      margin: bCS.margin,
      borderWidth: bCS.borderTopWidth,
      radius: bCS.borderRadius,
      fill: bCS.backgroundColor,
      // MyMail's mark is stroke-only — `fill: none` on every element
      // (`app-logo.md` §6.1) — so its colour is the stroke, arriving through
      // `currentColor` from the badge's own `color`. Reading `fill` here (as
      // MyNotes correctly does for its filled path) would return "none" in
      // every theme and compare equal to itself forever.
      glyphColour: svg ? getComputedStyle(svg).color : null,
      glyphStroke: svg?.querySelector('path')
        ? getComputedStyle(svg.querySelector('path') as SVGPathElement).stroke
        : null,

      labelInk: ink ? { x: r3(ink.x), y: r3(ink.y), w: r3(ink.width), h: r3(ink.height) } : null,
      labelItem: item ? { x: r3(item.x), y: r3(item.y), w: r3(item.width), h: r3(item.height) } : null,
      wrapDelta, unwrapDelta,

      // Gap to the label: badge's right edge to the label's leading edge, not
      // read off the `gap` property (that would assert the CSS against itself)
      // and not taken from the header's own box.
      //
      // Measured to the **flex-item** box, not the ink box, for the reason §6.1
      // gives for pinning the label's y there: the ink box's edges are the
      // resolved face's, and `app-name-label.md` §3.1 records that GitHub's CI
      // runner resolves `system-ui` about 1.25x wider than a local machine. The
      // two agree at 8.000 here and would not have to. The ink box is returned
      // alongside so a failure can say which is which.
      // null, never 0 — "could not measure" is not "no gap".
      gapToLabel: item ? r3(item.left - b.right) : null,
      gapToLabelInk: ink ? r3(ink.left - b.right) : null,

      // `app-name-label.md` §6.1's method: read the computed values from the
      // element the label text actually inherits from, **resolved at runtime**,
      // rather than from `.sidebar-header` on the assumption that they are the
      // same element. They are today — the label is a bare text node whose
      // parent IS the header — but wrapping it in a <span> with its own
      // font-size would leave a header-based assertion green while the rendered
      // label changed. `labelParentIsHeader` records which case was measured, so
      // the assumption breaks loudly instead of silently. It is MyMail's
      // counterpart of MyNotes' `labelParentIsAnchor`.
      ...(() => {
        const host = (labelNode?.parentElement ?? header) as HTMLElement;
        const cs = getComputedStyle(host);
        return {
          labelFound: !!labelNode,
          labelText: labelNode ? labelNode.textContent!.trim() : null,
          labelParentIsHeader: host === header,
          labelFontFamily: cs.fontFamily,
          labelFontSize: cs.fontSize,
          labelFontWeight: cs.fontWeight,
        };
      })(),

      badgeIsFirst: header.firstElementChild === badge,
      // MyMail's badge is a plain <div>, not inside a link — the counterpart of
      // MyNotes' `badgeInsideAnchor`, asserted in the opposite direction
      // (`app-logo.md` §8).
      badgeIsPlainDiv: badge.tagName === 'DIV'
        && !badge.closest('a') && !badge.closest('button'),
      ariaHidden: badge.getAttribute('aria-hidden') === 'true'
        || svg?.getAttribute('aria-hidden') === 'true',
    };
  });

  // Both contracts' geometry rests on this probe not disturbing what it
  // measures, so it is checked on every run rather than argued once
  // (`app-name-label.md` §6.1).
  const expectProbeNeutral = (m: Awaited<ReturnType<typeof geometry>>) => {
    expect(m.labelFound, 'no label text node — nothing below was measured').toBe(true);
    expect(m.wrapDelta, 'the wrapper moved the label — every figure here is suspect')
      .toEqual({ dx: 0, dy: 0, dw: 0, dh: 0 });
    expect(m.unwrapDelta, 'the page was not restored after measuring')
      .toEqual({ dx: 0, dy: 0, dw: 0, dh: 0 });
  };

  // ---------------------------------------------------------------------------
  // The badge — ../mysuite/spec/app-logo.md
  // ---------------------------------------------------------------------------

  for (const root of [16, 24]) {
    for (const theme of ['light', 'dark'] as const) {
      test(`badge and glyph render at contract size — ${theme}, ${root}px root`, async ({ page }) => {
        if (theme === 'dark') await darkMode(page);
        await setRoot(page, root);
        const m = await geometry(page);
        // **Deliberately no `expectProbeNeutral` and no gap assertion here.**
        // Nothing in this test involves the label, and a badge test that goes
        // red for a label reason points the reader at the wrong element. The
        // gap is the one badge value that needs the label, so it has a test of
        // its own below. *(mynotes-dev-b hit the bad version of this: deleting
        // its label turned four badge-size tests red.)*

        // `app-logo.md` §3.1.
        expect(m.badge.w).toBeCloseTo(28, 1);
        expect(m.badge.h).toBeCloseTo(28, 1);
        expect(m.radius).toBe('6px');

        // An absent mark is a state this app can actually reach: `<Icon>`
        // returns null for a name missing from the vendored bundle, so pruning
        // 'mail' from gen-lucide.mjs's ICONS list leaves an empty blue square
        // and a green build (`app-logo.md` §9.5). It would satisfy every box
        // assertion here, which is why this one comes first and is not folded
        // into them.
        expect(m.glyph, 'no <svg> in the badge — the mark did not render').not.toBeNull();
        // `app-logo.md` §3.2.
        expect(m.glyph!.w).toBeCloseTo(17, 1);
        expect(m.glyph!.h).toBeCloseTo(17, 1);

        // The badge is sized in px and the label in rem, so the pair of roots
        // above also records that the badge does NOT grow with the reader's
        // font. Both sibling apps ship it that way; the rem question is an open
        // design item upstream (`app-logo.md` §9.2), not a local choice.

        // Centring: equal (28 − 17) / 2 = 5.5px insets on all four sides. The
        // box sizes alone do NOT imply this — with the global
        // `* { box-sizing: border-box }`, a `padding-left` on .logo-icon keeps
        // the badge 28x28 and the glyph 17x17 while sliding the mark off centre,
        // and every other assertion here stays green.
        expect(m.inset!.left, 'left inset').toBeCloseTo(5.5, 1);
        expect(m.inset!.top, 'top inset').toBeCloseTo(5.5, 1);
        expect(m.inset!.right, 'right inset').toBeCloseTo(5.5, 1);
        expect(m.inset!.bottom, 'bottom inset').toBeCloseTo(5.5, 1);

        // `app-logo.md` §3.1 also specifies these are zero; centring is achieved
        // by flex alignment, not by padding that happens to be symmetric.
        expect(m.padding).toBe('0px');
        expect(m.margin).toBe('0px');
        expect(m.borderWidth).toBe('0px');
      });
    }
  }

  // `app-logo.md` §3.4's 8px, and `app-name-label.md` §3.3 defers to it rather
  // than restating it — so this one assertion holds a value from both contracts.
  //
  // Measured at two roots because the gap is `px` while the label around it is
  // `rem`: at one root a `gap: 0.5rem` would be indistinguishable from `8px`.
  for (const root of [16, 24]) {
    test(`the badge is 8px from the label — ${root}px root`, async ({ page }) => {
      await setRoot(page, root);
      const m = await geometry(page);
      expectProbeNeutral(m);
      expect(m.gapToLabel, 'label text node not found').not.toBeNull();
      expect(
        m.gapToLabel!,
        `gap to the label's flex-item box (its ink box gives ${m.gapToLabelInk}; if the two differ, the face is what differs and only the first is the contract's)`,
      ).toBeCloseTo(8, 1);
    });
  }

  // `app-logo.md` §3.3 imposes a floor on how much of the glyph box the mark's
  // rendered INK must span, and there is deliberately no assertion for it here.
  //
  // MyNotes can assert it with `path.getBoundingClientRect()` because its mark
  // is fill-only, so geometry and ink coincide. **MyMail's is stroke-only**, so
  // the two differ — by 8.3 percentage points at lucide-1.25.0 — and that
  // section requires every figure reported against the floor to name which box
  // it is. A geometry-shaped number asserted against an ink-shaped floor would
  // be the exact mistake it exists to prevent, and it would pass.
  //
  // The measurement that would be right is a pixel count off a high-scale
  // element screenshot, and it belongs to the event that can actually break it:
  // a Lucide version bump, since the mark is vendored and gets redrawn between
  // releases. web/AGENTS.md carries that procedure as a step of the upgrade.
  // Recorded here so the omission reads as a decision.

  // `app-logo.md` §4: (16, 14) at rest, in all three apps.
  //
  // **The root sizes are chosen, and 24 is the one that matters.** Until
  // `.logo-icon` gained `align-self: flex-start`, this badge read a clean 14.000
  // at a 16px root and 19.797 at 24px: `.sidebar-header` centres its children,
  // so the badge's y was a remainder of the row's tallest item, and the badge
  // wins that only up to a ~16.97px root. A 16px-only test would have been green
  // throughout the whole time it was broken.
  //
  // The label's assertions further down have the **opposite** exposure and are
  // pinned at a 16px root for it — there, the label is the shorter box and the
  // one being centred. **This repo has now demonstrated both directions: a
  // defect invisible at the default root, and a defect visible only at it. A
  // suite that picks one root picks which of the two it can never see.**
  for (const root of [16, 20, 24, 32]) {
    test(`the badge sits 16px from the left and 14px from the top — ${root}px root`, async ({ page }) => {
      await setRoot(page, root);
      const box = await page.locator(BADGE).boundingBox();
      expect(box, 'badge has no box').not.toBeNull();
      expect(box!.x, 'distance from the left viewport edge').toBeCloseTo(16, 1);
      expect(box!.y, 'distance from the top viewport edge').toBeCloseTo(14, 1);

      // Not scrolled — "at rest" is part of the claim, and the sidebar scrolls
      // the whole header off the window under enough folders
      // (`app-logo.md` §9.1, `app-name-label.md` §4.5), which would give a y
      // that means something else entirely.
      expect(await page.evaluate(() => window.scrollY)).toBe(0);
      expect(await page.locator('.sidebar').evaluate(el => el.scrollTop)).toBe(0);
    });
  }

  // **This is what makes the badge's position *authored* rather than merely
  // correct** (`app-logo.md` §4.2). A remainder that happens to be zero and a
  // number that is declared are the same reading and different conformance;
  // only a mutation tells them apart. This grows the LABEL's line-height — a
  // property neither `.sidebar` nor `.sidebar-header` mentions — and requires
  // the badge not to move. `.logo-icon`'s `align-self: flex-start` is what makes
  // that true, and deleting it fails these two and the 20/24/32px cases above.
  //
  // MyMail needs one such declaration where MyCal and MyNotes each need two:
  // its badge and label are direct children of the same flex row, so there is no
  // intermediate brand block with its own centring to escape
  // (`app-name-label.md` §6.3).
  //
  // Note the asymmetry with the label, which this contract deliberately does NOT
  // give the same protection: `app-name-label.md` §4.3 records the label's
  // vertical position rather than mandating it, because a declaration would move
  // three working apps ~0.8px to defend a value that is currently correct
  // everywhere. So there is no label counterpart of this test, on purpose.
  for (const lh of ['2.2', '4']) {
    test(`the badge does not move when the label's line-height grows to ${lh}`, async ({ page }) => {
      const before = (await page.locator(BADGE).boundingBox())!;
      const headerBefore = (await page.locator(HEADER).boundingBox())!;

      await page.evaluate(v => {
        (document.querySelector('.sidebar-header') as HTMLElement).style.lineHeight = v;
      }, lh);

      const headerAfter = (await page.locator(HEADER).boundingBox())!;
      // Prove the mutation actually took: if the header did not grow, the badge
      // holding still proves nothing at all.
      expect(headerAfter.height, 'label line-height did not change the header — vacuous test')
        .toBeGreaterThan(headerBefore.height + 1);

      const after = (await page.locator(BADGE).boundingBox())!;
      expect(after.y, 'badge y moved with the label — it is still a remainder').toBeCloseTo(before.y, 1);
      expect(after.y).toBeCloseTo(14, 1);
    });
  }

  // ---------------------------------------------------------------------------
  // The app-name label — ../mysuite/spec/app-name-label.md
  // ---------------------------------------------------------------------------

  // `app-name-label.md` §3.1: the declared stack, and only the declared stack.
  // "The same font" means the same request made of the platform; it does not and
  // cannot mean the same rendered face, because `system-ui` resolves per
  // machine. This sandbox resolves it to a monospace face — which is the sandbox
  // and not the app — so the face is unassertable here and §3.1 mandates only
  // the string.
  //
  // The stack is authored on `body` and nowhere nearer the label
  // (web/static/app.css), which is why §8.1 lists editing it as the first silent
  // breakage: a change made for body copy is a change to this contract and will
  // not look like one. This assertion is what makes it look like one.
  test('the label resolves the contract font stack', async ({ page }) => {
    const m = await geometry(page);
    expectProbeNeutral(m);
    expect(m.labelFontFamily).toBe('system-ui, -apple-system, "Segoe UI", Roboto, sans-serif');
  });

  // `app-name-label.md` §3.2: `1.1rem`, computing to 17.6px at a 16px root, and
  // it must scale with the root.
  //
  // **The second root is what makes this a claim about `rem`.** At a 16px root
  // alone, a `px` declaration and a `rem` declaration are the same reading. Two
  // roots narrow it; they do not close it, and §7.1 is explicit that no rendered
  // suite can — `1.1rem` → `1.10rem` is identical computed AND identical
  // serialised. `tools/check-contract.py` compares the declaration text and is
  // the only thing that sees that edit.
  //
  // Asserted in px because that is what getComputedStyle returns; it is a rem
  // value in the CSS, deliberately.
  for (const [root, expected] of [[16, '17.6px'], [24, '26.4px']] as const) {
    test(`the label is 1.1rem/600, read from its own host — ${root}px root`, async ({ page }) => {
      await setRoot(page, root);
      const m = await geometry(page);
      expectProbeNeutral(m);

      expect(m.labelText).toBe('MyMail');
      // Not a style assertion: it records WHICH element the two below were read
      // from. Wrapping the label in a <span> is layout-neutral (measured), so
      // nothing else here would notice — but a class on that span carrying its
      // own font-size would change the rendered label while these stayed green.
      expect(
        m.labelParentIsHeader,
        'the label is no longer a bare text node in .sidebar-header — the values below were read from a different element, so re-derive them before trusting this test',
      ).toBe(true);

      expect(m.labelFontSize).toBe(expected);
      expect(m.labelFontWeight).toBe('600');
    });
  }

  // `app-name-label.md` §4.1: the label is vertically centred in a row whose top
  // edge is y = 14 and whose height is the row's tallest item. Tolerance
  // ±0.02px, and the contract quotes the measured value rather than the closed
  // form: `14 + (28 − 26.390625)/2` gives 14.8047, every app measures 14.797,
  // and the 1/128px difference is Chromium's LayoutUnit snapping (§4.1.1). So
  // the number below is a measurement, not arithmetic — do not "correct" it.
  //
  // **Pinned on the flex-item box, not the ink box** (§6.1). The ink box is the
  // resolved face's ascent and descent and is not transferable between machines;
  // the flex-item box is the line box and is pure layout.
  //
  // **The 16px root is mandatory here** (§4.1.1, mycal-dev's finding, kept
  // because the inversion is the point): above ~17px the label is the taller box
  // and sits flat at 14, so a large-root test finds it perfectly aligned and
  // measures the one case where a centring defect is absent. The 24px case below
  // records the swap rather than replacing the 16px one.
  test('the label sits at 14.797 at a 16px root — the centred case', async ({ page }) => {
    const m = await geometry(page);
    expectProbeNeutral(m);
    expect(
      Math.abs(m.labelItem!.y - 14.797),
      `label flex-item y was ${m.labelItem!.y} (ink box ${m.labelInk!.y}, for reference only)`,
    ).toBeLessThanOrEqual(0.02);

    // x is checked as a **consequence**, not as a fourth pin (§3.3): it is the
    // badge's 16 + the badge's 28 + the 8px gap, each owned by app-logo.md. If
    // this is ever wrong, the defect is in one of those three.
    expect(m.labelItem!.x, 'x is entailed by app-logo.md §4, §3.1 and §3.4').toBeCloseTo(52, 1);
  });

  // The other half of §4.1's swap: above the crossover the label is the tallest
  // item, so it sits flat against the header's padding and the badge becomes the
  // remainder — which is what `.logo-icon`'s `align-self: flex-start` now
  // absorbs. Both ends are asserted because each is blind to the other's defect.
  for (const root of [20, 24, 32]) {
    test(`the label sits flat at 14 above the crossover — ${root}px root`, async ({ page }) => {
      await setRoot(page, root);
      const m = await geometry(page);
      expectProbeNeutral(m);
      expect(Math.abs(m.labelItem!.y - 14), `label flex-item y was ${m.labelItem!.y}`)
        .toBeLessThanOrEqual(0.02);
    });
  }

  // ---------------------------------------------------------------------------
  // Colour and accessibility
  // ---------------------------------------------------------------------------

  test('badge fill and glyph colour follow the theme', async ({ page }) => {
    const light = await geometry(page);
    // `.logo-icon` reads --sidebar-badge, an alias of --primary declared once in
    // :root. Naming the alias matters: --primary is also the sidebar-footer
    // contract's focus-outline colour with under a point of WCAG 1.4.11
    // headroom (`app-logo.md` §7.3), so a future re-point of the alias is the
    // one change that would make these two assertions diverge.
    expect(light.fill).toBe('rgb(37, 99, 235)');            // --sidebar-badge → --primary, light
    expect(light.glyphColour).toBe('rgb(255, 255, 255)');
    // The stroke resolves through currentColor from the badge's own `color`;
    // asserted separately because `fill: none` means the stroke is all the ink
    // this mark has.
    expect(light.glyphStroke).toBe('rgb(255, 255, 255)');

    await darkMode(page);
    const dark = await geometry(page);
    expect(dark.fill).toBe('rgb(59, 130, 246)');            // --primary, dark
    expect(dark.glyphColour).toBe('rgb(255, 255, 255)');
    expect(dark.glyphStroke).toBe('rgb(255, 255, 255)');

    // Prove the theme switch actually moved the fill, rather than both reads
    // landing on the same value and the pair passing vacuously.
    expect(dark.fill).not.toBe(light.fill);
  });

  // `app-logo.md` §8: the badge is decorative, and that ruling holds *because*
  // the visible app-name label beside it carries the identity. So these are one
  // claim — removing the label is what would give the badge a naming obligation.
  test('the badge is decorative and the label carries the identity', async ({ page }) => {
    const m = await geometry(page);
    expect(m.ariaHidden, 'badge must be hidden from the accessibility tree').toBe(true);
    expect(m.badgeIsFirst, 'badge must precede the label in reading order').toBe(true);
    // The counterpart of MyNotes' `badgeInsideAnchor`, asserted the other way:
    // MyMail's badge is a plain <div> and is not focusable or activatable.
    expect(m.badgeIsPlainDiv, 'badge must not be wrapped in a link or button').toBe(true);
    expect(m.labelFound, 'the visible label is what makes the badge decorative').toBe(true);
  });
});
