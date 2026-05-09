import { useState, useEffect, useCallback } from 'preact/hooks';
import { api } from '../api/client.js';
import { navigate } from '../router.js';
import { MessageList } from '../components/MessageList.js';
import type { components } from '../api/types.js';

type Folder = components['schemas']['Folder'];
type MessageSummary = components['schemas']['MessageSummary'];

const PAGE_SIZE = 50;
const TRASH_ID = 4;
const JUNK_ID = 7;
// Drafts (3), Scheduled (5), Snoozed (6) cannot be move targets
const NO_MOVE_IDS = new Set([3, 5, 6]);

interface FolderViewProps {
  folder: Folder;
  folders: Folder[];
}

export function FolderView({ folder, folders }: FolderViewProps) {
  const [items, setItems] = useState<MessageSummary[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set());
  const [moveTo, setMoveTo] = useState('');

  const load = useCallback(async (newOffset: number) => {
    setLoading(true);
    setError(null);
    try {
      const result = await api.folders.listMessages(folder.id, PAGE_SIZE, newOffset);
      setItems(result.items);
      setTotal(result.total);
      setOffset(newOffset);
      setSelectedIds(new Set());
      setMoveTo('');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load messages');
    } finally {
      setLoading(false);
    }
  }, [folder.id]);

  useEffect(() => { void load(0); }, [load]);

  const handleMarkAllRead = async () => {
    try {
      await api.folders.markAllRead(folder.id);
      setItems(prev => prev.map(m => ({ ...m, read: true })));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to mark all read');
    }
  };

  const handleEmpty = async () => {
    const label = folder.id === TRASH_ID ? 'Trash' : 'Junk';
    if (!confirm(`Permanently delete all messages in ${label}? This cannot be undone.`)) return;
    try {
      await api.folders.deleteAllMessages(folder.id);
      void load(0);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to empty folder');
    }
  };

  const handleBulkMark = async (read: boolean) => {
    const ids = [...selectedIds];
    try {
      await api.messages.bulkUpdate(ids, { read });
      setItems(prev => prev.map(m => selectedIds.has(m.id) ? { ...m, read } : m));
      setSelectedIds(new Set());
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to update messages');
    }
  };

  const handleBulkDelete = async () => {
    const ids = [...selectedIds];
    try {
      await api.messages.delete(ids);
      void load(0);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete messages');
    }
  };

  const handleBulkMove = async () => {
    if (!moveTo) return;
    const ids = [...selectedIds];
    const destId = Number(moveTo);
    setMoveTo('');
    try {
      await api.messages.move(ids, destId);
      void load(0);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to move messages');
    }
  };

  const toggleSelect = (id: number) => {
    setSelectedIds(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  };

  const toggleSelectAll = () => {
    if (items.every(m => selectedIds.has(m.id))) {
      setSelectedIds(new Set());
    } else {
      setSelectedIds(new Set(items.map(m => m.id)));
    }
  };

  const canEmpty = folder.id === TRASH_ID || folder.id === JUNK_ID;
  const hasPrev = offset > 0;
  const hasNext = offset + PAGE_SIZE < total;
  const moveFolders = folders.filter(f => f.id !== folder.id && !NO_MOVE_IDS.has(f.id));
  const pageStart = total === 0 ? 0 : offset + 1;
  const pageEnd = Math.min(offset + PAGE_SIZE, total);

  return (
    <div class="folder-view">
      <div class="folder-toolbar">
        <button class="btn btn-ghost btn-sm" onClick={() => void handleMarkAllRead()}>
          Mark all read
        </button>
        {canEmpty && (
          <button class="btn btn-danger btn-sm" onClick={() => void handleEmpty()}>
            Empty
          </button>
        )}
        <span class="ml-auto" />
        {total > 0 && (
          <span class="pagination-info">{pageStart}–{pageEnd} of {total}</span>
        )}
        <button
          class="btn btn-ghost btn-sm btn-icon"
          disabled={!hasPrev}
          onClick={() => void load(offset - PAGE_SIZE)}
          aria-label="Previous page"
        >
          ‹
        </button>
        <button
          class="btn btn-ghost btn-sm btn-icon"
          disabled={!hasNext}
          onClick={() => void load(offset + PAGE_SIZE)}
          aria-label="Next page"
        >
          ›
        </button>
      </div>

      {selectedIds.size > 0 && (
        <div class="bulk-toolbar">
          <span class="bulk-count">{selectedIds.size} selected</span>
          <div class="toolbar-sep" />
          <button class="btn btn-ghost btn-sm" onClick={() => void handleBulkMark(true)}>
            Mark read
          </button>
          <button class="btn btn-ghost btn-sm" onClick={() => void handleBulkMark(false)}>
            Mark unread
          </button>
          <button class="btn btn-danger btn-sm" onClick={() => void handleBulkDelete()}>
            Delete
          </button>
          <div class="toolbar-sep" />
          <select
            class="move-select"
            value={moveTo}
            onChange={(e) => setMoveTo((e.target as HTMLSelectElement).value)}
          >
            <option value="">Move to…</option>
            {moveFolders.map(f => (
              <option key={f.id} value={String(f.id)}>{f.name}</option>
            ))}
          </select>
          {moveTo && (
            <button class="btn btn-primary btn-sm" onClick={() => void handleBulkMove()}>
              Move
            </button>
          )}
        </div>
      )}

      {error && <div class="folder-error">{error}</div>}

      {loading ? (
        <div class="folder-status">Loading…</div>
      ) : items.length === 0 ? (
        <div class="folder-status folder-empty">No messages</div>
      ) : (
        <div class="msg-list-wrap">
          <MessageList
            items={items}
            selectedIds={selectedIds}
            onToggleSelect={toggleSelect}
            onToggleSelectAll={toggleSelectAll}
            onRowClick={(id) => navigate(`#/message/${id}`)}
          />
        </div>
      )}
    </div>
  );
}
