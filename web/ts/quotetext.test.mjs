// node:test coverage for web/ts/util/quotetext.ts (exercised via its compiled
// output, web/static/util/quotetext.js). Run via build.sh or directly:
//   web/ts/vendor/test/unpack.sh && node --test web/ts/quotetext.test.mjs
//
// quoteHtmlToText produces the text/plain alternative for a reply or forward.
// It is the *only* producer of that half of the message: the editor gives us
// HTML, and what these tests pin is how the quoted block below the editor is
// rendered back down to text — quote markers, line breaks, and what is dropped.
//
// The inputs below are the shapes ComposeForm.buildComposeParts actually emits
// (attribution + <blockquote>, the <br>-joined "&gt; " fallback for text-only
// messages, and the forwarded-message header block), plus the shapes an
// arbitrary sanitized body_html can contribute inside the quote.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The module under test reads DOMParser and Node off the global object, so the
// DOM has to be installed before it runs.
const { JSDOM } = await import(path.resolve(__dirname, 'vendor/test/jsdom.js'));
const { window } = new JSDOM('');
globalThis.window = window;
globalThis.document = window.document;
globalThis.DOMParser = window.DOMParser;
globalThis.Node = window.Node;

const { quoteHtmlToText, stripLeadingBlankHtml, stripLeadingBlankLines } = await import(
  path.resolve(__dirname, '../static/util/quotetext.js')
);

// ---------------------------------------------------------------------------
// Nothing to quote
// ---------------------------------------------------------------------------

test('an empty quote yields empty text', () => {
  assert.equal(quoteHtmlToText(''), '');
});

test('markup carrying no text yields empty text', () => {
  assert.equal(quoteHtmlToText('<p>   </p>'), '');
  assert.equal(quoteHtmlToText('<p><br></p>'), '');
  assert.equal(quoteHtmlToText('<hr>'), '');
});

// ---------------------------------------------------------------------------
// Block structure → lines
// ---------------------------------------------------------------------------

test('each block element ends its line', () => {
  assert.equal(quoteHtmlToText('<p>one</p><p>two</p>'), 'one\ntwo');
  assert.equal(quoteHtmlToText('<div>one</div><div>two</div>'), 'one\ntwo');
  assert.equal(quoteHtmlToText('<h1>Title</h1><p>body</p>'), 'Title\nbody');
});

test('list items become one line each', () => {
  assert.equal(quoteHtmlToText('<ul><li>a</li><li>b</li></ul>'), 'a\nb');
  assert.equal(quoteHtmlToText('<ol><li>a</li><li>b</li></ol>'), 'a\nb');
});

test('table rows become one line each', () => {
  assert.equal(
    quoteHtmlToText('<table><tr><td>a</td><td>b</td></tr><tr><td>c</td></tr></table>'),
    'ab\nc',
  );
});

test('<br> breaks the line without ending the block', () => {
  assert.equal(quoteHtmlToText('<p>one<br>two</p>'), 'one\ntwo');
});

test('inline elements do not break the line', () => {
  assert.equal(
    quoteHtmlToText('<p>plain <b>bold</b> <i>italic</i> <a href="https://x/">link</a></p>'),
    'plain bold italic link',
  );
});

test('a block that already ended its line does not end it twice', () => {
  // The <p> closes the line; the enclosing <div> must not add another.
  assert.equal(quoteHtmlToText('<div><p>a</p></div><p>b</p>'), 'a\nb');
});

test('leading and trailing blank lines are trimmed away', () => {
  assert.equal(quoteHtmlToText('<p><br></p><p>body</p><p><br></p>'), 'body');
});

test('runs of blank lines collapse to a single one', () => {
  assert.equal(
    quoteHtmlToText('<p>a</p><p><br></p><p><br></p><p><br></p><p>b</p>'),
    'a\n\nb',
  );
});

// ---------------------------------------------------------------------------
// Whitespace between tags is layout, not text
// ---------------------------------------------------------------------------
//
// A pretty-printed body_html carries a newline and a run of indentation between
// every pair of tags. Reading those as line breaks turned a nested-table
// message into a screenful of bare "> " lines with the indentation preserved.

test('indentation between blocks does not become blank lines', () => {
  assert.equal(quoteHtmlToText('<div>\n  <p>one</p>\n  <p>two</p>\n</div>'), 'one\ntwo');
});

test('indentation before the text of a block is dropped', () => {
  assert.equal(quoteHtmlToText('<p>\n      body\n</p>'), 'body');
});

test('a newline inside a paragraph is a word gap, not a line break', () => {
  // This is how a browser renders it, and <br> remains the way to break a line.
  assert.equal(quoteHtmlToText('<p>one\ntwo</p>'), 'one two');
});

test('whitespace between two inline runs survives as a single space', () => {
  assert.equal(quoteHtmlToText('<p><b>one</b>\n  <i>two</i></p>'), 'one two');
});

test('a non-breaking space is content and is never collapsed', () => {
  assert.equal(quoteHtmlToText('<p>a&nbsp;&nbsp;b</p>'), 'a  b');
  // A spacer paragraph keeps its line — only the surrounding indentation goes.
  assert.equal(quoteHtmlToText('<p>a</p>\n<p>&nbsp;</p>\n<p>b</p>'), 'a\n\u00a0\nb');
});

test('a pretty-printed nested-table message reads as its text', () => {
  // The shape of a typical marketing mail: layout tables, one line of copy.
  const body = [
    '<div>', '  <table>', '    <tr>', '      <td>',
    '        <table>', '          <tr>', '            <td>',
    '              <p>Review the new publishing limits.</p>',
    '            </td>', '          </tr>', '        </table>',
    '      </td>', '    </tr>', '  </table>', '</div>',
  ].join('\n');
  assert.equal(
    quoteHtmlToText(`<p>On Tue, Alice wrote:</p><blockquote>${body}</blockquote>`),
    'On Tue, Alice wrote:\n> Review the new publishing limits.',
  );
});

// ---------------------------------------------------------------------------
// Text content
// ---------------------------------------------------------------------------

test('character references are decoded to the characters they name', () => {
  assert.equal(quoteHtmlToText('<p>&amp; &lt; &gt; &quot; &nbsp;x</p>'), '& < > "  x');
});

test('newlines inside a <pre> start new lines', () => {
  // Whitespace is text there, so it survives the collapsing done everywhere else.
  assert.equal(quoteHtmlToText('<pre>one\ntwo</pre>'), 'one\ntwo');
  assert.equal(quoteHtmlToText('<pre>a\n\n  b</pre>'), 'a\n\n  b');
});

test('comments are dropped', () => {
  // QUOTE_MARKER is an HTML comment, so this is also what keeps the marker out
  // of the text/plain alternative.
  assert.equal(quoteHtmlToText('<p>a</p><!--mymail-quote--><p>b</p>'), 'a\nb');
});

// ---------------------------------------------------------------------------
// Quote markers
// ---------------------------------------------------------------------------

test('a blockquote prefixes every line it contains', () => {
  assert.equal(
    quoteHtmlToText('<blockquote><p>one</p><p>two</p></blockquote>'),
    '> one\n> two',
  );
});

test('each nesting level adds another marker', () => {
  assert.equal(
    quoteHtmlToText('<blockquote><p>outer</p><blockquote><p>inner</p></blockquote></blockquote>'),
    '> outer\n> > inner',
  );
});

test('a blank line inside a quote still carries its markers', () => {
  // Otherwise the quote would visually break apart at every paragraph gap.
  assert.equal(
    quoteHtmlToText('<blockquote><p>a</p><p><br></p><p>b</p></blockquote>'),
    '> a\n> \n> b',
  );
});

test('text returning to depth zero loses the markers again', () => {
  assert.equal(
    quoteHtmlToText('<p>before</p><blockquote><p>quoted</p></blockquote><p>after</p>'),
    'before\n> quoted\nafter',
  );
});

test('markers are emitted once per line, not once per nesting level', () => {
  // Three <div> wrappers inside one <blockquote> must not yield "> > > ".
  assert.equal(
    quoteHtmlToText('<blockquote><div><div><div>deep</div></div></div></blockquote>'),
    '> deep',
  );
});

// ---------------------------------------------------------------------------
// The shapes ComposeForm actually builds
// ---------------------------------------------------------------------------

test('a reply to an HTML message reads as attribution then quoted body', () => {
  const quoteHtml =
    '<p>On Tue, 01 Jul 2025 10:00:00 GMT, Alice wrote:</p>' +
    '<blockquote style="margin:0 0 0 0.8ex;border-left:1px solid #ccc;padding-left:1ex">' +
    '<p>First line.</p><p>Second line.</p></blockquote>';
  assert.equal(
    quoteHtmlToText(quoteHtml),
    'On Tue, 01 Jul 2025 10:00:00 GMT, Alice wrote:\n> First line.\n> Second line.',
  );
});

test('a reply to a text-only message keeps the markers it was given', () => {
  // buildComposeParts writes those "&gt; " prefixes itself and does not wrap the
  // body in a <blockquote>, so they must not be doubled up here.
  const quoteHtml =
    '<p>On Tue, 01 Jul 2025 10:00:00 GMT, Alice wrote:</p>' +
    '<p>&gt; First line.<br>&gt; <br>&gt; Second line.</p>';
  assert.equal(
    quoteHtmlToText(quoteHtml),
    'On Tue, 01 Jul 2025 10:00:00 GMT, Alice wrote:\n> First line.\n> \n> Second line.',
  );
});

test('a reply to a reply nests the older round one level deeper', () => {
  const quoteHtml =
    '<p>On Wed, Bob wrote:</p>' +
    '<blockquote><p>Answering you.</p>' +
    '<p>On Tue, Alice wrote:</p>' +
    '<blockquote><p>Original.</p></blockquote></blockquote>';
  assert.equal(
    quoteHtmlToText(quoteHtml),
    'On Wed, Bob wrote:\n> Answering you.\n> On Tue, Alice wrote:\n> > Original.',
  );
});

test('a forward keeps its header block unquoted', () => {
  const quoteHtml =
    '<p>---------- Forwarded message ----------</p>' +
    '<p>From: Alice &lt;alice@example.com&gt;</p>' +
    '<p>Date: Tue, 01 Jul 2025 10:00:00 GMT</p>' +
    '<p>Subject: Hello</p>' +
    '<p>To: bob@example.com</p>' +
    '<p><br></p>' +
    '<p>Body.</p>';
  assert.equal(
    quoteHtmlToText(quoteHtml),
    '---------- Forwarded message ----------\n' +
      'From: Alice <alice@example.com>\n' +
      'Date: Tue, 01 Jul 2025 10:00:00 GMT\n' +
      'Subject: Hello\n' +
      'To: bob@example.com\n' +
      '\n' +
      'Body.',
  );
});

test('a forward of a text-only message unwraps its <pre>', () => {
  assert.equal(quoteHtmlToText('<pre>line one\nline two</pre>'), 'line one\nline two');
});

// ---------------------------------------------------------------------------
// Pathological input
// ---------------------------------------------------------------------------

// A `>` chain re-quotes everything before it on every round, so the nesting a
// long-running thread reaches is bounded only by how long it ran. Recursing
// once per level would blow the stack; MAX_QUOTE_DEPTH (400) is what stops it.
test('nesting past the depth cap flattens instead of overflowing the stack', () => {
  const depth = 1000; // comfortably past the cap, and past the native stack
  const html = '<blockquote>'.repeat(depth) + 'bottom' + '</blockquote>'.repeat(depth);

  const text = quoteHtmlToText(html);

  assert.match(text, /bottom$/, 'the innermost text must survive');
  // The cap is checked against the tree depth, and <body> is depth 0, so the
  // 400th blockquote sits at depth 400 and is flattened to its text. The 399
  // above it each contributed a marker.
  assert.equal((text.match(/> /g) ?? []).length, 399);
});

// ---------------------------------------------------------------------------
// Trimming the top of the quoted body
// ---------------------------------------------------------------------------
//
// buildComposeParts runs the replied-to body through these before wrapping it,
// so the attribution is followed by the first quoted word rather than by a run
// of bare "> " lines — a run that would otherwise be re-quoted on every round.

test('an empty body stays empty', () => {
  assert.equal(stripLeadingBlankHtml(''), '');
  assert.equal(stripLeadingBlankHtml('<p><br></p>'), '');
});

test('leading empty blocks are dropped', () => {
  assert.equal(stripLeadingBlankHtml('<p><br></p><p>body</p>'), '<p>body</p>');
  assert.equal(stripLeadingBlankHtml('<div><br></div><div>body</div>'), '<div>body</div>');
  assert.equal(stripLeadingBlankHtml('<br><br>body'), 'body');
  assert.equal(stripLeadingBlankHtml('<p>&nbsp;</p><p>body</p>'), '<p>body</p>');
  assert.equal(stripLeadingBlankHtml('  \n <p>body</p>'), '<p>body</p>');
});

test('blank lines further down are left alone', () => {
  assert.equal(
    stripLeadingBlankHtml('<p><br></p><p>a</p><p><br></p><p>b</p><p><br></p>'),
    '<p>a</p><p><br></p><p>b</p><p><br></p>',
  );
});

test('a wrapped body is trimmed inside its wrapper', () => {
  // Mailers routinely wrap the whole body in one or more <div>s.
  assert.equal(
    stripLeadingBlankHtml('<div><div><p><br></p><p>body</p></div></div>'),
    '<div><div><p>body</p></div></div>',
  );
  // Including a previous round's quote, which is what nests deepest.
  assert.equal(
    stripLeadingBlankHtml('<blockquote><p><br></p><p>older</p></blockquote>'),
    '<blockquote><p>older</p></blockquote>',
  );
});

test('a text-free block that is still content survives', () => {
  // Dropping these would silently lose the top of the message.
  assert.equal(stripLeadingBlankHtml('<p><img src="cid:x"></p>'), '<p><img src="cid:x"></p>');
  assert.equal(stripLeadingBlankHtml('<hr><p>body</p>'), '<hr><p>body</p>');
  assert.equal(
    stripLeadingBlankHtml('<table><tr><td></td></tr></table>'),
    '<table><tbody><tr><td></td></tr></tbody></table>',
  );
});

test('whitespace inside a <pre> is not trimmed', () => {
  // It is part of the text there, not layout, so we never descend into a <pre>.
  // (The one newline the HTML parser itself eats right after the open tag is
  // outside our control and was never in the document to begin with.)
  assert.equal(stripLeadingBlankHtml('<pre>  indented\n\n  more</pre>'), '<pre>  indented\n\n  more</pre>');
  assert.equal(stripLeadingBlankHtml('<p><br></p><pre>  x</pre>'), '<pre>  x</pre>');
});

test('trimming leaves nothing for the text alternative to quote', () => {
  const quoted = `<p>On Tue, Alice wrote:</p><blockquote>${stripLeadingBlankHtml(
    '<div><p><br></p><p><br></p><p>First line.</p></div>',
  )}</blockquote>`;
  assert.equal(quoteHtmlToText(quoted), 'On Tue, Alice wrote:\n> First line.');
});

test('leading blank lines are dropped from a text-only body', () => {
  assert.equal(stripLeadingBlankLines('\n\n  \nbody\n\nmore\n'), 'body\n\nmore\n');
  assert.equal(stripLeadingBlankLines('body'), 'body');
  assert.equal(stripLeadingBlankLines('   body'), '   body'); // indentation is not a blank line
  assert.equal(stripLeadingBlankLines(''), '');
});
