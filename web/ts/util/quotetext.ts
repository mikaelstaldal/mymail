// Building and serialising a reply's quoted block. Lives apart from ComposeForm
// so it can be exercised directly by web/ts/quotetext.test.mjs under jsdom.

// Elements after which a line break is emitted when serialising quoted HTML to
// the plain-text alternative.
const BLOCK_TAGS = new Set([
  'P', 'DIV', 'BLOCKQUOTE', 'LI', 'TR', 'PRE', 'HR', 'TABLE', 'UL', 'OL',
  'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'FIGURE', 'DL', 'DT', 'DD',
]);

// Guards against a stack overflow on pathologically nested quote chains.
const MAX_QUOTE_DEPTH = 400;

// Line-oriented accumulator. The `> ` markers are emitted once, as each line is
// opened, from the blockquote nesting depth at that point. Prefixing a whole
// subtree on the way back up instead would re-scan the accumulated text at
// every level — quadratic in nesting depth, which is exactly the shape of a
// long reply chain.
interface QuoteTextAcc {
  chunks: string[];
  atLineStart: boolean;
}

// The characters HTML collapses. U+00A0 is deliberately absent: a non-breaking
// space is content, and a mailer's `&nbsp;` spacers must survive intact.
const COLLAPSIBLE_WS = /[ \t\n\r\f]+/g;

function openLine(acc: QuoteTextAcc, quoteDepth: number): void {
  if (!acc.atLineStart) return;
  if (quoteDepth > 0) acc.chunks.push('> '.repeat(quoteDepth));
  acc.atLineStart = false;
}

function endLine(acc: QuoteTextAcc, quoteDepth: number): void {
  openLine(acc, quoteDepth); // an empty line still carries its quote markers
  acc.chunks.push('\n');
  acc.atLineStart = true;
}

function pushInline(acc: QuoteTextAcc, text: string, quoteDepth: number): void {
  openLine(acc, quoteDepth);
  acc.chunks.push(text);
}

// Verbatim, newlines included — only reached inside <pre>.
function emitPreformatted(acc: QuoteTextAcc, s: string, quoteDepth: number): void {
  const lines = s.split('\n');
  for (let i = 0; i < lines.length; i++) {
    if (i > 0) endLine(acc, quoteDepth);
    if (lines[i]) pushInline(acc, lines[i], quoteDepth);
  }
}

// Everywhere else, whitespace collapses the way a browser renders it: the
// newlines and indentation of a pretty-printed body_html are layout, not text.
// Treating them as line breaks is what turned a nested-table message into a
// screenful of bare `> ` lines. Whitespace that opens a line is indentation and
// goes; whitespace between two inline runs is a word gap and stays.
function emitText(acc: QuoteTextAcc, s: string, quoteDepth: number): void {
  let text = s.replace(COLLAPSIBLE_WS, ' ');
  if (acc.atLineStart && text.startsWith(' ')) text = text.slice(1);
  if (text) pushInline(acc, text, quoteDepth);
}

function nodeToText(
  node: Node, depth: number, quoteDepth: number, acc: QuoteTextAcc, preserveWs: boolean,
): void {
  if (node.nodeType === Node.TEXT_NODE) {
    const text = node.nodeValue ?? '';
    if (preserveWs) emitPreformatted(acc, text, quoteDepth);
    else emitText(acc, text, quoteDepth);
    return;
  }
  if (node.nodeType !== Node.ELEMENT_NODE) return;
  const el = node as Element;
  if (el.tagName === 'BR') {
    endLine(acc, quoteDepth);
    return;
  }
  const childPreserveWs = preserveWs || el.tagName === 'PRE';
  if (depth >= MAX_QUOTE_DEPTH) {
    const text = el.textContent ?? '';
    if (childPreserveWs) emitPreformatted(acc, text, quoteDepth);
    else emitText(acc, text, quoteDepth);
    return;
  }

  // Quote depth grows with each round, matching the plain-text convention.
  const childQuoteDepth = el.tagName === 'BLOCKQUOTE' ? quoteDepth + 1 : quoteDepth;
  for (const child of Array.from(el.childNodes)) {
    nodeToText(child, depth + 1, childQuoteDepth, acc, childPreserveWs);
  }
  if (BLOCK_TAGS.has(el.tagName) && !acc.atLineStart) endLine(acc, childQuoteDepth);
}

/**
 * Plain-text rendering of the quoted block, for the text/plain alternative.
 * Derived purely from quoteHtml so that a reopened draft reconstructs exactly
 * the same text as the original compose did.
 */
export function quoteHtmlToText(html: string): string {
  if (!html) return '';
  // DOMParser yields an inert document: no scripts run and no resources load.
  const doc = new DOMParser().parseFromString(html, 'text/html');
  const acc: QuoteTextAcc = { chunks: [], atLineStart: true };
  nodeToText(doc.body, 0, 0, acc, false);
  return acc.chunks.join('').replace(/\n{3,}/g, '\n\n').trim();
}

// Elements that are content in themselves: an otherwise text-free block holding
// one of these is not blank, and dropping it would lose part of the message.
const REPLACED_TAGS = 'img,hr,table,video,audio,iframe,object,embed,svg,canvas,input,button,select,textarea';

// Containers whose *own* leading blanks are worth stripping too, so that a body
// wrapped in one or more <div>s is trimmed like a bare one. Anything else —
// <pre>, inline elements, replaced content — is left exactly as it came.
const DESCEND_TAGS = new Set(['DIV', 'BLOCKQUOTE', 'SECTION', 'ARTICLE', 'MAIN', 'CENTER']);

function isBlankElement(el: Element): boolean {
  if (el.matches(REPLACED_TAGS) || el.querySelector(REPLACED_TAGS)) return false;
  // U+00A0 (&nbsp;) counts as blank: a spacer paragraph is often written that way.
  return !/[^\s\u00a0]/.test(el.textContent ?? '');
}

function stripLeadingBlanks(parent: Element, depth: number): void {
  if (depth >= MAX_QUOTE_DEPTH) return;
  while (parent.firstChild) {
    const child = parent.firstChild;
    if (child.nodeType === Node.ELEMENT_NODE) {
      const el = child as Element;
      if (!isBlankElement(el)) {
        if (DESCEND_TAGS.has(el.tagName)) stripLeadingBlanks(el, depth + 1);
        return;
      }
    } else if (child.nodeType === Node.TEXT_NODE) {
      if (/[^\s\u00a0]/.test(child.nodeValue ?? '')) return;
    }
    // Blank element, whitespace-only text, or a comment: drop it and look again.
    parent.removeChild(child);
  }
}

/**
 * The quoted body with the empty paragraphs, <br>s and whitespace many mailers
 * leave at the top of a message removed. Without this, every reply round adds
 * another run of bare `> ` lines between the attribution and the first quoted
 * word, and they accumulate down the thread.
 */
export function stripLeadingBlankHtml(html: string): string {
  if (!html) return '';
  // DOMParser yields an inert document: no scripts run and no resources load.
  const doc = new DOMParser().parseFromString(html, 'text/html');
  stripLeadingBlanks(doc.body, 0);
  return doc.body.innerHTML;
}

/** The same trimming for a text/plain body, which has no markup to inspect. */
export function stripLeadingBlankLines(text: string): string {
  return text.replace(/^(?:[^\S\n]*\n)+/, '');
}
