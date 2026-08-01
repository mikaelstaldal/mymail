import { useState, useEffect, useRef } from 'preact/hooks';
import { api, NotFoundError } from '../api/client.js';
import { navigate } from '../router.js';
import { showToast } from '../util/toast.js';
import { getMycalUrl, isDemo } from '../util/config.js';
import { formatDateFull, formatDateAdaptive } from '../util/date.js';
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

function senderName(addr: string): string {
  const m = addr.match(/^([^<>]+?)\s*<[^>]+>$/);
  return m ? m[1].trim() : addr;
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
  return (
    <li class={`thread-entry${isCurrent ? ' thread-current' : ''}`}>
      {/* The two buttons are siblings: an Open button nested inside the
          expand button would be invalid markup and unreachable by click. */}
      <div class="thread-entry-row">
        <button
          class="thread-entry-btn"
          onClick={() => !isCurrent && onToggle(entry.id)}
          disabled={isCurrent}
        >
          <span class="thread-from">{senderName(entry.from_addr)}</span>
          <span class="thread-subject">{entry.subject || '(no subject)'}</span>
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
                <span class="msg-detail-value">{expandedMsg.from_addr}</span>
                <span class="thread-expanded-sep" />
                <span class="msg-detail-label">Date</span>
                <span class="msg-detail-value">{formatDateFull(expandedMsg.date)}</span>
              </div>
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
  const [icsImportStatus, setIcsImportStatus] = useState<Record<number, 'loading' | 'success' | string>>({});
  const msgDetailRef = useRef<HTMLDivElement>(null);
  const threadStripRef = useRef<HTMLDivElement>(null);
  const [threadHeight, setThreadHeight] = useState<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setNotFound(false);
    setError(null);
    setMsg(null);
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

    api.messages.get(id).then(m => {
      if (cancelled) return;
      setMsg(m);
      setLoading(false);
      if (!m.read) {
        void api.messages.patch(id, { read: true }).then(() => {
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
        void api.messages.patch(entryId, { read: true }).then(() => {
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
    const startHeight = threadStripRef.current?.offsetHeight ?? 200;
    const containerHeight = msgDetailRef.current?.offsetHeight ?? 600;
    document.body.style.cursor = 'ns-resize';
    document.body.style.userSelect = 'none';
    const onMove = (ev: MouseEvent) => {
      const delta = startY - ev.clientY;
      setThreadHeight(Math.max(60, Math.min(startHeight + delta, containerHeight * 0.8)));
    };
    const onUp = () => {
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
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

  const handleDelete = async () => {
    if (!msg || actionInFlight) return;
    const isPermanent = msg.folder_id === JUNK_ID || msg.folder_id === TRASH_ID;
    if (!confirm(isPermanent
      ? 'Permanently delete this message? This cannot be undone.'
      : 'Delete this message?')) return;
    const sourceFolderId = msg.folder_id;
    setActionInFlight(true);
    try {
      await api.messages.deleteSingle(msg.id);
      navigateToSourceFolder(sourceFolderId);
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'Failed to delete message');
      setActionInFlight(false);
    }
  };

  const handleDiscardDraft = async () => {
    if (!msg || actionInFlight) return;
    if (!confirm('Discard this draft?')) return;
    setActionInFlight(true);
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
      await api.messages.markJunk(msg.id);
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
      await api.messages.markNotJunk(msg.id);
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
    if (!confirm('Cancel this scheduled message and move it to Drafts?')) return;
    setActionInFlight(true);
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

  const importToMycal = async (attachmentId: number) => {
    const base = getMycalUrl().replace(/\/$/, '');
    if (!base) return;
    setIcsImportStatus(prev => ({ ...prev, [attachmentId]: 'loading' }));
    try {
      const dataResp = await fetch(`api/v1/attachments/${attachmentId}`);
      if (!dataResp.ok) throw new Error(`Failed to fetch attachment (${dataResp.status})`);
      const blob = await dataResp.blob();
      const importResp = await fetch(`${base}/api/v1/import-single`, {
        method: 'POST',
        headers: { 'Content-Type': 'text/calendar' },
        body: blob,
      });
      if (importResp.status === 201) {
        setIcsImportStatus(prev => ({ ...prev, [attachmentId]: 'success' }));
      } else {
        let errMsg = `Error ${importResp.status}`;
        try {
          const body = await importResp.json() as { error?: string };
          if (body.error) errMsg = body.error;
        } catch { /* ignore */ }
        setIcsImportStatus(prev => ({ ...prev, [attachmentId]: errMsg }));
      }
    } catch (e) {
      setIcsImportStatus(prev => ({
        ...prev,
        [attachmentId]: e instanceof Error ? e.message : 'Import failed',
      }));
    }
  };

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

  const threadEntries = thread && thread.total > 1 ? thread.items : null;

  return (
    <div class="msg-detail" ref={msgDetailRef}>
      <div class="msg-detail-actions">
        {folderId === DRAFTS_ID ? (
          <>
            <button class="btn btn-primary btn-sm" onClick={() => navigate(`#/compose?draft=${id}`)}>
              Edit
            </button>
            <button class="btn btn-danger btn-sm" disabled={actionInFlight} onClick={() => void handleDiscardDraft()}>
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
              if (!snoozeOpen && canCancelSnooze && msg.snoozed_until) {
                const d = new Date(msg.snoozed_until);
                const pad = (n: number) => String(n).padStart(2, '0');
                setSnoozeValue(`${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`);
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
          <button class="btn btn-ghost btn-sm" onClick={() => setSnoozeOpen(false)}>
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
          <button class="btn btn-ghost btn-sm" onClick={() => setRescheduleOpen(false)}>
            Cancel
          </button>
        </div>
      )}

      <div class="msg-detail-header">
        <div class="msg-detail-field">
          <span class="msg-detail-label">From</span>
          <span class="msg-detail-value">
            {msg.from_addr}
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
                const status = icsImportStatus[a.id];
                return (
                  <li key={a.id}>
                    <a
                      href={`api/v1/attachments/${a.id}`}
                      download={a.filename}
                      class="attachment-link"
                    >
                      📎 {a.filename}
                      <span class="attachment-size">({formatBytes(a.size)})</span>
                    </a>
                    {isIcs(a) && getMycalUrl() && (
                      <button
                        class="import-cal-btn"
                        disabled={status === 'loading' || status === 'success'}
                        onClick={() => void importToMycal(a.id)}
                        title="Import this event into MyCal"
                      >
                        {status === 'loading' ? 'Importing…' : status === 'success' ? 'Imported' : '📅 Import to Calendar'}
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
      </div>

      {showHeaders && (
        <div class="msg-headers-panel">
          {headersLoading
            ? <div class="msg-headers-loading">Loading…</div>
            : <pre class="msg-headers-pre">{allHeaders}</pre>
          }
        </div>
      )}

      {hasBoth && (
        <div class="body-toggle">
          <button
            class={`btn btn-ghost btn-sm${effectiveView === 'html' ? ' active' : ''}`}
            onClick={() => handleBodyViewChange('html')}
          >
            HTML
          </button>
          <button
            class={`btn btn-ghost btn-sm${effectiveView === 'text' ? ' active' : ''}`}
            onClick={() => handleBodyViewChange('text')}
          >
            Plain
          </button>
        </div>
      )}

      <div class="msg-detail-body">
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
