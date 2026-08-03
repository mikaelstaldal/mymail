// node:test coverage for web/ts/util/confirm.ts (exercised via its compiled
// output, web/static/util/confirm.js). Run via build.sh or directly:
//   node --test web/ts/confirm.test.mjs
//
// This module is the store behind every confirmation the UI asks for, and what
// it replaced — window.confirm — could not lose an answer: it blocked the page
// until one arrived. This one hands out a promise instead, so the rules that
// matter are the ones about promises that must not be stranded. A caller that
// awaits confirmDialog() and never hears back is a button that stops working
// with no error anywhere, which is why both paths below are pinned:
// superseding an unanswered question resolves it (false), and answering an id
// that is no longer current does nothing rather than resolving the wrong one.
//
// No DOM is involved — the dialog itself is ConfirmDialog.tsx, which subscribes
// to this and is not reachable from here.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const { confirmDialog, answerConfirm, subscribe } = await import(
  path.resolve(__dirname, '../static/util/confirm.js')
);

const QUESTION = {
  title: 'Delete message',
  body: 'Delete this message?',
  confirmLabel: 'Delete',
  cancelLabel: 'Keep',
  destructive: true,
};

// Collects what the view would be told to render, and leaves the store empty
// again for the next test.
function watch() {
  const seen = [];
  const unsubscribe = subscribe(r => seen.push(r));
  return { seen, unsubscribe };
}

test('an answered question resolves with the answer', async () => {
  const { seen, unsubscribe } = watch();
  try {
    const answer = confirmDialog(QUESTION);
    const open = seen.at(-1);
    assert.equal(open.title, 'Delete message');
    assert.equal(open.confirmLabel, 'Delete');
    assert.equal(open.destructive, true);

    answerConfirm(open.id, true);
    assert.equal(await answer, true);
    assert.equal(seen.at(-1), null, 'the dialog closes once answered');
  } finally {
    unsubscribe();
  }
});

test('declining resolves false', async () => {
  const { seen, unsubscribe } = watch();
  try {
    const answer = confirmDialog(QUESTION);
    answerConfirm(seen.at(-1).id, false);
    assert.equal(await answer, false);
  } finally {
    unsubscribe();
  }
});

test('a superseding question resolves the previous one false', async () => {
  const { seen, unsubscribe } = watch();
  try {
    const first = confirmDialog(QUESTION);
    const firstId = seen.at(-1).id;

    // Nothing answered the first question — a second one arrived instead. Its
    // caller is sitting on `await`, so it has to be told something.
    const second = confirmDialog({ ...QUESTION, title: 'Discard draft' });
    assert.equal(await first, false);

    const secondId = seen.at(-1).id;
    assert.notEqual(secondId, firstId);
    assert.equal(seen.at(-1).title, 'Discard draft');

    answerConfirm(secondId, true);
    assert.equal(await second, true);
  } finally {
    unsubscribe();
  }
});

test('answering a stale id does nothing', async () => {
  const { seen, unsubscribe } = watch();
  try {
    const first = confirmDialog(QUESTION);
    const staleId = seen.at(-1).id;
    const second = confirmDialog(QUESTION);
    assert.equal(await first, false);

    // A click that was already in flight when the question was replaced must
    // not answer the one that took its place.
    answerConfirm(staleId, true);
    assert.notEqual(seen.at(-1), null, 'the current question is still open');

    answerConfirm(seen.at(-1).id, false);
    assert.equal(await second, false);

    // And once nothing is open, a late answer is simply ignored.
    answerConfirm(staleId, true);
    assert.equal(seen.at(-1), null);
  } finally {
    unsubscribe();
  }
});

test('subscribing reports the question already open', async () => {
  const answer = confirmDialog(QUESTION);
  let latest;
  const unsubscribe = subscribe(r => { latest = r; });
  try {
    assert.notEqual(latest, null);
    assert.equal(latest.title, 'Delete message');
    answerConfirm(latest.id, false);
    assert.equal(await answer, false);
  } finally {
    unsubscribe();
  }
});
