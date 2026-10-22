// Pure presentation logic: plain warning detail and IDN sender display. Structured
// link warnings, rendering, dialog cancellation and downloads are covered by phishing.spec.ts.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { senderParts, warningText, warningParts, decodeWarningDomain, revealFormatChars } from '../static/util/phishing.js';

test('sender name cannot hide the actual address', () => {
  assert.deepEqual(senderParts('"support@paypal.com" <thief@evil.example>'), {
    name: 'support@paypal.com', address: 'thief@evil.example', ascii: 'thief@evil.example', idn: false,
  });
  assert.equal(senderParts('Jane Doe <jane@example.com>').name, 'Jane Doe');
  assert.equal(senderParts('jane@example.com').address, 'jane@example.com');
});
test('several mailboxes are shown as received, not as the last one', () => {
  assert.deepEqual(senderParts('x@evil.com, Bank <ceo@bank.com>'), {
    name: '', address: 'x@evil.com, Bank <ceo@bank.com>', ascii: 'x@evil.com, Bank <ceo@bank.com>', idn: false,
  });
  assert.equal(senderParts('"Doe, Jane" <jane@example.com>').name, 'Doe, Jane');
  assert.equal(senderParts('"Doe, Jane" <jane@example.com>').address, 'jane@example.com');
});
test('IDN display preserves the original address and decodes labels', () => {
  const sender = senderParts('Support <help@xn--bcher-kva.de>');
  assert.equal(sender.address, 'help@bücher.de');
  assert.equal(sender.ascii, 'help@xn--bcher-kva.de');
  assert.equal(sender.idn, true);
  assert.equal(senderParts('help@bücher.de').idn, true);
  assert.equal(senderParts('help@bücher.de').ascii, 'help@xn--bcher-kva.de');
  assert.equal(senderParts('help@xn--paypal-.com').address, 'help@xn--paypal-.com');
  assert.equal(senderParts('help@xn--paypal-.com').idn, true);
  for (const invalid of ['xn--paypal-', 'xn--', 'xn--!', 'xn--zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz']) {
    assert.equal(decodeWarningDomain(invalid), invalid);
  }
});
test('plain warnings explain their details without inventing a positive verdict', () => {
  for (const [warning, detail] of [
    [{ kind: 'auth_fail', failed_checks: ['dmarc', 'dkim_alignment', 'spf'] }, 'DMARC, DKIM alignment, SPF'],
    [{ kind: 'display_name_address', address: 'support@paypal.com' }, 'support@paypal.com'],
    [{ kind: 'lookalike_domain', domain: 'bücher.de', punycode: 'xn--bcher-kva.de', resembles: 'bucher.de' }, 'resembles bucher.de'],
    [{ kind: 'reply_to_mismatch', address: 'thief@evil.example' }, 'thief@evil.example'],
    [{ kind: 'first_time_sender' }, 'never sent'],
    [{ kind: 'malformed_sender' }, 'malformed'],
    [{ kind: 'risky_attachment', filename: 'bill.pdf.exe', reason: 'double_extension' }, 'double extension'],
    [{ kind: 'risky_attachment', filename: 'run.exe', reason: 'executable_extension' }, 'executable extension'],
    [{ kind: 'risky_attachment', filename: 'file', reason: 'executable_content_type' }, 'executable content type'],
  ]) assert.ok(warningText(warning).includes(detail), JSON.stringify(warning));
});
test('a bidi override cannot make the sender address display as another', () => {
  assert.equal(senderParts('PayPal <\u202Emoc.lapyap@ecivres>').address, '<U+202E>moc.lapyap@ecivres');
  const idn = senderParts('Support <service@\u202Emoc.lapyap>');
  assert.equal(idn.address, 'service@<U+202E>moc.lapyap');
  assert.equal(idn.ascii, 'service@<U+202E>moc.lapyap');
  assert.equal(senderParts('x@evil.com, \u2067Bank\u2069 <ceo@bank.com>').address, 'x@evil.com, <U+2067>Bank<U+2069> <ceo@bank.com>');
  // URL normalization drops these from a domain; they must still show.
  for (const c of ['\u200B', '\u00AD', '\uFEFF']) {
    const hex = c.codePointAt(0).toString(16).toUpperCase().padStart(4, '0');
    assert.deepEqual(senderParts(`a@pay${c}pal.com`), { name: '', address: `a@pay<U+${hex}>pal.com`, ascii: `a@pay<U+${hex}>pal.com`, idn: false });
  }
  assert.equal(revealFormatChars('a\u200Bb\u2028c\u061Cd'), 'a<U+200B>b<U+2028>c<U+061C>d');
  assert.equal(revealFormatChars('help@bücher.de'), 'help@bücher.de');
  assert.equal(warningText({ kind: 'reply_to_mismatch', address: 'x@\u202Emoc.lapyap' }),
    'Replies will go to x@<U+202E>moc.lapyap, on a different domain from the sender');
});

test('all bidi controls are revealed without changing ordinary filenames', () => {
  for (const code of [0x061c, 0x200e, 0x200f, 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069]) {
    const filename = `invoice${String.fromCodePoint(code)}fdp.exe`;
    const visible = `invoice<U+${code.toString(16).toUpperCase().padStart(4, '0')}>fdp.exe`;
    assert.equal(revealFormatChars(filename), visible);
    assert.deepEqual(warningParts({ kind: 'risky_attachment', filename, reason: 'executable_extension' }),
      ['Attachment “', { value: visible }, '” has a potentially executable extension']);
    assert.equal(warningText({ kind: 'risky_attachment', filename, reason: 'executable_extension' }),
      `Attachment “${visible}” has a potentially executable extension`);
  }
  assert.equal(revealFormatChars('חשבונית.pdf'), 'חשבונית.pdf');
});
