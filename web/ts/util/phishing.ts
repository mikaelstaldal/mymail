import type { components } from '../api/types.js';
export type MessageWarning = components['schemas']['MessageWarning'];

/** Plain warning text; deceptive links use structured markup in MessageDetail.
 * Warning decisions belong to the server. */
export function warningText(w: MessageWarning): string | null {
  switch (w.kind) {
    case 'auth_fail': return `This sender failed authentication checks: ${(w.failed_checks ?? []).map(c => ({ dmarc: 'DMARC', dkim_alignment: 'DKIM alignment', spf: 'SPF' })[c] ?? c).join(', ')}`;
    case 'display_name_address': return `The sender's name contains a different address or domain: ${w.address}`;
    case 'lookalike_domain': return `The sender's domain uses international characters: ${w.domain} (${w.punycode})${w.resembles ? `; it resembles ${w.resembles}` : ''}`;
    case 'reply_to_mismatch': return `Replies will go to ${w.address}, on a different domain from the sender`;
    case 'first_time_sender': return 'You have never sent mail to this sender';
    case 'deceptive_link': return null;
    case 'risky_attachment': return `Attachment “${w.filename}” ${attachmentReason(w.reason)}`;
  }
}
export function attachmentReason(reason: MessageWarning['reason']): string {
  switch (reason) {
    case 'double_extension': return 'has a double extension';
    case 'executable_extension': return 'has a potentially executable extension';
    default: return 'has a potentially executable content type';
  }
}

/** Decode the server's ASCII domain for display. Mirrors demo/phishing.ts. */
export function decodeWarningDomain(host: string): string {
  return host.split('.').map(label => {
    if (!label.startsWith('xn--')) return label;
    if (label.length > 63) return label;
    const input = label.slice(4); let n = 128, i = 0, bias = 72;
    const split = input.lastIndexOf('-');
    const out = split < 0 ? [] : Array.from(input.slice(0, split), c => c.codePointAt(0)!);
    let pos = split < 0 ? 0 : split + 1;
    while (pos < input.length) {
      const old = i; let w = 1;
      for (let k = 36; ; k += 36) {
        const c = input.charCodeAt(pos++);
        const digit = c >= 97 && c <= 122 ? c - 97 : c >= 48 && c <= 57 ? c - 22 : 36;
        if (pos > input.length || digit >= 36 || !Number.isSafeInteger(w)) return label;
        i += digit * w;
        if (!Number.isSafeInteger(i)) return label;
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
      if (n > 0x10ffff || (n >= 0xd800 && n <= 0xdfff)) return label;
      out.splice(i++, 0, n);
    }
    const decoded = String.fromCodePoint(...out);
    // An A-label must encode non-ASCII text and round-trip through IDNA.
    // Otherwise e.g. xn--paypal- could disguise itself as plain paypal.
    if (!/[^\x00-\x7f]/.test(decoded)) return label;
    try {
      if (new URL('https://' + decoded).hostname !== label.toLowerCase()) return label;
    } catch { return label; }
    return decoded;
  }).join('.');
}

export function senderParts(raw: string): { name: string; address: string; ascii: string; idn: boolean } {
  const angle = raw.lastIndexOf('<');
  const original = angle >= 0 && raw.trimEnd().endsWith('>') ? raw.slice(angle + 1, raw.lastIndexOf('>')).trim() : raw.trim();
  const name = angle >= 0 ? raw.slice(0, angle).trim().replace(/^"|"$/g, '') : '';
  const at = original.lastIndexOf('@');
  const host = original.slice(at + 1);
  let asciiHost = host.toLowerCase();
  try {
    const url = new URL('https://' + host);
    if (!url.username && !url.password && !url.port && url.pathname === '/' && !url.search && !url.hash) asciiHost = url.hostname;
  } catch { /* Preserve malformed domains as received. */ }
  const decoded = decodeWarningDomain(asciiHost);
  const idn = at >= 0 && (/^xn--|\.xn--/i.test(host) || decoded !== asciiHost || /[^\x00-\x7f]/.test(host));
  const ascii = at >= 0 ? original.slice(0, at + 1) + asciiHost : original;
  return { name, ascii, address: idn ? original.slice(0, at + 1) + decoded : original, idn };
}
