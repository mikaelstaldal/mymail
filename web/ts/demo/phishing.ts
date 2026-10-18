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
function warningIP(host: string): boolean { return host.includes(':') || /^\d+\.\d+\.\d+\.\d+$/.test(host); }
function warningHost(href: string): string {
  try {
    const url = new URL(href.startsWith('//') ? 'https:' + href : href);
    return ['http:', 'https:'].includes(url.protocol) ? warningDomain(url.hostname) : '';
  } catch { return ''; }
}
const warningTLDs = new Set(phishingICANNTLDs);
function warningTextHost(text: string): string {
  if (/\s/.test(text)) return '';
  const explicit = text.includes('://');
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
function demoAttachmentRisk(name: string, type: string): string {
  name = name.trim().replace(/[. ]+$/, '').toLowerCase(); const parts = name.split('.');
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
  const from = firstAddress(msg.fromAddr) ?? { address: '', name: '' };
  const auth = msg.senderAuthentication ?? unknownDemoAuth();
  if (auth.dmarc === 'pass' && state.identities.some(i => foldAddress(i.address) === foldAddress(from.address))) return out;
  const domain = warningDomain(from.address.split('@')[1] ?? '');
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
    const d = warningDomain(a.split('@')[1] ?? '');
    for (const candidate of [d, warningRegistrable(d)]) {
      const key = warningSkeleton(candidate); exact.add(candidate);
      if (!known.has(key) || candidate < known.get(key)!) known.set(key, candidate);
    }
  }
  const seen = new Set<string>();
  const lookalike = (host: string) => {
    host = warningDomain(host); if (!host || warningIP(host) || seen.has(host)) return; seen.add(host);
    const resembles = exact.has(host) || exact.has(warningRegistrable(host)) ? undefined : known.get(warningSkeleton(host)) ?? known.get(warningSkeleton(warningRegistrable(host)));
    if (!/(^|\.)xn--/.test(host) && !warningMixed(host) && (!resembles || resembles === host)) return;
    out.push({ kind: 'lookalike_domain', domain: decodeWarningDomain(host), punycode: host, ...(resembles && resembles !== host ? { resembles } : {}) });
  };
  lookalike(domain);
  let replies: ParsedAddress[] = [];
  try { replies = parseAddressList(msg.replyToAddr); } catch { /* malformed legacy header */ }
  for (const reply of replies) {
    if (warningRegistrable(reply.address.split('@')[1]) !== warningRegistrable(domain)) out.push({ kind: 'reply_to_mismatch', address: reply.address });
  }
  if (from.address && !demoSentTo(state).includes(foldAddress(from.address)) && !state.contacts.some(c => c.explicit && c.address === foldAddress(from.address))) out.push({ kind: 'first_time_sender' });
  for (const link of await parseDemoLinks(msg.bodyHtml)) {
    const host = warningHost(link.Href); if (!host) continue; lookalike(host);
    const textHost = warningTextHost(link.Text.trim());
    if (warningIP(host) || /(^|\.)xn--/.test(host) || (textHost && warningRegistrable(textHost) !== warningRegistrable(host))) out.push({ kind: 'deceptive_link', link_index: link.Index, text: link.Text, href: link.Href, decoded_host: decodeWarningDomain(host) });
  }
  for (const a of atts) { const reason = demoAttachmentRisk(a.filename, a.contentType); if (reason) out.push({ kind: 'risky_attachment', filename: a.filename, reason }); }
  return out;
}
