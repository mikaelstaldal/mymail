// Mirrors internal/phishing. PSL and Unicode confusables are pinned to the Go
// versions; document parsing is delegated to the controlled page's inert DOM.
interface DemoWarningLink { Index: number; Text: string; Href: string }
interface DemoWarning { kind: string; [key: string]: unknown }
interface DemoAuthentication { dmarc: string; dkim_aligned: boolean; spf: string }
const unknownDemoAuth = (): DemoAuthentication => ({ dmarc: 'unknown', dkim_aligned: false, spf: 'unknown' });
function warningDomain(s: string): string {
  s = s.toLowerCase().replace(/\.$/, '');
  try { return new URL('https://' + s).hostname.replace(/^\[|\]$/g, ''); } catch { return s; }
}
// Mirrors phishing.OversizedHost, including the pre-IDNA UTF-8 work cap.
function warningOversizedHost(raw: string): boolean {
  if (raw.length > 1016 || new TextEncoder().encode(raw).length > 1016) return true;
  const host = warningDomain(raw);
  return !warningIP(host) && (host.length > 253 || host.split('.').some(label => label.length > 63));
}
// RFC 3492 decoder. URL supplies the validated ASCII IDNA form first.
function decodeWarningDomain(host: string): string {
  return host.split('.').map(label => {
    if (!label.startsWith('xn--')) return label;
    const input = label.slice(4); let n = 128, i = 0, bias = 72;
    const split = input.lastIndexOf('-');
    const out = split < 0 ? [] : Array.from(input.slice(0, split), c => c.codePointAt(0)!);
    let pos = split < 0 ? 0 : split + 1;
    while (pos < input.length) {
      const old = i; let w = 1;
      for (let k = 36; ; k += 36) {
        const c = input.charCodeAt(pos++);
        const digit = c >= 97 && c <= 122 ? c - 97 : c >= 48 && c <= 57 ? c - 22 : 36;
        if (digit >= 36 || !Number.isFinite(w)) return label;
        i += digit * w;
        const t = k <= bias ? 1 : k >= bias + 26 ? 26 : k - bias;
        if (digit < t) break;
        w *= 36 - t;
      }
      let delta = old === 0 ? Math.floor((i - old) / 700) : Math.floor((i - old) / 2);
      delta += Math.floor(delta / (out.length + 1));
      let k = 0;
      while (delta > 455) { delta = Math.floor(delta / 35); k += 36; }
      bias = k + Math.floor(36 * delta / (delta + 38));
      n += Math.floor(i / (out.length + 1)); i %= out.length + 1;
      if (n > 0x10ffff) return label;
      out.splice(i++, 0, n);
    }
    return String.fromCodePoint(...out);
  }).join('.');
}
function warningSkeleton(host: string): string {
  return Array.from(decodeWarningDomain(warningDomain(host)).normalize('NFD'), c => phishingConfusables[c] ?? c).join('').normalize('NFD');
}
const suffixRules = new Set(phishingSuffixRules.map(rule => {
  const prefix = rule.startsWith('!') ? '!' : rule.startsWith('*.') ? '*.' : '';
  return prefix + warningDomain(rule.slice(prefix.length));
}));
function warningRegistrable(host: string): string {
  if (warningOversizedHost(host)) return host.toLowerCase();
  host = warningDomain(host); if (warningIP(host)) return host;
  const labels = host.split('.'); let suffix = 1;
  for (let i = 0; i < labels.length; i++) {
    const rest = labels.slice(i).join('.');
    if (suffixRules.has('!' + rest)) { suffix = labels.length - i - 1; break; }
    if (suffixRules.has(rest)) suffix = Math.max(suffix, labels.length - i);
    if (i > 0 && suffixRules.has('*.' + rest)) suffix = Math.max(suffix, labels.length - i + 1);
  }
  return labels.slice(Math.max(0, labels.length - suffix - 1)).join('.');
}
// Mirrors phishing.mapsToASCII. The leading x keeps a character that maps to
// nothing (soft hyphen) from leaving URL an empty, invalid host.
function warningMapsToASCII(host: string): boolean {
  return Array.from(host).some(c => {
    if (c.codePointAt(0)! < 0x80) return false;
    const m = warningDomain('x' + c);
    return m !== 'x' + c && !/(^|\.)xn--/.test(m) && /^[\x00-\x7f]*$/.test(m);
  });
}
function warningIP(host: string): boolean { return host.includes(':') || /^\d+\.\d+\.\d+\.\d+$/.test(host); }
function warningHost(href: string): string {
  // Bound raw authority analysis before URL performs IDNA conversion. Mirrors
  // Go url.Parse/Hostname, which leaves Unicode hosts undecoded.
  const authority = /^\s*(?:https?:)?\/\/([^/?#]*)/i.exec(href)?.[1];
  if (authority !== undefined) {
    const raw = authority.slice(authority.lastIndexOf('@') + 1).replace(/:\d*$/, '');
    if (!raw.startsWith('[') && !/\s/.test(raw) && warningOversizedHost(raw)) return raw.toLowerCase();
  }
  try {
    const url = new URL(href.startsWith('//') ? 'https:' + href : href);
    return ['http:', 'https:'].includes(url.protocol) ? (warningOversizedHost(url.hostname) ? url.hostname : warningDomain(url.hostname)) : '';
  } catch { return ''; }
}
const warningTLDs = new Set(phishingICANNTLDs);
function warningTextHost(text: string): string {
  if (/\s/.test(text)) return '';
  const explicit = text.includes('://');
  const bounded = warningHost(explicit ? text : 'https://' + text);
  if (warningOversizedHost(bounded)) {
    if (text.includes('@')) return '';
    return explicit || warningTLDs.has(bounded.split('.').pop()!) ? bounded : '';
  }
  try {
    const url = new URL(explicit ? text : 'https://' + text);
    if (url.username || url.password) return '';
    const host = warningHost(url.href);
    if (!host) return '';
    if (!explicit && !warningTLDs.has(host.split('.').pop()!) && !/^(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?(?:\/|$)/.test(text) && !text.startsWith('[')) return '';
    return host.includes('.') || warningIP(host) ? host : '';
  } catch { return ''; }
}
// Script test per label, ignoring Common/Inherited. Unicode property escapes
// are supported by the same ES2020 browsers that run the service worker.
const warningScriptPatterns = phishingScripts.flatMap(script => {
  try { return [new RegExp('\\p{Script=' + script + '}', 'u')]; } catch { return []; }
});
function warningMixed(host: string): boolean {
  return decodeWarningDomain(host).split('.').some(label => warningScriptPatterns.filter(p => p.test(label)).length > 1);
}
const warningExtensions = new Set('exe com bat cmd scr pif msi msp dll cpl ocx sys drv ps1 ps2 psm1 psd1 vbs vbe js jse wsf wsh hta sh bash zsh ksh fish py pyw rb pl php jar apk app dmg pkg deb rpm run desktop lnk reg vb vba iso img chm xll scpt scptd command url website gadget inf html htm xhtml shtml svg docm dotm xlsm xltm xlam pptm potm ppam ppsm sldm'.split(' '));
/** Mirrors mime.WordDecoder.DecodeHeader for legacy attachment names. */
function decodeAttachmentWords(header: string): string {
  const original = header;
  let out = '', betweenWords = false;
  for (;;) {
    const start = header.indexOf('=?');
    if (start < 0) break;
    const charsetEnd = header.indexOf('?', start + 2);
    if (charsetEnd < 0 || header.length < charsetEnd + 5 || header[charsetEnd + 2] !== '?') break;
    const end = header.indexOf('?=', charsetEnd + 3);
    if (end < 0) break;
    const kind = header[charsetEnd + 1].toLowerCase();
    const text = header.slice(charsetEnd + 3, end);
    let raw: string;
    try {
      if (kind === 'b') {
        const compact = text.replace(/[\r\n]/g, '');
        if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(compact)) throw new Error('invalid base64');
        raw = atob(compact);
      } else if (kind === 'q') {
        if (/[^\x20-\x7e\r\n\t]|=(?![0-9a-f]{2})/i.test(text)) throw new Error('invalid Q word');
        raw = text.replace(/_/g, ' ').replace(/=([0-9a-f]{2})/gi, (_: string, hex: string) => String.fromCharCode(parseInt(hex, 16)));
      } else throw new Error('invalid encoding');
    } catch {
      out += header.slice(0, end + 2);
      header = header.slice(end + 2);
      betweenWords = false;
      continue;
    }
    const prefix = header.slice(0, start);
    if (!betweenWords || /[^ \t\r\n]/.test(prefix)) out += prefix;
    const charset = header.slice(start + 2, charsetEnd).toLowerCase();
    try {
      if (charset === 'iso-8859-1') out += raw;
      else if (charset === 'us-ascii') out += raw.replace(/[\x80-\xff]/g, '\ufffd');
      else out += new TextDecoder(charset).decode(Uint8Array.from(raw, ch => ch.charCodeAt(0)));
    } catch { return original; }
    header = header.slice(end + 2);
    betweenWords = true;
  }
  return out + header;
}

/** Mirrors attachmentname.Normalize, including legacy RFC 2047 filenames. */
function normalizeAttachmentName(name: string): string {
  for (;;) {
    const previous = name;
    for (;;) {
      const decoded = decodeAttachmentWords(name);
      if (decoded === name) break;
      name = decoded;
    }
    name = name.replace(/[\p{Cc}\p{Cf}"\\/]/gu, '');
    if (name === previous) return name;
  }
}
function demoAttachmentRisk(name: string, type: string): string {
  name = normalizeAttachmentName(name).trim().replace(/[. ]+$/, '').toLowerCase(); const parts = name.split('.');
  if (parts.length > 2 && parts[parts.length - 1] && parts[parts.length - 2]) return 'double_extension';
  if (parts.length > 1 && warningExtensions.has(parts[parts.length - 1])) return 'executable_extension';
  type = type.split(';')[0].trim().toLowerCase();
  return ['text/html', 'application/xhtml+xml', 'image/svg+xml', 'text/vbscript', 'application/x-httpd-php', 'text/x-perl', 'text/x-ruby', 'text/x-shellscript', 'application/x-iso9660-image', 'application/vnd.ms-htmlhelp', 'application/x-msdownload', 'application/x-msdos-program', 'application/vnd.microsoft.portable-executable', 'application/x-executable', 'application/x-sharedlib', 'application/x-mach-binary', 'application/x-dosexec', 'application/x-sh', 'application/x-shellscript', 'application/x-bat', 'application/x-powershell', 'application/javascript', 'text/javascript', 'application/ecmascript', 'text/ecmascript', 'text/x-python', 'application/x-python-code', 'application/java-archive', 'application/vnd.android.package-archive'].includes(type) ? 'executable_content_type' : '';
}
/** Durable receipt-independent history; backfills old browser stores once. */
function demoSentTo(state: DemoState): string[] {
  if (state.sentTo === undefined) {
    state.sentTo = [];
    for (const msg of state.messages) if (msg.folderId === FOLDER_SENT) recordDemoSent(state, msg);
  }
  return state.sentTo;
}
function recordDemoSent(state: DemoState, msg: DemoMessage): void {
  const sent = demoSentTo(state);
  for (const list of [msg.toAddr, msg.ccAddr, msg.bccAddr]) {
    try { for (const a of parseAddressList(list)) { const folded = foldAddress(a.address); if (!sent.includes(folded)) sent.push(folded); } } catch { /* same as Go's invalid legacy list */ }
  }
}
async function demoWarnings(state: DemoState, msg: DemoMessage, atts: DemoAttachment[]): Promise<DemoWarning[]> {
  const out: DemoWarning[] = [];
  if ([FOLDER_SENT, FOLDER_DRAFTS, FOLDER_SCHEDULED].includes(msg.folderId)) return out;
  // Mirrors mail.ParseAddress: exactly one mailbox, or none at all. The compose
  // parser is looser than Go about dots, so apply Go's dot-atom rule on top.
  let from: ParsedAddress | null = null;
  try {
    const addrs = parseAddressList(msg.fromAddr);
    if (addrs.length === 1 && !/^\.|\.\.|\.@|@\.|\.$/.test(addrs[0].address)) from = addrs[0];
  } catch { /* malformed */ }
  const auth = msg.senderAuthentication ?? unknownDemoAuth();
  if (from && auth.dmarc === 'pass' && state.identities.some(i => foldAddress(i.address) === foldAddress(from!.address))) return out;
  if (!from) { from = { address: '', name: '' }; out.push({ kind: 'malformed_sender' }); }
  // Mirrors phishing.HasFormatChar: parses, but need not display as what it is.
  else if (/[\p{Cf}\p{Zl}\p{Zp}]/u.test(from.address)) out.push({ kind: 'malformed_sender' });
  const rawDomain = from.address.split('@')[1] ?? '';
  const domain = warningOversizedHost(rawDomain) ? rawDomain.toLowerCase() : warningDomain(rawDomain);
  if (auth.dmarc === 'fail') {
    const failed = ['dmarc']; if (!auth.dkim_aligned) failed.push('dkim_alignment'); if (['fail', 'softfail'].includes(auth.spf)) failed.push('spf');
    out.push({ kind: 'auth_fail', failed_checks: failed });
  }
  for (const token of from.name.split(/[^\p{L}\p{N}@._+\-]+/u).map(t => t.replace(/^\.+|\.+$/g, ''))) {
    if (token.includes('@')) {
      const embedded = firstAddress(token);
      if (embedded && foldAddress(embedded.address) !== foldAddress(from.address)) { out.push({ kind: 'display_name_address', address: token }); break; }
    } else { const host = warningTextHost(token); if (host && warningRegistrable(host) !== warningRegistrable(domain)) { out.push({ kind: 'display_name_address', address: token }); break; } }
  }
  const known = new Map<string,string>();
  const exact = new Set<string>();
  for (const a of [...state.identities.map(i => i.address), ...demoSentTo(state), ...state.contacts.filter(c => c.explicit).map(c => c.address)]) {
    const raw = a.split('@')[1] ?? '';
    if (warningOversizedHost(raw)) continue;
    const d = warningDomain(raw);
    for (const candidate of [d, warningRegistrable(d)]) {
      const key = warningSkeleton(candidate); exact.add(candidate);
      if (!known.has(key) || candidate < known.get(key)!) known.set(key, candidate);
    }
  }
  const seen = new Set<string>();
  // Mirrors the Go closure: raw is the host as received, since compatibility
  // characters map to plain ASCII under IDNA.
  const lookalike = (raw: string) => {
    if (warningOversizedHost(raw)) {
      raw = raw.toLowerCase();
      if (!seen.has(raw)) { seen.add(raw); out.push({ kind: 'lookalike_domain', domain: raw, punycode: raw }); }
      return;
    }
    const host = warningDomain(raw), compat = warningMapsToASCII(raw), key = compat ? raw : host;
    if (!host || warningIP(host) || seen.has(key)) return; seen.add(key);
    const resembles = !compat && (exact.has(host) || exact.has(warningRegistrable(host))) ? undefined : known.get(warningSkeleton(host)) ?? known.get(warningSkeleton(warningRegistrable(host)));
    if (!compat && !/(^|\.)xn--/.test(host) && !warningMixed(host) && !resembles) return;
    out.push({ kind: 'lookalike_domain', domain: compat ? raw : decodeWarningDomain(host), punycode: host, ...(resembles ? { resembles } : {}) });
  };
  lookalike(from.address.split('@')[1] ?? '');
  let replies: ParsedAddress[] = [];
  try { replies = parseAddressList(msg.replyToAddr); } catch { /* malformed legacy header */ }
  for (const reply of replies) {
    if (warningRegistrable(reply.address.split('@')[1]) !== warningRegistrable(domain)) out.push({ kind: 'reply_to_mismatch', address: reply.address });
  }
  if (from.address && !demoSentTo(state).includes(foldAddress(from.address)) && !state.contacts.some(c => c.explicit && c.address === foldAddress(from.address))) out.push({ kind: 'first_time_sender' });
  for (const link of await parseDemoLinks(msg.bodyHtml)) {
    const host = warningHost(link.Href); if (!host) continue; lookalike(host);
    const textHost = warningTextHost(link.Text.trim());
    if (warningOversizedHost(host) || warningOversizedHost(textHost) || warningIP(host) || /(^|\.)xn--/.test(host) || (textHost && warningRegistrable(textHost) !== warningRegistrable(host))) out.push({ kind: 'deceptive_link', link_index: link.Index, text: link.Text, href: link.Href, decoded_host: warningOversizedHost(host) ? host : decodeWarningDomain(host) });
  }
  for (const a of atts) { const reason = demoAttachmentRisk(a.filename, a.contentType); if (reason) out.push({ kind: 'risky_attachment', filename: a.filename, reason }); }
  return out;
}
