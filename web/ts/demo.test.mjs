// node:test coverage for the demo backend (web/ts/demo/*.ts, exercised via its
// compiled output in web/static/demo/). Run via build.sh or directly:
//   node --test web/ts/demo.test.mjs
//
// The demo files are classic worker scripts, not ES modules: they declare
// globals that importScripts() shares across one worker scope (see
// web/ts/demo/tsconfig.json). So this evaluates them as scripts in this
// process's global scope, exactly as the worker does, and then reads the
// declarations back out of it.
//
// store.js is the one file left out — it is nothing but IndexedDB, which this
// process does not have. A stub standing in for its five entry points is
// evaluated in its place, which is what lets api.js run here: everything above
// the store is then real code answering real Request objects.
//
// What is covered is the logic ported from the Go server — slugs, subject
// normalisation, address parsing, references truncation, search snippets and
// phrase matching, RFC 5322 assembly, filter evaluation — plus the endpoint
// behaviour that carries a parity rule: which folders refuse a move, what
// deleting does in Trash versus Inbox, how threads close over References, and
// the demo's own auto-reply.
//
// Parity with internal/handler and internal/repository is a contract (see the
// root AGENTS.md § Demo mode), and running the real endpoints here is what makes
// these assertions statements about the code that implements it rather than
// about a paraphrase of it.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import vm from 'node:vm';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

function evalScript(name) {
  const file = path.resolve(__dirname, '../static/demo', name);
  vm.runInThisContext(readFileSync(file, 'utf8'), { filename: file });
}

// Evaluated in this realm rather than a fresh vm context, so the values that
// come back are ordinary arrays and objects that deepEqual can compare.
for (const name of ['model.js', 'text.js', 'reply.js']) evalScript(name);

// The store stub. `state` is swapped per test; saveState only has to not throw,
// and the attachment blobs live in a Map.
globalThis.__demoState = null;
globalThis.__demoBlobs = new Map();
vm.runInThisContext(`
  function withStore(fn) { return fn(globalThis.__demoState); }
  function saveState(_state) { return Promise.resolve(); }
  function invalidateState() { /* the real one drops the IndexedDB cache */ }
  function getAttachmentData(id) { return Promise.resolve(globalThis.__demoBlobs.get(id) ?? null); }
  function putAttachmentData(id, data) { globalThis.__demoBlobs.set(id, data); return Promise.resolve(); }
  function removeAttachmentData(ids) { for (const id of ids) globalThis.__demoBlobs.delete(id); return Promise.resolve(); }
`, { filename: 'store-stub.js' });

evalScript('api.js');

// Pull the declarations out of the shared scope. A lexical `const` at the top
// level of a script is not a property of globalThis, so each has to be
// evaluated by name.
const {
  toSlug, uniqueSlug, normalizeSubject, stripHeaderControls, stripAngleBrackets,
  normalizeReferences, parseAddressList, parseAndFoldAddress, firstAddress,
  htmlEscape, hasExternalImages, tokenizeText, buildSnippet, phraseMatches,
  buildRawMessage, newMessageId,
  buildAutoReply, filterMatches, applyFilters,
  handleApiRequest, runScheduler,
  AUTO_REPLY_DELAY_MS, MAX_REFS_BYTES,
} = vm.runInThisContext(`({
  toSlug, uniqueSlug, normalizeSubject, stripHeaderControls, stripAngleBrackets,
  normalizeReferences, parseAddressList, parseAndFoldAddress, firstAddress,
  htmlEscape, hasExternalImages, tokenizeText, buildSnippet, phraseMatches,
  buildRawMessage, newMessageId,
  buildAutoReply, filterMatches, applyFilters,
  handleApiRequest, runScheduler,
  AUTO_REPLY_DELAY_MS, MAX_REFS_BYTES,
})`);

// ---------------------------------------------------------------------------
// Slugs — parity with repository.toSlug
// ---------------------------------------------------------------------------

test('toSlug lowercases and hyphenates', () => {
  assert.equal(toSlug('Work Stuff'), 'work-stuff');
  assert.equal(toSlug('  Spaces   everywhere  '), 'spaces-everywhere');
  assert.equal(toSlug('a---b'), 'a-b', 'separator runs collapse');
});

test('toSlug decomposes accents, leaving the combining mark as a separator', () => {
  // NFKD splits "ä" into "a" + a combining diaeresis, which is not [a-z0-9] and
  // so becomes a hyphen. Surprising, but it is exactly what repository.toSlug
  // produces, and the demo has to agree with it rather than be nicer.
  assert.equal(toSlug('Räkningar'), 'ra-kningar');
  assert.equal(toSlug('Résumés & CVs'), 're-sume-s-cvs');
});

test('toSlug falls back to "folder" when nothing survives', () => {
  assert.equal(toSlug('🎉🎉🎉'), 'folder');
  assert.equal(toSlug('---'), 'folder');
});

test('uniqueSlug appends the first free numeric suffix', () => {
  const taken = new Set(['work', 'work-2']);
  assert.equal(uniqueSlug(taken, 'work'), 'work-3');
  assert.equal(uniqueSlug(taken, 'other'), 'other');
});

// ---------------------------------------------------------------------------
// Subjects — parity with repository.normalizeSubject
// ---------------------------------------------------------------------------

test('normalizeSubject strips every stacked reply prefix', () => {
  assert.equal(normalizeSubject('Re: Fwd: Q2 Budget'), 'Q2 Budget');
  assert.equal(normalizeSubject('RE:  re: Hello'), 'Hello');
  assert.equal(normalizeSubject('SV: Möte'), 'Möte', 'the Swedish prefix too');
  assert.equal(normalizeSubject('Regarding the budget'), 'Regarding the budget',
    'a word merely starting with "re" is not a prefix');
});

// ---------------------------------------------------------------------------
// Header values — parity with service.StripHeaderControls and
// handler.normalizeReferences
// ---------------------------------------------------------------------------

test('stripHeaderControls removes CR, LF and NUL', () => {
  assert.equal(stripHeaderControls('a\r\nBcc: evil@example.com\0'), 'aBcc: evil@example.com');
});

test('stripAngleBrackets removes one surrounding pair only', () => {
  assert.equal(stripAngleBrackets('<id@host>'), 'id@host');
  assert.equal(stripAngleBrackets('id@host'), 'id@host');
  assert.equal(stripAngleBrackets('<<id@host>>'), '<id@host>');
});

test('normalizeReferences strips brackets and drops empties', () => {
  assert.equal(normalizeReferences(['<a@h>', '', 'b@h', '<c@h>']), 'a@h\nb@h\nc@h');
  assert.equal(normalizeReferences([]), '');
});

test('normalizeReferences truncates by dropping the oldest entries', () => {
  const many = Array.from({ length: 2000 }, (_, i) => `<ref-${i}-aaaaaaaaaaaaaaaaaaaa@example.com>`);
  const joined = normalizeReferences(many);
  assert.ok(Buffer.byteLength(joined) <= MAX_REFS_BYTES, 'fits the budget');
  const kept = joined.split('\n');
  assert.equal(kept[kept.length - 1], 'ref-1999-aaaaaaaaaaaaaaaaaaaa@example.com',
    'the most recent ancestor survives');
  assert.ok(!joined.includes('ref-0-'), 'the oldest is what gets dropped');
});

// ---------------------------------------------------------------------------
// Addresses — parity with service.ParseAddressList and
// repository.parseAndFoldAddress
// ---------------------------------------------------------------------------

test('parseAddressList handles display names, quotes and bare addresses', () => {
  assert.deepEqual(parseAddressList('Alice Smith <alice@example.com>'),
    [{ name: 'Alice Smith', address: 'alice@example.com' }]);
  assert.deepEqual(parseAddressList('bob@example.com'),
    [{ name: '', address: 'bob@example.com' }]);
  assert.deepEqual(parseAddressList('"Smith, Alice" <alice@example.com>, bob@example.com'),
    [
      { name: 'Smith, Alice', address: 'alice@example.com' },
      { name: '', address: 'bob@example.com' },
    ],
    'a comma inside a quoted display name does not separate');
});

test('parseAddressList rejects what the server rejects', () => {
  assert.throws(() => parseAddressList('not an address'));
  assert.throws(() => parseAddressList('alice@'));
  assert.throws(() => parseAddressList('alice@example.com, '));
});

test('parseAndFoldAddress requires a bare addr-spec and folds case', () => {
  assert.equal(parseAndFoldAddress('  Demo@Example.COM '), 'demo@example.com');
  assert.throws(() => parseAndFoldAddress('Demo User <demo@example.com>'),
    /display name/, 'an identity address may not carry a display name');
});

test('firstAddress returns null rather than throwing on junk', () => {
  assert.equal(firstAddress('nonsense'), null);
  assert.deepEqual(firstAddress('A B <a@b.example>, c@d.example'),
    { name: 'A B', address: 'a@b.example' });
});

// ---------------------------------------------------------------------------
// HTML — parity with html.EscapeString and sanitize.HasExternalImages
// ---------------------------------------------------------------------------

test('htmlEscape matches Go html.EscapeString', () => {
  assert.equal(htmlEscape(`<a href="x">&'`), '&lt;a href=&#34;x&#34;&gt;&amp;&#39;');
});

test('hasExternalImages only fires on http(s) image sources', () => {
  assert.ok(hasExternalImages('<p><img src="https://tracker.example/pixel.gif"></p>'));
  assert.ok(hasExternalImages("<img class='x' src='http://a/b.png'>"));
  assert.ok(!hasExternalImages('<img src="data:image/png;base64,AAAA">'));
  assert.ok(!hasExternalImages('<p>no images at all</p>'));
});

// ---------------------------------------------------------------------------
// Search — parity with repository.tokenizeText, buildSnippet and the FTS5
// phrase query sanitizeFTSQuery builds
// ---------------------------------------------------------------------------

test('tokenizeText splits on non-alphanumerics and folds case', () => {
  assert.deepEqual(tokenizeText('Hello, World! 42').map((t) => t.lower),
    ['hello', 'world', '42']);
  assert.deepEqual(tokenizeText('   ').map((t) => t.lower), []);
});

test('buildSnippet marks the matched terms and elides the rest', () => {
  const body = 'The quarterly budget review is scheduled for Thursday afternoon in the main room.';
  const snippet = buildSnippet(body, 'budget');
  assert.ok(snippet.includes('**budget**'), snippet);
});

test('buildSnippet starts at the beginning when nothing matches', () => {
  const snippet = buildSnippet('one two three', 'absent');
  assert.equal(snippet, 'one two three');
  assert.ok(!snippet.startsWith('…'));
});

test('phraseMatches requires consecutive words, as an FTS5 phrase does', () => {
  const haystack = tokenizeText('the quarterly budget review');
  assert.ok(phraseMatches(haystack, ['budget', 'review']));
  assert.ok(!phraseMatches(haystack, ['budget', 'thursday']), 'not an OR');
  assert.ok(!phraseMatches(haystack, ['review', 'budget']), 'order matters');
});

test('phraseMatches treats FTS5 operator keywords as literals', () => {
  // sanitizeFTSQuery quotes the whole query, so AND/OR/NOT/NEAR never operate.
  const haystack = tokenizeText('shipping AND handling');
  assert.ok(phraseMatches(haystack, ['shipping', 'and', 'handling']));
  assert.ok(!phraseMatches(haystack, ['shipping', 'handling']));
});

// ---------------------------------------------------------------------------
// RFC 5322 assembly — parity with service.BuildMIMEMessage's structure
// ---------------------------------------------------------------------------

const RAW_FIELDS = {
  fromName: 'Demo User',
  fromAddr: 'demo@example.com',
  toAddr: 'alice@example.com',
  ccAddr: '',
  bccAddr: '',
  replyToAddr: '',
  subject: 'Hello',
  bodyText: 'Line one\nLine two',
  bodyHtml: '',
  messageId: 'abc@example.com',
  inReplyTo: '',
  references: [],
  date: new Date('2026-03-04T05:06:07Z'),
};

test('buildRawMessage writes the header block the headers view shows', () => {
  const raw = buildRawMessage(RAW_FIELDS, []);
  assert.ok(raw.startsWith('Date: Wed, 04 Mar 2026 05:06:07 +0000\r\n'), raw.slice(0, 80));
  assert.ok(raw.includes('Message-ID: <abc@example.com>\r\n'));
  assert.ok(raw.includes('From: Demo User <demo@example.com>\r\n'));
  assert.ok(raw.includes('To: alice@example.com\r\n'));
  assert.ok(!raw.includes('\r\nCc:'), 'an empty Cc is omitted, as on the server');
  assert.ok(raw.includes('Content-Type: text/plain; charset=utf-8\r\n'));
  assert.ok(raw.includes('\r\n\r\nLine one\r\nLine two'), 'body lines are CRLF-terminated');
});

test('buildRawMessage re-adds angle brackets to threading headers', () => {
  const raw = buildRawMessage(
    { ...RAW_FIELDS, inReplyTo: 'parent@example.com', references: ['root@example.com', 'parent@example.com'] },
    [],
  );
  assert.ok(raw.includes('In-Reply-To: <parent@example.com>\r\n'));
  assert.ok(raw.includes('References: <root@example.com> <parent@example.com>\r\n'));
});

test('buildRawMessage nests alternative inside mixed when there are attachments', () => {
  const raw = buildRawMessage(
    { ...RAW_FIELDS, bodyHtml: '<p>Hello</p>' },
    [{ filename: 'note.txt', contentType: 'text/plain', data: new TextEncoder().encode('hi') }],
  );
  assert.ok(/^Content-Type: multipart\/mixed; boundary="/m.test(raw), 'top level is mixed');
  assert.ok(raw.includes('Content-Type: multipart/alternative; boundary="'));
  assert.ok(raw.includes('Content-Disposition: attachment; filename="note.txt"'));
  assert.ok(raw.includes('Content-Transfer-Encoding: base64'));
});

test('newMessageId takes its domain from the sender', () => {
  assert.ok(newMessageId('Demo User <demo@example.com>').endsWith('@example.com'));
  assert.ok(newMessageId('').endsWith('@localhost'), 'an unparseable sender falls back');
});

// ---------------------------------------------------------------------------
// Filters — parity with lda.filterMatches and the chain in lda.Run
// ---------------------------------------------------------------------------

const INBOUND = {
  fromAddr: 'Alice Smith <alice@example.com>',
  toAddr: 'demo@example.com',
  ccAddr: 'team@example.com',
  subject: 'Q2 Budget Review',
};

function filter(overrides) {
  return {
    id: 1, position: 0, name: '', matchFrom: '', matchTo: '', matchSubject: '',
    action: 'trash', folderId: null, stop: true, ...overrides,
  };
}

test('filterMatches ANDs every non-empty criterion, case-insensitively', () => {
  assert.ok(filterMatches(INBOUND, filter({ matchFrom: 'ALICE@EXAMPLE.COM' })));
  assert.ok(filterMatches(INBOUND, filter({ matchFrom: 'alice', matchSubject: 'budget' })));
  assert.ok(!filterMatches(INBOUND, filter({ matchFrom: 'alice', matchSubject: 'invoice' })));
});

test('filterMatches checks match_to against Cc as well as To', () => {
  assert.ok(filterMatches(INBOUND, filter({ matchTo: 'team@example.com' })));
  assert.ok(!filterMatches(INBOUND, filter({ matchTo: 'nobody@example.com' })));
});

test('applyFilters stops at the first matching stop filter', () => {
  const outcome = applyFilters(INBOUND, [
    filter({ id: 1, action: 'mark_read', matchFrom: 'alice', stop: true }),
    filter({ id: 2, action: 'trash', matchFrom: 'alice', stop: true }),
  ], new Set([1, 4, 7]));
  assert.equal(outcome.folderId, 1, 'the second filter never ran');
  assert.equal(outcome.markRead, true);
});

test('applyFilters keeps going past a non-stop filter', () => {
  const outcome = applyFilters(INBOUND, [
    filter({ id: 1, action: 'mark_read', matchFrom: 'alice', stop: false }),
    filter({ id: 2, action: 'move', folderId: 100, matchFrom: 'alice', stop: true }),
  ], new Set([1, 4, 7, 100]));
  assert.equal(outcome.folderId, 100);
  assert.equal(outcome.markRead, true);
});

test('applyFilters ignores a move to a folder that no longer exists', () => {
  const outcome = applyFilters(INBOUND, [
    filter({ action: 'move', folderId: 999, matchFrom: 'alice' }),
  ], new Set([1, 4, 7]));
  assert.equal(outcome.folderId, 1, 'falls back to Inbox');
});

test('applyFilters reports a dropped message as no folder at all', () => {
  const outcome = applyFilters(INBOUND, [
    filter({ action: 'drop', matchFrom: 'alice' }),
  ], new Set([1]));
  assert.equal(outcome.folderId, null);
});

// ---------------------------------------------------------------------------
// The auto-reply — demo-only behaviour, so the assertions are about it being
// derived rather than random, and about it landing in the right thread
// ---------------------------------------------------------------------------

const SENT = {
  id: 10,
  folderId: 2,
  messageId: 'sent-1@example.com',
  inReplyTo: null,
  references: null,
  fromAddr: 'Demo User <demo@example.com>',
  toAddr: 'Alice Smith <alice@example.com>',
  subject: 'Q2 Budget Review',
  bodyText: 'Can we meet on Thursday?',
};

test('buildAutoReply comes from the recipient and goes back to the sender', () => {
  const reply = buildAutoReply(SENT, new Date('2026-03-04T05:06:07Z'));
  assert.equal(reply.fromAddr, 'Alice Smith <alice@example.com>');
  assert.equal(reply.toAddr, 'Demo User <demo@example.com>');
  assert.equal(reply.subject, 'Re: Q2 Budget Review');
});

test('buildAutoReply is a pure function of the message it answers', () => {
  const a = buildAutoReply(SENT, new Date('2026-03-04T05:06:07Z'));
  const b = buildAutoReply(SENT, new Date('2026-03-04T05:06:07Z'));
  assert.equal(a.bodyText, b.bodyText, 'the same message always gets the same reply');

  const other = buildAutoReply({ ...SENT, bodyText: 'Something else entirely' },
    new Date('2026-03-04T05:06:07Z'));
  assert.notEqual(a.bodyText, other.bodyText, 'a different message gets a different one');
});

test('buildAutoReply quotes the original and greets by first name', () => {
  const reply = buildAutoReply(SENT, new Date('2026-03-04T05:06:07Z'));
  assert.ok(reply.bodyText.startsWith('Hi Demo,'), reply.bodyText.slice(0, 40));
  assert.ok(reply.bodyText.includes('> Can we meet on Thursday?'), 'the original is quoted');
  assert.ok(reply.bodyText.includes('On 2026-03-04,'), 'with an attribution line');
});

test('buildAutoReply extends the thread it is replying to', () => {
  const reply = buildAutoReply(SENT, new Date());
  assert.equal(reply.inReplyTo, 'sent-1@example.com');
  assert.equal(reply.references, 'sent-1@example.com');

  const deeper = buildAutoReply({ ...SENT, references: 'root@example.com' }, new Date());
  assert.equal(deeper.references, 'root@example.com\nsent-1@example.com');
});

test('buildAutoReply gives up when there is no recipient to answer from', () => {
  assert.equal(buildAutoReply({ ...SENT, toAddr: '' }, new Date()), null);
  assert.equal(buildAutoReply({ ...SENT, fromAddr: '' }, new Date()), null);
});

// ---------------------------------------------------------------------------
// Endpoints — the parity rules that live in api.ts rather than in a helper
// ---------------------------------------------------------------------------

const BUILTIN_FOLDERS = [
  [1, 'Inbox', 'inbox'], [2, 'Sent', 'sent'], [3, 'Drafts', 'drafts'], [4, 'Trash', 'trash'],
  [5, 'Scheduled', 'scheduled'], [6, 'Snoozed', 'snoozed'], [7, 'Junk', 'junk'],
];

function message(overrides) {
  return {
    id: 0, folderId: 1, identityId: null, messageId: null, inReplyTo: null, references: null,
    fromAddr: 'alice@example.com', toAddr: 'demo@example.com', ccAddr: '', bccAddr: '',
    replyToAddr: '', subject: 'Subject', date: '2026-03-04T05:06:07Z', bodyText: 'Body',
    bodyHtml: '', raw: 'Message-ID: <x>\r\n\r\nBody', read: false, flagged: false,
    hasAttachments: false, hasExternalImages: false, sendAt: null, snoozedUntil: null,
    snoozeFolder: null, sendError: null, sendFailureCount: 0,
    createdAt: '2026-03-04T05:06:07Z', updatedAt: '2026-03-04T05:06:07Z', ...overrides,
  };
}

/** A fresh store with the built-in folders, one identity, and `messages`. */
function newState(messages = []) {
  return {
    nextMessageId: messages.reduce((max, m) => Math.max(max, m.id + 1), 1),
    nextAttachmentId: 1,
    nextFolderId: 100,
    nextIdentityId: 2,
    nextContactId: 1,
    nextFilterId: 1,
    folders: BUILTIN_FOLDERS.map(([id, name, slug], i) => ({
      id, name, slug, position: i, createdAt: '2026-01-01T00:00:00Z',
    })),
    identities: [{
      id: 1, name: 'Demo User', address: 'demo@example.com',
      isDefault: true, position: 0, signature: '',
    }],
    contacts: [],
    filters: [],
    spamFilter: { enabled: false, scoreHeader: 'X-Spam-Score', scoreThreshold: 5 },
    messages,
    attachments: [],
    pendingReplies: [],
  };
}

/** Issues one API request against `state` and returns { status, body }. */
async function call(state, method, path, body) {
  globalThis.__demoState = state;
  const init = { method };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
    init.headers = { 'Content-Type': 'application/json' };
  }
  const request = new Request('https://demo.example/api/v1' + path, init);
  const response = await handleApiRequest(path.split('?')[0], request);
  const text = await response.text();
  return {
    status: response.status,
    headers: response.headers,
    body: text === '' ? null : (response.headers.get('Content-Type')?.includes('json')
      ? JSON.parse(text) : text),
  };
}

test('GET /folders reports each folder with its unread count', async () => {
  const state = newState([message({ id: 1 }), message({ id: 2, read: true })]);
  const res = await call(state, 'GET', '/folders');
  assert.equal(res.status, 200);
  assert.equal(res.body.total, 7);
  assert.equal(res.body.items[0].slug, 'inbox');
  assert.equal(res.body.items[0].unread_count, 1);
  assert.equal(res.body.items[1].unread_count, 0, 'Sent is empty');
});

test('POST /folders assigns a user id, a unique slug and an appended position', async () => {
  const state = newState();
  const first = await call(state, 'POST', '/folders', { name: 'Work Stuff' });
  assert.equal(first.status, 201);
  assert.equal(first.body.id, 100, 'user folders start at 100');
  assert.equal(first.body.slug, 'work-stuff');
  assert.equal(first.body.position, 7, 'appended after the built-ins');

  const clash = await call(state, 'POST', '/folders', { name: 'Work  Stuff' });
  assert.equal(clash.body.slug, 'work-stuff-2', 'a colliding slug is suffixed');

  const dup = await call(state, 'POST', '/folders', { name: 'Work Stuff' });
  assert.equal(dup.status, 409, 'but a duplicate *name* is a conflict');
});

test('built-in folders cannot be renamed or deleted', async () => {
  const state = newState();
  assert.equal((await call(state, 'PATCH', '/folders/1', { name: 'Posteingang' })).status, 400);
  assert.equal((await call(state, 'PATCH', '/folders/1', { position: 3 })).status, 200,
    'but they can be reordered');
  assert.equal((await call(state, 'DELETE', '/folders/1')).status, 400);
});

test('deleting a user folder moves its messages to Trash', async () => {
  const state = newState([message({ id: 1, folderId: 100 })]);
  state.folders.push({ id: 100, name: 'Work', slug: 'work', position: 7, createdAt: '2026-01-01T00:00:00Z' });
  assert.equal((await call(state, 'DELETE', '/folders/100')).status, 204);
  assert.equal(state.messages[0].folderId, 4);
});

test('reorder requires every existing id exactly once', async () => {
  const state = newState();
  assert.equal((await call(state, 'PATCH', '/folders/reorder', { ids: [1, 2, 3] })).status, 400);
  assert.equal((await call(state, 'PATCH', '/folders/reorder', { ids: [1, 1, 2, 3, 4, 5, 6] })).status, 400);
  const ok = await call(state, 'PATCH', '/folders/reorder', { ids: [7, 6, 5, 4, 3, 2, 1] });
  assert.equal(ok.status, 200);
  assert.equal(state.folders.find((f) => f.id === 7).position, 0);
});

test('folder listing is newest first and paginates', async () => {
  const state = newState([
    message({ id: 1, date: '2026-01-01T00:00:00Z' }),
    message({ id: 2, date: '2026-03-01T00:00:00Z' }),
    message({ id: 3, date: '2026-02-01T00:00:00Z' }),
  ]);
  const res = await call(state, 'GET', '/folders/1/messages?limit=2');
  assert.equal(res.body.total, 3, 'total counts everything, not just the page');
  assert.deepEqual(res.body.items.map((m) => m.id), [2, 3]);
});

test('a listing carries send_at and snoozed_until, as the summary projection does', async () => {
  // The Scheduled and Snoozed columns read these off the listing rather than
  // fetching each message, so a summary without them is a demo that silently
  // renders two empty columns. repository.summaryColumns selects both.
  const state = newState([
    message({ id: 1, folderId: 5, sendAt: '2030-04-01T09:00:00Z' }),
    message({ id: 2, folderId: 6, snoozedUntil: '2030-04-02T09:00:00Z', snoozeFolder: 1 }),
  ]);
  const scheduled = (await call(state, 'GET', '/folders/5/messages')).body.items[0];
  assert.equal(scheduled.send_at, '2030-04-01T09:00:00Z');
  assert.equal(scheduled.snoozed_until, null);

  const snoozed = (await call(state, 'GET', '/folders/6/messages')).body.items[0];
  assert.equal(snoozed.snoozed_until, '2030-04-02T09:00:00Z');
  assert.equal(snoozed.send_at, null);
});

test('a message in Trash is deleted for good, one in Inbox is moved there', async () => {
  const state = newState([message({ id: 1 }), message({ id: 2, folderId: 4 })]);
  assert.equal((await call(state, 'DELETE', '/messages/1')).status, 204);
  assert.equal(state.messages.find((m) => m.id === 1).folderId, 4);
  assert.equal((await call(state, 'DELETE', '/messages/2')).status, 204);
  assert.equal(state.messages.find((m) => m.id === 2), undefined);
});

test('Drafts, Scheduled and Snoozed refuse a delete or a move', async () => {
  for (const folderId of [3, 5, 6]) {
    const state = newState([message({ id: 1, folderId })]);
    assert.equal((await call(state, 'DELETE', '/messages/1')).status, 400, `folder ${folderId}`);
    assert.equal((await call(state, 'POST', '/messages/move', { ids: [1], folder_id: 1 })).status, 400);
  }
});

test('a bulk operation is all-or-nothing', async () => {
  const state = newState([message({ id: 1 })]);
  const res = await call(state, 'PATCH', '/messages', { ids: [1, 99], read: true });
  assert.equal(res.status, 404);
  assert.equal(state.messages[0].read, false, 'the existing message was left alone');
});

test('moving to Trash clears the scheduling fields', async () => {
  const state = newState([message({ id: 1, snoozedUntil: '2030-01-01T00:00:00Z', snoozeFolder: 1 })]);
  await call(state, 'POST', '/messages/move', { ids: [1], folder_id: 4 });
  const msg = state.messages[0];
  assert.equal(msg.snoozedUntil, null);
  assert.equal(msg.snoozeFolder, null);
});

test('mark-junk marks read; mark-not-junk returns it unread to the Inbox', async () => {
  const state = newState([message({ id: 1 })]);
  assert.equal((await call(state, 'POST', '/messages/1/mark-junk')).body.folder_id, 7);
  assert.equal(state.messages[0].read, true);
  assert.equal((await call(state, 'POST', '/messages/1/mark-not-junk')).body.folder_id, 1);
  assert.equal(state.messages[0].read, false);
  assert.equal((await call(state, 'POST', '/messages/1/mark-not-junk')).status, 400,
    'only a message in Junk can be un-junked');
});

test('snooze remembers the folder to come back to and refuses a near deadline', async () => {
  const state = newState([message({ id: 1, folderId: 100 })]);
  const soon = new Date(Date.now() + 10_000).toISOString();
  assert.equal((await call(state, 'POST', '/messages/1/snooze', { until: soon })).status, 400);

  const later = new Date(Date.now() + 3_600_000).toISOString();
  const res = await call(state, 'POST', '/messages/1/snooze', { until: later });
  assert.equal(res.status, 200);
  assert.equal(res.body.folder_id, 6);
  assert.equal(res.body.snooze_folder_id, 100);

  const cancelled = await call(state, 'DELETE', '/messages/1/snooze');
  assert.equal(cancelled.body.folder_id, 100, 'it goes back where it came from');
  assert.equal(state.messages[0].read, false);
});

test('an expired snooze is released the next time a request comes in', () => {
  const state = newState([message({
    id: 1, folderId: 6, read: true,
    snoozedUntil: '2026-01-01T00:00:00Z', snoozeFolder: 100,
  })]);
  const changed = runScheduler(state, Date.parse('2026-01-02T00:00:00Z'));
  assert.ok(changed);
  assert.equal(state.messages[0].folderId, 100);
  assert.equal(state.messages[0].read, false, 'and it is unread again');
});

test('a thread closes over In-Reply-To and References in both directions', async () => {
  const state = newState([
    message({ id: 1, messageId: 'a@x', subject: 'Plan' }),
    message({ id: 2, messageId: 'b@x', inReplyTo: 'a@x', references: 'a@x', subject: 'Re: Plan' }),
    message({ id: 3, messageId: 'c@x', inReplyTo: 'b@x', references: 'a@x\nb@x', subject: 'Re: Plan' }),
    message({ id: 4, messageId: 'z@x', subject: 'Unrelated' }),
  ]);
  const res = await call(state, 'GET', '/messages/2/thread');
  assert.equal(res.status, 200);
  assert.deepEqual(res.body.items.map((m) => m.id), [1, 2, 3], 'ordered oldest first');
  assert.equal(res.body.truncated, false);
});

test('a message that links to nothing falls back to same-folder subject matching', async () => {
  const state = newState([
    message({ id: 1, messageId: 'a@x', subject: 'Invoice 42' }),
    message({ id: 2, messageId: 'b@x', subject: 'Re: Invoice 42' }),
    message({ id: 3, messageId: 'c@x', subject: 'Invoice 42', folderId: 2 }),
  ]);
  const res = await call(state, 'GET', '/messages/1/thread');
  assert.deepEqual(res.body.items.map((m) => m.id), [1, 2], 'the Sent copy is not pulled in');
});

test('search matches a phrase and excludes Drafts, Scheduled and Junk by default', async () => {
  const state = newState([
    message({ id: 1, subject: 'Budget review', bodyText: 'the quarterly budget review' }),
    message({ id: 2, folderId: 3, subject: 'Budget review', bodyText: 'draft' }),
    message({ id: 3, folderId: 7, subject: 'Budget review', bodyText: 'junk' }),
    message({ id: 4, subject: 'Unrelated', bodyText: 'nothing here' }),
  ]);
  const res = await call(state, 'GET', '/messages/search?q=budget%20review');
  assert.deepEqual(res.body.items.map((m) => m.id), [1]);
  assert.ok(res.body.items[0].snippet.includes('**budget**'));

  const scoped = await call(state, 'GET', '/messages/search?q=budget&folder_id=3');
  assert.deepEqual(scoped.body.items.map((m) => m.id), [2], 'an explicit folder overrides that');
});

test('search rejects an empty query', async () => {
  assert.equal((await call(newState(), 'GET', '/messages/search?q=%20%20')).status, 400);
});

test('search refines on from_addr and to_addr the way repository.SearchMessages does', async () => {
  const state = newState([
    message({
      id: 1, fromAddr: '"Alice Andersson" <alice@example.com>', toAddr: 'demo@example.com',
      bodyText: 'the needle is here',
    }),
    message({
      id: 2, fromAddr: 'bob@other.example', toAddr: 'team@example.com',
      ccAddr: 'demo@example.com', bodyText: 'the needle is here',
    }),
  ]);
  const ids = async (query) =>
    (await call(state, 'GET', '/messages/search?q=needle' + query)).body.items.map((m) => m.id);

  assert.deepEqual(await ids(''), [1, 2], 'unfiltered');
  assert.deepEqual(await ids('&from_addr=alice%40example.com'), [1]);
  assert.deepEqual(await ids('&from_addr=ALICE'), [1], 'case-insensitive');
  assert.deepEqual(await ids('&from_addr=Andersson'), [1], 'the display name is part of from_addr');
  assert.deepEqual(await ids('&to_addr=team%40'), [2], 'matches the To header');
  assert.deepEqual(await ids('&to_addr=demo%40example.com'), [1, 2], 'and the Cc header');
  assert.deepEqual(await ids('&from_addr=bob&to_addr=demo%40example.com'), [2], 'ANDed');
  assert.deepEqual(await ids('&from_addr=carol'), [], 'no match');
  assert.deepEqual(await ids('&from_addr='), [1, 2], 'a blank value is no filter');
  assert.deepEqual(await ids('&to_addr=%20%20'), [1, 2], 'and so is whitespace only');
  assert.deepEqual(await ids('&to_addr=t%25m%40'), [], 'LIKE wildcards are literals');

  const tooLong = await call(state, 'GET', `/messages/search?q=needle&from_addr=${'a'.repeat(201)}`);
  assert.equal(tooLong.status, 400, 'over the maxLength the server rejects with 400');
});

// The server folds with unicode_lower() (see repository/sqlfunc.go) precisely so
// it agrees with toLowerCase() here; SQLite's built-in lower() is ASCII-only and
// would have made these two disagree.
test('search folds non-ASCII addresses the way the server does', async () => {
  const state = newState([
    message({
      id: 1, fromAddr: '"Åsa Öberg" <asa@example.com>', toAddr: '"Émile Ünger" <emile@example.com>',
      bodyText: 'the needle is here',
    }),
  ]);
  const ids = async (query) =>
    (await call(state, 'GET', '/messages/search?q=needle' + query)).body.items.map((m) => m.id);

  assert.deepEqual(await ids('&from_addr=' + encodeURIComponent('Åsa')), [1], 'as stored');
  assert.deepEqual(await ids('&from_addr=' + encodeURIComponent('åsa')), [1], 'lowercased');
  assert.deepEqual(await ids('&from_addr=' + encodeURIComponent('ÅSA ÖBERG')), [1], 'uppercased');
  assert.deepEqual(await ids('&to_addr=' + encodeURIComponent('émile ünger')), [1], 'to lowercased');
});

test('the message body is served as a document with its own CSP', async () => {
  const state = newState([message({ id: 1, bodyHtml: '<p>hi</p>' })]);
  const res = await call(state, 'GET', '/messages/1/body');
  assert.equal(res.status, 200);
  assert.ok(res.body.includes('<body><p>hi</p></body>'));
  assert.match(res.headers.get('Content-Security-Policy'), /img-src data:;/);
  // The policy is repeated inside the document: the page has to hand this to
  // the iframe as srcdoc (a sandboxed iframe's navigation never reaches a
  // service worker), and the headers do not survive that.
  assert.ok(res.body.includes(`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'">`),
    res.body.slice(0, 200));

  const external = await call(state, 'GET', '/messages/1/body?external=1');
  assert.match(external.headers.get('Content-Security-Policy'), /img-src https: data:;/);
  assert.ok(external.body.includes("img-src https: data:;"), 'the meta copy tracks it');
});

test('headers come from raw, and a draft has none', async () => {
  const state = newState([
    message({ id: 1, raw: 'Subject: Hi\r\nFrom: a@b\r\n\r\nbody text' }),
    message({ id: 2, folderId: 3, raw: null }),
  ]);
  const res = await call(state, 'GET', '/messages/1/headers');
  assert.equal(res.body, 'Subject: Hi\r\nFrom: a@b');
  assert.equal((await call(state, 'GET', '/messages/2/headers')).status, 404);
  assert.deepEqual((await call(state, 'GET', '/messages/2/raw')).body, {},
    'the raw download answers {} for a draft instead of 404');
});

test('sending stores the message in Sent and queues a reply', async () => {
  const state = newState();
  const res = await call(state, 'POST', '/messages/send', {
    to_addr: 'Alice Smith <alice@example.com>',
    subject: 'Hello',
    body_text: 'Hi there',
  });
  assert.equal(res.status, 201);

  const sent = state.messages.find((m) => m.id === res.body.id);
  assert.equal(sent.folderId, 2);
  assert.equal(sent.read, true);
  assert.ok(sent.messageId.endsWith('@example.com'), 'a Message-ID is assigned');
  assert.ok(sent.raw.includes('Subject: Hello'), 'and an RFC 5322 source is built');
  assert.equal(sent.fromAddr, 'demo@example.com', 'From comes from the default identity');

  assert.deepEqual(state.contacts.map((c) => c.address), ['alice@example.com'],
    'the recipient becomes a contact');
  assert.equal(state.pendingReplies.length, 1);
  // date is second-resolution (as the DB column is) while dueAt is a
  // millisecond clock reading, so the gap is the delay give or take a second.
  const gap = state.pendingReplies[0].dueAt - Date.parse(sent.date);
  assert.ok(gap >= AUTO_REPLY_DELAY_MS && gap < AUTO_REPLY_DELAY_MS + 1000, `gap was ${gap}`);
});

test('the queued reply arrives in the Inbox once its delay has passed', async () => {
  const state = newState();
  const res = await call(state, 'POST', '/messages/send', {
    to_addr: 'Alice Smith <alice@example.com>',
    subject: 'Q2 Budget Review',
    body_text: 'Can we meet on Thursday?',
  });

  const before = state.messages.length;
  assert.equal(runScheduler(state, Date.now()), false, 'nothing is due yet');
  assert.equal(state.messages.length, before);

  assert.equal(runScheduler(state, Date.now() + AUTO_REPLY_DELAY_MS), true);
  const reply = state.messages.find((m) => m.id !== res.body.id && m.folderId === 1);
  assert.ok(reply !== undefined, 'a reply landed in the Inbox');
  assert.equal(reply.subject, 'Re: Q2 Budget Review');
  assert.equal(reply.read, false);
  assert.equal(reply.inReplyTo, state.messages[0].messageId, 'threaded to what it answers');
  assert.equal(state.pendingReplies.length, 0, 'and it is only delivered once');
});

test('the reply runs through the filter chain like any inbound message', async () => {
  const state = newState();
  state.filters.push(filter({ id: 1, action: 'move', folderId: 100, matchFrom: 'alice@example.com' }));
  state.folders.push({ id: 100, name: 'Work', slug: 'work', position: 7, createdAt: '2026-01-01T00:00:00Z' });

  await call(state, 'POST', '/messages/send', { to_addr: 'alice@example.com', subject: 'Hi' });
  runScheduler(state, Date.now() + AUTO_REPLY_DELAY_MS);
  const reply = state.messages.find((m) => m.folderId === 100);
  assert.ok(reply !== undefined, 'the filter routed it out of the Inbox');
});

test('a send more than 60 s out is scheduled rather than sent', async () => {
  const state = newState();
  const sendAt = new Date(Date.now() + 3_600_000).toISOString();
  const res = await call(state, 'POST', '/messages/send', {
    to_addr: 'alice@example.com', subject: 'Later', send_at: sendAt,
  });
  assert.equal(res.status, 202);
  const msg = state.messages[0];
  assert.equal(msg.folderId, 5);
  assert.equal(msg.raw, null, 'it has no source until it is actually sent');
  assert.equal(state.pendingReplies.length, 0, 'and no reply is queued yet');

  // A send_at inside the threshold goes out immediately instead.
  const soon = await call(newState(), 'POST', '/messages/send', {
    to_addr: 'alice@example.com', subject: 'Now', send_at: new Date(Date.now() + 5_000).toISOString(),
  });
  assert.equal(soon.status, 201);
});

test('a scheduled message goes out when its time comes', async () => {
  const state = newState();
  const sendAt = new Date(Date.now() + 3_600_000).toISOString();
  await call(state, 'POST', '/messages/send', {
    to_addr: 'alice@example.com', subject: 'Later', send_at: sendAt,
  });
  assert.equal(runScheduler(state, Date.parse(sendAt) + 1000), true);
  assert.equal(state.messages[0].folderId, 2);
  assert.equal(state.messages[0].sendAt, null);
  assert.equal(state.pendingReplies.length, 1, 'and now its reply is queued');
});

test('cancelling a scheduled message returns it to Drafts', async () => {
  const state = newState([message({ id: 1, folderId: 5, sendAt: '2030-01-01T00:00:00Z' })]);
  const res = await call(state, 'DELETE', '/scheduled/1');
  assert.equal(res.body.folder_id, 3);
  assert.equal(state.messages[0].sendAt, null);
  assert.equal((await call(state, 'DELETE', '/scheduled/1')).status, 404, 'and only once');
});

test('a draft is a message with no raw, and PUT replaces it wholesale', async () => {
  const state = newState();
  const created = await call(state, 'POST', '/drafts', {
    to_addr: 'alice@example.com', subject: 'Draft', body_text: 'text', cc_addr: 'bob@example.com',
  });
  assert.equal(created.status, 201);
  const draft = state.messages[0];
  assert.equal(draft.folderId, 3);
  assert.equal(draft.raw, null);
  assert.equal(draft.fromAddr, 'demo@example.com', 'from_addr comes from the default identity');

  await call(state, 'PUT', `/drafts/${created.body.id}`, { subject: 'Only the subject' });
  assert.equal(draft.ccAddr, '', 'an omitted field is cleared, not preserved');
  assert.equal(draft.toAddr, '');
});

test('sending a draft moves it to Sent and it is no longer a draft', async () => {
  const state = newState();
  const created = await call(state, 'POST', '/drafts', {
    to_addr: 'alice@example.com', subject: 'Ready', body_text: 'go',
  });
  const res = await call(state, 'POST', `/drafts/${created.body.id}/send`);
  assert.equal(res.status, 201);
  assert.equal(state.messages[0].folderId, 2);
  assert.equal((await call(state, 'POST', `/drafts/${created.body.id}/send`)).status, 404);
});

test('an immediate draft send is dated now, not when the draft was written', async () => {
  // handler.executeSend inserts a new row with Date: now and deletes the draft,
  // so an old draft must not land far down a Sent list ordered by date — nor
  // disagree with the Date: header of the source built for it.
  const state = newState([message({
    id: 1, folderId: 3, raw: null, toAddr: 'alice@example.com', subject: 'Old',
    date: '2026-01-01T00:00:00Z', createdAt: '2026-01-01T00:00:00Z',
  })]);
  const before = Date.now();
  assert.equal((await call(state, 'POST', '/drafts/1/send')).status, 201);

  const sent = state.messages[0];
  assert.ok(Date.parse(sent.date) >= before - 1000, `date was ${sent.date}`);
  assert.ok(sent.raw.includes(`Date: ${rfc1123ZOf(sent.date)}`),
    'and the RFC 5322 source agrees with it');
});

/** The Date: header form buildRawMessage writes, for the assertion above. */
function rfc1123ZOf(timestamp) {
  const d = new Date(timestamp);
  const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  const pad = (n) => String(n).padStart(2, '0');
  return `${days[d.getUTCDay()]}, ${pad(d.getUTCDate())} ${months[d.getUTCMonth()]} ` +
    `${d.getUTCFullYear()} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())} +0000`;
}

test('a scheduled send keeps the date it was composed with', async () => {
  // The scheduler and /scheduled/{id}/send both UPDATE in place on the server,
  // so unlike the immediate path they must leave `date` alone. send_at has to
  // be in the future or the scheduler delivers it before the handler is reached.
  const future = new Date(Date.now() + 3_600_000).toISOString();
  const scheduled = () => newState([message({
    id: 1, folderId: 5, raw: null, toAddr: 'alice@example.com',
    sendAt: future, date: '2026-01-01T00:00:00Z',
  })]);

  const sentByUser = scheduled();
  assert.equal((await call(sentByUser, 'POST', '/scheduled/1/send')).status, 200);
  assert.equal(sentByUser.messages[0].folderId, 2);
  assert.equal(sentByUser.messages[0].date, '2026-01-01T00:00:00Z');

  const sentByScheduler = scheduled();
  assert.equal(runScheduler(sentByScheduler, Date.parse(future) + 1000), true);
  assert.equal(sentByScheduler.messages[0].folderId, 2);
  assert.equal(sentByScheduler.messages[0].date, '2026-01-01T00:00:00Z');
});

test('a rejected draft leaves nothing behind', async () => {
  // The server does the insert and the attachment copy in one transaction, so a
  // bad source_message_id must not leave a half-built draft in the store.
  const state = newState();
  const res = await call(state, 'POST', '/drafts', { subject: 'Forward', source_message_id: 99 });
  assert.equal(res.status, 400);
  assert.equal(state.messages.length, 0);
});

test('forwarding copies the source message attachments into the draft', async () => {
  const state = newState([message({ id: 1, hasAttachments: true })]);
  state.attachments.push({ id: 7, messageId: 1, filename: 'quote.txt', contentType: 'text/plain', size: 2 });
  globalThis.__demoBlobs.set(7, new TextEncoder().encode('hi').buffer);

  const res = await call(state, 'POST', '/drafts', { subject: 'Fwd: x', source_message_id: 1 });
  assert.equal(res.status, 201);
  const copied = state.attachments.filter((a) => a.messageId === res.body.id);
  assert.equal(copied.length, 1);
  assert.notEqual(copied[0].id, 7, 'the copy gets its own id');
  assert.equal(copied[0].filename, 'quote.txt');
  assert.equal(state.messages.find((m) => m.id === res.body.id).hasAttachments, true);
});

test('a draft with no recipient at all cannot be sent', async () => {
  const state = newState();
  const created = await call(state, 'POST', '/drafts', { subject: 'Nobody' });
  assert.equal((await call(state, 'POST', `/drafts/${created.body.id}/send`)).status, 400);
});

test('identities: the first is the default, and the last cannot be deleted', async () => {
  const state = newState();
  assert.equal((await call(state, 'DELETE', '/identities/1')).status, 400);

  const second = await call(state, 'POST', '/identities', {
    name: 'Other', address: 'Other@Example.COM', is_default: true,
  });
  assert.equal(second.status, 201);
  assert.equal(second.body.address, 'other@example.com', 'the address is casefolded');
  assert.equal(state.identities.find((i) => i.id === 1).isDefault, false, 'the old default stepped down');

  const dup = await call(state, 'POST', '/identities', { name: 'Dup', address: 'other@example.com' });
  assert.equal(dup.status, 409);
  assert.equal((await call(state, 'POST', '/identities', { name: 'Bad', address: 'A <a@b.example>' })).status, 400);
});

test('deleting the default identity promotes the next one and clears its drafts', async () => {
  const state = newState([message({ id: 1, folderId: 3, identityId: 1, fromAddr: 'demo@example.com' })]);
  await call(state, 'POST', '/identities', { name: 'Other', address: 'other@example.com' });
  assert.equal((await call(state, 'DELETE', '/identities/1')).status, 204);
  assert.equal(state.identities[0].isDefault, true);
  assert.equal(state.messages[0].identityId, null);
  assert.equal(state.messages[0].fromAddr, '');
});

test('contacts sort named first and filter on either field', async () => {
  const state = newState();
  await call(state, 'POST', '/contacts', { address: 'zoe@example.com', name: 'Zoe' });
  await call(state, 'POST', '/contacts', { address: 'anon@example.com' });
  await call(state, 'POST', '/contacts', { address: 'amy@example.com', name: 'Amy' });

  const all = await call(state, 'GET', '/contacts');
  assert.deepEqual(all.body.items.map((c) => c.address),
    ['amy@example.com', 'zoe@example.com', 'anon@example.com']);

  const byName = await call(state, 'GET', '/contacts?q=zo');
  assert.deepEqual(byName.body.items.map((c) => c.address), ['zoe@example.com']);
  const byAddress = await call(state, 'GET', '/contacts?q=anon');
  assert.deepEqual(byAddress.body.items.map((c) => c.address), ['anon@example.com']);
});

test('a move filter may only target Inbox, Trash, Junk or a user folder', async () => {
  const state = newState();
  for (const folderId of [2, 3, 5, 6]) {
    const res = await call(state, 'POST', '/filters', {
      action: 'move', folder_id: folderId, match_from: 'a@b',
    });
    assert.equal(res.status, 400, `folder ${folderId} must be rejected`);
  }
  assert.equal((await call(state, 'POST', '/filters', { action: 'move', match_from: 'a@b' })).status, 400,
    'move without a folder is rejected too');
  assert.equal((await call(state, 'POST', '/filters', { action: 'trash', match_from: 'a@b' })).status, 201);
  assert.equal((await call(state, 'POST', '/filters', { action: 'trash' })).status, 400,
    'a filter that matches nothing is rejected');
});

test('the spam filter settings round-trip and are validated', async () => {
  const state = newState();
  const res = await call(state, 'PUT', '/spam-filter', {
    enabled: true, score_header: 'X-Spam-Score', score_threshold: 4.5,
  });
  assert.equal(res.status, 200);
  assert.deepEqual(await (await call(state, 'GET', '/spam-filter')).body, {
    enabled: true, score_header: 'X-Spam-Score', score_threshold: 4.5,
  });
  assert.equal((await call(state, 'PUT', '/spam-filter', {
    enabled: true, score_header: '', score_threshold: 1,
  })).status, 400);
  assert.equal((await call(state, 'PUT', '/spam-filter', {
    enabled: true, score_header: 'X', score_threshold: -1,
  })).status, 400);
});

test('an unknown route is a 404 in the API error shape', async () => {
  const res = await call(newState(), 'GET', '/nonexistent');
  assert.equal(res.status, 404);
  assert.equal(typeof res.body.error, 'string');
});
