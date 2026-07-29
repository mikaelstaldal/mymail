import { useState, useEffect, useRef, useCallback } from 'preact/hooks';
import { api, NotFoundError } from '../api/client.js';
import { navigate } from '../router.js';
import { showToast } from '../util/toast.js';
import { quoteHtmlToText, stripLeadingBlankHtml, stripLeadingBlankLines } from '../util/quotetext.js';
import type { components } from '../api/types.js';

type Identity = components['schemas']['Identity'];
type Contact = components['schemas']['Contact'];
type MessageDetail = components['schemas']['MessageDetail'];
type AttachmentMeta = components['schemas']['AttachmentMeta'];

// Quill is loaded as a global script.
declare const Quill: {
  new(el: HTMLElement, opts: unknown): QuillEditor;
};
interface QuillEditor {
  root: HTMLElement;
  getText(): string;
  clipboard: { dangerouslyPasteHTML(html: string): void };
  enable(enabled: boolean): void;
  on(event: string, handler: () => void): void;
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
  return header.split(',').map(a => {
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
  return m ? m[1].trim() : addr.trim();
}

function addrSpec(addr: string): string {
  const m = addr.match(/<([^>]+)>/);
  return m ? m[1].trim() : addr.trim();
}

// body_text is stored with the CRLF line endings it arrived with; a stray CR
// left inside quoted HTML reappears as a blank line once that HTML is converted
// back to text for the text/plain alternative.
function normalizeNewlines(s: string): string {
  return s.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
}

function signatureToHtml(sig: string): string {
  const lines = normalizeNewlines(sig).split('\n');
  const parts: string[] = [];
  for (const line of lines) {
    if (line === '-- ') {
      parts.push('<hr>');
    } else {
      parts.push(line.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'));
    }
  }
  return parts.join('<br>');
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

function buildComposeParts(
  mode: 'new' | 'reply' | 'replyall' | 'forward',
  msg: MessageDetail | null,
  identity: Identity | null,
): ComposeParts {
  const sigHtml = identity?.signature ? signatureToHtml(identity.signature) : '';

  if (!msg || mode === 'new') {
    return {
      editorHtml: sigHtml ? `<p><br></p><p>${sigHtml}</p>` : '<p><br></p>',
      quoteHtml: '',
    };
  }

  const date = new Date(msg.date).toUTCString();
  const sender = displayName(msg.from_addr) || msg.from_addr;
  const sigPart = sigHtml ? `<p>${sigHtml}</p><p><br></p>` : '';
  const editorHtml = `<p><br></p>${sigPart}`;

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
    return { editorHtml, quoteHtml: `${fwdBlock}${body}` };
  }

  // reply / replyall
  // The quoted body is trimmed at the top so the first quoted line follows the
  // attribution directly; otherwise the blank lines a mailer left above its own
  // body become bare `> ` lines that pile up on every further round.
  const attribution = `<p>On ${esc(date)}, ${esc(sender)} wrote:</p>`;
  const body = msg.body_html
    ? `<blockquote style="margin:0 0 0 0.8ex;border-left:1px solid #ccc;padding-left:1ex">${stripLeadingBlankHtml(msg.body_html)}</blockquote>`
    : `<p>${stripLeadingBlankLines(normalizeNewlines(msg.body_text)).split('\n').map(l => '&gt; ' + esc(l)).join('<br>')}</p>`;

  return { editorHtml, quoteHtml: `${attribution}${body}` };
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

function joinQuotedText(editorText: string, quoteText: string): string {
  if (!quoteText) return editorText;
  return editorText.replace(/\n+$/, '') + '\n\n' + quoteText + '\n';
}

/** Inverse of joinQuotedText, for the rare path where Quill is unavailable. */
function stripQuotedText(text: string, quoteText: string): string {
  if (!quoteText) return text;
  const tail = '\n\n' + quoteText + '\n';
  return text.endsWith(tail) ? text.slice(0, -tail.length) : text;
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
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
  bodyText: string;
  sendAt: string;
}

function fieldsFromServerDraft(msg: MessageDetail, idents: Identity[]): RestoredFields {
  const splitAddr = (s: string) => s.trim() ? s.split(',').map(a => a.trim()).filter(Boolean) : [];
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
    bodyText: msg.body_text,
    sendAt: msg.send_at ? isoToDatetimeLocal(msg.send_at) : '',
  };
}

// ── Address field with tag pills and autocomplete ──────────────────────────

interface AddressFieldProps {
  label: string;
  tags: string[];
  onTagsChange: (tags: string[]) => void;
  extra?: preact.ComponentChildren;
}

function AddressField({ label, tags, onTagsChange, extra }: AddressFieldProps) {
  const [input, setInput] = useState('');
  const [suggestions, setSuggestions] = useState<Contact[]>([]);
  const [total, setTotal] = useState(0);
  const [showDrop, setShowDrop] = useState(false);
  const [selIdx, setSelIdx] = useState(0);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  function commitInput(raw: string) {
    const val = raw.trim().replace(/,$/, '').trim();
    if (!val) return;
    onTagsChange([...tags, val]);
    setInput('');
    setSuggestions([]);
    setShowDrop(false);
    setSelIdx(0);
  }

  function addContact(c: Contact) {
    const val = c.name ? `${c.name} <${c.address}>` : c.address;
    onTagsChange([...tags, val]);
    setInput('');
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
    setInput(v);
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
              <button type="button" onClick={() => removeTag(i)} aria-label="Remove">×</button>
            </span>
          ))}
          <input
            ref={inputRef}
            type="text"
            placeholder={tags.length === 0 ? 'Add recipient…' : ''}
            value={input}
            onInput={handleInput}
            onKeyDown={handleKeyDown}
            onBlur={() => setTimeout(() => setShowDrop(false), 150)}
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

  function quoteText(): string {
    if (quoteTextRef.current === null) {
      quoteTextRef.current = quoteHtmlToText(quoteHtmlRef.current);
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

  /** Restore a stored draft body, keeping its quoted half out of the editor. */
  function loadStoredBody(bodyHtml: string, fallbackText: string) {
    const { editorHtml, quoteHtml } = splitQuotedHtml(bodyHtml);
    setQuote(quoteHtml);
    if (editorRef.current && quillRef.current && editorHtml) {
      quillRef.current.clipboard.dangerouslyPasteHTML(editorHtml);
      bodyHtmlRef.current = quillRef.current.root.innerHTML;
      bodyTextRef.current = quillRef.current.getText();
    } else {
      bodyHtmlRef.current = editorHtml;
      bodyTextRef.current = stripQuotedText(fallbackText, quoteHtmlToText(quoteHtml));
    }
  }

  // For cleanup: snapshot of current form state via refs
  const stateRef = useRef({ identityId, to, cc, bcc, subject, sendAt });
  stateRef.current = { identityId, to, cc, bcc, subject, sendAt };

  // ── Build draft request body ─────────────────────────────────────────────
  const buildDraftBody = useCallback(() => {
    return {
      identity_id: stateRef.current.identityId,
      to_addr: stateRef.current.to.join(', '),
      cc_addr: stateRef.current.cc.join(', '),
      bcc_addr: stateRef.current.bcc.join(', '),
      subject: stateRef.current.subject,
      body_html: joinQuotedHtml(bodyHtmlRef.current, quoteHtmlRef.current),
      body_text: joinQuotedText(bodyTextRef.current, quoteText()),
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
          // Draft was deleted externally; recreate it transparently.
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
          subject: '', bodyHtml: '', bodyText: '', sendAt: '',
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

        loadStoredBody(fields.bodyHtml, fields.bodyText);

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
            bodyText: '',
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

          loadStoredBody(f.bodyHtml, f.bodyText);

          setLoading(false);
          return;
        }

        // Fresh new compose
        const sel = defaultIdent;
        setIdentityId(sel?.id);
        currentIdentityRef.current = sel ?? null;
        if (editorRef.current && quillRef.current) {
          const { editorHtml } = buildComposeParts('new', null, sel ?? null);
          quillRef.current.clipboard.dangerouslyPasteHTML(editorHtml);
          bodyHtmlRef.current = quillRef.current.root.innerHTML;
          bodyTextRef.current = quillRef.current.getText();
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

        if (mode === 'reply') {
          const replyTo = msg.reply_to_addr.trim();
          setTo(replyTo ? [replyTo] : [msg.from_addr]);
        } else if (mode === 'replyall') {
          const replyTo = msg.reply_to_addr.trim();
          const primary = replyTo ? [replyTo] : [msg.from_addr];
          const toFiltered = primary.filter(
            a => !isOwnAddress(a, allIdentityAddrs)
          );
          const originalTo = msg.to_addr ? msg.to_addr.split(',').map(s => s.trim()).filter(Boolean) : [];
          const originalCc = msg.cc_addr ? msg.cc_addr.split(',').map(s => s.trim()).filter(Boolean) : [];
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
        quillRef.current.clipboard.dangerouslyPasteHTML(parts.editorHtml);
        bodyHtmlRef.current = quillRef.current.root.innerHTML;
        bodyTextRef.current = quillRef.current.getText();
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
    q.on('text-change', () => {
      bodyHtmlRef.current = q.root.innerHTML;
      bodyTextRef.current = q.getText();
    });
    quillRef.current = q;
    return () => {
      quillRef.current = null;
    };
  }, []);

  // ── Auto-save every 30 seconds ────────────────────────────────────────────
  useEffect(() => {
    autoSaveTimerRef.current = setInterval(async () => {
      if (sendInFlight) return;
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
    // Swap signature: rebuild initial HTML and re-set
    // We do a simple approach: load current body, detect old sig block and replace
    // For simplicity, only swap if we can access Quill
    const q = quillRef.current;
    if (!q) return;
    // Operates on the editable half only — the quoted block is held outside the
    // editor and never contains a signature, so it is unaffected.
    // This is a simplification: we just swap the sig by re-building initial HTML
    // and re-inserting. In practice, swap logic requires finding the sig delimiter.
    // The spec says "swap signature block" - we implement this by rebuilding.
    const html = q.root.innerHTML;
    const newSigHtml = ident?.signature ? signatureToHtml(ident.signature) : '';
    const oldSigHtml = identities.find(i => i.id === identityId)?.signature
      ? signatureToHtml(identities.find(i => i.id === identityId)!.signature)
      : '';

    if (oldSigHtml && html.includes(oldSigHtml)) {
      const updated = newSigHtml
        ? html.replace(oldSigHtml, newSigHtml)
        : html.replace(`<p>${oldSigHtml}</p>`, '');
      q.clipboard.dangerouslyPasteHTML(updated);
    } else if (newSigHtml) {
      // Insert new sig at the beginning
      const updated = `<p>${newSigHtml}</p>${html}`;
      q.clipboard.dangerouslyPasteHTML(updated);
    }
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
          <AddressField label="Cc" tags={cc} onTagsChange={setCc} />
        )}

        {/* Bcc */}
        {showBcc && (
          <AddressField label="Bcc" tags={bcc} onTagsChange={setBcc} />
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
              {quoteOpen ? '▾' : '▸'} Quoted text ({formatBytes(quoteSize)})
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
              <button type="button" onClick={() => void removeExistingAttachment(a.id)}>×</button>
            </span>
          ))}
          {files.map((f, i) => (
            <span key={i} class="cf-attached-file cf-new-attach">
              {f.name}
              <button type="button" onClick={() => removeFile(i)}>×</button>
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
          disabled={sendInFlight}
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
              if (confirm('Discard this draft?')) {
                await api.drafts.delete(draftIdRef.current).catch(() => {/* ignore */});
                draftIdRef.current = null;
                discardedRef.current = true;
                clearLocalDraft();
                navigate('#/inbox');
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
