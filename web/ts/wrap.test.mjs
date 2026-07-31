// node:test coverage for web/ts/util/wrap.ts (exercised via its compiled
// output, web/static/util/wrap.js). Run via build.sh or directly:
//   node --test web/ts/wrap.test.mjs
//
// This module decides every line break a composed message gets: reflowEdits
// keeps the editor filled to the column as the author types, and wrapText does
// the same to the text/plain alternative on the way out, including the quoted
// half that never enters the editor. What these tests pin is where the break
// lands, what a continuation line has to repeat to stay readable in a
// plain-text mailer, what is deliberately left over-long, and — for the editor
// — that a break moves rather than accumulating, and that a break the author
// typed is never dissolved.
//
// No DOM is involved, so unlike quotetext.test.mjs this needs no jsdom. What
// cannot be reached from here is the Quill wiring in ComposeForm: which breaks
// count as the editor's own, and how the edits become a delta.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const {
  wrapText, wrapEdits, reflowEdits, normalizeWrapColumn,
  WRAP_COLUMN, WRAP_OFF, MIN_WRAP_COLUMN, MAX_WRAP_COLUMN,
} = await import(path.resolve(__dirname, '../static/util/wrap.js'));

/**
 * A run of exactly `n` characters made of short words, so it has break
 * opportunities — and never a leading or trailing space, so a test can place
 * one itself and know where the only break near that point is.
 */
function words(n) {
  let s = '';
  while (s.length < n) s += (s ? ' ' : '') + 'ab';
  s = s.slice(0, n);
  return s.endsWith(' ') ? s.slice(0, -1) + 'c' : s;
}

const longest = text => Math.max(...text.split('\n').map(l => l.length));

// ---------------------------------------------------------------------------
// Nothing to do
// ---------------------------------------------------------------------------

test('empty input stays empty', () => {
  assert.equal(wrapText(''), '');
});

test('the wrap column is 80', () => {
  assert.equal(WRAP_COLUMN, 80);
});

test('lines at or under the column are untouched', () => {
  const at80 = words(80);
  assert.equal(at80.length, 80);
  assert.equal(wrapText(at80), at80);
  assert.equal(wrapText('short line'), 'short line');
});

test('line structure and blank lines survive', () => {
  const text = 'one\n\n\ntwo\n';
  assert.equal(wrapText(text), text);
});

// ---------------------------------------------------------------------------
// Where the break lands
// ---------------------------------------------------------------------------

test('a line one character over the column is broken', () => {
  const at81 = words(81);
  const out = wrapText(at81);
  assert.equal(out.split('\n').length, 2);
  assert.ok(longest(out) <= 80);
});

test('the break takes the last space at or before the column', () => {
  // 78 characters, then a space at index 78, then a word: the whole first 78
  // fit, so the break must be that space and not an earlier one.
  const line = words(78) + ' tail';
  assert.equal(wrapText(line), words(78) + '\ntail');
});

test('a space exactly at the column is a break opportunity', () => {
  const line = words(80) + ' tail';
  assert.equal(wrapText(line), words(80) + '\ntail');
});

test('words are never split', () => {
  const line = 'word '.repeat(40).trim();
  for (const l of wrapText(line).split('\n')) {
    assert.ok(/^(word )*word$/.test(l), `unexpected line: ${JSON.stringify(l)}`);
  }
});

test('a long paragraph wraps into lines that all fit', () => {
  const para = 'lorem ipsum dolor sit amet consectetur adipiscing elit '.repeat(20).trim();
  const out = wrapText(para);
  assert.ok(longest(out) <= 80);
  // Nothing lost or invented: the words come back in order.
  assert.equal(out.split('\n').join(' '), para);
});

test('every line is filled — no break earlier than it had to be', () => {
  const para = 'lorem ipsum dolor sit amet consectetur adipiscing elit '.repeat(20).trim();
  const lines = wrapText(para).split('\n');
  for (let i = 0; i < lines.length - 1; i++) {
    const nextWord = lines[i + 1].split(' ')[0];
    assert.ok(lines[i].length + 1 + nextWord.length > 80,
      `line ${i} could have held one more word: ${JSON.stringify(lines[i])}`);
  }
});

test('spacing inside a line is preserved; only the break is consumed', () => {
  const line = 'a  b   c ' + words(80);
  const out = wrapText(line);
  assert.ok(out.startsWith('a  b   c'), out);
  assert.ok(!/[ \t]$/m.test(out), 'a break at a single space leaves no trailing blank');
});

test('a break at a run of blanks keeps all but the one it replaces', () => {
  // Swallowing the run would delete the author's second space, and could not be
  // undone: dissolving a break puts back one space, so the paragraph would
  // re-flow and lose another every time the break moved.
  const line = words(78) + '  tail';
  assert.equal(wrapText(line), words(78) + ' \ntail');
  const out = wrapText(words(78) + '    tail');
  assert.equal(out, words(78) + '   \ntail');
  assert.equal(out.split('\n').join(' ').replace(/ +/g, ' '),
    (words(78) + '    tail').replace(/ +/g, ' '), 'no word lost');
});

test('a continuation line never opens with a blank', () => {
  for (const gap of [' ', '  ', '     ', '\t\t']) {
    const out = wrapText(words(78) + gap + 'tail');
    for (const l of out.split('\n')) assert.ok(!/^[ \t]/.test(l), JSON.stringify(out));
  }
});

// ---------------------------------------------------------------------------
// Trailing blanks are not a reason to break
// ---------------------------------------------------------------------------

test('a line over the column only by trailing blanks is left alone', () => {
  const line = 'x'.repeat(79) + '   ';
  assert.equal(wrapText(line), line);
  assert.deepEqual(wrapEdits(line), []);
});

test('trailing blanks never buy a line holding just the prefix', () => {
  // The break used to land inside the trailing run, leaving `> ` on its own.
  const line = '> ' + 'a'.repeat(78) + ' ';
  assert.equal(wrapText(line), line);
});

test('trailing blanks never invent an empty line', () => {
  const text = 'x'.repeat(79) + '   \nnext line';
  assert.equal(wrapText(text), text);
  assert.deepEqual(reflowEdits('x'.repeat(79) + '   ', new Set()), []);
});

test('a line that is too long without its trailing blanks still wraps', () => {
  const out = wrapText(words(78) + ' tail   ');
  assert.equal(out, words(78) + '\ntail   ', 'the blanks stay where they were');
});

test('tabs are break opportunities too', () => {
  // The tab is the last whitespace before the column — what follows it is one
  // unbreakable word — so it is where the break has to land.
  const line = words(78) + '\t' + 'x'.repeat(10);
  assert.equal(wrapText(line), words(78) + '\n' + 'x'.repeat(10));
});

// ---------------------------------------------------------------------------
// What stays over-long on purpose
// ---------------------------------------------------------------------------

test('a word wider than the line is left whole', () => {
  const url = 'https://example.com/' + 'x'.repeat(100);
  assert.equal(wrapText(url), url);
});

test('an over-long word does not swallow what follows it', () => {
  const url = 'https://example.com/' + 'x'.repeat(100);
  assert.equal(wrapText(`${url} tail`), `${url}\ntail`);
});

test('text before an over-long word still wraps', () => {
  const url = 'x'.repeat(100);
  const out = wrapText(`${words(78)} ${url}`);
  assert.equal(out, `${words(78)}\n${url}`);
});

// ---------------------------------------------------------------------------
// Quoted lines keep their markers
// ---------------------------------------------------------------------------

test('a quoted line repeats its marker on every continuation line', () => {
  const out = wrapText('> ' + words(200));
  const lines = out.split('\n');
  assert.ok(lines.length > 1);
  for (const l of lines) assert.ok(l.startsWith('> '), `unquoted continuation: ${l}`);
  assert.ok(longest(out) <= 80);
});

test('nested quote depth is preserved', () => {
  const out = wrapText('> > > ' + words(200));
  for (const l of out.split('\n')) assert.ok(l.startsWith('> > > '), l);
  assert.ok(longest(out) <= 80);
});

test('a marker run without spaces is recognised', () => {
  const out = wrapText('>>' + words(200));
  for (const l of out.split('\n')) assert.ok(l.startsWith('>>'), l);
});

test('leading indentation is repeated on continuation lines', () => {
  const out = wrapText('    ' + words(200));
  for (const l of out.split('\n')) assert.ok(l.startsWith('    '), JSON.stringify(l));
  assert.ok(longest(out) <= 80);
});

test('a quote nested past the column is left alone rather than mangled', () => {
  // 45 levels of "> " is 90 characters of marker: there is no room for content,
  // and breaking would make no progress.
  const line = '> '.repeat(45) + 'text';
  assert.equal(wrapText(line), line);
});

test('no line is emitted holding only quote markers', () => {
  const out = wrapText('>    ' + words(200));
  for (const l of out.split('\n')) {
    assert.ok(/[^>\s]/.test(l), `content-free line: ${JSON.stringify(l)}`);
  }
});

// ---------------------------------------------------------------------------
// Stability across a save / reopen cycle
// ---------------------------------------------------------------------------

test('re-filling settles, whatever the spacing', () => {
  // A break made at a run of blanks used to eat one, so the flat text differed
  // from what was typed and the paragraph re-flowed on the next keystroke.
  const inputs = [
    'bb  a  bb  a  a  dddd  a  ccc',
    'One sentence.  Another sentence.  A third one, somewhat longer than the rest.',
    'tabs\there\tand  mixed   runs of blanks between the words of this line',
  ];
  for (const width of [20, 40, 80]) {
    for (const input of inputs) {
      const edits = reflowEdits(input, new Set(), { width });
      const once = applyEdits(input, edits);
      const soft = softPositions(input, edits);
      assert.deepEqual(reflowEdits(once, soft, { width }), [],
        `re-flowed again at ${width}: ${JSON.stringify(input)}`);
      assert.equal(once.replace(/\n/g, ' '), input, 'a blank went missing');
    }
  }
});

test('wrapping is idempotent', () => {
  const inputs = [
    'lorem ipsum dolor sit amet consectetur adipiscing elit '.repeat(20).trim(),
    '> ' + words(300),
    '> > ' + 'word '.repeat(60).trim(),
    'https://example.com/' + 'x'.repeat(100) + ' and some trailing words here',
    'para one\n\n' + words(150) + '\n\n> quoted ' + words(150) + '\n',
  ];
  for (const input of inputs) {
    const once = wrapText(input);
    assert.equal(wrapText(once), once, `not idempotent: ${JSON.stringify(input.slice(0, 40))}`);
  }
});

test('the signature delimiter is not disturbed', () => {
  const text = 'body\n-- \nSomeone\n';
  assert.equal(wrapText(text), text);
});

// ---------------------------------------------------------------------------
// The width is a parameter; the export is the default
// ---------------------------------------------------------------------------

test('an explicit width overrides the default', () => {
  assert.equal(wrapText('aaa bbb ccc', 7), 'aaa bbb\nccc');
  assert.equal(wrapText('aaa bbb ccc', 11), 'aaa bbb ccc');
});

// ---------------------------------------------------------------------------
// normalizeWrapColumn — reading the preference behind that width
// ---------------------------------------------------------------------------

test('a stored column is used as given', () => {
  assert.equal(normalizeWrapColumn('72'), 72);
  assert.equal(normalizeWrapColumn(72), 72);
  assert.equal(normalizeWrapColumn(' 72 '), 72);
});

test('nothing stored means the default', () => {
  assert.equal(normalizeWrapColumn(null), WRAP_COLUMN);
  assert.equal(normalizeWrapColumn(undefined), WRAP_COLUMN);
  assert.equal(normalizeWrapColumn(''), WRAP_COLUMN);
  assert.equal(normalizeWrapColumn('   '), WRAP_COLUMN);
});

test('an unreadable setting falls back rather than breaking composition', () => {
  assert.equal(normalizeWrapColumn('wide'), WRAP_COLUMN);
  assert.equal(normalizeWrapColumn('8o'), WRAP_COLUMN);
  assert.equal(normalizeWrapColumn(NaN), WRAP_COLUMN);
  assert.equal(normalizeWrapColumn(Infinity), WRAP_COLUMN);
});

test('a column outside the range is clamped, not discarded', () => {
  assert.equal(normalizeWrapColumn('1'), MIN_WRAP_COLUMN);
  assert.equal(normalizeWrapColumn('100000'), MAX_WRAP_COLUMN);
  assert.equal(normalizeWrapColumn(String(MAX_WRAP_COLUMN)), MAX_WRAP_COLUMN);
});

test('zero or less turns wrapping off', () => {
  assert.equal(WRAP_OFF, 0);
  assert.equal(normalizeWrapColumn('0'), WRAP_OFF);
  assert.equal(normalizeWrapColumn(0), WRAP_OFF);
  assert.equal(normalizeWrapColumn('-40'), WRAP_OFF);
});

test('an empty setting is the default, not off', () => {
  // The two must not be confused: nothing stored is a reader who never touched
  // the setting, and they get 80.
  assert.notEqual(normalizeWrapColumn(''), WRAP_OFF);
  assert.notEqual(normalizeWrapColumn(null), WRAP_OFF);
  assert.notEqual(normalizeWrapColumn('nonsense'), WRAP_OFF);
});

test('a fractional column is rounded to a whole one', () => {
  assert.equal(normalizeWrapColumn('72.4'), 72);
  assert.equal(normalizeWrapColumn('72.6'), 73);
});

// ---------------------------------------------------------------------------
// Wrapping turned off
// ---------------------------------------------------------------------------

test('a width of zero breaks nothing', () => {
  const para = 'lorem ipsum dolor sit amet consectetur '.repeat(20).trim();
  assert.equal(wrapText(para, WRAP_OFF), para);
  assert.deepEqual(wrapEdits(para, { width: WRAP_OFF }), []);
});

test('nothing is asked about a line nobody will break', () => {
  let asked = 0;
  wrapEdits(words(200), { width: WRAP_OFF, canWrapLine: () => { asked++; return true; } });
  assert.equal(asked, 0);
});

test('turning wrapping off takes back the breaks the editor made', () => {
  const para = 'lorem ipsum dolor sit amet consectetur '.repeat(10).trim();
  const { text, soft } = firstWrap(para);
  assert.ok(soft.size > 0);
  assert.equal(applyEdits(text, reflowEdits(text, soft, { width: WRAP_OFF })), para);
});

test('breaks the author typed survive with wrapping off', () => {
  const text = 'Hi Bob,\nThanks for the note.';
  assert.equal(applyEdits(text, reflowEdits(text, new Set(), { width: WRAP_OFF })), text);
});

test('the range leaves room to wrap at either end', () => {
  const line = 'lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor';
  for (const width of [MIN_WRAP_COLUMN, WRAP_COLUMN, MAX_WRAP_COLUMN]) {
    const out = wrapText(line.repeat(20), width);
    assert.ok(longest(out) <= width, `width ${width} produced a line of ${longest(out)}`);
  }
});

// ---------------------------------------------------------------------------
// wrapEdits — the positional form the editor applies as a Quill delta
// ---------------------------------------------------------------------------

/** Apply edits to a string the way updateContents applies them to a document. */
function applyEdits(text, edits) {
  let out = '';
  let pos = 0;
  for (const e of edits) {
    out += text.slice(pos, e.at) + e.insert;
    pos = e.at + e.remove;
  }
  return out + text.slice(pos);
}

test('nothing to wrap yields no edits', () => {
  assert.deepEqual(wrapEdits('short\nlines\n'), []);
  assert.deepEqual(wrapEdits(''), []);
});

test('an edit replaces exactly one character of the break', () => {
  const text = words(78) + '  tail';
  const [edit, ...rest] = wrapEdits(text);
  assert.deepEqual(rest, []);
  assert.equal(edit.at, 79, 'the last blank of the run, so the rest stay put');
  assert.equal(edit.remove, 1);
  assert.equal(edit.insert, '\n');
});

test('a quoted line carries its prefix in the inserted text', () => {
  const [edit] = wrapEdits('> > ' + words(200));
  assert.equal(edit.insert, '\n> > ');
});

test('edits are ascending and never overlap', () => {
  const text = words(200) + '\n\n> ' + words(200) + '\nshort\n' + words(300);
  const edits = wrapEdits(text);
  assert.ok(edits.length > 4);
  let end = -1;
  for (const e of edits) {
    assert.ok(e.at >= end, `edit at ${e.at} overlaps the previous one ending at ${end}`);
    end = e.at + e.remove;
  }
});

test('applying the edits gives exactly what wrapText produces', () => {
  const inputs = [
    words(200),
    'para one\n\n' + words(150) + '\n\n> quoted ' + words(150) + '\n',
    'lorem ipsum dolor sit amet '.repeat(30).trim(),
    '  indented ' + words(150),
    'https://example.com/' + 'x'.repeat(100) + ' and a few trailing words go here',
  ];
  for (const input of inputs) {
    assert.equal(applyEdits(input, wrapEdits(input)), wrapText(input));
  }
});

test('a line the caller refuses is left alone, the rest still wrap', () => {
  const bullet = words(200);
  const para = words(200);
  const text = bullet + '\n' + para;
  const bulletStart = 0;
  const edits = wrapEdits(text, { canWrapLine: lineStart => lineStart !== bulletStart });
  assert.ok(edits.length > 0);
  for (const e of edits) assert.ok(e.at > bullet.length, 'the refused line was broken');
});

test('the refusal is only asked about lines that are too long', () => {
  const asked = [];
  const text = 'short\n' + words(200) + '\nalso short\n';
  wrapEdits(text, { canWrapLine: (start, end) => { asked.push([start, end]); return true; } });
  assert.deepEqual(asked, [[6, 6 + 200]], 'asked once, with the long line’s bounds');
});

test('the prefix can be turned off, for a caller that must undo its breaks', () => {
  const [edit] = wrapEdits('> > ' + words(200), { prefix: false });
  assert.equal(edit.insert, '\n');
});

// ---------------------------------------------------------------------------
// reflowEdits — what the editor applies on every change
// ---------------------------------------------------------------------------

/** Wrap `text` from scratch and report the resulting soft-break positions. */
function firstWrap(text) {
  const edits = reflowEdits(text, new Set());
  return { text: applyEdits(text, edits), soft: softPositions(text, edits) };
}

/** Positions of the inserted breaks, in the coordinates of the *result*. */
function softPositions(text, edits) {
  const soft = new Set();
  let drift = 0;
  for (const e of edits) {
    if (!e.join) soft.add(e.at + drift);
    drift += e.insert.length - e.remove;
  }
  return soft;
}

test('a first pass over unwrapped text just wraps it', () => {
  const para = 'lorem ipsum dolor sit amet consectetur '.repeat(10).trim();
  const { text, soft } = firstWrap(para);
  assert.equal(text, wrapText(para));
  assert.equal(soft.size, text.split('\n').length - 1);
  for (const p of soft) assert.equal(text[p], '\n');
});

test('re-wrapping an already-wrapped document is a no-op', () => {
  const { text, soft } = firstWrap('lorem ipsum dolor sit amet consectetur '.repeat(10).trim());
  assert.deepEqual(reflowEdits(text, soft), [], 'a settled document must not churn');
});

test('a break moves rather than spawning a short line', () => {
  // The failure this exists to prevent: inserting a word early in a wrapped
  // paragraph used to push one word onto a line of its own, then another.
  const para = 'alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega';
  const first = firstWrap(para);
  const edited = first.text.slice(0, 6) + 'INSERTED ' + first.text.slice(6);
  const soft = new Set([...first.soft].map(p => p + 'INSERTED '.length));

  const result = applyEdits(edited, reflowEdits(edited, soft));
  const lines = result.split('\n');
  assert.ok(lines.every(l => l.length <= WRAP_COLUMN));
  // Re-filled, not fragmented: only the last line may be short.
  for (let i = 0; i < lines.length - 1; i++) {
    const nextWord = lines[i + 1].split(' ')[0];
    assert.ok(lines[i].length + 1 + nextWord.length > WRAP_COLUMN,
      `line ${i} is short: ${JSON.stringify(lines[i])}`);
  }
  assert.equal(result.replace(/\n/g, ' '), edited.replace(/\n/g, ' '), 'no words lost');
});

test('deleting text pulls the following line back up', () => {
  const para = 'lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore';
  const { text, soft } = firstWrap(para);
  assert.ok(text.includes('\n'));
  // Cut the first ten characters, which leaves the first line short.
  const shortened = text.slice(10);
  const moved = new Set([...soft].map(p => p - 10));
  const result = applyEdits(shortened, reflowEdits(shortened, moved));
  assert.equal(result, wrapText(shortened.replace(/\n/g, ' ')));
});

test('a break the author typed is never dissolved', () => {
  // Only the positions handed in count as soft; a hard break stays put even
  // when the two lines around it would fit on one.
  const text = 'Hi Bob,\nThanks for the note.';
  assert.deepEqual(reflowEdits(text, new Set()), []);
});

test('a paragraph the caller refuses is left exactly as it is', () => {
  // Refusing to re-fill a line must mean refusing to take it apart too: the
  // breaks in it are what hold it away from the text above, and dissolving
  // them is how a wrapped paragraph turned into a list item swallowed it.
  const { text, soft } = firstWrap('lorem ipsum dolor sit amet consectetur '.repeat(10).trim());
  const edited = text.replace('lorem', 'lorem EXTRA');
  const moved = new Set([...soft].map(p => p + ' EXTRA'.length));
  assert.deepEqual(reflowEdits(edited, moved, { canWrapLine: () => false }), []);
});

test('refusing one paragraph does not freeze the others', () => {
  const para = 'lorem ipsum dolor sit amet consectetur '.repeat(10).trim();
  const first = firstWrap(para);
  const doc = first.text + '\n' + para;
  const soft = first.soft;
  const edits = reflowEdits(doc, soft, {
    canWrapLine: start => start > first.text.length,
  });
  assert.ok(edits.length > 0, 'the second paragraph still wraps');
  for (const e of edits) {
    assert.ok(e.at > first.text.length, `touched the refused paragraph at ${e.at}`);
  }
});

test('the refusal is asked about a line holding a break, long or not', () => {
  const asked = [];
  // "one two" is far under the column but its break is dissolvable, so whether
  // it may be re-shaped still has to be settled.
  reflowEdits('one\ntwo', new Set([3]), {
    canWrapLine: (start, end) => { asked.push([start, end]); return true; },
  });
  assert.deepEqual(asked, [[0, 7]]);
});

test('reflow edits stay ascending and non-overlapping', () => {
  const doc = 'lorem ipsum dolor sit amet '.repeat(12).trim() + '\n\n' +
    'consectetur adipiscing elit sed '.repeat(12).trim();
  const { text, soft } = firstWrap(doc);
  const edited = text.replace('lorem', 'LOREM IPSUM DOLOR');
  const moved = new Set([...soft].map(p => p + 'LOREM IPSUM DOLOR'.length - 'lorem'.length));
  let end = -1;
  for (const e of reflowEdits(edited, moved)) {
    assert.ok(e.at >= end, `edit at ${e.at} overlaps the previous, ending at ${end}`);
    end = e.at + e.remove;
  }
});

test('dissolving a break restores exactly one space', () => {
  const text = 'one\ntwo';
  const edits = reflowEdits(text, new Set([3]), { width: 80 });
  assert.deepEqual(edits, [{ at: 3, remove: 1, insert: ' ', join: true }]);
  assert.equal(applyEdits(text, edits), 'one two');
});
