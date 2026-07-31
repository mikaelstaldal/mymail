// Soft-wrapping a composed message at a fixed column. Lives apart from
// ComposeForm so it can be exercised directly by web/ts/wrap.test.mjs; it
// touches no DOM, so that test needs no jsdom.
//
// Three entry points over one algorithm:
//   wrapText    — string in, wrapped string out. The text/plain alternative on
//                 the way out, including the quoted half that never enters the
//                 editor.
//   wrapEdits   — the same breaks as positions, for a caller that has to apply
//                 them to something other than a string.
//   reflowEdits — what the editor uses on every change: the breaks it made
//                 earlier are dissolved back into spaces and the paragraph is
//                 wrapped again, so a break can move instead of only ever being
//                 added. Without that, typing a word into an already-wrapped
//                 paragraph pushes one word onto a line of its own, then
//                 another, and the paragraph unravels as it is edited.

/**
 * Column after which a line is broken, unless the reader has chosen another.
 * RFC 5322 permits far more, so this is a readability limit: an unwrapped
 * paragraph arrives as one enormous line in any mailer that does not re-flow
 * text itself.
 */
export const WRAP_COLUMN = 80;

/** Narrow enough to be eccentric, wide enough to leave room past a quote marker. */
export const MIN_WRAP_COLUMN = 20;

/** RFC 5322's hard ceiling on a line, so a setting can never make one illegal. */
export const MAX_WRAP_COLUMN = 998;

/**
 * Width that turns wrapping off altogether. There is no column zero, so the
 * setting can carry "no wrapping" without a second flag to keep in step.
 */
export const WRAP_OFF = 0;

/**
 * A stored or typed wrap column as a width the wrapper can use.
 *
 * Zero or less means off. Anything unset, blank or unreadable falls back to the
 * default rather than failing: this reads a preference, and a broken one must
 * neither stop a message being written nor silently turn a feature off. A
 * number inside neither case is clamped to the range, not rejected, so a slip
 * of the keyboard narrows the column rather than reverting it to 80.
 */
export function normalizeWrapColumn(raw: string | number | null | undefined): number {
  const value = typeof raw === 'string' ? raw.trim() : raw;
  if (value === null || value === undefined || value === '') return WRAP_COLUMN;
  const n = Number(value);
  if (!Number.isFinite(n)) return WRAP_COLUMN;
  const column = Math.round(n);
  if (column <= WRAP_OFF) return WRAP_OFF;
  return Math.min(MAX_WRAP_COLUMN, Math.max(MIN_WRAP_COLUMN, column));
}

// What a continuation line repeats: the quote markers a line carries (`> `,
// `> > `, …) and any indentation before them. Repeating the markers is what
// keeps a wrapped quote a quote — a continuation line without its `> ` reads as
// newly written text to the recipient.
const PREFIX_RE = /^[ \t]*(?:>[ \t]?)*/;

/** One edit: the run at `at`, `remove` characters long, becomes `insert`. */
export interface WrapEdit {
  at: number;
  remove: number;
  insert: string;
  /** Set when the edit dissolves an earlier break rather than making one. */
  join?: boolean;
}

export interface WrapOptions {
  width?: number;
  /**
   * Repeat indentation and quote markers on continuation lines. On for the text
   * form. Off when the breaks have to stay dissolvable: a repeated prefix
   * cannot be told apart from text the author typed, so it could not be undone.
   */
  prefix?: boolean;
  /**
   * Whether a line may be re-shaped at all, asked with the line's bounds. It is
   * consulted only about lines something might be done to — one long enough to
   * break, or (in reflowEdits) one holding a break that might be dissolved — so
   * a caller that has to ask something expensive about a line pays for it only
   * when it matters. Refusing a line leaves it exactly as it is: neither
   * broken further nor pulled back together.
   */
  canWrapLine?: (lineStart: number, lineEnd: number) => boolean;
}

function isBreak(text: string, i: number): boolean {
  const c = text[i];
  return c === ' ' || c === '\t';
}

/** First non-blank character in `[from, end)`, or -1 if there is none. */
function firstContent(text: string, from: number, end: number): number {
  for (let i = from; i < end; i++) {
    if (!isBreak(text, i)) return i;
  }
  return -1;
}

function collectLineEdits(
  text: string, start: number, lineEnd: number, width: number, usePrefix: boolean, out: WrapEdit[],
): void {
  // Trailing blanks are not worth breaking at: everything past such a break is
  // whitespace, so it would buy a continuation line holding nothing but the
  // prefix — or, in the editor, an empty paragraph. A line that is only too
  // long because of blanks nobody can see is not too long.
  let end = lineEnd;
  while (end > start && isBreak(text, end - 1)) end--;

  const prefix = usePrefix ? PREFIX_RE.exec(text.slice(start, end))![0] : '';
  const avail = width - prefix.length;
  // A quote nested deeper than the wrap column leaves no room for content.
  // There is nothing useful to emit, and breaking would not make progress.
  if (avail <= 0) return;
  const insert = '\n' + prefix;

  let from = start + prefix.length;
  while (end - from > avail) {
    // Never break inside the whitespace that opens the remainder: that would
    // leave a line holding nothing but the prefix.
    const content = firstContent(text, from, end);
    if (content < 0) return;

    let br = -1;
    for (let i = from + avail; i > content; i--) {
      if (isBreak(text, i)) { br = i; break; }
    }
    if (br < 0) {
      for (let i = from + avail + 1; i < end; i++) {
        if (isBreak(text, i)) { br = i; break; }
      }
    }
    // A single word wider than the line — a long URL, typically. Left whole:
    // splitting it would corrupt it, and no plain-text convention licenses that.
    if (br < 0) return;

    // The break replaces the *last* character of the whitespace run it lands
    // in: the continuation line never starts with a blank, and a run of several
    // survives as trailing whitespace on the line that opened it. Swallowing
    // the whole run instead would delete text — the second of two spaces after
    // a sentence — and, worse, could not be undone: dissolving a break puts
    // back one space, so a break that ate two would move every time the
    // paragraph was re-filled and eat another.
    let runEnd = br + 1;
    while (runEnd < end && isBreak(text, runEnd)) runEnd++;

    out.push({ at: runEnd - 1, remove: 1, insert });
    from = runEnd;
  }
}

/**
 * Every break needed to bring `text` within the width, in ascending order.
 * A width of WRAP_OFF asks for none, and is answered without consulting
 * `canWrapLine` about lines nobody is going to break.
 */
export function wrapEdits(text: string, opts: WrapOptions = {}): WrapEdit[] {
  const width = opts.width ?? WRAP_COLUMN;
  const usePrefix = opts.prefix ?? true;
  const out: WrapEdit[] = [];
  if (width <= WRAP_OFF) return out;
  let start = 0;
  for (;;) {
    const nl = text.indexOf('\n', start);
    const end = nl < 0 ? text.length : nl;
    if (end - start > width && (!opts.canWrapLine || opts.canWrapLine(start, end))) {
      collectLineEdits(text, start, end, width, usePrefix, out);
    }
    if (nl < 0) break;
    start = nl + 1;
  }
  return out;
}

function dissolve(text: string, at: ReadonlySet<number>): string {
  const chars = text.split('');
  for (const i of at) chars[i] = ' ';
  return chars.join('');
}

/** Whether the line starting at `lineStart` opens with a quote marker. */
export function isQuotedLine(text: string, lineStart: number): boolean {
  let i = lineStart;
  while (i < text.length && isBreak(text, i)) i++;
  return text[i] === '>';
}

// Breaks inside a line the caller will not let us wrap. Refusing to *re-fill* a
// line has to mean refusing to take it apart as well: dissolving its breaks
// would merge it into what precedes it, which is how making one visual line of
// a wrapped paragraph into a list item swallows the text above it.
function refusedBreaks(
  flat: string,
  soft: readonly number[],
  canWrapLine: (lineStart: number, lineEnd: number) => boolean,
): Set<number> {
  const keep = new Set<number>();
  let i = 0;
  let start = 0;
  for (;;) {
    const nl = flat.indexOf('\n', start);
    const end = nl < 0 ? flat.length : nl;
    const first = i;
    while (i < soft.length && soft[i] < end) i++;
    // Only lines that hold a break need asking about; the rest have nothing to
    // keep, and wrapEdits asks separately about the ones it might break.
    if (i > first && !canWrapLine(start, end)) {
      for (let k = first; k < i; k++) keep.add(soft[k]);
    }
    if (nl < 0) break;
    start = nl + 1;
  }
  return keep;
}

/**
 * Edits that re-fill a document whose earlier breaks are known.
 *
 * `softBreaks` holds the indices of the newlines the wrapper inserted itself.
 * Each is dissolved back into the single space it replaced — which keeps the
 * unwrapped text the same length as the document, so one set of indices
 * describes both and no position mapping is needed — and the result is wrapped
 * again. Breaks that come out where they already were are dropped, so an edit
 * that changes no line produces no edits at all.
 *
 * At a width of WRAP_OFF this dissolves and re-fills nothing, which leaves the
 * document holding only the breaks the author typed: turning wrapping off takes
 * back the breaks the editor made, rather than freezing them in place.
 *
 * Edits are ascending and non-overlapping: applied in order, each one's `at` is
 * an index into the original text.
 */
export function reflowEdits(
  text: string,
  softBreaks: ReadonlySet<number>,
  opts: WrapOptions = {},
): WrapEdit[] {
  const flat = softBreaks.size === 0 ? text : dissolve(text, softBreaks);
  const wanted = wrapEdits(flat, { ...opts, prefix: false });
  const all = [...softBreaks].sort((a, b) => a - b);
  const keep = opts.canWrapLine ? refusedBreaks(flat, all, opts.canWrapLine) : null;
  const soft = keep === null ? all : all.filter(p => !keep.has(p));

  const out: WrapEdit[] = [];
  const join = (at: number): WrapEdit => ({ at, remove: 1, insert: ' ', join: true });
  let si = 0;
  for (const w of wanted) {
    // Breaks before this one are gone for good: nothing wants them back.
    while (si < soft.length && soft[si] < w.at) out.push(join(soft[si++]));
    // A break the new wrapping lands on exactly is already right where it
    // belongs, and re-stating it would churn the document on every keystroke.
    let unchanged = false;
    while (si < soft.length && soft[si] < w.at + w.remove) {
      unchanged ||= soft[si] === w.at && w.remove === 1 && w.insert === '\n';
      si++;
    }
    if (!unchanged) out.push(w);
  }
  while (si < soft.length) out.push(join(soft[si++]));
  return out;
}

/**
 * The same text with a line break inserted after the last space at or before
 * `width` on every line longer than that. Only whitespace at the break point is
 * consumed; spacing elsewhere in the line, blank lines, and the line structure
 * the author typed are all preserved, which also makes this idempotent — a
 * reopened draft re-wraps to exactly what was stored.
 */
export function wrapText(text: string, width = WRAP_COLUMN): string {
  if (!text) return '';
  return applyWrapEdits(text, wrapEdits(text, { width }));
}

/** `text` with `edits` applied — the string form of what a caller would do. */
export function applyWrapEdits(text: string, edits: readonly WrapEdit[]): string {
  if (edits.length === 0) return text;
  let out = '';
  let pos = 0;
  for (const e of edits) {
    out += text.slice(pos, e.at) + e.insert;
    pos = e.at + e.remove;
  }
  return out + text.slice(pos);
}
