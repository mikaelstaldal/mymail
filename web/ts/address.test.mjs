import test from 'node:test';
import assert from 'node:assert/strict';

// Pure string handling — no DOM needed, so no jsdom install here.
const {
  isValidAddressList, hasValidRecipient, isValidAddress, splitAddressList, formatAddress,
  normalizeAddressEntry,
} = await import('../static/util/address.js');

test('isValidAddressList accepts the forms the compose fields produce', () => {
  assert.equal(isValidAddressList('a@b.com'), true);
  assert.equal(isValidAddressList('Alice <alice@example.com>'), true);
  assert.equal(isValidAddressList('"Doe, Jane" <jane@example.com>'), true);
  assert.equal(isValidAddressList('a@b.com, Alice <alice@example.com>'), true);
});

test('isValidAddressList treats an empty list as valid', () => {
  assert.equal(isValidAddressList(''), true);
  assert.equal(isValidAddressList('   '), true);
});

test('isValidAddressList rejects malformed entries', () => {
  assert.equal(isValidAddressList('not-an-address'), false);
  assert.equal(isValidAddressList('a@b.com, oops'), false);
  assert.equal(isValidAddressList('a@b.com,'), false);
  assert.equal(isValidAddressList('Alice <alice@example.com'), false);
  assert.equal(isValidAddressList('a b@c.com'), false);
  assert.equal(isValidAddressList('@b.com'), false);
  assert.equal(isValidAddressList('a@'), false);
});

test('a display-name comma does not split the list', () => {
  // One recipient, not two — the same rule splitAddressList applies in the
  // demo backend and net/mail applies on the server.
  assert.equal(isValidAddressList('"Doe, Jane" <jane@example.com>'), true);
  assert.equal(hasValidRecipient('"Doe, Jane" <jane@example.com>', '', ''), true);
});

test('hasValidRecipient needs at least one recipient', () => {
  assert.equal(hasValidRecipient('', '', ''), false);
  assert.equal(hasValidRecipient('a@b.com', '', ''), true);
  assert.equal(hasValidRecipient('', 'a@b.com', ''), true);
  assert.equal(hasValidRecipient('', '', 'a@b.com'), true);
});

test('hasValidRecipient rejects when any list is malformed', () => {
  // Cc would be a 400 from the server even though To is fine.
  assert.equal(hasValidRecipient('a@b.com', 'oops', ''), false);
  assert.equal(hasValidRecipient('oops', '', ''), false);
});

test('isValidAddress judges one entry, not a list', () => {
  assert.equal(isValidAddress('a@b.com'), true);
  assert.equal(isValidAddress('Alice <alice@example.com>'), true);
  // Half-typed: what a contact search looks like before a suggestion is picked.
  // Committing this to a pill would make every later draft save fail.
  assert.equal(isValidAddress('jane'), false);
  assert.equal(isValidAddress('jane@'), false);
  assert.equal(isValidAddress(''), false);
});

test('splitAddressList splits on top-level commas only', () => {
  assert.deepEqual(splitAddressList('a@b.com, c@d.com'), ['a@b.com', ' c@d.com']);
  assert.deepEqual(splitAddressList('"Doe, Jane" <j@e.com>'), ['"Doe, Jane" <j@e.com>']);
  assert.deepEqual(
    splitAddressList('"Doe, Jane" <j@e.com>, a@b.com'),
    ['"Doe, Jane" <j@e.com>', ' a@b.com'],
  );
});

test('formatAddress quotes a display name that would otherwise split', () => {
  // Contact names are stored unquoted, so the comma form is what comes back
  // from the server for an Outlook-style sender.
  assert.equal(formatAddress('Doe, Jane', 'jane@example.com'), '"Doe, Jane" <jane@example.com>');
  assert.equal(formatAddress('Alice', 'alice@example.com'), 'Alice <alice@example.com>');
  assert.equal(formatAddress('', 'alice@example.com'), 'alice@example.com');
  assert.equal(formatAddress('  ', 'alice@example.com'), 'alice@example.com');
  assert.equal(formatAddress('John Q. Public', 'jq@example.com'), '"John Q. Public" <jq@example.com>');
  // A quote in the name is dropped rather than escaped — no parser downstream
  // of this handles backslash escapes.
  assert.equal(formatAddress('Jane "Jay" Doe', 'jane@example.com'), 'Jane Jay Doe <jane@example.com>');
});

test('normalizeAddressEntry makes a stored sender sendable again', () => {
  // What service.DecodeAddressHeader stores for an Outlook-style sender: the
  // quotes are gone, so replying would produce an unsendable recipient.
  const stored = 'Doe, Jane <jane@example.com>';
  // Well-formed read as one entry, broken read as a list — which is how it is
  // read once it is a recipient. This gap is why the blur commit checks the
  // list form, not the entry form.
  assert.equal(isValidAddress(stored), true);
  assert.equal(isValidAddressList(stored), false);
  assert.equal(normalizeAddressEntry(stored), '"Doe, Jane" <jane@example.com>');
  assert.equal(isValidAddressList(normalizeAddressEntry(stored)), true);
});

test('normalizeAddressEntry leaves ordinary entries alone', () => {
  assert.equal(normalizeAddressEntry('alice@example.com'), 'alice@example.com');
  assert.equal(normalizeAddressEntry(' Alice <alice@example.com> '), 'Alice <alice@example.com>');
  assert.equal(
    normalizeAddressEntry('"Doe, Jane" <jane@example.com>'),
    '"Doe, Jane" <jane@example.com>',
  );
  // Nothing to repair and nothing to guess at: left as typed, and still invalid.
  assert.equal(normalizeAddressEntry('jane'), 'jane');
});

test('what formatAddress produces is what the send gate accepts', () => {
  const tag = formatAddress('Doe, Jane', 'jane@example.com');
  assert.equal(isValidAddress(tag), true);
  assert.equal(hasValidRecipient(tag, '', ''), true);
  // And it survives the round-trip through a stored list.
  assert.deepEqual(splitAddressList([tag, 'a@b.com'].join(', ')), [tag, ' a@b.com']);
});
