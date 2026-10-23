// Mailto parsing and browser-handler routes, tested through compiled output.
// Covers decoding, untrusted headers, and deployment paths; browser consent is browser-owned.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseMailto, mailtoHandlerUrl } from '../static/util/mailto.js';
import { currentRoute } from '../static/router.js';

test('RFC 6068 fields decode once, preserving plus and repeated recipients', () => {
  assert.deepEqual(parseMailto('MAILTO:a+tag@example.org?TO=b%40example.org&cc=c%40example.org&CC=d%40example.org&bcc=e%40example.org&subject=A+B%2520&body=%3Cb%3EHi%3C%2Fb%3E%0D%0Anext'), {
    to: ['a+tag@example.org', 'b@example.org'], cc: ['c@example.org', 'd@example.org'],
    bcc: ['e@example.org'], subject: 'A+B%20', body: '<b>Hi</b>\nnext',
  });
});
test('unsafe headers and unsupported fields never populate compose headers', () => {
  assert.deepEqual(parseMailto('mailto:?to=a%0D%0ABcc:evil&subject=x%00&from=evil&attach=file&body=hi'), {
    to: [], cc: [], bcc: [], subject: '', body: 'hi',
  });
  for (const uri of ['https://example.org', 'mailto:%ZZ', 'mailto:?body=%FF']) {
    assert.equal(parseMailto(uri), null);
  }
});
test('handler keeps deployment prefix and round-trips nested URI encoding', () => {
  const template = mailtoHandlerUrl('https://example.org/mail/?old=yes#/settings/preferences');
  assert.equal(template, 'https://example.org/mail/#/compose?mailto=%s');
  const uri = 'mailto:a+tag@example.org?subject=Hi%20there&body=a%26b';
  globalThis.window = { location: new URL(template.replace('%s', encodeURIComponent(uri))) };
  const route = currentRoute();
  assert.equal(route.type, 'compose');
  assert.equal(route.mailto, uri);
  assert.equal(parseMailto(route.mailto).body, 'a&b');
  delete globalThis.window;
});
