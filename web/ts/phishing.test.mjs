// Pure presentation logic: plain warning detail and IDN sender display. Structured
// link warnings, rendering, dialog cancellation and downloads are covered by phishing.spec.ts.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { senderParts, warningText, decodeWarningDomain } from '../static/util/phishing.js';

test('sender name cannot hide the actual address', () => {
  assert.deepEqual(senderParts('"support@paypal.com" <thief@evil.example>'), {
    name: 'support@paypal.com', address: 'thief@evil.example', ascii: 'thief@evil.example', idn: false,
  });
  assert.equal(senderParts('Jane Doe <jane@example.com>').name, 'Jane Doe');
  assert.equal(senderParts('jane@example.com').address, 'jane@example.com');
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
    [{ kind: 'risky_attachment', filename: 'bill.pdf.exe', reason: 'double_extension' }, 'double extension'],
    [{ kind: 'risky_attachment', filename: 'run.exe', reason: 'executable_extension' }, 'executable extension'],
    [{ kind: 'risky_attachment', filename: 'file', reason: 'executable_content_type' }, 'executable content type'],
  ]) assert.ok(warningText(warning).includes(detail), JSON.stringify(warning));
});
