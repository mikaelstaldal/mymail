// Text handling ported from the Go server: slugs, subject normalisation,
// address lists, header sanitisation, search tokenisation and snippets, and
// RFC 5322 assembly.
//
// Every function here names the Go original it mirrors. When one of those
// changes on the server, change it here too — parity with the Go server is the
// contract (see AGENTS.md § Demo mode). This is also the only file in the demo
// backend that is pure enough to unit-test without a browser, which is why the
// logic worth testing lives here rather than inline in api.ts.
//
// See model.ts for why these are globals rather than module exports.

// ── Slugs ────────────────────────────────────────────────────────────────────

/**
 * A display name turned into a URL-safe slug. Mirrors repository.toSlug:
 * NFKD-decompose (so an accented letter contributes its ASCII base), lower-case,
 * collapse every non-alphanumeric run to a single hyphen, trim, and fall back to
 * "folder" when nothing survives.
 */
function toSlug(name: string): string {
  const decomposed = name.normalize('NFKD').toLowerCase();
  const slug = decomposed.replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  return slug === '' ? 'folder' : slug;
}

/** base if free, else base-2, base-3, … Mirrors FolderRepository.uniqueSlug. */
function uniqueSlug(taken: Set<string>, base: string): string {
  if (!taken.has(base)) return base;
  for (let n = 2; ; n++) {
    const candidate = `${base}-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
}

// ── Subjects ─────────────────────────────────────────────────────────────────

/** Mirrors repository.subjectPrefixRe. */
const SUBJECT_PREFIX_RE = /^[ \t]*(re|fwd|fw|aw|wg|res|enc|vs|sv):[ \t]*/i;

/**
 * A subject with every leading reply/forward prefix removed, used as the
 * thread's subject-based fallback key. Mirrors repository.normalizeSubject.
 */
function normalizeSubject(subject: string): string {
  let s = subject;
  for (;;) {
    const next = s.replace(SUBJECT_PREFIX_RE, '');
    if (next === s) break;
    s = next;
  }
  return s.trim();
}

// ── Header values ────────────────────────────────────────────────────────────

/** Mirrors service.StripHeaderControls: drop CR, LF and NUL. */
function stripHeaderControls(s: string): string {
  return s.replace(/[\r\n\0]/g, '');
}

/** Mirrors handler.stripAngleBrackets: one surrounding pair, if present. */
function stripAngleBrackets(s: string): string {
  if (s.length >= 2 && s.startsWith('<') && s.endsWith('>')) return s.slice(1, -1);
  return s;
}

/**
 * The `references` column value for a References list: control characters and
 * angle brackets stripped from each entry, joined with newlines, and truncated
 * to MAX_REFS_BYTES by dropping the *oldest* entries — the recent ancestry is
 * what threading needs. Mirrors handler.normalizeReferences.
 */
function normalizeReferences(refs: string[]): string {
  let cleaned = refs
    .map((r) => stripAngleBrackets(stripHeaderControls(r)))
    .filter((r) => r !== '');
  let joined = cleaned.join('\n');
  if (byteLength(joined) <= MAX_REFS_BYTES) return joined;
  while (cleaned.length > 0) {
    cleaned = cleaned.slice(1);
    joined = cleaned.join('\n');
    if (byteLength(joined) <= MAX_REFS_BYTES) break;
  }
  return joined;
}

// ── Addresses ────────────────────────────────────────────────────────────────

interface ParsedAddress {
  name: string;
  address: string;
}

/**
 * An addr-spec: exactly one @, neither half empty, no whitespace, no angle
 * brackets. Deliberately narrower than RFC 5322 (which allows quoted local
 * parts and comments) and narrower than Go's net/mail — the demo has to reject
 * what the server rejects, and a demo that accepted *more* would let the user
 * save something the real server would refuse.
 */
const ADDR_SPEC_RE = /^[^\s<>,@"]+@[^\s<>,@"]+$/;

/**
 * Parses a comma-separated address list the way service.ParseAddressList does,
 * throwing when any element is malformed. Commas inside a quoted display name
 * or inside angle brackets do not separate.
 */
function parseAddressList(s: string): ParsedAddress[] {
  const parts = splitAddressList(s);
  const out: ParsedAddress[] = [];
  for (const part of parts) {
    const trimmed = part.trim();
    if (trimmed === '') throw new Error('empty address');
    out.push(parseAddress(trimmed));
  }
  if (out.length === 0) throw new Error('no addresses');
  return out;
}

/** Splits on top-level commas only. */
function splitAddressList(s: string): string[] {
  const parts: string[] = [];
  let current = '';
  let inQuotes = false;
  let inAngles = false;
  for (const ch of s) {
    if (ch === '"') inQuotes = !inQuotes;
    else if (!inQuotes && ch === '<') inAngles = true;
    else if (!inQuotes && ch === '>') inAngles = false;
    if (ch === ',' && !inQuotes && !inAngles) {
      parts.push(current);
      current = '';
      continue;
    }
    current += ch;
  }
  parts.push(current);
  return parts;
}

/** Mirrors service.ParseAddress for the forms the compose UI can produce. */
function parseAddress(s: string): ParsedAddress {
  const angle = s.lastIndexOf('<');
  if (angle >= 0) {
    if (!s.endsWith('>')) throw new Error(`malformed address: ${s}`);
    const address = s.slice(angle + 1, -1).trim();
    if (!ADDR_SPEC_RE.test(address)) throw new Error(`malformed address: ${s}`);
    let name = s.slice(0, angle).trim();
    if (name.startsWith('"') && name.endsWith('"') && name.length >= 2) {
      name = name.slice(1, -1);
    }
    return { name, address };
  }
  if (!ADDR_SPEC_RE.test(s)) throw new Error(`malformed address: ${s}`);
  return { name: '', address: s };
}

/**
 * Unicode simple case folding, as repository.parseAndFoldAddress applies to
 * every stored identity and contact address. toLowerCase is the same mapping
 * for everything an address can contain.
 */
function foldAddress(address: string): string {
  return address.toLowerCase();
}

/**
 * Validates a bare addr-spec — no display name allowed — and returns it folded.
 * Mirrors repository.parseAndFoldAddress; throws on anything else.
 */
function parseAndFoldAddress(s: string): string {
  const parsed = parseAddress(s.trim());
  if (parsed.name !== '') throw new Error('address must not have a display name');
  return foldAddress(parsed.address);
}

/** The display name and address of the first entry, or nulls. Mirrors lda.parseFromAddr. */
function firstAddress(addrList: string): ParsedAddress | null {
  try {
    const addrs = parseAddressList(addrList);
    return addrs.length > 0 ? addrs[0] : null;
  } catch {
    return null;
  }
}

// ── HTML ─────────────────────────────────────────────────────────────────────

/** Mirrors Go's html.EscapeString, which is what the search snippet goes through. */
function htmlEscape(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&#34;')
    .replace(/'/g, '&#39;');
}

/**
 * Whether the HTML references an image over http(s). Mirrors
 * sanitize.HasExternalImages, which tokenises the document and checks every
 * <img src>; a worker has no HTML parser, so this scans for the same attribute
 * with a regex. It over-matches on an `src` inside a comment or a text node,
 * which only ever turns the "load external images" prompt on when it need not
 * have been — the safe direction.
 */
const EXTERNAL_IMG_RE = /<img\b[^>]*\bsrc\s*=\s*("|')?\s*https?:\/\//i;

function hasExternalImages(html: string): boolean {
  return EXTERNAL_IMG_RE.test(html);
}

// ── Search ───────────────────────────────────────────────────────────────────

interface TextToken {
  start: number;
  end: number;
  lower: string;
}

/**
 * Splits text into alphanumeric word tokens, approximating the FTS5 unicode61
 * tokenizer (split on non-alphanumeric, fold to lower case). Mirrors
 * repository.tokenizeText; offsets are into the same string the caller slices,
 * so JS's UTF-16 indexes stand in for Go's byte offsets throughout.
 */
function tokenizeText(text: string): TextToken[] {
  const tokens: TextToken[] = [];
  let start = -1;
  let i = 0;
  for (const ch of text) {
    if (/[\p{L}\p{N}]/u.test(ch)) {
      if (start < 0) start = i;
    } else if (start >= 0) {
      tokens.push({ start, end: i, lower: text.slice(start, i).toLowerCase() });
      start = -1;
    }
    i += ch.length;
  }
  if (start >= 0) {
    tokens.push({ start, end: text.length, lower: text.slice(start).toLowerCase() });
  }
  return tokens;
}

/**
 * A short highlighted excerpt of body around the first occurrence of a query
 * term, matched terms wrapped in ** and … at truncated boundaries. Mirrors
 * repository.buildSnippet, including its behaviour when nothing matches (a
 * window from the start).
 */
function buildSnippet(body: string, query: string): string {
  const bodyTokens = tokenizeText(body);
  if (bodyTokens.length === 0) return body.trim();

  const querySet = new Set(tokenizeText(query).map((t) => t.lower));

  let match = -1;
  if (querySet.size > 0) {
    match = bodyTokens.findIndex((t) => querySet.has(t.lower));
  }

  let lo = 0;
  let hi = SNIPPET_CONTEXT_TOKENS;
  if (match >= 0) {
    lo = Math.max(match - Math.floor(SNIPPET_CONTEXT_TOKENS / 2), 0);
    hi = lo + SNIPPET_CONTEXT_TOKENS;
  }
  if (hi > bodyTokens.length) hi = bodyTokens.length;

  let out = lo > 0 ? '…' : '';
  for (let i = lo; i < hi; i++) {
    const t = bodyTokens[i];
    if (i > lo) out += body.slice(bodyTokens[i - 1].end, t.start);
    const word = body.slice(t.start, t.end);
    out += querySet.has(t.lower) ? `**${word}**` : word;
  }
  if (hi < bodyTokens.length) out += '…';
  return out;
}

/**
 * Whether haystack contains the query tokens as a contiguous phrase.
 *
 * The server passes the search string to FTS5 as one quoted phrase
 * (repository.sanitizeFTSQuery), so `AND`, `NOT` and `"` are all literals and a
 * multi-word query only matches consecutive words. That is what this
 * reproduces, over the same five indexed columns.
 */
function phraseMatches(haystackTokens: TextToken[], queryTokens: string[]): boolean {
  if (queryTokens.length === 0) return false;
  for (let i = 0; i + queryTokens.length <= haystackTokens.length; i++) {
    let all = true;
    for (let j = 0; j < queryTokens.length; j++) {
      if (haystackTokens[i + j].lower !== queryTokens[j]) {
        all = false;
        break;
      }
    }
    if (all) return true;
  }
  return false;
}

// ── RFC 5322 assembly ────────────────────────────────────────────────────────

interface RawMessageFields {
  fromName: string;
  fromAddr: string;
  toAddr: string;
  ccAddr: string;
  bccAddr: string;
  replyToAddr: string;
  subject: string;
  bodyText: string;
  bodyHtml: string;
  /** Without angle brackets, as stored. */
  messageId: string;
  inReplyTo: string;
  references: string[];
  date: Date;
}

interface RawAttachment {
  filename: string;
  contentType: string;
  data: Uint8Array;
}

/**
 * Assembles the RFC 5322 source stored in `raw` for a sent message, mirroring
 * service.BuildMIMEMessage's structure: a text/plain body, a
 * multipart/alternative when there is HTML too, wrapped in a multipart/mixed
 * when there are attachments.
 *
 * Two things the Go version does are left out, because nothing in the demo
 * transmits this message — it is only ever shown back in the headers view and
 * offered as an .eml download. Text parts are written as UTF-8 with
 * Content-Transfer-Encoding 8bit rather than quoted-printable, and display
 * names and subjects are written literally rather than as RFC 2047 encoded
 * words. Both are listed in spec/REQUIREMENTS.md § Demo Mode.
 */
function buildRawMessage(fields: RawMessageFields, attachments: RawAttachment[]): string {
  const headers: string[] = [];
  const header = (name: string, value: string) => headers.push(`${name}: ${value}\r\n`);

  header('Date', rfc1123Z(fields.date));
  header('Message-ID', `<${fields.messageId}>`);
  header('From', fields.fromName !== '' ? `${fields.fromName} <${fields.fromAddr}>` : fields.fromAddr);
  if (fields.toAddr !== '') header('To', fields.toAddr);
  if (fields.ccAddr !== '') header('Cc', fields.ccAddr);
  if (fields.bccAddr !== '') header('Bcc', fields.bccAddr);
  if (fields.replyToAddr !== '') header('Reply-To', fields.replyToAddr);
  header('Subject', fields.subject);
  if (fields.inReplyTo !== '') header('In-Reply-To', `<${fields.inReplyTo}>`);
  if (fields.references.length > 0) {
    header('References', fields.references.map((r) => `<${r}>`).join(' '));
  }
  header('MIME-Version', '1.0');

  const body = buildRawBody(fields, attachments);
  header('Content-Type', body.contentType);
  if (body.encoding !== '') header('Content-Transfer-Encoding', body.encoding);

  return headers.join('') + '\r\n' + body.text;
}

interface RawBody {
  contentType: string;
  encoding: string;
  text: string;
}

function buildRawBody(fields: RawMessageFields, attachments: RawAttachment[]): RawBody {
  const hasText = fields.bodyText !== '';
  const hasHtml = fields.bodyHtml !== '';

  let inner: RawBody;
  if (hasText && hasHtml) {
    const boundary = mimeBoundary('alt');
    const parts = [
      `--${boundary}\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n${crlf(fields.bodyText)}\r\n`,
      `--${boundary}\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n${crlf(fields.bodyHtml)}\r\n`,
      `--${boundary}--\r\n`,
    ];
    inner = {
      contentType: `multipart/alternative; boundary="${boundary}"`,
      encoding: '',
      text: parts.join(''),
    };
  } else if (hasHtml) {
    inner = {
      contentType: 'text/html; charset=utf-8',
      encoding: '8bit',
      text: crlf(fields.bodyHtml),
    };
  } else {
    inner = {
      contentType: 'text/plain; charset=utf-8',
      encoding: '8bit',
      text: crlf(fields.bodyText),
    };
  }

  if (attachments.length === 0) return inner;

  const boundary = mimeBoundary('mix');
  let text = `--${boundary}\r\nContent-Type: ${inner.contentType}\r\n`;
  if (inner.encoding !== '') text += `Content-Transfer-Encoding: ${inner.encoding}\r\n`;
  text += `\r\n${inner.text}\r\n`;
  for (const att of attachments) {
    text += `--${boundary}\r\nContent-Type: ${att.contentType}\r\n`;
    text += `Content-Disposition: attachment; filename="${att.filename.replace(/"/g, '')}"\r\n`;
    text += 'Content-Transfer-Encoding: base64\r\n\r\n';
    text += base64Wrapped(att.data);
  }
  text += `--${boundary}--\r\n`;
  return { contentType: `multipart/mixed; boundary="${boundary}"`, encoding: '', text };
}

/** Line-ending normalisation: a stored body uses \n, an RFC 5322 body uses \r\n. */
function crlf(s: string): string {
  return s.replace(/\r\n/g, '\n').replace(/\n/g, '\r\n');
}

/** Mirrors service.writeBase64Wrapped: lines of at most 76 characters. */
function base64Wrapped(data: Uint8Array): string {
  let binary = '';
  for (const byte of data) binary += String.fromCharCode(byte);
  let encoded = btoa(binary);
  let out = '';
  while (encoded.length > 76) {
    out += encoded.slice(0, 76) + '\r\n';
    encoded = encoded.slice(76);
  }
  if (encoded.length > 0) out += encoded + '\r\n';
  return out;
}

/**
 * A MIME boundary. Counter-derived rather than random so the demo backend has
 * no non-deterministic behaviour that a test would have to work around; a
 * boundary only has to be absent from the parts it separates, and this one is.
 */
let mimeBoundaryCounter = 0;

function mimeBoundary(kind: string): string {
  mimeBoundaryCounter++;
  return `----=_mymail_demo_${kind}_${mimeBoundaryCounter}`;
}

const RFC1123Z_DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const RFC1123Z_MONTHS = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
];

/** The Date header format, as Go's time.RFC1123Z produces it (always UTC here). */
function rfc1123Z(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return (
    `${RFC1123Z_DAYS[date.getUTCDay()]}, ${pad(date.getUTCDate())} ` +
    `${RFC1123Z_MONTHS[date.getUTCMonth()]} ${date.getUTCFullYear()} ` +
    `${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}:${pad(date.getUTCSeconds())} +0000`
  );
}

/**
 * A v4-shaped identifier for generated Message-IDs. crypto.randomUUID is not
 * available in every context a static bundle might be opened from (it needs a
 * secure context, and so does the service worker, but the worker is what calls
 * this and the two are not guaranteed to agree), so fall back to getRandomValues.
 */
function randomUUID(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

/**
 * A new Message-ID value without angle brackets, with the domain taken from the
 * sender. Mirrors the derivation in service.BuildMIMEMessage.
 */
function newMessageId(fromAddr: string): string {
  let domain = 'localhost';
  const parsed = firstAddress(fromAddr);
  if (parsed !== null) {
    const at = parsed.address.lastIndexOf('@');
    if (at >= 0) domain = parsed.address.slice(at + 1);
  }
  return `${randomUUID()}@${domain}`;
}
