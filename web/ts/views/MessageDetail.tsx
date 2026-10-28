import { useState, useEffect, useRef, useMemo, useId } from 'preact/hooks';
import { api, NotFoundError } from '../api/client.js';
import { navigate, restoreMessageList } from '../router.js';
import { showToast } from '../util/toast.js';
import { confirmDialog } from '../util/confirm.js';
import { getMycalUrl, isDemo } from '../util/config.js';
import { formatDateFull, formatDateAdaptive, defaultSnoozeDateTime } from '../util/date.js';
import { hasValidRecipient, isValidAddress } from '../util/address.js';
import { findIcsLinks } from '../util/icslinks.js';
import { senderParts, warningText, warningParts, revealFormatChars, attachmentReason, type MessageWarning } from '../util/phishing.js';
import { foldContactAddress } from '../util/casefold.js';
import { Icon } from '../components/Icon.js';
import type { components } from '../api/types.js';

type MessageDetailType = components['schemas']['MessageDetail'];
type MessageSummary = components['schemas']['MessageSummary'];
type Folder = components['schemas']['Folder'];

const INBOX_ID = 1;
const DRAFTS_ID = 3;
const TRASH_ID = 4;
const SCHEDULED_ID = 5;
const SNOOZED_ID = 6;
const JUNK_ID = 7;

/** Deferred rather than sent, per handler.isScheduled. */
const SCHEDULE_THRESHOLD_MS = 60_000;

/**
 * Whether sending now would schedule rather than send. `_tick` is unused: it is
 * there so a caller can re-evaluate this as the clock passes the threshold.
 */
function isDeferred(sendAt: string | null, _tick?: number): boolean {
  return sendAt !== null && Date.parse(sendAt) > Date.now() + SCHEDULE_THRESHOLD_MS;
}

const MANAGED_FOLDER_IDS = new Set([DRAFTS_ID, SCHEDULED_ID, SNOOZED_ID]);
const NO_MOVE_DEST_IDS = new Set([DRAFTS_ID, SCHEDULED_ID, SNOOZED_ID]);

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

type AttachmentMeta = components['schemas']['AttachmentMeta'];

function isIcs(a: AttachmentMeta): boolean {
  return a.content_type === 'text/calendar'
    || a.content_type === 'application/ics'
    || a.filename.toLowerCase().endsWith('.ics');
}

// Keys into icsImportStatus. An event can be imported from an attachment or
// from a link in the HTML body, and the two share one status map — the prefixes
// keep an attachment id from colliding with a URL.
const attachmentKey = (id: number): string => `att:${id}`;
const linkKey = (url: string): string => `link:${url}`;

function Sender({ raw }: { raw: string }) {
  const sender = senderParts(raw);
  return <span class="sender-display">
    {sender.name && <bdi class="sender-name">{sender.name}</bdi>}
    <bdi class="sender-address">{sender.address}</bdi>
    {sender.idn && <span class="sender-idn">IDN (<bdi>{sender.ascii}</bdi>)</span>}
  </span>;
}

function LinkWarning({ warning }: { warning: MessageWarning }) {
  const tooltipId = useId();
  const tooltipRef = useRef<HTMLSpanElement>(null);
  const [position, setPosition] = useState<{ left: number; top?: number; bottom?: number; maxHeight: number } | null>(null);
  const visible = position !== null;

  useEffect(() => {
    if (!visible) return;
    const dismiss = () => setPosition(null);
    const onScroll = (event: Event) => {
      // Let long values scroll inside the tooltip itself.
      if (!tooltipRef.current?.contains(event.target as Node)) dismiss();
    };
    document.addEventListener('scroll', onScroll, true);
    window.addEventListener('resize', dismiss);
    return () => {
      document.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('resize', dismiss);
    };
  }, [visible]);

  const show = (element: HTMLElement) => {
    const rect = element.getBoundingClientRect();
    const below = window.innerHeight - rect.bottom;
    const placeBelow = below >= Math.min(240, rect.top);
    setPosition({
      left: Math.max(16, Math.min(rect.left, window.innerWidth - 320)),
      ...(placeBelow ? { top: rect.bottom } : { bottom: window.innerHeight - rect.top }),
      maxHeight: Math.max(0, Math.min(320, (placeBelow ? below : rect.top) - 8)),
    });
  };

  return <span class="phishing-link-warning" tabIndex={0}
    aria-describedby={visible ? tooltipId : undefined}
    onMouseEnter={event => show(event.currentTarget)}
    onMouseLeave={event => { if (!event.currentTarget.matches(':focus-within')) setPosition(null); }}
    onFocus={event => show(event.currentTarget)}
    onBlur={() => setPosition(null)}
    onKeyDown={event => { if (event.key === 'Escape') setPosition(null); }}>
    <span>Check this link:</span>
    <bdi class="phishing-warning-url">“{revealFormatChars(warning.text ?? '')}”</bdi>
    <span>goes to</span>
    <bdi class="phishing-warning-url">{revealFormatChars(warning.href ?? '')}</bdi>
    {warning.decoded_host && <>
      <span>(host:</span>
      <bdi class="phishing-warning-url phishing-warning-host">{revealFormatChars(warning.decoded_host ?? '')}</bdi>
      <span>)</span>
    </>}
    {position && <span ref={tooltipRef} id={tooltipId} role="tooltip" class="phishing-link-tooltip"
      style={{ left: `${position.left}px`, top: position.top === undefined ? undefined : `${position.top}px`,
        bottom: position.bottom === undefined ? undefined : `${position.bottom}px`, maxHeight: `${position.maxHeight}px` }}>
      <span>Link text: <bdi>{revealFormatChars(warning.text ?? '')}</bdi></span>
      <span>Destination: <bdi>{revealFormatChars(warning.href ?? '')}</bdi></span>
      {warning.decoded_host && <span>Host: <bdi>{revealFormatChars(warning.decoded_host ?? '')}</bdi></span>}
    </span>}
  </span>;
}

/** The first-time-sender warning, shown beside the From address it is about
 * rather than in the banner above the body. */
function FirstTimeSenderNote({ warnings }: { warnings: MessageWarning[] }) {
  const warning = warnings.find(w => w.kind === 'first_time_sender');
  if (!warning) return null;
  return <span class="sender-first-time" role="note" aria-label="Sender warning">{warningText(warning)}</span>;
}

/** Keep untrusted values separate from the application's warning sentence. */
function WarningText({ warning: w }: { warning: MessageWarning }) {
  return <>{warningParts(w)?.map(part => typeof part === 'string' ? part : <bdi>{part.value}</bdi>)}</>;
}

function WarningBanner({ warnings: all }: { warnings: MessageWarning[] }) {
  const linkCount = all.filter(w => w.kind === 'deceptive_link').length;
  const firstLink = all.find(w => w.kind === 'deceptive_link');
  const warnings = all.filter(w => w.kind !== 'first_time_sender'
    && (w.kind !== 'deceptive_link' || w === firstLink));
  if (!warnings.length) return null;
  return <div class="phishing-warnings" role="note" aria-label="Message warnings">
    <ul>{warnings.map((warning, i) => <li key={i} class={warning.kind === 'auth_fail' || warning.kind === 'malformed_sender' || warning.kind === 'lookalike_domain' ? 'phishing-warning-strong' : ''}>
      {warning.kind === 'deceptive_link'
        ? linkCount > 1 ? `Message contains ${linkCount} deceptive links` : <LinkWarning warning={warning} />
        : <WarningText warning={warning} />}
    </li>)}</ul>
  </div>;
}

function autoResizeIframe(iframe: HTMLIFrameElement | null): void {
  if (!iframe) return;
  try {
    const h = iframe.contentDocument?.documentElement.scrollHeight;
    if (h) iframe.style.height = h + 'px';
  } catch (_) { /* sandboxed - CSS fallback applies */ }
}

interface BodyIframeProps {
  id: number;
  externalImages: boolean;
}

/**
 * The body document, fetched rather than linked, for demo mode only.
 *
 * A sandboxed iframe has an opaque origin, and a browser does not consult a
 * service worker for a navigation out of one — so the demo's in-browser backend
 * never sees an `<iframe src>` and the request escapes to a server that is not
 * there. A plain fetch from this (controlled) page does reach the worker, and
 * the document it returns carries its own <meta> CSP because the response
 * headers are lost on the way into srcdoc.
 *
 * Returns null against the real server, where the src attribute is used as-is.
 */
function useDemoBodyDocument(url: string): string | null {
  const [document, setDocument] = useState<string | null>(null);
  useEffect(() => {
    if (!isDemo()) return;
    let cancelled = false;
    setDocument(null);
    void fetch(url)
      .then(res => res.text())
      .then(text => { if (!cancelled) setDocument(text); })
      .catch(() => { if (!cancelled) setDocument('<!DOCTYPE html><html><body></body></html>'); });
    return () => { cancelled = true; };
  }, [url]);
  return document;
}

function BodyIframe({ id, externalImages }: BodyIframeProps) {
  const ref = useRef<HTMLIFrameElement>(null);
  const handleLoad = () => autoResizeIframe(ref.current);
  const url = `api/v1/messages/${id}/body${externalImages ? '?external=1' : ''}`;
  const demoDocument = useDemoBodyDocument(url);
  // The sandbox is identical either way; only where the document comes from differs.
  const source = isDemo() ? { srcdoc: demoDocument ?? '' } : { src: url };
  return (
    <iframe
      ref={ref}
      class="body-iframe"
      {...source}
      sandbox="allow-popups allow-popups-to-escape-sandbox allow-downloads"
      onLoad={handleLoad}
      title="Message body"
    />
  );
}

interface ThreadEntryProps {
  entry: MessageSummary;
  isCurrent: boolean;
  expanded: boolean;
  expandedMsg: MessageDetailType | null;
  expandedLoading: boolean;
  onToggle: (id: number) => void;
  onOpen: (id: number) => void;
}

function ThreadEntry({ entry, isCurrent, expanded, expandedMsg, expandedLoading, onToggle, onOpen }: ThreadEntryProps) {
  const { display: dateDisplay } = formatDateAdaptive(entry.date);
  const folderLabel = entry.folder_id === JUNK_ID ? 'Junk' : entry.folder_id === TRASH_ID ? 'Trash' : null;
  return (
    <li class={`thread-entry${isCurrent ? ' thread-current' : ''}${folderLabel ? ' thread-excluded-folder' : ''}`}>
      {/* The two buttons are siblings: an Open button nested inside the
          expand button would be invalid markup and unreachable by click. */}
      <div class="thread-entry-row">
        <button
          class="thread-entry-btn"
          onClick={() => !isCurrent && onToggle(entry.id)}
          disabled={isCurrent}
        >
          <span class="thread-from"><Sender raw={entry.from_addr} /></span>
          <span class="thread-subject">{entry.subject || '(no subject)'}</span>
          {folderLabel && <span class="thread-folder-label">{folderLabel}</span>}
          <span class="thread-date" title={formatDateFull(entry.date)}>{dateDisplay}</span>
          {!entry.read && <span class="thread-unread-dot" aria-label="Unread" />}
        </button>
        {/* Rendered even for the current entry, where CSS keeps it invisible:
            it reserves the column so the dates line up down the strip. */}
        <button
          class="btn btn-ghost btn-sm thread-open-btn"
          onClick={() => !isCurrent && onOpen(entry.id)}
          disabled={isCurrent}
          title="Open this message in the main view"
        >
          Open
        </button>
      </div>
      {expanded && (
        <div class="thread-expanded">
          {expandedLoading ? (
            <div class="thread-expanded-status">Loading…</div>
          ) : expandedMsg ? (
            <>
              <div class="thread-expanded-meta">
                <span class="msg-detail-label">From</span>
                <span class="msg-detail-value"><Sender raw={expandedMsg.from_addr} /><FirstTimeSenderNote warnings={expandedMsg.warnings} /></span>
                <span class="thread-expanded-sep" />
                <span class="msg-detail-label">Date</span>
                <span class="msg-detail-value">{formatDateFull(expandedMsg.date)}</span>
              </div>
              <WarningBanner warnings={expandedMsg.warnings} />
              {expandedMsg.body_html ? (
                <BodyIframe id={expandedMsg.id} externalImages={false} />
              ) : (
                <pre class="body-text">{expandedMsg.body_text}</pre>
              )}
            </>
          ) : (
            <div class="thread-expanded-status">Failed to load</div>
          )}
        </div>
      )}
    </li>
  );
}

export interface MessageDetailProps {
  id: number;
  folders: Folder[];
}

export function MessageDetail({ id, folders }: MessageDetailProps) {
  const [msg, setMsg] = useState<MessageDetailType | null>(null);
  const [loading, setLoading] = useState(true);
  const [notFound, setNotFound] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [bodyView, setBodyView] = useState<'html' | 'text'>(
    () => localStorage.getItem('preferredBodyView') === 'text' ? 'text' : 'html'
  );
  const [externalImages, setExternalImages] = useState(false);
  const [thread, setThread] = useState<{ total: number; truncated: boolean; items: MessageSummary[] } | null>(null);
  const [expandedThreadId, setExpandedThreadId] = useState<number | null>(null);
  const expandedThreadIdRef = useRef<number | null>(null);
  const [expandedMsg, setExpandedMsg] = useState<MessageDetailType | null>(null);
  const [expandedLoading, setExpandedLoading] = useState(false);
  const [showHeaders, setShowHeaders] = useState(false);
  const [allHeaders, setAllHeaders] = useState<string | null>(null);
  const [headersLoading, setHeadersLoading] = useState(false);
  const [moveTo, setMoveTo] = useState('');
  const [snoozeOpen, setSnoozeOpen] = useState(false);
  const [snoozeValue, setSnoozeValue] = useState('');
  const [rescheduleOpen, setRescheduleOpen] = useState(false);
  const [rescheduleValue, setRescheduleValue] = useState('');
  const [actionInFlight, setActionInFlight] = useState(false);
  const [icsImportStatus, setIcsImportStatus] = useState<Record<string, 'loading' | 'success' | string>>({});
  const [canAddSender, setCanAddSender] = useState(false);
  const bodyRef = useRef<HTMLDivElement>(null);
  const threadStripRef = useRef<HTMLDivElement>(null);
  const [threadHeight, setThreadHeight] = useState<number | null>(null);
  const [scheduleTick, setScheduleTick] = useState(0);

  useEffect(() => {
    const body = bodyRef.current;
    const strip = threadStripRef.current;
    const container = body?.parentElement;
    if (!body || !strip || !container) return;
    // A saved pixel height must fit again when the viewport changes. Measure
    // the fixed siblings too, since a narrower toolbar can wrap onto more rows.
    const observer = new ResizeObserver(() => {
      const occupiedHeight = [...container.children]
        .filter(child => child !== body && child !== strip)
        .reduce((sum, child) => sum + child.getBoundingClientRect().height, 0);
      const maximum = Math.max(60, container.getBoundingClientRect().height - occupiedHeight - 60);
      setThreadHeight(height => height === null ? null : Math.min(height, maximum));
    });
    observer.observe(container);
    return () => observer.disconnect();
  }, [msg?.id, thread?.total]);

  // Parsing the body is not free, and it depends on nothing else in this view.
  // Skipped entirely with no MyCal configured, which is most deployments — the
  // pref is read here as well as at render, so a message opened before it was
  // set picks the links up when the route brings this view back.
  const calendarLinks = useMemo(
    () => msg === null || getMycalUrl() === '' ? [] : findIcsLinks(msg.body_html),
    [msg?.body_html],
  );

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setNotFound(false);
    setError(null);
    setMsg(null);
    setCanAddSender(false);
    setThread(null);
    setExpandedThreadId(null);
    expandedThreadIdRef.current = null;
    setExpandedMsg(null);
    setExternalImages(false);
    setActionInFlight(false);
    setThreadHeight(null);
    setShowHeaders(false);
    setAllHeaders(null);
    setMoveTo('');
    setSnoozeOpen(false);
    // Keyed by attachment id and by link URL, and a URL can recur across
    // messages — an "Imported" from the previous message must not carry over.
    setIcsImportStatus({});

    async function checkSenderContact(message: MessageDetailType) {
      const raw = message.from_addr.trim();
      // A malformed sender has no one address to save, whatever the UI's
      // looser parsing would make of it.
      if ([2, DRAFTS_ID, SCHEDULED_ID].includes(message.folder_id) || !isValidAddress(raw)
        || message.warnings.some(w => w.kind === 'malformed_sender')) return;
      // Match the stored addr-spec, not the display-only decoded IDN address.
      const angle = raw.lastIndexOf('<');
      const address = foldContactAddress(angle >= 0 ? raw.slice(angle + 1, -1).trim() : raw);
      for (let offset = 0; !cancelled; offset += 100) {
        const page = await api.contacts.list({ q: address, limit: 100, offset });
        if (cancelled) return;
        const contact = page.items.find(c => c.address === address);
        if (contact || offset + page.items.length >= page.total || page.items.length === 0) {
          setCanAddSender(!contact?.explicit);
          return;
        }
      }
    }

    api.messages.get(id).then(m => {
      if (cancelled) return;
      setMsg(m);
      setLoading(false);
      void checkSenderContact(m).catch(() => {
        // Keep this optional action hidden until contact status is known.
      });
      if (!m.read) {
        void api.messages.markRead(id).then(() => {
          window.dispatchEvent(new CustomEvent('folder-reload'));
        });
      }
    }).catch(e => {
      if (cancelled) return;
      if (e instanceof NotFoundError) {
        setNotFound(true);
      } else {
        setError(e instanceof Error ? e.message : 'Failed to load message');
      }
      setLoading(false);
    });

    api.messages.thread(id).then(t => {
      if (!cancelled) setThread(t);
    }).catch(() => { /* non-fatal */ });

    return () => { cancelled = true; };
  }, [id]);

  const handleBodyViewChange = (view: 'html' | 'text') => {
    setBodyView(view);
    localStorage.setItem('preferredBodyView', view);
  };

  const handleExpandThread = async (entryId: number) => {
    if (expandedThreadIdRef.current === entryId) {
      expandedThreadIdRef.current = null;
      setExpandedThreadId(null);
      setExpandedMsg(null);
      return;
    }
    expandedThreadIdRef.current = entryId;
    setExpandedThreadId(entryId);
    setExpandedMsg(null);
    setExpandedLoading(true);
    try {
      const m = await api.messages.get(entryId);
      // Only apply if the user hasn't clicked a different entry while waiting.
      if (expandedThreadIdRef.current !== entryId) return;
      if (!m.read) {
        void api.messages.markRead(entryId).then(() => {
          window.dispatchEvent(new CustomEvent('folder-reload'));
        });
        setThread(t => t ? {
          ...t,
          items: t.items.map(i => i.id === entryId ? { ...i, read: true } : i),
        } : null);
      }
      setExpandedMsg(m);
      setExpandedLoading(false);
    } catch (_) {
      if (expandedThreadIdRef.current === entryId) setExpandedLoading(false);
    }
  };

  const handleResizeStart = (e: MouseEvent) => {
    e.preventDefault();
    const startY = e.clientY;
    // Keep subpixel heights: offsetHeight rounds to integers and can over-allocate the
    // thread, leaving the body just below its 60px minimum on some font metrics.
    const startHeight = threadStripRef.current?.getBoundingClientRect().height ?? 200;
    const availableHeight = startHeight + (bodyRef.current?.getBoundingClientRect().height ?? 400);
    document.body.style.cursor = 'ns-resize';
    document.body.style.userSelect = 'none';
    // Iframes otherwise capture pointer events as the divider crosses the body.
    document.body.classList.add('thread-resizing');
    const onMove = (ev: MouseEvent) => {
      const delta = startY - ev.clientY;
      setThreadHeight(Math.max(60, Math.min(startHeight + delta, availableHeight - 60)));
    };
    const onUp = () => {
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      document.body.classList.remove('thread-resizing');
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  };

  const handleToggleHeaders = async () => {
    if (showHeaders) { setShowHeaders(false); return; }
    setShowHeaders(true);
    if (allHeaders !== null) return;
    setHeadersLoading(true);
    try {
      const text = await api.messages.getHeaders(msg!.id);
      setAllHeaders(text);
    } catch (_) {
      setAllHeaders('(failed to load headers)');
    } finally {
      setHeadersLoading(false);
    }
  };

  const folderSlug = (fid: number) =>
    folders.find(f => f.id === fid)?.slug ?? 'inbox';

  const navigateToSourceFolder = (fid: number) =>
    navigate(fid === INBOX_ID ? '#/inbox' : `#/folder/${folderSlug(fid)}`);

  useEffect(() => {
    const handleKey = (event: KeyboardEvent) => {
      if (event.key !== 'Backspace' || event.defaultPrevented || event.repeat
        || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || !msg) return;
      const target = event.target;
      if (target instanceof HTMLElement && (target.isContentEditable
        || target.closest('input, textarea, select, [contenteditable]'))) return;
      if (document.querySelector('[aria-modal="true"]')) return;
      event.preventDefault();
      restoreMessageList(msg.id, msg.folder_id);
      navigateToSourceFolder(msg.folder_id);
    };
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [msg, folders]);

  const handleDelete = async () => {
    if (!msg || actionInFlight) return;
    const isPermanent = msg.folder_id === JUNK_ID || msg.folder_id === TRASH_ID;
    const sourceFolderId = msg.folder_id;
    // Claimed before the question, not after it: `window.confirm` blocked the
    // event loop, so nothing else could start while it was up, but the dialog
    // does not — and this flag is what keeps two actions off the same message.
    setActionInFlight(true);
    if (!await confirmDialog({
      title: 'Delete message',
      body: isPermanent
        ? 'Permanently delete this message? This cannot be undone.'
        : 'Delete this message? It will be moved to Trash.',
      confirmLabel: 'Delete',
      cancelLabel: 'Keep',
      destructive: true,
    })) {
      setActionInFlight(false);
      return;
    }
    try {
      await api.messages.deleteSingle(msg.id);
      navigateToSourceFolder(sourceFolderId);
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to delete message');
      setActionInFlight(false);
    }
  };

  // Re-render at the instant a deferred send stops being one, so the button
  // stops saying "Schedule" the moment it would no longer schedule.
  const sendAt = msg?.send_at ?? null;
  useEffect(() => {
    if (sendAt === null) return;
    const ms = Date.parse(sendAt) - Date.now() - SCHEDULE_THRESHOLD_MS;
    if (ms <= 0) return;
    const t = setTimeout(() => setScheduleTick(n => n + 1), ms + 100);
    return () => clearTimeout(t);
  }, [sendAt, scheduleTick]);

  const handleSendDraft = async () => {
    if (!msg || actionInFlight) return;
    const deferred = isDeferred(msg.send_at);
    setActionInFlight(true);
    if (!await confirmDialog({
      title: deferred ? 'Schedule draft' : 'Send draft',
      body: deferred
        ? 'Schedule this draft as it is?'
        : 'Send this draft as it is?',
      confirmLabel: deferred ? 'Schedule' : 'Send',
      cancelLabel: 'Cancel',
    })) {
      setActionInFlight(false);
      return;
    }
    try {
      // The server sends what it has stored, so nothing needs uploading first.
      // 202 means the draft's send_at was far enough out to be scheduled.
      const { status } = await api.drafts.send(msg.id);
      navigate(status === 202 ? '#/folder/scheduled' : '#/folder/sent');
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to send draft');
      setActionInFlight(false);
    }
  };

  const handleDiscardDraft = async () => {
    if (!msg || actionInFlight) return;
    setActionInFlight(true);
    if (!await confirmDialog({
      title: 'Discard draft',
      body: 'Permanently delete this draft? This cannot be undone.',
      confirmLabel: 'Delete',
      cancelLabel: 'Keep',
      destructive: true,
    })) {
      setActionInFlight(false);
      return;
    }
    try {
      await api.drafts.delete(msg.id);
      navigate('#/folder/drafts');
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to discard draft');
      setActionInFlight(false);
    }
  };

  const handleMove = async () => {
    if (!msg || !moveTo || actionInFlight) return;
    const destId = Number(moveTo);
    const sourceFolderId = msg.folder_id;
    setMoveTo('');
    setActionInFlight(true);
    try {
      await api.messages.patch(msg.id, { folder_id: destId });
      navigateToSourceFolder(sourceFolderId);
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to move message');
      setActionInFlight(false);
    }
  };

  const handleSnooze = async () => {
    if (!msg || !snoozeValue || actionInFlight) return;
    const until = new Date(snoozeValue).toISOString();
    const sourceFolderId = msg.folder_id;
    setActionInFlight(true);
    try {
      await api.messages.snooze(msg.id, until);
      setSnoozeOpen(false);
      navigateToSourceFolder(sourceFolderId);
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to snooze message');
      setActionInFlight(false);
    }
  };

  const handleMarkJunk = async () => {
    if (!msg || actionInFlight) return;
    const sourceFolderId = msg.folder_id;
    setActionInFlight(true);
    try {
      const result = await api.messages.markJunk(msg.id);
      if (result.blocked_sender) showToast(`Future mail from ${result.blocked_sender} goes to Junk`);
      navigateToSourceFolder(sourceFolderId);
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to mark as junk');
      setActionInFlight(false);
    }
  };

  const handleMarkNotJunk = async () => {
    if (!msg || actionInFlight) return;
    setActionInFlight(true);
    try {
      const result = await api.messages.markNotJunk(msg.id);
      if (result.blocked_sender) showToast(`${result.blocked_sender} unblocked`);
      navigate('#/inbox');
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to mark as not junk');
      setActionInFlight(false);
    }
  };

  const handleCancelSnooze = async () => {
    if (!msg || actionInFlight) return;
    setActionInFlight(true);
    try {
      const result = await api.messages.cancelSnooze(msg.id);
      navigateToSourceFolder(result.folder_id);
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to cancel snooze');
      setActionInFlight(false);
    }
  };

  const handleCancelSchedule = async () => {
    if (!msg || actionInFlight) return;
    // "Cancel" is the action here, so it cannot also be the label that backs
    // out of it — both buttons say what they do instead.
    setActionInFlight(true);
    if (!await confirmDialog({
      title: 'Cancel scheduled message',
      body: 'Cancel this scheduled message and move it to Drafts? It will not be sent.',
      confirmLabel: 'Move to Drafts',
      cancelLabel: 'Keep scheduled',
    })) {
      setActionInFlight(false);
      return;
    }
    try {
      await api.scheduled.cancel(msg.id);
      navigate('#/folder/drafts');
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to cancel scheduled message');
      setActionInFlight(false);
    }
  };

  const handleSendNow = async () => {
    if (!msg || actionInFlight) return;
    setActionInFlight(true);
    try {
      await api.scheduled.sendNow(msg.id);
      navigate('#/folder/sent');
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to send message');
      setActionInFlight(false);
    }
  };

  const handleReschedule = async () => {
    if (!msg || !rescheduleValue || actionInFlight) return;
    const sendAt = new Date(rescheduleValue).toISOString();
    setActionInFlight(true);
    try {
      await api.scheduled.reschedule(msg.id, sendAt);
      setRescheduleOpen(false);
      const updated = await api.messages.get(msg.id);
      setMsg(updated);
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to reschedule message');
    } finally {
      setActionInFlight(false);
    }
  };

  /**
   * Runs one import against MyCal and records its outcome under `key`.
   *
   * `post` receives MyCal's base URL and does the POST — the two callers differ
   * only in what they send: an attachment's bytes, or a URL for MyCal to fetch
   * itself. Everything after the response is the same for both.
   */
  const runIcsImport = async (key: string, post: (base: string) => Promise<Response>) => {
    const base = getMycalUrl().replace(/\/$/, '');
    if (!base) return;
    setIcsImportStatus(prev => ({ ...prev, [key]: 'loading' }));
    try {
      const importResp = await post(base);
      if (importResp.status === 201) {
        setIcsImportStatus(prev => ({ ...prev, [key]: 'success' }));
      } else {
        let errMsg = `Error ${importResp.status}`;
        try {
          const body = await importResp.json() as { error?: string };
          if (body.error) errMsg = body.error;
        } catch { /* ignore */ }
        setIcsImportStatus(prev => ({ ...prev, [key]: errMsg }));
      }
    } catch (e) {
      setIcsImportStatus(prev => ({
        ...prev,
        [key]: e instanceof Error ? e.message : 'Import failed',
      }));
    }
  };

  const importToMycal = (attachmentId: number) =>
    runIcsImport(attachmentKey(attachmentId), async base => {
      const dataResp = await fetch(`api/v1/attachments/${attachmentId}`);
      if (!dataResp.ok) throw new Error(`Failed to fetch attachment (${dataResp.status})`);
      const blob = await dataResp.blob();
      return fetch(`${base}/api/v1/import-single`, {
        method: 'POST',
        headers: { 'Content-Type': 'text/calendar' },
        body: blob,
      });
    });

  // The bytes are never fetched here: MyCal's import-single takes a URL and
  // fetches it itself, size-capped. That is also the only way this can work
  // from a page — reading a response from an arbitrary sender's host would need
  // that host to grant us CORS, which no calendar link does.
  const importLinkToMycal = (url: string) =>
    runIcsImport(linkKey(url), base => fetch(`${base}/api/v1/import-single`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url }),
    }));

  if (loading) return <div class="msg-detail-status">Loading…</div>;
  if (notFound) return <div class="msg-detail-status msg-detail-not-found">Not found</div>;
  if (error) return <div class="msg-detail-status msg-detail-error">{error}</div>;
  if (!msg) return null;

  const folderId = msg.folder_id;
  const hasHtml = msg.body_html !== '';
  const hasText = msg.body_text !== '';
  const hasBoth = hasHtml && hasText;
  const effectiveView = hasBoth ? bodyView : (hasHtml ? 'html' : 'text');

  const moveFolders = folders.filter(f => f.id !== folderId && !NO_MOVE_DEST_IDS.has(f.id));
  const canMove = !MANAGED_FOLDER_IDS.has(folderId);
  const canDelete = !MANAGED_FOLDER_IDS.has(folderId);
  const canSnooze = folderId === INBOX_ID || folderId === SNOOZED_ID || folderId >= 100;
  const canCancelSnooze = folderId === SNOOZED_ID;
  const canMarkJunk = folderId !== SNOOZED_ID && folderId !== SCHEDULED_ID && folderId !== DRAFTS_ID && folderId !== JUNK_ID;
  const canMarkNotJunk = folderId === JUNK_ID;
  const canCancelSchedule = folderId === SCHEDULED_ID;
  const showSendFailed = msg.send_failed && folderId !== TRASH_ID;

  // Sending a draft untouched is only offered when the server would accept it —
  // it refuses a draft with no recipient, and rejects a malformed address list.
  const canSendDraft = hasValidRecipient(msg.to_addr, msg.cc_addr, msg.bcc_addr);
  // A stored send_at defers the send rather than performing it, on the same
  // threshold the server applies (see handler.isScheduled). The threshold moves
  // with the clock, so this is re-derived on the tick below rather than only at
  // load: a detail view left open must not keep offering to schedule a send
  // that would now go out immediately.
  const draftScheduled = isDeferred(msg.send_at, scheduleTick);

  const threadEntries = thread && thread.total > 1 ? thread.items : null;

  return (
    <div class="msg-detail">
      <div class="msg-detail-actions">
        <button
          class="btn btn-ghost btn-sm msg-back-to-list"
          title="back to list"
          aria-label="back to list"
          onClick={() => navigateToSourceFolder(folderId)}
        >
          <Icon name="arrow-left" />
        </button>
        {folderId === DRAFTS_ID ? (
          <>
            <button
              class="btn btn-primary btn-sm"
              disabled={actionInFlight || !canSendDraft}
              title={canSendDraft ? undefined : 'This draft has no valid recipient'}
              onClick={() => void handleSendDraft()}
            >
              {draftScheduled ? 'Schedule' : 'Send'}
            </button>
            <button class="btn btn-ghost btn-sm" onClick={() => navigate(`#/compose?draft=${id}`)}>
              Edit
            </button>
            <button class="btn btn-danger btn-sm" data-escape-action="discard" disabled={actionInFlight} onClick={() => void handleDiscardDraft()}>
              Discard
            </button>
          </>
        ) : (
          <>
            <button class="btn btn-ghost btn-sm" onClick={() => navigate(`#/compose?reply=${id}`)}>
              Reply
            </button>
            <button class="btn btn-ghost btn-sm" onClick={() => navigate(`#/compose?replyall=${id}`)}>
              Reply All
            </button>
            <button class="btn btn-ghost btn-sm" onClick={() => navigate(`#/compose?forward=${id}`)}>
              Forward
            </button>
          </>
        )}
        <span class="toolbar-sep" />
        {canMove && (
          <div class="move-inline">
            <select
              class="move-select"
              value={moveTo}
              disabled={actionInFlight}
              onChange={e => setMoveTo((e.target as HTMLSelectElement).value)}
            >
              <option value="">Move to…</option>
              {moveFolders.map(f => (
                <option key={f.id} value={String(f.id)}>{f.name}</option>
              ))}
            </select>
            {moveTo && (
              <button class="btn btn-primary btn-sm" disabled={actionInFlight} onClick={() => void handleMove()}>
                Move
              </button>
            )}
          </div>
        )}
        {canDelete && (
          <button class="btn btn-danger btn-sm" disabled={actionInFlight} onClick={() => void handleDelete()}>
            Delete
          </button>
        )}
        {canSnooze && (
          <button
            class={`btn btn-ghost btn-sm${snoozeOpen ? ' active' : ''}`}
            disabled={actionInFlight}
            onClick={() => {
              if (!snoozeOpen) {
                const existingSnooze = canCancelSnooze && msg.snoozed_until;
                if (existingSnooze) {
                  const d = new Date(existingSnooze);
                  const pad = (n: number) => String(n).padStart(2, '0');
                  setSnoozeValue(`${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`);
                } else {
                  setSnoozeValue(defaultSnoozeDateTime());
                }
              }
              setSnoozeOpen(o => !o);
            }}
          >
            {canCancelSnooze ? 'Edit snooze' : 'Snooze'}
          </button>
        )}
        {canCancelSnooze && (
          <button class="btn btn-ghost btn-sm" disabled={actionInFlight} onClick={() => void handleCancelSnooze()}>
            Cancel snooze
          </button>
        )}
        {canCancelSnooze && msg.snoozed_until && (
          <span class="snooze-until-display" title={msg.snoozed_until}>
            until {formatDateFull(msg.snoozed_until)}
          </span>
        )}
        {canMarkJunk && (
          <button class="btn btn-ghost btn-sm" disabled={actionInFlight} onClick={() => void handleMarkJunk()}>
            Mark as junk
          </button>
        )}
        {canMarkNotJunk && (
          <button class="btn btn-ghost btn-sm" disabled={actionInFlight} onClick={() => void handleMarkNotJunk()}>
            Not junk
          </button>
        )}
        {canCancelSchedule && (
          <button
            class={`btn btn-ghost btn-sm${rescheduleOpen ? ' active' : ''}`}
            disabled={actionInFlight}
            onClick={() => {
              if (!rescheduleOpen && msg.send_at) {
                const d = new Date(msg.send_at);
                const pad = (n: number) => String(n).padStart(2, '0');
                setRescheduleValue(`${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`);
              }
              setRescheduleOpen(o => !o);
            }}
          >
            Edit schedule
          </button>
        )}
        {canCancelSchedule && (
          <button class="btn btn-ghost btn-sm" disabled={actionInFlight} onClick={() => void handleSendNow()}>
            Send now
          </button>
        )}
        {canCancelSchedule && (
          <button class="btn btn-ghost btn-sm" disabled={actionInFlight} onClick={() => void handleCancelSchedule()}>
            Cancel schedule
          </button>
        )}
        {canCancelSchedule && msg.send_at && (
          <span class="snooze-until-display" title={msg.send_at}>
            scheduled {formatDateFull(msg.send_at)}
          </span>
        )}
        <span class="ml-auto" />
        {hasBoth && (
          <div class="body-toggle">
            <span>HTML</span>
            <label class="body-toggle-control">
              <input
                type="checkbox"
                role="switch"
                aria-label="Plain text"
                checked={effectiveView === 'text'}
                onChange={e => handleBodyViewChange((e.target as HTMLInputElement).checked ? 'text' : 'html')}
              />
              <span class="body-toggle-track" aria-hidden="true" />
            </label>
            <span>Plain</span>
          </div>
        )}
        {folderId !== DRAFTS_ID && (
          <button
            class={`btn btn-ghost btn-sm${showHeaders ? ' active' : ''}`}
            onClick={() => void handleToggleHeaders()}
          >
            All headers
          </button>
        )}
        <span class="msg-folder-indicator">{folders.find(f => f.id === folderId)?.name ?? ''}</span>
      </div>

      {snoozeOpen && (
        <div class="snooze-panel">
          <span class="snooze-label">Snooze until:</span>
          <input
            type="datetime-local"
            class="snooze-input"
            value={snoozeValue}
            onInput={e => setSnoozeValue((e.target as HTMLInputElement).value)}
          />
          <button
            class="btn btn-primary btn-sm"
            disabled={!snoozeValue || actionInFlight}
            onClick={() => void handleSnooze()}
          >
            Snooze
          </button>
          <button class="btn btn-ghost btn-sm" data-escape-action="cancel" onClick={() => setSnoozeOpen(false)}>
            Cancel
          </button>
        </div>
      )}

      {rescheduleOpen && (
        <div class="snooze-panel">
          <span class="snooze-label">Send at:</span>
          <input
            type="datetime-local"
            class="snooze-input"
            value={rescheduleValue}
            onInput={e => setRescheduleValue((e.target as HTMLInputElement).value)}
          />
          <button
            class="btn btn-primary btn-sm"
            disabled={!rescheduleValue || actionInFlight}
            onClick={() => void handleReschedule()}
          >
            Reschedule
          </button>
          <button class="btn btn-ghost btn-sm" data-escape-action="cancel" onClick={() => setRescheduleOpen(false)}>
            Cancel
          </button>
        </div>
      )}

      <div class="msg-detail-header">
        <div class="msg-detail-field">
          <span class="msg-detail-label">From</span>
          <span class="msg-detail-value">
            <Sender raw={msg.from_addr} />
            <FirstTimeSenderNote warnings={msg.warnings} />
            {canAddSender && (
              <button class="btn btn-ghost btn-sm"
                title="Edit and save this sender as a contact to suppress first-time-sender warnings"
                onClick={() => navigate(`#/settings/contacts?sender=${msg.id}`)}>
                Add sender to contacts
              </button>
            )}
            {showSendFailed && (
              <span class="msg-badge-fail" title="Send failed">!</span>
            )}
          </span>
        </div>
        {msg.to_addr && (
          <div class="msg-detail-field">
            <span class="msg-detail-label">To</span>
            <span class="msg-detail-value">{msg.to_addr}</span>
          </div>
        )}
        {msg.cc_addr && (
          <div class="msg-detail-field">
            <span class="msg-detail-label">Cc</span>
            <span class="msg-detail-value">{msg.cc_addr}</span>
          </div>
        )}
        <div class="msg-detail-field">
          <span class="msg-detail-label">Date</span>
          <span class="msg-detail-value">{formatDateFull(msg.date)}</span>
        </div>
        <div class="msg-detail-field">
          <span class="msg-detail-label">Subject</span>
          <span class="msg-detail-value msg-detail-subject">{msg.subject || '(no subject)'}</span>
        </div>

        {msg.attachments.length > 0 && (
          <div class="msg-detail-field msg-detail-attachments">
            <span class="msg-detail-label">Attachments</span>
            <ul class="attachment-list">
              {msg.attachments.map(a => {
                const status = icsImportStatus[attachmentKey(a.id)];
                const risk = msg.warnings.find(w => w.kind === 'risky_attachment' && w.filename === a.filename);
                return (
                  <li key={a.id}>
                    <a
                      href={risk ? undefined : `api/v1/attachments/${a.id}`}
                      role={risk ? 'button' : undefined}
                      tabIndex={risk ? 0 : undefined}
                      onKeyDown={risk ? e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); e.currentTarget.click(); } } : undefined}
                      onClick={risk ? e => {
                        e.preventDefault();
                        void confirmDialog({ title: 'Download risky attachment?', body: <>“<bdi>{revealFormatChars(a.filename)}</bdi>” {attachmentReason(risk.reason)}. Download only if you trust its source.</>, confirmLabel: 'Download', cancelLabel: 'Cancel' }).then(ok => {
                          if (ok) {
                            const link = document.createElement('a');
                            link.href = `api/v1/attachments/${a.id}`;
                            link.download = a.filename;
                            document.body.append(link);
                            link.click();
                            link.remove();
                          }
                        });
                      } : undefined}
                      download={a.filename}
                      class="attachment-link"
                    >
                      <Icon name="paperclip" size={14} />
                      <bdi>{revealFormatChars(a.filename)}</bdi>
                      {risk && <span class="attachment-risk">Warning: {attachmentReason(risk.reason)}</span>}
                      <span class="attachment-size">({formatBytes(a.size)})</span>
                    </a>
                    {isIcs(a) && getMycalUrl() && (
                      <button
                        class="import-cal-btn"
                        disabled={status === 'loading' || status === 'success'}
                        onClick={() => void importToMycal(a.id)}
                        title="Import this event into MyCal"
                      >
                        {status === 'loading' ? 'Importing…' : status === 'success' ? 'Imported'
                          : <><Icon name="calendar-plus" size={14} /> Import to Calendar</>}
                      </button>
                    )}
                    {isIcs(a) && status && status !== 'loading' && status !== 'success' && (
                      <span class="import-cal-error">{status}</span>
                    )}
                  </li>
                );
              })}
            </ul>
          </div>
        )}

        {/*
          Calendar links found in the HTML body. They cannot get a button beside
          them where they appear: the body renders in a sandboxed iframe, so the
          list is repeated here instead.
        */}
        {calendarLinks.length > 0 && getMycalUrl() && (
          <div class="msg-detail-field msg-detail-attachments">
            <span class="msg-detail-label">Calendar links</span>
            <ul class="attachment-list">
              {calendarLinks.map(link => {
                const status = icsImportStatus[linkKey(link.url)];
                return (
                  <li key={link.url} class="calendar-link-row">
                    {/* Deliberately not an <a>: this is a URL from an
                        unauthenticated sender, shown so it can be read.
                        The URL shown is the one that would be fetched, not the
                        href it was found as — those differ for a webcal link,
                        and the point of showing it is to be read before
                        clicking. The title names the other one when they
                        differ. */}
                    <span
                      class="attachment-link attachment-link-static"
                      title={link.original === link.url ? undefined : `Link in message: ${link.original}`}
                    >
                      <span class="calendar-link-label">{link.label}</span>
                      <span class="attachment-size calendar-link-url">{link.url}</span>
                    </span>
                    <button
                      class="import-cal-btn"
                      disabled={status === 'loading' || status === 'success'}
                      onClick={() => void importLinkToMycal(link.url)}
                      // No aria-label naming the link: it would override the
                      // button's text, and that text is the only thing that
                      // announces "Importing…" / "Imported". An ambiguous name
                      // among rows costs less than a state change nobody hears.
                      title="Import this event into MyCal"
                    >
                      {status === 'loading' ? 'Importing…' : status === 'success' ? 'Imported'
                        : <><Icon name="calendar-plus" size={14} /> Import to Calendar</>}
                    </button>
                    {status && status !== 'loading' && status !== 'success' && (
                      <span class="import-cal-error">{status}</span>
                    )}
                  </li>
                );
              })}
            </ul>
          </div>
        )}
      </div>

      {showHeaders && (
        <div class="msg-headers-panel">
          {headersLoading
            ? <div class="msg-headers-loading">Loading…</div>
            : <pre class="msg-headers-pre">{allHeaders}</pre>
          }
        </div>
      )}

      <div class="msg-detail-body" ref={bodyRef}>
        <WarningBanner warnings={msg.warnings} />
        {effectiveView === 'html' ? (
          <>
            {msg.has_external_images && !externalImages && (
              <div class="external-images-bar">
                <span>External images blocked.</span>
                <button
                  class="btn btn-ghost btn-sm"
                  onClick={() => setExternalImages(true)}
                >
                  Load external images
                </button>
              </div>
            )}
            <BodyIframe id={msg.id} externalImages={externalImages} />
          </>
        ) : (
          <pre class="body-text">{msg.body_text}</pre>
        )}
      </div>

      {threadEntries && (
        <>
          <div class="thread-resize-handle" onMouseDown={handleResizeStart} />
          <div
            ref={threadStripRef}
            class="thread-strip"
            style={threadHeight !== null ? { height: `${threadHeight}px`, maxHeight: 'none' } : undefined}
          >
            <div class="thread-strip-header">
              <span>Thread ({thread!.truncated ? `${thread!.total}+` : thread!.total} messages)</span>
              {thread!.truncated && (
                <span class="thread-truncated">— thread too long, showing oldest {thread!.total}</span>
              )}
            </div>
            <ul class="thread-list">
              {threadEntries.map(entry => (
                <ThreadEntry
                  key={entry.id}
                  entry={entry}
                  isCurrent={entry.id === id}
                  expanded={expandedThreadId === entry.id}
                  expandedMsg={expandedThreadId === entry.id ? expandedMsg : null}
                  expandedLoading={expandedThreadId === entry.id && expandedLoading}
                  onToggle={entryId => void handleExpandThread(entryId)}
                  onOpen={entryId => navigate(`#/message/${entryId}`)}
                />
              ))}
            </ul>
          </div>
        </>
      )}
    </div>
  );
}
