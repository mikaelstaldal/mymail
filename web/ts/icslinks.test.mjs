// node:test coverage for web/ts/util/icslinks.ts (exercised via its compiled
// output, web/static/util/icslinks.js). Run via build.sh or directly:
//   web/ts/vendor/test/unpack.sh && node --test web/ts/icslinks.test.mjs
//
// findIcsLinks decides which links in an incoming HTML mail body get an
// "Import to Calendar" button — so every case here is a decision about markup
// that arrived from outside. The scheme allowlist in particular is the reason
// `javascript:` and `data:` hrefs never reach a fetch.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The module under test reads DOMParser off the global object, so the DOM has
// to be installed before it runs.
const { JSDOM } = await import(path.resolve(__dirname, 'vendor/test/jsdom.js'));
const { window } = new JSDOM('');
globalThis.window = window;
globalThis.document = window.document;
globalThis.DOMParser = window.DOMParser;
globalThis.Node = window.Node;

const { findIcsLinks } = await import(path.resolve(__dirname, '../static/util/icslinks.js'));

const urls = html => findIcsLinks(html).map(l => l.url);

// ---------------------------------------------------------------------------
// Nothing to find
// ---------------------------------------------------------------------------

test('an empty body yields no links', () => {
  assert.deepEqual(findIcsLinks(''), []);
});

test('a body with no links yields none', () => {
  assert.deepEqual(findIcsLinks('<p>See you at the party.</p>'), []);
});

test('a non-calendar http link is ignored', () => {
  assert.deepEqual(urls('<a href="https://example.com/agenda.pdf">agenda</a>'), []);
  assert.deepEqual(urls('<a href="https://example.com/">home</a>'), []);
});

// ---------------------------------------------------------------------------
// What counts as an iCalendar link
// ---------------------------------------------------------------------------

test('an .ics path is found over http and https', () => {
  assert.deepEqual(urls('<a href="https://example.com/e/meeting.ics">add</a>'),
    ['https://example.com/e/meeting.ics']);
  assert.deepEqual(urls('<a href="http://example.com/meeting.ics">add</a>'),
    ['http://example.com/meeting.ics']);
});

test('a query string does not hide the .ics suffix', () => {
  // The common shape of a one-time calendar link: the path ends in .ics and
  // everything identifying the invitation is in the query.
  assert.deepEqual(urls('<a href="https://example.com/e/meeting.ics?token=abc123">add</a>'),
    ['https://example.com/e/meeting.ics?token=abc123']);
});

test('a fragment does not hide the .ics suffix', () => {
  assert.deepEqual(urls('<a href="https://example.com/meeting.ics#top">add</a>'),
    ['https://example.com/meeting.ics#top']);
});

test('the .ics suffix is matched case-insensitively', () => {
  assert.deepEqual(urls('<a href="https://example.com/MEETING.ICS">add</a>'),
    ['https://example.com/MEETING.ICS']);
  assert.deepEqual(urls('<a href="https://example.com/Meeting.Ics">add</a>'),
    ['https://example.com/Meeting.Ics']);
});

test('.ics in the query or the host is not enough', () => {
  // A file name in a query says nothing about what the response will be.
  assert.deepEqual(urls('<a href="https://example.com/download?file=meeting.ics">add</a>'), []);
  assert.deepEqual(urls('<a href="https://meeting.ics.example.com/e">add</a>'), []);
  assert.deepEqual(urls('<a href="https://example.com/page#meeting.ics">add</a>'), []);
});

// ---------------------------------------------------------------------------
// Extensionless endpoints
//
// The shape bulk-mail platforms actually send. A rule that only knows about
// `.ics` catches the static-file case and misses most real invitations — and
// misses them invisibly, with no button and nothing to say one was owed.
// ---------------------------------------------------------------------------

test('the extensionless-endpoint shape a real invitation used qualifies', () => {
  // The shape of a link from a Swedish bulk-mail platform, rewritten as a
  // synthetic URL: extensionless /iCalendar final segment, opaque ids in the
  // path, tracking parameters in the query. The host and ids in the real one
  // named an organisation and a person, which is not something a public
  // repository should carry — the shape is the whole of what this pins.
  const url = 'https://example.com/api/Events/'
    + '00000000-0000-0000-0000-000000000000/iCalendar'
    + '?AccountId=11111111-1111-1111-1111-111111111111'
    + '&ContactId=22222222-2222-2222-2222-222222222222'
    + '&IssueId=33333333-3333-3333-3333-333333333333'
    + '&ir=44444444-4444-4444-4444-444444444444';
  assert.deepEqual(urls(`<a href="${url}">Lägg till i kalender</a>`), [url]);
});

test('the whole query survives detection, tracking parameters included', () => {
  // Those parameters are not noise to strip: without them the endpoint does not
  // answer. What is offered, shown and POSTed has to be the URL as written.
  const url = 'https://example.org/e/ical?AccountId=1&ContactId=2&ir=3';
  const [link] = findIcsLinks(`<a href="${url}">add</a>`);
  assert.equal(link.url, url);
  assert.equal(link.original, url);
});

test('a whole path segment naming the format qualifies', () => {
  assert.deepEqual(urls('<a href="https://example.com/api/Events/7/iCalendar">add</a>'),
    ['https://example.com/api/Events/7/iCalendar']);
  assert.deepEqual(urls('<a href="https://example.com/events/7/ical">add</a>'),
    ['https://example.com/events/7/ical']);
  assert.deepEqual(urls('<a href="https://example.com/download/ics">add</a>'),
    ['https://example.com/download/ics']);
  assert.deepEqual(urls('<a href="https://example.com/ICS/7">add</a>'),
    ['https://example.com/ICS/7']);
});

test('a trailing slash does not hide the last segment', () => {
  assert.deepEqual(urls('<a href="https://example.com/events/7/iCalendar/">add</a>'),
    ['https://example.com/events/7/iCalendar/']);
});

test('the segment test is equality, not substring', () => {
  // The reason it is worth stating: each of these contains one of the words.
  assert.deepEqual(urls('<a href="https://example.com/medical/records">x</a>'), []);
  assert.deepEqual(urls('<a href="https://example.com/basics">x</a>'), []);
  assert.deepEqual(urls('<a href="https://example.com/physical-therapy/1">x</a>'), []);
  assert.deepEqual(urls('<a href="https://example.com/icalendars/list">x</a>'), []);
  assert.deepEqual(urls('<a href="https://example.com/my-ics">x</a>'), []);
  assert.deepEqual(urls('<a href="https://ical.example.com/news">x</a>'), []);
});

test('a format parameter whose value names the format qualifies', () => {
  assert.deepEqual(urls('<a href="https://example.com/e/7?format=ical">add</a>'),
    ['https://example.com/e/7?format=ical']);
  assert.deepEqual(urls('<a href="https://example.com/e/7?type=ics&id=3">add</a>'),
    ['https://example.com/e/7?type=ics&id=3']);
  assert.deepEqual(urls('<a href="https://example.com/e/7?Format=iCalendar">add</a>'),
    ['https://example.com/e/7?Format=iCalendar']);
  assert.deepEqual(urls('<a href="https://example.com/e/7?output=ICS">add</a>'),
    ['https://example.com/e/7?output=ICS']);
});

test('a format parameter naming something else does not', () => {
  assert.deepEqual(urls('<a href="https://example.com/e/7?format=pdf">x</a>'), []);
  assert.deepEqual(urls('<a href="https://example.com/e/7?format=icsx">x</a>'), []);
  assert.deepEqual(urls('<a href="https://example.com/e/7?format=my-ics">x</a>'), []);
  // Not a format parameter at all.
  assert.deepEqual(urls('<a href="https://example.com/e/7?category=ics">x</a>'), []);
  assert.deepEqual(urls('<a href="https://example.com/e/7?calendar=Work">x</a>'), []);
});

test('link text is never what qualifies a link', () => {
  // Unbounded and translated differently in every message.
  assert.deepEqual(urls('<a href="https://example.com/e/7">Add to calendar</a>'), []);
  assert.deepEqual(urls('<a href="https://example.com/e/7">Lägg till i kalender</a>'), []);
});

// ---------------------------------------------------------------------------
// webcal:
//
// None of these can reach the UI today: the inbound sanitiser's href pattern is
// `^(https?://|mailto:)`, so a webcal link is stored with its <a> unwrapped.
// They pin the behaviour the moment that pattern changes — see the note in
// util/icslinks.ts.
// ---------------------------------------------------------------------------

test('a webcal: link is rewritten to https, since MyCal fetches over HTTP', () => {
  const [link] = findIcsLinks('<a href="webcal://example.com/feed/meeting.ics">subscribe</a>');
  assert.equal(link.url, 'https://example.com/feed/meeting.ics');
  assert.equal(link.original, 'webcal://example.com/feed/meeting.ics');
});

test('a webcal: link qualifies on its scheme, with no .ics suffix', () => {
  assert.deepEqual(urls('<a href="webcal://example.com/calendars/team">subscribe</a>'),
    ['https://example.com/calendars/team']);
});

test('the webcal rewrite keeps the port, query and fragment', () => {
  assert.deepEqual(urls('<a href="webcal://example.com:8443/f.ics?k=1#x">sub</a>'),
    ['https://example.com:8443/f.ics?k=1#x']);
});

test('the webcal rewrite re-parses, so https default ports collapse', () => {
  // webcal: is a non-special scheme and serialises by different rules, so
  // splicing the string would leave :443 on and make one feed look like two.
  assert.deepEqual(urls('<a href="webcal://example.com:443/f.ics">sub</a>'),
    ['https://example.com/f.ics']);
  assert.deepEqual(
    urls('<a href="webcal://example.com:443/f.ics">a</a><a href="https://example.com/f.ics">b</a>'),
    ['https://example.com/f.ics']);
});

test('a webcal: URL with no host is ignored', () => {
  // Nothing sensible to rewrite it to.
  assert.deepEqual(urls('<a href="webcal:meeting.ics">sub</a>'), []);
});

// ---------------------------------------------------------------------------
// Schemes that must never reach a fetch
// ---------------------------------------------------------------------------

test('non-http schemes are rejected even when they end in .ics', () => {
  assert.deepEqual(urls('<a href="javascript:alert(1)//meeting.ics">add</a>'), []);
  assert.deepEqual(urls('<a href="mailto:someone@example.com">mail</a>'), []);
  assert.deepEqual(urls('<a href="mailto:invite@example.com?subject=meeting.ics">mail</a>'), []);
  assert.deepEqual(urls('<a href="data:text/calendar,BEGIN:VCALENDAR">add</a>'), []);
  assert.deepEqual(urls('<a href="file:///tmp/meeting.ics">add</a>'), []);
  assert.deepEqual(urls('<a href="ftp://example.com/meeting.ics">add</a>'), []);
});

test('a URL carrying credentials is rejected', () => {
  // Well-formed, and would have MyCal fetch with a sender-supplied password.
  assert.deepEqual(urls('<a href="https://user:pass@example.com/m.ics">add</a>'), []);
  assert.deepEqual(urls('<a href="https://user@example.com/m.ics">add</a>'), []);
  assert.deepEqual(urls('<a href="webcal://user:pass@example.com/m.ics">sub</a>'), []);
});

test('a private or loopback host is returned, deliberately', () => {
  // Not this module's call: deciding a host is safe to connect to needs DNS
  // resolution and a dial-time re-check, which a page cannot do. MyCal refuses
  // these itself (httputil.ValidateExternalURL + SafeDialContext), where the
  // refusal can be correct. Pinned so a "fix" here is a deliberate one.
  assert.deepEqual(urls('<a href="http://127.0.0.1:9200/m.ics">add</a>'),
    ['http://127.0.0.1:9200/m.ics']);
  assert.deepEqual(urls('<a href="http://169.254.169.254/latest/meta-data/m.ics">add</a>'),
    ['http://169.254.169.254/latest/meta-data/m.ics']);
  assert.deepEqual(urls('<a href="http://localhost/m.ics">add</a>'), ['http://localhost/m.ics']);
});

test('a relative href is skipped rather than resolved', () => {
  // A mail body has no base URL; resolving against MyMail's own origin would
  // produce a link into MyMail.
  assert.deepEqual(urls('<a href="/meeting.ics">add</a>'), []);
  assert.deepEqual(urls('<a href="meeting.ics">add</a>'), []);
  assert.deepEqual(urls('<a href="//example.com/meeting.ics">add</a>'), []);
});

test('an empty or whitespace href is skipped', () => {
  assert.deepEqual(urls('<a href="">add</a>'), []);
  assert.deepEqual(urls('<a href="   ">add</a>'), []);
});

test('surrounding whitespace in an href is tolerated', () => {
  assert.deepEqual(urls('<a href="\n  https://example.com/m.ics  ">add</a>'),
    ['https://example.com/m.ics']);
});

// ---------------------------------------------------------------------------
// Dedupe and ordering
// ---------------------------------------------------------------------------

test('the same URL appearing twice yields one entry', () => {
  const html = '<a href="https://example.com/m.ics">Add to calendar</a>'
    + '<p>or paste <a href="https://example.com/m.ics">https://example.com/m.ics</a></p>';
  assert.deepEqual(urls(html), ['https://example.com/m.ics']);
});

test('an http and a webcal form of the same URL collapse to one', () => {
  const html = '<a href="webcal://example.com/m.ics">subscribe</a>'
    + '<a href="https://example.com/m.ics">download</a>';
  assert.deepEqual(urls(html), ['https://example.com/m.ics']);
});

test('distinct URLs are all returned, in document order', () => {
  const html = '<a href="https://example.com/b.ics">b</a>'
    + '<a href="https://example.com/a.ics">a</a>';
  assert.deepEqual(urls(html), ['https://example.com/b.ics', 'https://example.com/a.ics']);
});

// ---------------------------------------------------------------------------
// Labels
// ---------------------------------------------------------------------------

test('the label is the link text, whitespace collapsed', () => {
  const [link] = findIcsLinks('<a href="https://example.com/m.ics">  Add to\n  calendar </a>');
  assert.equal(link.label, 'Add to calendar');
});

test('a link with no text falls back to the file name', () => {
  const [link] = findIcsLinks('<a href="https://example.com/e/team%20lunch.ics"><img src="x"></a>');
  assert.equal(link.label, 'team lunch.ics');
});

test('a text-carrying duplicate names an image-only first occurrence', () => {
  // The button-then-text-link shape: the button has no text of its own.
  const html = '<a href="https://example.com/m.ics"><img src="btn.png"></a>'
    + '<a href="https://example.com/m.ics">Add to calendar</a>';
  const links = findIcsLinks(html);
  assert.equal(links.length, 1);
  assert.equal(links[0].label, 'Add to calendar');
});

test('the first link text wins over a later one', () => {
  const html = '<a href="https://example.com/m.ics">Add to calendar</a>'
    + '<a href="https://example.com/m.ics">https://example.com/m.ics</a>';
  assert.equal(findIcsLinks(html)[0].label, 'Add to calendar');
});

test('a webcal link with no text and no path falls back to the host', () => {
  const [link] = findIcsLinks('<a href="webcal://example.com/"><img src="x"></a>');
  assert.equal(link.label, 'example.com');
});

// ---------------------------------------------------------------------------
// Markup shapes a real body arrives in
// ---------------------------------------------------------------------------

test('links are found inside tables and nested markup', () => {
  const html = '<table><tr><td><div><b>'
    + '<a href="https://example.com/m.ics">Add</a></b></div></td></tr></table>';
  assert.deepEqual(urls(html), ['https://example.com/m.ics']);
});

test('an entity-encoded href is decoded by the parser', () => {
  assert.deepEqual(urls('<a href="https://example.com/m.ics?a=1&amp;b=2">add</a>'),
    ['https://example.com/m.ics?a=1&b=2']);
});

test('an <a> without an href is not a link', () => {
  assert.deepEqual(urls('<a name="meeting.ics">anchor</a>'), []);
});

test('malformed markup is still parsed rather than refused', () => {
  // The body has been through the sanitiser, so it should be well-formed — but
  // it is the parser that decides that, not the assumption.
  assert.deepEqual(urls('<p><a href="https://example.com/m.ics">add<p>more'),
    ['https://example.com/m.ics']);
  assert.deepEqual(urls('<div><a href="https://example.com/m.ics">add</div>'),
    ['https://example.com/m.ics']);
  assert.deepEqual(urls('<a href=https://example.com/m.ics>add</a>'),
    ['https://example.com/m.ics']);
});
