// Tests for web/ts/util/signature.ts — the identity signature as a region of the
// Quill document. No DOM: everything here is ops in, ops or positions out.
//
// The ops in these tests are the shapes Quill actually produces, taken from
// driving the vendored Quill 2.0.3 under jsdom: a `<br>` becomes a block break,
// a `-- ` delimiter's `<hr>` becomes two empty blocks, and a block that the
// wrapper splits keeps its formats only on the half that inherits the old
// newline. ComposeForm's Quill wiring is what turns these into edits; what is
// pinned here is the arithmetic it depends on.

import test from 'node:test';
import assert from 'node:assert/strict';

const {
  SIGNATURE, signatureToHtml, signatureRange, signatureOps, findSignature,
} = await import('../static/util/signature.js');

const MARK = { [SIGNATURE]: 'y' };
const SOFT = { 'ql-softwrap': 'y' };

test('signatureToHtml escapes text and renders the delimiter as a rule', () => {
  assert.equal(signatureToHtml('Mikael'), 'Mikael');
  assert.equal(signatureToHtml('A & B <x>'), 'A &amp; B &lt;x&gt;');
  assert.equal(signatureToHtml('one\ntwo'), 'one<br>two');
  assert.equal(signatureToHtml('-- \nName'), '<hr><br>Name');
  // Only the exact delimiter line, not a line that merely starts with dashes.
  assert.equal(signatureToHtml('--\nName'), '--<br>Name');
  assert.equal(signatureToHtml('a\r\nb\rc'), 'a<br>b<br>c');
});

test('signatureRange finds nothing in a document with no signature', () => {
  assert.equal(signatureRange([{ insert: '\nhello\n' }]), null);
  assert.equal(signatureRange([]), null);
});

test('signatureRange spans whole blocks, terminator included', () => {
  // "\nSig\n": a blank paragraph to type into, then the signature.
  const ops = [{ insert: '\n' }, { insert: 'Sig' }, { insert: '\n', attributes: MARK }];
  assert.deepEqual(signatureRange(ops), { index: 1, length: 4 });
});

test('signatureRange covers every marked line of a multi-line signature', () => {
  const ops = [
    { insert: '\n' },
    { insert: 'Mikael' }, { insert: '\n', attributes: MARK },
    { insert: 'LMS IT' }, { insert: '\n', attributes: MARK },
  ];
  // From the start of "Mikael" to past the newline after "LMS IT".
  assert.deepEqual(signatureRange(ops), { index: 1, length: 14 });
});

test('signatureRange counts the empty blocks a delimiter becomes', () => {
  // What `-- \nName` renders as: the `<hr>` contributes two empty blocks.
  const ops = [
    { insert: '\n' },
    { insert: '\n\n', attributes: MARK },
    { insert: 'Name' }, { insert: '\n', attributes: MARK },
  ];
  assert.deepEqual(signatureRange(ops), { index: 1, length: 7 });
});

test('signatureRange takes in an unmarked line between two marked ones', () => {
  // A split can leave a hole: the format lands on the half that inherits the
  // old newline. The span stays contiguous rather than becoming fragments.
  const ops = [
    { insert: 'A' }, { insert: '\n', attributes: MARK },
    { insert: 'B' }, { insert: '\n' },
    { insert: 'C' }, { insert: '\n', attributes: MARK },
  ];
  assert.deepEqual(signatureRange(ops), { index: 0, length: 6 });
});

test('signatureRange stops at the last marked line', () => {
  // Enter at the end of the signature starts a paragraph outside it.
  const ops = [
    { insert: 'Sig' }, { insert: '\n', attributes: MARK },
    { insert: 'body written below' }, { insert: '\n' },
  ];
  assert.deepEqual(signatureRange(ops), { index: 0, length: 4 });
});

test('signatureRange misses the half a split left unmarked', () => {
  // Why autoWrapEditor has to carry the mark onto the newline it inserts: with
  // only the second half marked, the swap would replace that half and leave the
  // first sitting above the new signature. This is the failure, pinned.
  const wrapped = [
    { insert: '\n' },
    { insert: 'Mikael Staldal | Senior' }, { insert: '\n', attributes: SOFT },
    { insert: 'Consultant' }, { insert: '\n', attributes: MARK },
  ];
  assert.deepEqual(signatureRange(wrapped), { index: 25, length: 11 });

  const carried = [
    { insert: '\n' },
    { insert: 'Mikael Staldal | Senior' }, { insert: '\n', attributes: { ...SOFT, ...MARK } },
    { insert: 'Consultant' }, { insert: '\n', attributes: MARK },
  ];
  assert.deepEqual(signatureRange(carried), { index: 1, length: 35 });
});

test('signatureRange keeps its count across an embed', () => {
  // An embed is one position and no line break. The wrapper's own scan gives up
  // on such a document; this one does not have to, since it needs no alignment
  // with the flat text.
  const ops = [
    { insert: { image: 'x' } }, { insert: '\n' },
    { insert: 'Sig' }, { insert: '\n', attributes: MARK },
  ];
  assert.deepEqual(signatureRange(ops), { index: 2, length: 4 });
});

test('signatureOps marks every line break', () => {
  assert.deepEqual(signatureOps('Sig\n'), [
    { insert: 'Sig' }, { insert: '\n', attributes: MARK },
  ]);
  assert.deepEqual(signatureOps('A\nB\n'), [
    { insert: 'A' }, { insert: '\n', attributes: MARK },
    { insert: 'B' }, { insert: '\n', attributes: MARK },
  ]);
  // Empty lines carry the mark too — that is all a delimiter leaves behind.
  assert.deepEqual(signatureOps('\n\nName\n'), [
    { insert: '\n', attributes: MARK },
    { insert: '\n', attributes: MARK },
    { insert: 'Name' }, { insert: '\n', attributes: MARK },
  ]);
});

test('signatureOps terminates a signature that does not terminate itself', () => {
  assert.deepEqual(signatureOps('Sig'), signatureOps('Sig\n'));
});

test('signatureOps writes nothing for an identity with no signature', () => {
  assert.deepEqual(signatureOps(''), []);
});

test('signatureOps and signatureRange agree', () => {
  for (const text of ['Sig\n', 'A\nB\n', '\n\nName\n', 'A\n\nB\n']) {
    const ops = signatureOps(text);
    assert.deepEqual(signatureRange(ops), { index: 0, length: text.length },
      `round trip for ${JSON.stringify(text)}`);
  }
});

test('findSignature requires a match to start a line', () => {
  assert.equal(findSignature('Hi Bob,\n\nMikael\nLMS IT\n', 'Mikael\nLMS IT\n'), 9);
  // "kael" is in the document, but not as a block of its own.
  assert.equal(findSignature('Hi Bob,\n\nMikael\n', 'kael\n'), -1);
  assert.equal(findSignature('Mikael\n', 'Mikael\n'), 0);
  assert.equal(findSignature('nothing here\n', 'Mikael\n'), -1);
  assert.equal(findSignature('Mikael\n', ''), -1);
});

test('findSignature takes the last occurrence', () => {
  // A body that quotes the signature is above it, never below.
  const flat = 'Sig\nsomething\nSig\n';
  assert.equal(findSignature(flat, 'Sig\n'), 14);
});
