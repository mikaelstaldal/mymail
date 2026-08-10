// Finding iCalendar links in an HTML mail body, so the message view can offer
// the same "Import to Calendar" action it offers for an .ics *attachment*.
//
// Lives apart from MessageDetail.tsx so it can be exercised directly by
// web/ts/icslinks.test.mjs under jsdom: the component itself is not reachable
// from the frontend tests, and every rule below is a decision about
// attacker-supplied markup.
//
// The body renders in a sandboxed iframe, so nothing here can put a button next
// to the link in place — these results are rendered as a list in the message
// detail area instead.
//
// **This is not an SSRF filter and must not be relied on as one.** What is
// found here is POSTed to MyCal, which fetches it; deciding that a host is safe
// to connect to needs DNS resolution and a dial-time re-check, neither of which
// a page can do. MyCal does both (`httputil.ValidateExternalURL` +
// `SafeDialContext` + `SafeCheckRedirect`) — see spec/ARCHITECTURE.md
// § Calendar Import Talks to MyCal from the Browser. A loopback or private
// address is therefore *deliberately* returned by this module and refused
// there, where the refusal can be correct.

/** One iCalendar link found in a message body. */
export interface IcsLink {
  /** The URL to hand to MyCal — `webcal:` rewritten to `https:`. */
  url: string;
  /**
   * The href as parsed, before that rewrite. This is the URL's WHATWG
   * serialisation and not the source text: the host is lower-cased,
   * surrounding whitespace is gone and percent-encoding is normalised.
   */
  original: string;
  /** The link's own text, or the file name from the URL when it has none. */
  label: string;
}

// Only these reach MyCal. Everything else an <a href> can carry — `javascript:`,
// `data:`, `tel:`, and `mailto:`, which the sanitiser does keep — is ignored
// rather than filtered later.
//
// `webcal:` is handled and **cannot currently occur in a stored body**: the
// inbound sanitiser's href pattern is `^(https?://|mailto:)`
// (`reHref` in internal/sanitize/sanitize.go), so a `webcal:` link arrives with
// its <a> unwrapped and its text kept. This branch is what a decision to allow
// that scheme would need, not dead weight it would have to grow — and it is the
// reason the tests cover it. Allowing `webcal` means touching an allowlist held
// by TestOutgoingIsSupersetOfInbound and TestSentHTMLSurvivesBeingReceived, so
// it is the sanitiser's owner's call rather than this module's.
const HTTP_SCHEMES = new Set(['http:', 'https:']);
const WEBCAL_SCHEME = 'webcal:';

// The words that name the format, used for both the path-segment and the
// query-value test below. Compared for *equality* against a whole segment or a
// whole value, never as a substring: `ical` inside `/medical/` or `/physical/`
// says nothing about the response.
const CALENDAR_WORDS = new Set(['ics', 'ical', 'icalendar']);

// Query parameters whose *value* is taken as naming the response format.
// `format=ical` and `type=ics` are the two that occur; `fmt` and `output` are
// the same idea spelled differently, and `calendar` is here because it costs
// nothing — it fires only on a value that is literally one of CALENDAR_WORDS,
// so `?calendar=Work` is unaffected.
const FORMAT_PARAMS = new Set(['format', 'type', 'fmt', 'output', 'calendar']);

/** Path segments, percent-decoded where possible, with empties dropped. */
function pathSegments(u: URL): string[] {
  return u.pathname.split('/').filter(s => s !== '').map(decodeSegment);
}

function decodeSegment(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

/**
 * Whether a parsed URL points at iCalendar data.
 *
 * **Recall matters more than precision here, and the two costs are not
 * symmetric.** A false positive is cheap and visible: MyCal fetches, fails to
 * parse, answers 400, and the error appears beside the button the user chose to
 * press. A false negative is invisible — no button, and nothing to say that
 * anything was missed, which is the bug this whole feature exists to fix. So
 * the rules below are generous, as long as each stays a statement about the URL.
 *
 * Three ways to qualify, all on path or query and never on the fragment:
 *
 * 1. **The path ends in `.ics`.** The static-file case, and the only one a
 *    naive rule catches.
 * 2. **A whole path segment is `ics` / `ical` / `icalendar`**, case-insensitive
 *    — `/api/Events/<uuid>/iCalendar`, `/events/1/ical`, `/download/ics`. Bulk
 *    mail platforms generate extensionless endpoints of this shape as a matter
 *    of course, so rule 1 alone misses most real invitations.
 * 3. **A format parameter whose value is one of those words** — `?format=ical`,
 *    `?type=ics`. The *value* names the format; a file name in a query
 *    (`?file=x.ics`) deliberately does not qualify, since it says nothing about
 *    what the response will be.
 *
 * Link *text* is never consulted. "Add to calendar" is unbounded, translated
 * differently in every mail, and would make this a guess about prose rather
 * than a statement about a URL.
 *
 * A `webcal:` URL qualifies on its scheme alone — that scheme exists for
 * nothing but calendar subscription.
 *
 * Embedded credentials disqualify a URL outright. `https://user:pass@host/x.ics`
 * is a well-formed link that would have MyCal fetch with a sender's credentials;
 * there is no legitimate calendar link of that shape, so it is not offered.
 */
function isIcsUrl(u: URL): boolean {
  if (u.username !== '' || u.password !== '') return false;
  if (u.protocol === WEBCAL_SCHEME) return u.host !== '';
  if (!HTTP_SCHEMES.has(u.protocol)) return false;

  if (u.pathname.toLowerCase().endsWith('.ics')) return true;
  if (pathSegments(u).some(s => CALENDAR_WORDS.has(s.toLowerCase()))) return true;
  // Parameter names are matched case-insensitively too: a query built by a
  // mail platform is as likely to spell it `Format` as `format`.
  for (const [name, value] of Array.from(u.searchParams)) {
    if (FORMAT_PARAMS.has(name.toLowerCase()) && CALENDAR_WORDS.has(value.trim().toLowerCase())) {
      return true;
    }
  }
  return false;
}

/**
 * The URL MyCal is asked to fetch, or null if it cannot be formed. `webcal:` is
 * a subscription scheme with no transport of its own — conventionally it means
 * HTTPS — and MyCal fetches over HTTP, so it is rewritten.
 *
 * Re-parsed rather than string-spliced: `webcal:` is a non-special scheme and
 * serialises by different rules, so `webcal://h:443/x` has to go back through
 * the parser to come out as `https://h/x` rather than keeping a default port
 * that would make one feed look like two.
 */
function resolveUrl(u: URL): string | null {
  if (u.protocol !== WEBCAL_SCHEME) return u.href;
  try {
    return new URL('https:' + u.href.slice(WEBCAL_SCHEME.length)).href;
  } catch {
    return null;
  }
}

/** The last path segment, used as a label when the link carries no text. */
function fileNameOf(u: URL): string {
  return pathSegments(u).pop() || u.host || u.href;
}

/**
 * Every iCalendar link in a sanitized HTML body, in document order, deduplicated
 * by resolved URL.
 *
 * Parsed as a document rather than matched with a regex: an href can be quoted
 * or not, entity-encoded, or split across lines, and the parser is the only
 * thing that agrees with what the recipient's browser saw.
 *
 * Relative hrefs are skipped rather than resolved. A mail body has no base URL —
 * resolving one against MyMail's own origin would produce a link to MyMail.
 *
 * The same link commonly appears twice (a styled button and the URL in text);
 * the first occurrence wins, except that a later occurrence's link text is taken
 * when the first had none.
 */
export function findIcsLinks(html: string): IcsLink[] {
  if (html === '') return [];
  const doc = new DOMParser().parseFromString(html, 'text/html');
  const found = new Map<string, { link: IcsLink; hasText: boolean }>();

  for (const a of Array.from(doc.querySelectorAll('a[href]'))) {
    const href = (a.getAttribute('href') ?? '').trim();
    if (href === '') continue;
    let u: URL;
    try {
      u = new URL(href);
    } catch {
      continue; // relative, or not a URL at all
    }
    if (!isIcsUrl(u)) continue;
    const url = resolveUrl(u);
    if (url === null) continue;

    const text = (a.textContent ?? '').replace(/\s+/g, ' ').trim();
    const seen = found.get(url);
    if (seen === undefined) {
      found.set(url, {
        link: { url, original: u.href, label: text !== '' ? text : fileNameOf(u) },
        hasText: text !== '',
      });
    } else if (text !== '' && !seen.hasText) {
      seen.link.label = text;
      seen.hasText = true;
    }
  }

  return Array.from(found.values(), entry => entry.link);
}
