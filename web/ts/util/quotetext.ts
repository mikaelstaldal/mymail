// Serialisation of a reply's quoted HTML block to the text/plain alternative.
// Lives apart from ComposeForm so it can be exercised directly by
// web/ts/quotetext.test.mjs under jsdom.

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

function emitText(acc: QuoteTextAcc, s: string, quoteDepth: number): void {
  const lines = s.split('\n');
  for (let i = 0; i < lines.length; i++) {
    if (i > 0) endLine(acc, quoteDepth);
    if (lines[i]) {
      openLine(acc, quoteDepth);
      acc.chunks.push(lines[i]);
    }
  }
}

function nodeToText(node: Node, depth: number, quoteDepth: number, acc: QuoteTextAcc): void {
  if (node.nodeType === Node.TEXT_NODE) {
    emitText(acc, node.nodeValue ?? '', quoteDepth);
    return;
  }
  if (node.nodeType !== Node.ELEMENT_NODE) return;
  const el = node as Element;
  if (el.tagName === 'BR') {
    endLine(acc, quoteDepth);
    return;
  }
  if (depth >= MAX_QUOTE_DEPTH) {
    emitText(acc, el.textContent ?? '', quoteDepth);
    return;
  }

  // Quote depth grows with each round, matching the plain-text convention.
  const childQuoteDepth = el.tagName === 'BLOCKQUOTE' ? quoteDepth + 1 : quoteDepth;
  for (const child of Array.from(el.childNodes)) {
    nodeToText(child, depth + 1, childQuoteDepth, acc);
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
  nodeToText(doc.body, 0, 0, acc);
  return acc.chunks.join('').replace(/\n{3,}/g, '\n\n').trim();
}
