import { useState, useEffect, useRef, useCallback } from 'preact/hooks';
import { api, NotFoundError } from '../api/client.js';
import { navigate } from '../router.js';
import { showToast } from '../util/toast.js';
import { confirmDialog } from '../util/confirm.js';
import { quoteHtmlToText, stripLeadingBlankHtml, stripLeadingBlankLines } from '../util/quotetext.js';
import { reflowEdits, wrapText, isQuotedLine } from '../util/wrap.js';
import {
  SIGNATURE, signatureToHtml, signatureRange, signatureOps, findSignature,
} from '../util/signature.js';
import type { DeltaOp, SignatureRange } from '../util/signature.js';
import { getWrapColumn } from '../util/config.js';
import { Icon } from '../components/Icon.js';
import {
  hasValidRecipient, isValidAddressList, splitAddressList, formatAddress, normalizeAddressEntry,
} from '../util/address.js';
import type { components } from '../api/types.js';

type Identity = components['schemas']['Identity'];
type Contact = components['schemas']['Contact'];
type MessageDetail = components['schemas']['MessageDetail'];
type AttachmentMeta = components['schemas']['AttachmentMeta'];

// Quill is loaded as a global script.
declare const Quill: {
  new(el: HTMLElement, opts: unknown): QuillEditor;
  import(name: string): unknown;
  register(format: unknown, suppressWarning?: boolean): void;
};
interface QuillEditor {
  root: HTMLElement;
  getText(): string;
  getLength(): number;
  getContents(): { ops: DeltaOp[] };
  updateContents(delta: DeltaBuilder): void;
  insertText(index: number, text: string, formats: Record<string, unknown>, source: string): void;
  deleteText(index: number, length: number, source: string): void;
  setSelection(index: number, source: string): void;
  format(name: string, value: unknown, source: string): void;
  formatLine(index: number, length: number, name: string, value: unknown, source: string): void;
  focus(): void;
  clipboard: {
    dangerouslyPasteHTML(html: string): void;
    /** The delta a paste of this HTML would produce, without pasting it. */
    convert(input: { html: string }): DeltaBuilder;
  };
  keyboard: {
    addBinding(key: Record<string, unknown>, handler: KeyHandler): void;
    bindings: Record<string, KeyBinding[]>;
  };
  enable(enabled: boolean): void;
  on(event: string, handler: () => void): void;
}
interface QuillRange { index: number; length: number }
interface KeyContext { format: Record<string, unknown> }
type KeyHandler = (this: { quill: QuillEditor }, range: QuillRange, context: KeyContext) => boolean;
interface KeyBinding { key: string; handler: KeyHandler }
/** The subset of Quill's Delta used to describe a batch of wrap edits. */
interface DeltaBuilder {
  retain(length: number): DeltaBuilder;
  delete(length: number): DeltaBuilder;
  insert(text: string, attributes?: Record<string, unknown>): DeltaBuilder;
  ops: DeltaOp[];
}
/** The subset of Parchment used to declare the soft-break format. */
interface ParchmentLike {
  ClassAttributor: new(attrName: string, keyName: string, options: { scope: number }) => unknown;
  Scope: { BLOCK: number };
}

// ── Helpers ────────────────────────────────────────────────────────────────

function stripSubjectPrefixes(subject: string): string {
  const re = /^[ \t]*(re|fwd|fw|aw|wg|res|enc|vs|sv):[ \t]+/i;
  let s = subject;
  let m: RegExpMatchArray | null;
  while ((m = s.match(re))) s = s.slice(m[0].length);
  return s;
}

function parseAddrSpecs(header: string): string[] {
  if (!header.trim()) return [];
  return splitAddressList(header).map(a => {
    const m = a.match(/<([^>]+)>/);
    return (m ? m[1] : a).trim().toLowerCase();
  }).filter(Boolean);
}

function isOwnAddress(addrString: string, identityAddrs: Set<string>): boolean {
  const spec = parseAddrSpecs(addrString)[0] ?? '';
  if (identityAddrs.has(spec)) return true;
  const plusIdx = spec.indexOf('+');
  const atIdx = spec.indexOf('@');
  if (plusIdx !== -1 && atIdx !== -1 && plusIdx < atIdx) {
    return identityAddrs.has(spec.slice(0, plusIdx) + spec.slice(atIdx));
  }
  return false;
}

function displayName(addr: string): string {
  const m = addr.match(/^([^<>]+?)\s*<[^>]+>$/);
  const name = m ? m[1].trim() : addr.trim();
  // Quoting is a wire-format detail — a pill reading `"Doe, Jane"` would just
  // look like a mistake.
  return name.length >= 2 && name.startsWith('"') && name.endsWith('"')
    ? name.slice(1, -1)
    : name;
}

function addrSpec(addr: string): string {
  const m = addr.match(/<([^>]+)>/);
  return m ? m[1].trim() : addr.trim();
}

/** What the text in an address input would become as a pill, if anything. */
function pendingAddress(raw: string): string {
  return raw.trim().replace(/,$/, '').trim();
}

/** The tags of an address field including whatever is still in its input. */
function withPending(tags: string[], input: string): string[] {
  const pending = pendingAddress(input);
  return pending ? [...tags, pending] : tags;
}

// body_text is stored with the CRLF line endings it arrived with; a stray CR
// left inside quoted HTML reappears as a blank line once that HTML is converted
// back to text for the text/plain alternative.
function normalizeNewlines(s: string): string {
  return s.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
}

// Quoted material is deliberately kept *out* of the Quill document. Quill 2
// re-derives and diffs the whole document on every DOM mutation, so an editable
// buffer containing the quote makes each keystroke cost O(entire thread) — a
// long `>` chain (which re-quotes everything before it on every round, so it
// grows quadratically in reply depth) freezes the browser. The quote therefore
// lives in a ref, is shown read-only below the editor, and is concatenated back
// on at save/send time, separated by QUOTE_MARKER so a reopened draft can be
// split apart again. bluemonday drops comments, so the marker never reaches a
// recipient (drafts are stored verbatim; sanitization happens at send).
const QUOTE_MARKER = '<!--mymail-quote-->';

interface ComposeParts {
  /** Goes into the Quill editor — only what the user is expected to type into. */
  editorHtml: string;
  /** Held aside, read-only, and appended after QUOTE_MARKER on save/send. */
  quoteHtml: string;
}

interface NewCompose extends ComposeParts {
  /**
   * Not part of editorHtml: the signature is written into the editor as its own
   * step, so the blocks it becomes can be marked as the signature (see
   * util/signature.ts). Pasting it as one string with the rest is what left the
   * identity swap with nothing it could reliably find later.
   */
  signatureHtml: string;
}

function buildComposeParts(
  mode: 'new' | 'reply' | 'replyall' | 'forward',
  msg: MessageDetail | null,
  identity: Identity | null,
): NewCompose {
  const signatureHtml = identity?.signature ? signatureToHtml(identity.signature) : '';
  // One blank paragraph to type into; the signature is appended after it. Reply
  // and Forward used to ask for a second blank paragraph below the signature,
  // which Quill discarded in every case but one (a signature ending in a block
  // of its own, i.e. one with a `-- ` delimiter). Asking for it uniformly, and
  // being given it uniformly, are both defensible; being given it for one
  // signature in five is not, so it goes.
  const editorHtml = '<p><br></p>';

  if (!msg || mode === 'new') {
    return { editorHtml, quoteHtml: '', signatureHtml };
  }

  const date = new Date(msg.date).toUTCString();
  const sender = displayName(msg.from_addr) || msg.from_addr;

  if (mode === 'forward') {
    const fwdBlock = [
      '<p>---------- Forwarded message ----------</p>',
      `<p>From: ${esc(msg.from_addr)}</p>`,
      `<p>Date: ${esc(date)}</p>`,
      `<p>Subject: ${esc(msg.subject)}</p>`,
      `<p>To: ${esc(msg.to_addr)}</p>`,
      '<p><br></p>',
    ].join('');

    const body = msg.body_html || `<pre>${esc(normalizeNewlines(msg.body_text))}</pre>`;
    return { editorHtml, quoteHtml: `${fwdBlock}${body}`, signatureHtml };
  }

  // reply / replyall
  // The quoted body is trimmed at the top so the first quoted line follows the
  // attribution directly; otherwise the blank lines a mailer left above its own
  // body become bare `> ` lines that pile up on every further round.
  const attribution = `<p>On ${esc(date)}, ${esc(sender)} wrote:</p>`;
  const body = msg.body_html
    ? `<blockquote style="margin:0 0 0 0.8ex;border-left:1px solid #ccc;padding-left:1ex">${stripLeadingBlankHtml(msg.body_html)}</blockquote>`
    : `<p>${stripLeadingBlankLines(normalizeNewlines(msg.body_text)).split('\n').map(l => '&gt; ' + esc(l)).join('<br>')}</p>`;

  return { editorHtml, quoteHtml: `${attribution}${body}`, signatureHtml };
}

/** Split a stored body_html back into its editable and quoted halves. */
function splitQuotedHtml(html: string): ComposeParts {
  const i = html.indexOf(QUOTE_MARKER);
  if (i === -1) return { editorHtml: html, quoteHtml: '' };
  return { editorHtml: html.slice(0, i), quoteHtml: html.slice(i + QUOTE_MARKER.length) };
}

/** Recombine the two halves for storage and sending. */
function joinQuotedHtml(editorHtml: string, quoteHtml: string): string {
  return quoteHtml ? editorHtml + QUOTE_MARKER + quoteHtml : editorHtml;
}

// Both halves are wrapped by the caller before they get here. Wrapping is
// line-based and the halves are separated by a blank line, so wrapping them
// separately gives the same result as wrapping the joined text.
function joinQuotedText(editorText: string, quoteText: string): string {
  if (!quoteText) return editorText;
  return editorText.replace(/\n+$/, '') + '\n\n' + quoteText + '\n';
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// ── Wrapping the editor at column 80 ───────────────────────────────────────

// The break the wrapper inserts is an ordinary paragraph break carrying one
// extra block format, which is what lets a later edit tell its own breaks from
// the ones the author typed and re-fill the paragraph around them. It is a
// class rather than an inline style so the sanitiser drops it on the way out —
// `class` is on no allowlist — leaving it a fact about the draft, never about
// the message. The attribute name must match the class prefix, or Quill's
// clipboard cannot map the class back to the format when a draft is reopened.
const SOFT_BREAK = 'ql-softwrap';
const SOFT_BREAK_FORMAT = { [SOFT_BREAK]: 'y' };

// Formats that belong to the line they sit on. In Quill's model the newline
// carries the block's attributes, so splitting such a line either duplicates it
// (a wrapped bullet becomes two bullets) or leaves the first half unformatted.
// Those lines are left long here; the save-time wrap still bounds body_text.
// A soft break that has acquired one of these — the author selected a wrapped
// paragraph and made it a list — stops counting as one, so re-filling never
// merges two list items into one.
const UNSPLITTABLE_FORMATS = ['list', 'header', 'blockquote', 'code-block', 'align', 'indent'];

function unsplittable(attributes: Record<string, unknown>): boolean {
  return UNSPLITTABLE_FORMATS.some(f => f in attributes);
}

/** Whether the caret sits at the very end of the signature. */
function atSignatureEnd(q: QuillEditor, index: number): boolean {
  const range = signatureRange(q.getContents().ops);
  return range !== null && index === range.index + range.length - 1;
}

/**
 * Enter inside a wrapped paragraph, made hard — and Enter past the signature,
 * kept out of it.
 *
 * Splitting a block clones it, mark and all, so a break the author made inside
 * a soft-wrapped paragraph comes out looking like one of ours — and the next
 * re-fill would dissolve it, fusing two paragraphs the author had just
 * separated. Clearing the soft-break mark on the inserted newline is what makes
 * the break hard; the half after it keeps its own mark, which is still correct.
 *
 * Cloning is what is wanted inside the signature — both halves of a split
 * signature line are still signature — but not at its end, where Enter is how
 * an author starts writing below it. The new newline terminates the half above,
 * so it carries the signature mark; the paragraph the caret lands in is the new
 * one, and its mark is cleared. Without that the signature would swallow
 * whatever is typed there, and the next identity swap would delete it.
 *
 * Only a plain paragraph is taken over: in a list item, where Enter means far
 * more than a line break, Quill's own handling stands.
 */
function hardEnter(this: { quill: QuillEditor }, range: QuillRange, context: KeyContext): boolean {
  const q = this.quill;
  const formats = context.format;
  const soft = SOFT_BREAK in formats;
  const past = SIGNATURE in formats && atSignatureEnd(q, range.index + range.length);
  if ((!soft && !past) || unsplittable(formats)) return true;
  if (range.length > 0) q.deleteText(range.index, range.length, 'user');
  const inserted: Record<string, unknown> = {};
  if (soft) inserted[SOFT_BREAK] = null;
  if (SIGNATURE in formats) inserted[SIGNATURE] = formats[SIGNATURE];
  q.insertText(range.index, '\n', inserted, 'user');
  q.setSelection(range.index + 1, 'silent');
  q.focus();
  if (past) q.format(SIGNATURE, null, 'user');
  // Quill's Enter carries the inline formats at the caret across the split, and
  // taking Enter over means carrying them too — otherwise a break in the middle
  // of a bold paragraph is followed by unbolded typing. Only block formats are
  // deliberately dropped, and by this point the only ones left are our own two
  // marks, both of which have just been placed deliberately.
  for (const [name, value] of Object.entries(formats)) {
    if (name === SOFT_BREAK || name === SIGNATURE || name === 'code' || name === 'link') continue;
    if (Array.isArray(value)) continue;
    q.format(name, value, 'user');
  }
  return false;
}

interface EditorScan {
  text: string;
  /** Indices of the newlines this wrapper inserted. */
  soft: Set<number>;
  /** Block attributes of every newline, by index. */
  lines: Map<number, Record<string, unknown>>;
}

/**
 * The document as flat text plus the block attributes of each line, in one
 * pass. Returns null for a document holding an embed: `getContents` reports one
 * position for an image that contributes no characters, so every index after it
 * would be out of step — and an embed is exactly where a misplaced break would
 * be most destructive. Such a document is left alone; the save-time wrap still
 * bounds body_text.
 */
function scanEditor(q: QuillEditor): EditorScan | null {
  const text: string[] = [];
  const soft = new Set<number>();
  const lines = new Map<number, Record<string, unknown>>();
  let len = 0;
  for (const op of q.getContents().ops) {
    if (typeof op.insert !== 'string') return null;
    const attributes = op.attributes ?? {};
    for (let i = op.insert.indexOf('\n'); i !== -1; i = op.insert.indexOf('\n', i + 1)) {
      lines.set(len + i, attributes);
      if (attributes[SOFT_BREAK] && !unsplittable(attributes)) soft.add(len + i);
    }
    text.push(op.insert);
    len += op.insert.length;
  }
  // The newline a Quill document always ends with terminates the document, not
  // a line: there is nothing after it to pull back up. It can still end up
  // marked — splitting a block clones its formats onto the half that inherits
  // the old newline — and dissolving it would replace the terminator with a
  // space that Quill then has to terminate again, appending one more blank on
  // every keystroke.
  soft.delete(len - 1);
  return { text: text.join(''), soft, lines };
}

/**
 * Keep every paragraph in the editor filled to `width`, so the line breaks the
 * author sees while typing are the ones the recipient receives.
 *
 * Applied as one delta rather than an edit at a time: a single document update
 * (a long paste wraps in one pass, not one pass per line), a single undo step,
 * and Quill transforms the caret through it, so typing continues uninterrupted
 * at the same character — now on the new line.
 */
function autoWrapEditor(q: QuillEditor, width: number): void {
  const scan = scanEditor(q);
  if (scan === null) return;

  const edits = reflowEdits(scan.text, scan.soft, {
    width,
    // A quoted line is left to the save-time wrap, which is the only one that
    // can repeat the `> ` markers: a break made here has to stay dissolvable,
    // and a marker it had inserted could not be told from one the author typed.
    // Wrapping it here anyway would ship continuation lines with no marker at
    // all, which read to the recipient as newly written text.
    canWrapLine: (start, end) =>
      !unsplittable(scan.lines.get(end) ?? {}) && !isQuotedLine(scan.text, start),
  });
  if (edits.length === 0) return;

  // Newline indices in ascending order, which is the order the edits come in,
  // so one walking pointer answers "which line is this break being made in".
  const lineEnds = [...scan.lines.keys()];
  const Delta = Quill.import('delta') as new () => DeltaBuilder;
  const delta = new Delta();
  let pos = 0;
  let line = 0;
  for (const e of edits) {
    while (line < lineEnds.length && lineEnds[line] < e.at) line++;
    // A break made inside the signature stays inside it. The newline being
    // inserted terminates the half above, so unless it carries the mark that
    // half stops counting as signature — and an identity swap, which replaces
    // what is marked, would leave it behind while inserting the new signature
    // below it. Nothing carries a mark onto a dissolved break: that one is a
    // space, and the newline it replaces is the marked one that survives.
    const marked = line < lineEnds.length
      && scan.lines.get(lineEnds[line])?.[SIGNATURE] != null;
    if (e.at > pos) delta.retain(e.at - pos);
    if (e.remove > 0) delta.delete(e.remove);
    delta.insert(
      e.insert,
      e.join ? undefined
        : marked ? { ...SOFT_BREAK_FORMAT, [SIGNATURE]: 'y' } : SOFT_BREAK_FORMAT,
    );
    pos = e.at + e.remove;
  }
  q.updateContents(delta);
}

// ── The signature as a region of the document ──────────────────────────────

/**
 * What the editor makes of a signature's HTML, as flat text.
 *
 * Quill is asked rather than predicted: it is the one that turns a `<br>` into
 * a block break and drops the `<hr>` a `-- ` delimiter becomes, and every
 * attempt to second-guess it is what this whole mechanism replaces. `convert`
 * drops the newline terminating the last block — pasted, it would become the
 * document's own terminator — but a signature inserted mid-document has to keep
 * it, or it runs into the line below.
 */
function signatureText(q: QuillEditor, sigHtml: string): string {
  if (!sigHtml) return '';
  const ops = q.clipboard.convert({ html: `<p>${sigHtml}</p>` }).ops;
  const text = ops.map(op => (typeof op.insert === 'string' ? op.insert : '')).join('');
  return text.endsWith('\n') ? text : text + '\n';
}

/** Where a signature goes in a document that has none: after everything else. */
function signatureEnd(q: QuillEditor): SignatureRange {
  return { index: q.getLength(), length: 0 };
}

/** Put `text` in the document at `at`, marked, replacing whatever `at` covers. */
function writeSignature(q: QuillEditor, at: SignatureRange, text: string): void {
  const ops = signatureOps(text);
  if (at.length === 0 && ops.length === 0) return;
  const Delta = Quill.import('delta') as new () => DeltaBuilder;
  const delta = new Delta();
  if (at.index > 0) delta.retain(at.index);
  if (at.length > 0) delta.delete(at.length);
  for (const op of ops) delta.insert(op.insert as string, op.attributes);
  q.updateContents(delta);
}

/**
 * Mark the signature in a document this editor did not build.
 *
 * A draft saved before the mark existed carries its signature as ordinary
 * paragraphs, so there is nothing for `signatureRange` to find and an identity
 * swap would append the new signature below the old one — the very failure the
 * mark exists to prevent. The signature is located by text instead, with the
 * wrapper's own breaks dissolved back into the spaces they replaced, so one
 * written as a single long line still matches after being wrapped into two.
 * Dissolving preserves length, so a position in the dissolved text is the same
 * position in the document.
 *
 * Nothing found means nothing marked, and the swap then appends — which is also
 * what it does for an identity that never had a signature, and is the one
 * honest answer when the old one cannot be located.
 */
function adoptSignature(q: QuillEditor, sigHtml: string): void {
  if (!sigHtml || signatureRange(q.getContents().ops)) return;
  const scan = scanEditor(q);
  if (scan === null) return;
  const flat = scan.text.split('');
  for (const i of scan.soft) flat[i] = ' ';
  const needle = signatureText(q, sigHtml);
  const at = findSignature(flat.join(''), needle);
  if (at < 0) return;
  q.formatLine(at, needle.length, SIGNATURE, 'y', 'api');
}

// ── LocalStorage draft helpers ─────────────────────────────────────────────

interface LocalDraft {
  id: number | null;
  savedAt: string;
  identityId: number | undefined;
  to: string[];
  cc: string[];
  bcc: string[];
  subject: string;
  bodyHtml: string;
  sendAt: string;
}

function readLocalDraft(): LocalDraft | null {
  try {
    const raw = localStorage.getItem('composeDraft');
    if (!raw) return null;
    return JSON.parse(raw) as LocalDraft;
  } catch {
    return null;
  }
}

function saveLocalDraft(draft: LocalDraft): void {
  try {
    localStorage.setItem('composeDraft', JSON.stringify(draft));
  } catch { /* ignore quota errors */ }
}

function clearLocalDraft(): void {
  localStorage.removeItem('composeDraft');
}

function isoToDatetimeLocal(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

interface RestoredFields {
  identityId: number | undefined;
  to: string[];
  cc: string[];
  bcc: string[];
  subject: string;
  bodyHtml: string;
  sendAt: string;
}

function fieldsFromServerDraft(msg: MessageDetail, idents: Identity[]): RestoredFields {
  // Top-level commas only — a quoted display name may contain one, and
  // splitting there would turn one recipient into two malformed pills.
  const splitAddr = (s: string) =>
    s.trim() ? splitAddressList(s).map(normalizeAddressEntry).filter(Boolean) : [];
  const fromSpec = parseAddrSpecs(msg.from_addr)[0] ?? '';
  const matchedIdent =
    idents.find(i => i.address.toLowerCase() === fromSpec.toLowerCase()) ??
    idents.find(i => i.is_default) ??
    idents[0];
  return {
    identityId: matchedIdent?.id,
    to: splitAddr(msg.to_addr),
    cc: splitAddr(msg.cc_addr),
    bcc: splitAddr(msg.bcc_addr),
    subject: msg.subject,
    bodyHtml: msg.body_html,
    sendAt: msg.send_at ? isoToDatetimeLocal(msg.send_at) : '',
  };
}

// ── Address field with tag pills and autocomplete ──────────────────────────

interface AddressFieldProps {
  label: string;
  tags: string[];
  onTagsChange: (tags: string[]) => void;
  /**
   * What is typed but not yet a pill. Held by the parent because it is still a
   * recipient as far as the user is concerned: it decides whether Send is
   * offered, and Send folds it into `tags` before saving.
   */
  input: string;
  onInputChange: (value: string) => void;
  extra?: preact.ComponentChildren;
}

function AddressField({ label, tags, onTagsChange, input, onInputChange, extra }: AddressFieldProps) {
  const [suggestions, setSuggestions] = useState<Contact[]>([]);
  const [total, setTotal] = useState(0);
  const [showDrop, setShowDrop] = useState(false);
  const [selIdx, setSelIdx] = useState(0);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  // What is in the field right now, readable before the state update lands: a
  // blur arriving in the same tick as a suggestion click would otherwise still
  // see the pre-click text and commit it a second time.
  const inputValRef = useRef('');

  function commitInput(raw: string) {
    const val = pendingAddress(raw);
    if (!val) return;
    onTagsChange([...tags, val]);
    inputValRef.current = '';
    onInputChange('');
    setSuggestions([]);
    setShowDrop(false);
    setSelIdx(0);
  }

  function addContact(c: Contact) {
    onTagsChange([...tags, formatAddress(c.name, c.address)]);
    inputValRef.current = '';
    onInputChange('');
    setSuggestions([]);
    setShowDrop(false);
    setSelIdx(0);
    inputRef.current?.focus();
  }

  function removeTag(idx: number) {
    onTagsChange(tags.filter((_, i) => i !== idx));
  }

  function handleInput(e: Event) {
    const v = (e.target as HTMLInputElement).value;
    inputValRef.current = v;
    onInputChange(v);
    setSelIdx(0);

    if (timerRef.current) clearTimeout(timerRef.current);
    const q = v.replace(/,$/, '').trim();
    if (!q) { setSuggestions([]); setShowDrop(false); return; }

    timerRef.current = setTimeout(async () => {
      try {
        const res = await api.contacts.list({ q, limit: 10 });
        setSuggestions(res.items);
        setTotal(res.total);
        setShowDrop(res.items.length > 0);
        setSelIdx(0);
      } catch { /* ignore */ }
    }, 280);
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === 'Enter' || e.key === 'Tab' || e.key === ',') {
      if (showDrop && suggestions.length > 0 && e.key !== 'Tab') {
        e.preventDefault();
        addContact(suggestions[selIdx]);
        return;
      }
      if (input.trim()) {
        e.preventDefault();
        commitInput(input);
      }
      return;
    }
    if (e.key === 'Backspace' && !input && tags.length > 0) {
      removeTag(tags.length - 1);
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSelIdx(i => Math.min(i + 1, suggestions.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSelIdx(i => Math.max(i - 1, 0));
    } else if (e.key === 'Escape') {
      setShowDrop(false);
    }
  }

  return (
    <div class="cf-field-row">
      <span class="cf-field-label">{label}</span>
      <div class="cf-autocomplete-wrap">
        <div class="cf-tags-input" onClick={() => inputRef.current?.focus()}>
          {tags.map((t, i) => (
            <span key={i} class="cf-addr-tag" title={addrSpec(t)}>
              {displayName(t) || t}
              <button type="button" onClick={() => removeTag(i)} aria-label="Remove"><Icon name="x" size={13} /></button>
            </span>
          ))}
          <input
            ref={inputRef}
            type="text"
            placeholder={tags.length === 0 ? 'Add recipient…' : ''}
            value={input}
            onInput={handleInput}
            onKeyDown={handleKeyDown}
            onBlur={() => {
              // Turn a finished address into a pill on the way out, so it is
              // visibly part of the message. Only a well-formed one: a pill is
              // what gets saved, and the server rejects the whole draft over a
              // malformed address list — which would make every later auto-save
              // fail, and the navigate-away save fail silently. Half-typed text
              // stays in the field where it can still be corrected.
              // Checked as a list, not as one address: `Doe, Jane <j@e.com>`
              // is a well-formed entry but splits into a broken list, which is
              // exactly what the server would refuse.
              if (isValidAddressList(pendingAddress(inputValRef.current))) {
                commitInput(inputValRef.current);
              }
              setTimeout(() => setShowDrop(false), 150);
            }}
            onFocus={() => input.trim() && suggestions.length > 0 && setShowDrop(true)}
          />
        </div>
        {showDrop && (
          <div class="cf-autocomplete-dropdown">
            {suggestions.map((c, i) => (
              <div
                key={c.id}
                class={`cf-ac-item${i === selIdx ? ' selected' : ''}`}
                onMouseDown={() => addContact(c)}
              >
                <span class="cf-ac-name">{c.name || c.address}</span>
                {c.name && <span class="cf-ac-addr">{c.address}</span>}
              </div>
            ))}
            {total > 10 && (
              <div class="cf-ac-hint">Type more to narrow ({total} matches)</div>
            )}
          </div>
        )}
      </div>
      {extra && <div class="cf-field-actions">{extra}</div>}
    </div>
  );
}

// ── Main ComposeForm ───────────────────────────────────────────────────────

export interface ComposeFormProps {
  replyId?: number;
  replyAllId?: number;
  forwardId?: number;
  draftId?: number;
}

export function ComposeForm({ replyId, replyAllId, forwardId, draftId }: ComposeFormProps) {
  const sourceId = replyId ?? replyAllId ?? forwardId;
  const mode: 'new' | 'reply' | 'replyall' | 'forward' | 'edit' =
    draftId !== undefined ? 'edit' :
    replyId !== undefined ? 'reply' :
    replyAllId !== undefined ? 'replyall' :
    forwardId !== undefined ? 'forward' : 'new';

  const [loading, setLoading] = useState(true);
  const [identities, setIdentities] = useState<Identity[]>([]);
  const [identityId, setIdentityId] = useState<number | undefined>();
  const [to, setTo] = useState<string[]>([]);
  const [cc, setCc] = useState<string[]>([]);
  const [bcc, setBcc] = useState<string[]>([]);
  // Text typed into an address field but not yet committed to a pill.
  const [toInput, setToInput] = useState('');
  const [ccInput, setCcInput] = useState('');
  const [bccInput, setBccInput] = useState('');
  const [showCc, setShowCc] = useState(false);
  const [showBcc, setShowBcc] = useState(false);
  const [subject, setSubject] = useState('');
  const [sendLater, setSendLater] = useState(false);
  const [sendAt, setSendAt] = useState('');
  const [files, setFiles] = useState<File[]>([]);
  const [existingAttachments, setExistingAttachments] = useState<AttachmentMeta[]>([]);
  const [saveStatus, setSaveStatus] = useState<'saving' | 'saved' | 'error' | null>(null);
  const [sendInFlight, setSendInFlight] = useState(false);
  const [sendError, setSendError] = useState<string | null>(null);

  // Threading fields (set from pre-population)
  const inReplyToRef = useRef('');
  const refsRef = useRef<string[]>([]);

  // Quill
  const editorRef = useRef<HTMLDivElement | null>(null);
  const quillRef = useRef<QuillEditor | null>(null);

  // Draft persistence
  const draftIdRef = useRef<number | null>(null);
  const discardedRef = useRef(false);
  // True from the moment the Discard question goes up until it is answered, and
  // for good once it is answered "delete". `window.confirm` blocked the event
  // loop, so the 30-second autosave could not run while the user was reading
  // the question; the dialog does not block, so it can — and a PUT that lands
  // after the DELETE 404s straight into performSave's recreate-transparently
  // path, putting the deleted draft back under a new id that nothing owns.
  const discardPendingRef = useRef(false);
  const autoSaveTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const currentIdentityRef = useRef<Identity | null>(null);

  // Quill content cached in refs (survive Quill cleanup on unmount)
  const bodyHtmlRef = useRef('');
  const bodyTextRef = useRef('');

  // Quoted material, held outside the editor (see QUOTE_MARKER)
  const quoteHtmlRef = useRef('');
  // null = not derived yet. Deriving it costs O(quote), so it is done lazily and
  // cached: whichever comes first of the initial preview, a save, or a send pays
  // for it. That is one pass over the quote, not one per keystroke.
  const quoteTextRef = useRef<string | null>(null);
  const [quoteSize, setQuoteSize] = useState(0);
  const [quoteOpen, setQuoteOpen] = useState(false);

  // Wrapped here rather than at join time so that the preview below the editor
  // shows the quote exactly as it will be sent.
  function quoteText(): string {
    if (quoteTextRef.current === null) {
      quoteTextRef.current = wrapText(quoteHtmlToText(quoteHtmlRef.current), getWrapColumn());
    }
    return quoteTextRef.current;
  }

  function setQuote(html: string) {
    quoteHtmlRef.current = html;
    quoteTextRef.current = html ? null : '';
    setQuoteSize(html.length);
  }

  function dropQuote() {
    quoteHtmlRef.current = '';
    quoteTextRef.current = '';
    setQuoteSize(0);
    setQuoteOpen(false);
  }

  /**
   * Fill a fresh editor: the blank composition area, then the signature written
   * in as its own marked step so a later identity change can find it again.
   */
  function initEditor(parts: NewCompose) {
    const q = quillRef.current;
    if (!q) return;
    q.clipboard.dangerouslyPasteHTML(parts.editorHtml);
    writeSignature(q, signatureEnd(q), signatureText(q, parts.signatureHtml));
    bodyHtmlRef.current = q.root.innerHTML;
    bodyTextRef.current = q.getText();
  }

  /** Restore a stored draft body, keeping its quoted half out of the editor. */
  function loadStoredBody(bodyHtml: string) {
    const { editorHtml, quoteHtml } = splitQuotedHtml(bodyHtml);
    setQuote(quoteHtml);
    if (editorRef.current && quillRef.current && editorHtml) {
      const q = quillRef.current;
      q.clipboard.dangerouslyPasteHTML(editorHtml);
      // A draft saved with the mark brings it back with it; one saved before
      // the mark existed gets its signature found and marked here, once.
      const ident = currentIdentityRef.current;
      adoptSignature(q, ident?.signature ? signatureToHtml(ident.signature) : '');
      bodyHtmlRef.current = q.root.innerHTML;
      bodyTextRef.current = q.getText();
    } else {
      // Without the editor there is no text to read out of it, so the editable
      // half is rendered down from its own HTML — the same way the quoted half
      // always is. The stored body_text is not used for this: it holds both
      // halves joined and wrapped at whatever column was set when it was saved,
      // so recovering one half from it means matching a tail that a change of
      // column silently invalidates, and a near miss appends the quote twice.
      bodyHtmlRef.current = editorHtml;
      bodyTextRef.current = quoteHtmlToText(editorHtml);
    }
  }

  // For cleanup: snapshot of current form state via refs
  const stateRef = useRef({ identityId, to, cc, bcc, subject, sendAt });
  stateRef.current = { identityId, to, cc, bcc, subject, sendAt };

  // ── Build draft request body ─────────────────────────────────────────────
  // The editor text arrives already wrapped (autoWrapEditor breaks it as it is
  // typed), so wrapText here is a backstop for what the editor cannot break in
  // place: list items, and a document holding an embed. It is idempotent, so it
  // costs nothing when the editor has already done the work.
  const buildDraftBody = useCallback(() => {
    return {
      identity_id: stateRef.current.identityId,
      to_addr: stateRef.current.to.join(', '),
      cc_addr: stateRef.current.cc.join(', '),
      bcc_addr: stateRef.current.bcc.join(', '),
      subject: stateRef.current.subject,
      body_html: joinQuotedHtml(bodyHtmlRef.current, quoteHtmlRef.current),
      body_text: joinQuotedText(wrapText(bodyTextRef.current, getWrapColumn()), quoteText()),
      in_reply_to: inReplyToRef.current || undefined,
      references: refsRef.current.length > 0 ? refsRef.current : undefined,
      send_at: stateRef.current.sendAt ? new Date(stateRef.current.sendAt).toISOString() : null,
    };
  }, []);

  // ── Persist draft to localStorage (new-compose only) ────────────────────
  const persistLocalDraft = useCallback((serverUpdatedAt: string) => {
    if (mode !== 'new') return;
    saveLocalDraft({
      id: draftIdRef.current,
      savedAt: serverUpdatedAt,
      identityId: stateRef.current.identityId,
      to: stateRef.current.to,
      cc: stateRef.current.cc,
      bcc: stateRef.current.bcc,
      subject: stateRef.current.subject,
      bodyHtml: joinQuotedHtml(bodyHtmlRef.current, quoteHtmlRef.current),
      sendAt: stateRef.current.sendAt,
    });
  }, [mode]);

  // ── Save draft ───────────────────────────────────────────────────────────
  const performSave = useCallback(async (pendingFiles: File[]): Promise<void> => {
    const body = buildDraftBody();
    const hasFiles = pendingFiles.length > 0;

    try {
      if (draftIdRef.current === null) {
        const res = hasFiles
          ? await api.drafts.createWithAttachments(body, pendingFiles)
          : await api.drafts.create(body);
        draftIdRef.current = res.id;
        persistLocalDraft(res.updated_at);
      } else {
        let res: { id: number; updated_at: string };
        try {
          res = hasFiles
            ? await api.drafts.updateWithAttachments(draftIdRef.current, body, pendingFiles)
            : await api.drafts.update(draftIdRef.current, body);
        } catch (e) {
          if (!(e instanceof NotFoundError)) throw e;
          // Draft was deleted externally; recreate it transparently — unless we
          // are the ones deleting it, in which case recreating it would undo
          // the discard the user just confirmed.
          if (discardPendingRef.current) return;
          draftIdRef.current = null;
          res = hasFiles
            ? await api.drafts.createWithAttachments(body, pendingFiles)
            : await api.drafts.create(body);
          draftIdRef.current = res.id;
        }
        persistLocalDraft(res.updated_at);
      }
      setFiles([]);
      setSaveStatus('saved');
    } catch (e) {
      setSaveStatus('error');
      showToast(e instanceof Error ? e.message : 'Auto-save failed');
      throw e;
    }
  }, [buildDraftBody, persistLocalDraft]);

  // ── Initialization and pre-population ────────────────────────────────────
  useEffect(() => {
    let cancelled = false;

    async function init() {
      const [identResult] = await Promise.all([
        api.identities.list(),
      ]);
      if (cancelled) return;

      const idents = identResult.items;
      setIdentities(idents);
      const defaultIdent = idents.find(i => i.is_default) ?? idents[0];

      if (draftId !== undefined) {
        // Editing an existing draft
        draftIdRef.current = draftId;
        let serverDraft: MessageDetail | null = null;
        try {
          serverDraft = await api.messages.get(draftId);
        } catch { /* use defaults on load failure */ }
        if (cancelled) return;

        let fields: RestoredFields = {
          identityId: defaultIdent?.id,
          to: [], cc: [], bcc: [],
          subject: '', bodyHtml: '', sendAt: '',
        };
        if (serverDraft) {
          fields = fieldsFromServerDraft(serverDraft, idents);
          inReplyToRef.current = serverDraft.in_reply_to ?? '';
          refsRef.current = serverDraft.references ?? [];
          if (serverDraft.attachments.length > 0) {
            setExistingAttachments(serverDraft.attachments);
          }
        }

        const restoredIdent = fields.identityId !== undefined
          ? (idents.find(i => i.id === fields.identityId) ?? defaultIdent)
          : defaultIdent;
        setIdentityId(restoredIdent?.id);
        currentIdentityRef.current = restoredIdent ?? null;

        setTo(fields.to);
        setCc(fields.cc);
        setBcc(fields.bcc);
        if (fields.cc.length > 0) setShowCc(true);
        if (fields.bcc.length > 0) setShowBcc(true);
        setSubject(fields.subject);
        if (fields.sendAt) { setSendAt(fields.sendAt); setSendLater(true); }

        loadStoredBody(fields.bodyHtml);

        setLoading(false);
        return;
      }

      if (!sourceId) {
        // New compose: check localStorage for a saved draft
        const localDraft = readLocalDraft();

        if (localDraft) {
          let resolvedId = localDraft.id;
          let fields: RestoredFields | null = null;

          if (localDraft.id !== null) {
            try {
              const serverDraft = await api.messages.get(localDraft.id);
              // spec: use server version when its updated_at >= localDraft.savedAt
              if (serverDraft.updated_at >= localDraft.savedAt) {
                fields = fieldsFromServerDraft(serverDraft, idents);
              }
              // else: local is newer (unsaved edits) — fields stays null → use localStorage below
            } catch (e) {
              if (e instanceof NotFoundError) {
                // Draft deleted on server; clear id so next save creates a fresh one
                resolvedId = null;
              }
              // Other errors: conservatively use localStorage (server state unknown)
            }
          }

          if (cancelled) return;

          draftIdRef.current = resolvedId;

          // Apply restored fields (server-sourced or localStorage-sourced)
          const f: RestoredFields = fields ?? {
            identityId: localDraft.identityId,
            to: localDraft.to,
            cc: localDraft.cc,
            bcc: localDraft.bcc,
            subject: localDraft.subject,
            bodyHtml: localDraft.bodyHtml,
            sendAt: localDraft.sendAt,
          };

          const restoredIdent = f.identityId !== undefined
            ? (idents.find(i => i.id === f.identityId) ?? defaultIdent)
            : defaultIdent;
          setIdentityId(restoredIdent?.id);
          currentIdentityRef.current = restoredIdent ?? null;

          setTo(f.to);
          setCc(f.cc);
          setBcc(f.bcc);
          if (f.cc.length > 0) setShowCc(true);
          if (f.bcc.length > 0) setShowBcc(true);
          setSubject(f.subject);
          if (f.sendAt) { setSendAt(f.sendAt); setSendLater(true); }

          loadStoredBody(f.bodyHtml);

          setLoading(false);
          return;
        }

        // Fresh new compose
        const sel = defaultIdent;
        setIdentityId(sel?.id);
        currentIdentityRef.current = sel ?? null;
        if (editorRef.current && quillRef.current) {
          initEditor(buildComposeParts('new', null, sel ?? null));
        }
        setLoading(false);
        return;
      }

      let msg: MessageDetail | null = null;
      try {
        msg = await api.messages.get(sourceId);
      } catch { /* use null */ }
      if (cancelled) return;

      // Identity matching
      let selectedIdent = defaultIdent;
      if (mode !== 'forward' && msg) {
        const candidates = new Set(parseAddrSpecs(msg.to_addr + ',' + msg.cc_addr));
        const match = idents.find(i => candidates.has(i.address.toLowerCase()));
        if (match) selectedIdent = match;
      }
      currentIdentityRef.current = selectedIdent ?? null;
      setIdentityId(selectedIdent?.id);

      if (msg) {
        // Subject
        const stripped = stripSubjectPrefixes(msg.subject);
        setSubject(mode === 'forward' ? `Fwd: ${stripped}` : `Re: ${stripped}`);

        // Threading headers
        if (mode !== 'forward') {
          inReplyToRef.current = msg.message_id ?? '';
          refsRef.current = [
            ...msg.references,
            ...(msg.message_id ? [`<${msg.message_id}>`] : []),
          ];
        }

        // To / Cc population
        const allIdentityAddrs = new Set(idents.map(i => i.address.toLowerCase()));

        // A stored sender is re-quoted on the way into a pill: it is about to
        // become part of an address list again, and it was not stored as one
        // that survives being re-parsed (see normalizeAddressEntry).
        if (mode === 'reply') {
          const replyTo = msg.reply_to_addr.trim();
          setTo([normalizeAddressEntry(replyTo || msg.from_addr)]);
        } else if (mode === 'replyall') {
          const replyTo = msg.reply_to_addr.trim();
          const primary = [normalizeAddressEntry(replyTo || msg.from_addr)];
          const toFiltered = primary.filter(
            a => !isOwnAddress(a, allIdentityAddrs)
          );
          const originalTo = msg.to_addr ? splitAddressList(msg.to_addr).map(normalizeAddressEntry).filter(Boolean) : [];
          const originalCc = msg.cc_addr ? splitAddressList(msg.cc_addr).map(normalizeAddressEntry).filter(Boolean) : [];
          const ccFiltered = [...originalTo, ...originalCc].filter(
            a => !isOwnAddress(a, allIdentityAddrs)
          );
          setTo(toFiltered);
          setCc(ccFiltered);
          if (ccFiltered.length > 0) setShowCc(true);
        }

        // Forward: attachments copied server-side via source_message_id in POST
        if (mode === 'forward') {
          setExistingAttachments(msg.attachments);
        }
      }

      // Initialize Quill content. Only the editable half is pasted in; the
      // quoted half is held aside so typing stays O(reply), not O(thread).
      const parts = buildComposeParts(mode as 'new' | 'reply' | 'replyall' | 'forward', msg, selectedIdent ?? null);
      setQuote(parts.quoteHtml);
      // A reply is written against what it answers, so start with the quote
      // showing. Forwards and reopened drafts stay collapsed.
      if (mode === 'reply' || mode === 'replyall') setQuoteOpen(true);
      if (editorRef.current && quillRef.current) {
        initEditor(parts);
      }

      setLoading(false);

      // For forward: immediately create draft (copies attachments server-side)
      if (mode === 'forward' && msg) {
        setSaveStatus('saving');
        try {
          const res = await api.drafts.create({
            source_message_id: sourceId,
            identity_id: selectedIdent?.id,
            subject: mode === 'forward' ? `Fwd: ${stripSubjectPrefixes(msg.subject)}` : undefined,
          });
          draftIdRef.current = res.id;
          setSaveStatus('saved');
        } catch (e) {
          setSaveStatus('error');
          showToast(e instanceof Error ? e.message : 'Failed to create draft');
        }
      }
    }

    void init();
    return () => { cancelled = true; };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // ── Initialize Quill after first render ──────────────────────────────────
  useEffect(() => {
    if (!editorRef.current) return;
    const Parchment = Quill.import('parchment') as ParchmentLike;
    Quill.register(
      new Parchment.ClassAttributor(SOFT_BREAK, SOFT_BREAK, { scope: Parchment.Scope.BLOCK }),
      true,
    );
    Quill.register(
      new Parchment.ClassAttributor(SIGNATURE, SIGNATURE, { scope: Parchment.Scope.BLOCK }),
      true,
    );
    const q = new Quill(editorRef.current, {
      modules: {
        toolbar: [
          ['bold', 'italic', 'underline'],
          [{ list: 'ordered' }, { list: 'bullet' }],
          ['link', 'clean'],
        ],
      },
      theme: 'snow',
    });

    // Quill stops at the first handler that does not return true, and its own
    // Enter is registered first, so the correction has to be moved to the front
    // of the chain to get a say at all. `shiftKey: null` matches either way:
    // Shift+Enter reaches the same handler and needs the same correction.
    q.keyboard.addBinding({ key: 'Enter', shiftKey: null }, hardEnter);
    const enterBindings = q.keyboard.bindings['Enter'];
    enterBindings.unshift(enterBindings.pop()!);

    // Wrapping is itself a text change, so it has to be kept from re-entering.
    // Every other source is wrapped, not just typing: a paste, a restored
    // draft and an undo all have to end up within the column too.
    let wrapping = false;
    let composing = false;
    function wrapNow() {
      if (wrapping || composing) return;
      wrapping = true;
      try {
        autoWrapEditor(q, getWrapColumn());
      } finally {
        wrapping = false;
      }
    }

    q.on('text-change', () => {
      wrapNow();
      bodyHtmlRef.current = q.root.innerHTML;
      bodyTextRef.current = q.getText();
    });

    // Editing the document under an active IME discards what is being
    // composed, so wrapping waits for the composition to finish. Quill's own
    // compositionend handler runs first (it registered first) and emits its
    // text-change while `composing` is still set, hence the explicit wrap here.
    const onCompositionStart = () => { composing = true; };
    const onCompositionEnd = () => { composing = false; wrapNow(); };
    q.root.addEventListener('compositionstart', onCompositionStart);
    q.root.addEventListener('compositionend', onCompositionEnd);

    quillRef.current = q;
    return () => {
      q.root.removeEventListener('compositionstart', onCompositionStart);
      q.root.removeEventListener('compositionend', onCompositionEnd);
      quillRef.current = null;
    };
  }, []);

  // ── Auto-save every 30 seconds ────────────────────────────────────────────
  useEffect(() => {
    autoSaveTimerRef.current = setInterval(async () => {
      if (sendInFlight || discardPendingRef.current) return;
      setSaveStatus('saving');
      try {
        await performSave(files);
      } catch { /* toast already shown */ }
    }, 30_000);

    return () => {
      if (autoSaveTimerRef.current) clearInterval(autoSaveTimerRef.current);
    };
  }, [performSave, files, sendInFlight]);

  // ── Navigate-away save ────────────────────────────────────────────────────
  useEffect(() => {
    return () => {
      if (discardedRef.current) return;
      const body = buildDraftBody();
      if (draftIdRef.current === null) {
        api.drafts.create(body).catch(e => {
          const el = document.createElement('div');
          el.style.cssText = 'position:fixed;bottom:16px;right:16px;background:#ef4444;color:#fff;padding:10px 16px;border-radius:6px;font-size:13px;z-index:9999';
          el.textContent = 'Warning: draft not saved — ' + (e instanceof Error ? e.message : 'unknown error');
          document.body.appendChild(el);
          setTimeout(() => el.remove(), 6000);
        });
      } else {
        api.drafts.update(draftIdRef.current, body).catch(() => {/* ignore */});
      }
    };
  }, [buildDraftBody]);

  // ── Identity change: swap signature ─────────────────────────────────────
  function handleIdentityChange(newId: number) {
    setIdentityId(newId);
    const ident = identities.find(i => i.id === newId) ?? null;
    currentIdentityRef.current = ident;

    const q = quillRef.current;
    if (!q) return;
    // Operates on the editable half only — the quoted block is held outside the
    // editor and never contains a signature, so it is unaffected.
    //
    // The old signature is wherever the mark says it is, whatever has since
    // been typed around it and however the wrapper has broken it up. Nothing is
    // marked when the previous identity had no signature, or when the author
    // deleted it, or when the draft predates the mark and could not be adopted;
    // the new signature then goes where a signature goes — after everything
    // written so far, which is directly above the quote, since the quote is not
    // in the editor.
    const at = signatureRange(q.getContents().ops) ?? signatureEnd(q);
    writeSignature(q, at, signatureText(q, ident?.signature ? signatureToHtml(ident.signature) : ''));
  }

  // ── File input ───────────────────────────────────────────────────────────
  function handleFileInput(e: Event) {
    const input = e.target as HTMLInputElement;
    const newFiles = Array.from(input.files ?? []);
    setFiles(prev => [...prev, ...newFiles]);
    input.value = '';
  }

  function removeFile(idx: number) {
    setFiles(prev => prev.filter((_, i) => i !== idx));
  }

  async function removeExistingAttachment(attachId: number) {
    if (draftIdRef.current === null) return;
    try {
      await api.drafts.deleteAttachment(draftIdRef.current, attachId);
      setExistingAttachments(prev => prev.filter(a => a.id !== attachId));
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to remove attachment');
    }
  }

  // ── Send ─────────────────────────────────────────────────────────────────
  async function handleSend() {
    if (sendInFlight) return;
    setSendInFlight(true);
    setSendError(null);

    // An address still sitting in an input is one the user counts as a
    // recipient — it is why Send is clickable — so commit it before the save
    // that decides what actually goes out. stateRef is what performSave reads,
    // and it is only refreshed on render, so set it here rather than waiting.
    const foldedTo = withPending(to, toInput);
    const foldedCc = withPending(cc, ccInput);
    const foldedBcc = withPending(bcc, bccInput);
    setTo(foldedTo); setToInput('');
    setCc(foldedCc); setCcInput('');
    setBcc(foldedBcc); setBccInput('');
    stateRef.current = { ...stateRef.current, to: foldedTo, cc: foldedCc, bcc: foldedBcc };

    // Save first
    try {
      setSaveStatus('saving');
      await performSave(files);
    } catch (e) {
      setSendError(e instanceof Error ? e.message : 'Save failed before send');
      setSendInFlight(false);
      return;
    }

    if (draftIdRef.current === null) {
      setSendError('No draft to send');
      setSendInFlight(false);
      return;
    }

    try {
      const result = await api.drafts.send(draftIdRef.current);
      discardedRef.current = true;
      clearLocalDraft();
      if (result.status === 202) {
        navigate('#/folder/scheduled');
      } else {
        navigate('#/folder/sent');
      }
    } catch (e) {
      setSendError(e instanceof Error ? e.message : 'Send failed');
      setSendInFlight(false);
    }
  }

  // ── Render ────────────────────────────────────────────────────────────────
  // Text still in an address input counts too. It is on screen, so a Send
  // button greyed out next to a perfectly good address would just look broken —
  // and handleSend commits it before saving, so it is a recipient in fact.
  const canSend = hasValidRecipient(
    withPending(to, toInput).join(', '),
    withPending(cc, ccInput).join(', '),
    withPending(bcc, bccInput).join(', '),
  );

  const saveLabel =
    saveStatus === 'saving' ? 'Saving…' :
    saveStatus === 'saved' ? 'Draft saved' :
    saveStatus === 'error' ? 'Save failed' : '';

  return (
    <div class="compose-form">
      {loading && <div class="compose-loading-overlay">Loading…</div>}
      {/* Header fields */}
      <div class="compose-fields" style={loading ? 'visibility:hidden' : ''}>
        {/* From */}
        <div class="cf-field-row">
          <span class="cf-field-label">From</span>
          <div class="cf-field-input">
            <select
              value={identityId ?? ''}
              onChange={e => handleIdentityChange(Number((e.target as HTMLSelectElement).value))}
            >
              {identities.map(i => (
                <option key={i.id} value={i.id}>
                  {i.name ? `${i.name} <${i.address}>` : i.address}
                  {i.is_default ? ' (default)' : ''}
                </option>
              ))}
            </select>
          </div>
          <div class="cf-field-actions" />
        </div>

        {/* To */}
        <AddressField
          label="To"
          tags={to}
          onTagsChange={setTo}
          input={toInput}
          onInputChange={setToInput}
          extra={
            <>
              {!showCc && (
                <button type="button" class="cf-toggle-btn" onClick={() => setShowCc(true)}>Cc</button>
              )}
              {!showBcc && (
                <button type="button" class="cf-toggle-btn" onClick={() => setShowBcc(true)}>Bcc</button>
              )}
            </>
          }
        />

        {/* Cc */}
        {showCc && (
          <AddressField label="Cc" tags={cc} onTagsChange={setCc} input={ccInput} onInputChange={setCcInput} />
        )}

        {/* Bcc */}
        {showBcc && (
          <AddressField label="Bcc" tags={bcc} onTagsChange={setBcc} input={bccInput} onInputChange={setBccInput} />
        )}

        {/* Subject */}
        <div class="cf-field-row">
          <span class="cf-field-label">Subject</span>
          <input
            class="cf-subject-input"
            type="text"
            placeholder="Subject"
            value={subject}
            onInput={e => setSubject((e.target as HTMLInputElement).value)}
          />
          <div class="cf-field-actions" />
        </div>
      </div>

      {/* Quill editor */}
      <div class="compose-editor-wrap" style={loading ? 'visibility:hidden' : ''}>
        <div ref={editorRef} />
      </div>

      {/* Quoted material — read-only, kept out of the editor for performance */}
      {!loading && quoteSize > 0 && (
        <div class="compose-quote">
          <div class="compose-quote-bar">
            <button
              type="button"
              class="btn btn-ghost btn-sm compose-quote-toggle"
              aria-expanded={quoteOpen}
              onClick={() => setQuoteOpen(o => !o)}
            >
              <Icon name={quoteOpen ? 'chevron-down' : 'chevron-right'} size={14} /> Quoted text ({formatBytes(quoteSize)})
            </button>
            <span class="compose-quote-note">included when you send</span>
            <button type="button" class="btn btn-ghost btn-sm" onClick={dropQuote}>
              Remove
            </button>
          </div>
          {quoteOpen && <pre class="compose-quote-preview">{quoteText()}</pre>}
        </div>
      )}

      {/* Attachments */}
      {!loading && (existingAttachments.length > 0 || files.length > 0) && (
        <div class="compose-attachments">
          {existingAttachments.map(a => (
            <span key={a.id} class="cf-attached-file">
              {a.filename}
              <span class="cf-attach-size">({formatBytes(a.size)})</span>
              <button type="button" aria-label="Remove attachment" onClick={() => void removeExistingAttachment(a.id)}><Icon name="x" size={14} /></button>
            </span>
          ))}
          {files.map((f, i) => (
            <span key={i} class="cf-attached-file cf-new-attach">
              {f.name}
              <button type="button" aria-label="Remove attachment" onClick={() => removeFile(i)}><Icon name="x" size={14} /></button>
            </span>
          ))}
        </div>
      )}

      {/* Send later panel */}
      {sendLater && (
        <div class="compose-send-later">
          <label>Send at:</label>
          <input
            type="datetime-local"
            value={sendAt}
            onInput={e => setSendAt((e.target as HTMLInputElement).value)}
          />
          <button type="button" class="btn btn-ghost btn-sm" onClick={() => { setSendLater(false); setSendAt(''); }}>
            Cancel
          </button>
        </div>
      )}

      {/* Send error (400 form validation) */}
      {sendError && (
        <div class="compose-send-error">{sendError}</div>
      )}

      {/* Bottom actions */}
      <div class="compose-actions">
        <button
          class="btn btn-primary"
          disabled={sendInFlight || !canSend}
          title={canSend ? undefined : 'Add a valid recipient first'}
          onClick={() => void handleSend()}
        >
          {sendLater ? 'Schedule' : 'Send'}
        </button>
        <button
          class="btn btn-ghost btn-sm"
          onClick={() => setSendLater(v => !v)}
        >
          Send later
        </button>
        <label class="btn btn-ghost btn-sm cf-attach-btn">
          Attach
          <input
            type="file"
            multiple
            style="display:none"
            onChange={handleFileInput}
          />
        </label>
        <span class="compose-save-status">{saveLabel}</span>
        <button
          class="btn btn-ghost btn-sm ml-auto"
          onClick={async () => {
            if (draftIdRef.current !== null) {
              discardPendingRef.current = true;
              if (await confirmDialog({
                title: 'Discard draft',
                body: 'Permanently delete this draft? This cannot be undone.',
                confirmLabel: 'Delete',
                cancelLabel: 'Keep',
                destructive: true,
              })) {
                await api.drafts.delete(draftIdRef.current).catch(() => {/* ignore */});
                draftIdRef.current = null;
                discardedRef.current = true;
                clearLocalDraft();
                navigate('#/inbox');
              } else {
                discardPendingRef.current = false;
              }
            } else {
              discardedRef.current = true;
              clearLocalDraft();
              navigate('#/inbox');
            }
          }}
        >
          Discard
        </button>
      </div>
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
