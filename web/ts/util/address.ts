/**
 * Address-list validation, used to decide whether a message can be sent.
 *
 * This mirrors what the server accepts — `handler.validateAndStripAddrList`
 * over `service.ParseAddressList`, and `handler.sendDraft`'s rule that at least
 * one of To/Cc/Bcc must be non-empty. It is a pre-flight check only: the server
 * remains the authority, and anything that slips through still comes back as a
 * 400 shown inline. The point is to keep the Send button from offering an
 * action that is guaranteed to fail.
 *
 * The same parsing lives in `web/ts/demo/text.ts` (`parseAddressList`), which
 * cannot be shared: the demo backend is classic worker scripts with no imports.
 * Keep the two in step — see CLAUDE.md § Demo mode.
 */

/** An addr-spec, as strict as the demo backend's ADDR_SPEC_RE. */
const ADDR_SPEC_RE = /^[^\s<>,@"]+@[^\s<>,@"]+$/;

/** Splits on top-level commas only — those outside quotes and angle brackets. */
export function splitAddressList(s: string): string[] {
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

/** True when `s` is a single address, with or without a display name. */
export function isValidAddress(s: string): boolean {
  const angle = s.lastIndexOf('<');
  if (angle >= 0) {
    if (!s.endsWith('>')) return false;
    return ADDR_SPEC_RE.test(s.slice(angle + 1, -1).trim());
  }
  return ADDR_SPEC_RE.test(s);
}

/**
 * True when every entry of a comma-separated list is a valid address. The empty
 * list is valid — the server accepts an empty Cc the same as an absent one.
 */
export function isValidAddressList(list: string): boolean {
  if (!list.trim()) return true;
  return splitAddressList(list).every(p => isValidAddress(p.trim()));
}

/**
 * Anything a display name may not contain unquoted. A comma is the one that
 * bites: unquoted, `Doe, Jane <jane@example.com>` is two addresses, one of them
 * malformed — to this module, to net/mail on the server, and to the MTA.
 */
const NAME_NEEDS_QUOTING = /["(),.:;<>@[\]\\]/;

/**
 * Renders a display name and an address as one list entry, the way Go's
 * mail.Address.String does on the way out. Contact names are stored unquoted
 * (net/mail strips the quotes before `UpsertContact` sees them), so a name that
 * arrived quoted has to be quoted again here.
 *
 * A `"` or `\` in the name is dropped rather than escaped: an escape would have
 * to be understood by every parser this list passes through, including the
 * demo backend's, and a quote character in a display name is not worth that.
 */
export function formatAddress(name: string, address: string): string {
  const clean = name.replace(/["\\]/g, '').trim();
  if (clean === '') return address;
  return NAME_NEEDS_QUOTING.test(clean)
    ? `"${clean}" <${address}>`
    : `${clean} <${address}>`;
}

/**
 * Re-quotes one stored address so it can go back out as part of a list.
 *
 * `service.DecodeAddressHeader` rebuilds a header as `name + " <" + addr + ">"`
 * with no quoting, so a message from `"Doe, Jane" <jane@example.com>` is stored
 * with the quotes gone — a form the server's own validator then rejects.
 * Replying to such a message would otherwise produce a recipient that can never
 * be sent. A single entry is unambiguous (the address is the last `<…>`), so it
 * can be repaired here; a list that was already stored that way cannot be, since
 * the comma is indistinguishable from a separator by the time it is split.
 */
export function normalizeAddressEntry(entry: string): string {
  const s = entry.trim();
  const angle = s.lastIndexOf('<');
  if (angle < 0 || !s.endsWith('>')) return s;
  return formatAddress(s.slice(0, angle), s.slice(angle + 1, -1).trim());
}

/**
 * True when the given recipient lists (To, Cc, Bcc — in any order) name at
 * least one recipient and none of them is malformed.
 */
export function hasValidRecipient(...lists: string[]): boolean {
  if (!lists.every(isValidAddressList)) return false;
  return lists.some(l => l.trim() !== '');
}
