// The identity signature as a region of the Quill document.
//
// Changing the From identity swaps the signature block, which means finding the
// old one first. Looking for the HTML `signatureToHtml` produced inside the
// editor's `innerHTML` does not work, because Quill never gives that string
// back: it turns every `<br>` into a block break, drops the `<hr>` a `-- `
// delimiter becomes, collapses runs of spaces, and the auto-wrapper splits any
// line past the wrap column into two paragraphs. The match therefore failed for
// nearly every real signature, and the swap then either prepended the new
// signature above a body that still carried the old one, or — when the new
// identity had no signature — did nothing at all and sent the previous
// identity's name and employer to the recipient with no visible cue.
//
// So the signature is marked instead, with a block format Quill maintains as
// part of the document. The mark moves when text is typed above it, is cloned
// onto both halves when the wrapper splits one of its lines, comes back with an
// undo, and survives a save and reopen — the mark is a class, and Quill's
// clipboard maps a class back to its format when the draft is pasted in again.
// Locating the signature is then a scan of the document rather than a guess
// about how the editor rendered a string.
//
// This is the same device the soft-break mark uses, and it carries the same two
// obligations: the sanitiser must never allow `class` (or the mark ships to
// recipients), and the format's attribute name must stay equal to the class
// prefix (or the clipboard cannot map the class back when a draft is reopened).
//
// Everything here is pure — ops in, ops or positions out — so it is exercised
// directly by web/ts/signature.test.mjs. The Quill wiring stays in ComposeForm.

/** One op of a Quill delta, as much of it as this module needs. */
export interface DeltaOp {
  insert?: string | Record<string, unknown>;
  attributes?: Record<string, unknown>;
}

/** Block format marking a line as part of the signature. */
export const SIGNATURE = 'ql-signature';

/** A span of the document, in Quill positions. */
export interface SignatureRange {
  index: number;
  /** Includes the newline terminating the last line, so the span is whole blocks. */
  length: number;
}

/**
 * A stored plain-text signature as the HTML the editor is given.
 *
 * The standard delimiter line (`-- `) becomes an `<hr>`; every other line is
 * escaped and the breaks between them become `<br>`. What Quill makes of that
 * is Quill's business — this module never assumes the result resembles its
 * input, which is exactly the assumption that failed before.
 */
export function signatureToHtml(sig: string): string {
  const lines = sig.replace(/\r\n/g, '\n').replace(/\r/g, '\n').split('\n');
  const parts: string[] = [];
  for (const line of lines) {
    if (line === '-- ') {
      parts.push('<hr>');
    } else {
      parts.push(line.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'));
    }
  }
  return parts.join('<br>');
}

/**
 * The span the marked blocks occupy, or null if the document holds no signature.
 *
 * It runs from the start of the first marked line to the end of the last,
 * including that line's terminating newline: the span is then a whole number of
 * blocks, so replacing it leaves neither a stray empty paragraph or a signature
 * fused onto the line below it.
 *
 * An unmarked line between two marked ones is taken in rather than skipped.
 * Splitting a block clones its formats onto the half that inherits the old
 * newline, not necessarily onto both, so a hole can appear in the middle; the
 * signature is contiguous by construction, so the span is the honest answer and
 * a list of fragments is not.
 */
export function signatureRange(ops: readonly DeltaOp[]): SignatureRange | null {
  let pos = 0;
  let lineStart = 0;
  let first = -1;
  let last = -1;
  for (const op of ops) {
    // An embed occupies one position and is not a line break. Standing in for
    // it keeps every index after it in step, which is why this can run over a
    // document holding an image where the wrapper's own scan gives up.
    const text = typeof op.insert === 'string' ? op.insert : ' ';
    const marked = op.attributes != null && op.attributes[SIGNATURE] != null;
    for (let i = 0; i < text.length; i++, pos++) {
      if (text[i] !== '\n') continue;
      if (marked) {
        if (first < 0) first = lineStart;
        last = pos;
      }
      lineStart = pos + 1;
    }
  }
  return first < 0 ? null : { index: first, length: last + 1 - first };
}

/**
 * `text` as inserts whose every line break carries the mark.
 *
 * The text is what the editor makes of the signature's HTML, so it is whole
 * lines terminated by a newline; a missing terminator is added rather than
 * rejected, since a signature that does not end its own block runs into the
 * line below it.
 */
export function signatureOps(text: string): DeltaOp[] {
  if (!text) return [];
  const body = text.endsWith('\n') ? text.slice(0, -1) : text;
  const ops: DeltaOp[] = [];
  for (const line of body.split('\n')) {
    if (line) ops.push({ insert: line });
    ops.push({ insert: '\n', attributes: { [SIGNATURE]: 'y' } });
  }
  return ops;
}

/**
 * Where `needle` sits in `flat`, or -1.
 *
 * Used once per document the editor did not build — a draft saved before the
 * mark existed — to find a signature that is only ordinary paragraphs. The
 * caller passes the document with the wrapper's own breaks dissolved back into
 * the spaces they replaced, so a signature written as one line still matches
 * after being wrapped into two.
 *
 * The last occurrence wins: the signature sits below whatever has been typed,
 * so a body that happens to repeat it is the earlier match. A match must also
 * start a line, since half a block is not a signature.
 */
export function findSignature(flat: string, needle: string): number {
  if (!needle) return -1;
  for (let at = flat.lastIndexOf(needle); at >= 0; at = flat.lastIndexOf(needle, at - 1)) {
    if (at === 0 || flat[at - 1] === '\n') return at;
  }
  return -1;
}
