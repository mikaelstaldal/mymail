// Covers threading normalization at the compose request boundary, including
// incoming references plus the replied-to Message-ID and oversized legacy IDs.
// Component rendering is outside this plain-function test's scope.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { composeThreading } from '../static/util/threading.js';

test('reply to mail with 1000 references retains the newest 1000 IDs', () => {
  const refs = Array.from({ length: 1000 }, (_, i) => `<${i}@example.com>`);
  const result = composeThreading('reply@example.com', [...refs, '<reply@example.com>']);
  assert.equal(result.references.length, 1000);
  assert.equal(result.references[0], refs[1]);
  assert.equal(result.references.at(-1), '<reply@example.com>');
  assert.equal(result.in_reply_to, 'reply@example.com');
});

test('oversized incoming or legacy identifiers are omitted without changing valid IDs', () => {
  const valid = 'é'.repeat(998);
  assert.deepEqual(composeThreading('x'.repeat(999), ['x'.repeat(999), valid]), {
    in_reply_to: undefined, references: [valid],
  });
  assert.equal(composeThreading(valid, []).in_reply_to, valid);
});
