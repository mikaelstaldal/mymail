import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import { api } from '../../api/client.js';
import { confirmDialog } from '../../util/confirm.js';
import { Icon } from '../../components/Icon.js';
import type { components } from '../../api/types.js';

type Folder = components['schemas']['Folder'];

export function Folders() {
  const [allFolders, setAllFolders] = useState<Folder[]>([]);
  const [userFolders, setUserFolders] = useState<Folder[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [createError, setCreateError] = useState<string | null>(null);
  const [savingCreate, setSavingCreate] = useState(false);
  const [editId, setEditId] = useState<number | null>(null);
  const [editName, setEditName] = useState('');
  const [editError, setEditError] = useState<string | null>(null);
  const [savingEdit, setSavingEdit] = useState(false);
  const [overIdx, setOverIdx] = useState<number | null>(null);
  const dragIdxRef = useRef<number | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.folders.list();
      const sorted = [...res.items].sort((a, b) => a.position - b.position);
      setAllFolders(sorted);
      setUserFolders(sorted.filter(f => f.id >= 100));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load folders');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  async function createFolder() {
    if (!newName.trim()) return;
    setSavingCreate(true);
    setCreateError(null);
    try {
      await api.folders.create(newName.trim());
      setCreating(false);
      setNewName('');
      await load();
    } catch (e) {
      setCreateError(e instanceof Error ? e.message : 'Failed to create folder');
    } finally {
      setSavingCreate(false);
    }
  }

  function startEdit(folder: Folder) {
    setEditId(folder.id);
    setEditName(folder.name);
    setEditError(null);
  }

  async function saveEdit() {
    if (!editName.trim() || editId === null) return;
    setSavingEdit(true);
    setEditError(null);
    try {
      await api.folders.patch(editId, { name: editName.trim() });
      setEditId(null);
      await load();
    } catch (e) {
      setEditError(e instanceof Error ? e.message : 'Failed to rename folder');
    } finally {
      setSavingEdit(false);
    }
  }

  async function deleteFolder(id: number, name: string) {
    if (!await confirmDialog({
      title: 'Delete folder',
      body: `Delete folder "${name}"? Messages in it will be moved to Trash.`,
      confirmLabel: 'Delete',
      cancelLabel: 'Keep',
      destructive: true,
    })) return;
    setError(null);
    try {
      await api.folders.delete(id);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete folder');
    }
  }

  async function handleDrop(toIdx: number) {
    const fromIdx = dragIdxRef.current;
    dragIdxRef.current = null;
    setOverIdx(null);
    if (fromIdx === null || fromIdx === toIdx) return;
    const newUser = [...userFolders];
    const [moved] = newUser.splice(fromIdx, 1);
    newUser.splice(toIdx, 0, moved);
    setUserFolders(newUser);
    const builtins = allFolders.filter(f => f.id < 100);
    const ids = [...builtins.map(f => f.id), ...newUser.map(f => f.id)];
    try {
      await api.folders.reorder(ids);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to reorder');
      void load();
    }
  }

  if (loading) return <div class="settings-loading">Loading…</div>;

  return (
    <div>
      {error && <div class="settings-error">{error}</div>}
      <div class="settings-toolbar">
        <button
          class="btn btn-primary btn-sm"
          onClick={() => { setCreating(true); setNewName(''); setCreateError(null); }}
          disabled={creating}
        >
          + New Folder
        </button>
      </div>
      {creating && (
        <div class="settings-form">
          {createError && <div class="settings-error">{createError}</div>}
          <div class="settings-field">
            <label>Folder Name</label>
            <input
              type="text"
              value={newName}
              onInput={e => setNewName((e.target as HTMLInputElement).value)}
              placeholder="My Folder"
              // eslint-disable-next-line jsx-a11y/no-autofocus
              autoFocus
              onKeyDown={e => {
                if (e.key === 'Enter') void createFolder();
                if (e.key === 'Escape') setCreating(false);
              }}
            />
          </div>
          <div class="settings-form-actions">
            <button
              class="btn btn-primary btn-sm"
              onClick={() => void createFolder()}
              disabled={savingCreate || !newName.trim()}
            >
              {savingCreate ? 'Creating…' : 'Create'}
            </button>
            <button class="btn btn-ghost btn-sm" onClick={() => setCreating(false)} disabled={savingCreate}>
              Cancel
            </button>
          </div>
        </div>
      )}
      {userFolders.length === 0 && !creating && (
        <div class="settings-empty">No custom folders yet.</div>
      )}
      {userFolders.map((folder, idx) => (
        editId === folder.id ? (
          <div key={folder.id} class="settings-form" style="margin-bottom:6px">
            {editError && <div class="settings-error">{editError}</div>}
            <div class="settings-field">
              <label>Folder Name</label>
              <input
                type="text"
                value={editName}
                onInput={e => setEditName((e.target as HTMLInputElement).value)}
                autoFocus
                onKeyDown={e => {
                  if (e.key === 'Enter') void saveEdit();
                  if (e.key === 'Escape') setEditId(null);
                }}
              />
            </div>
            <div class="settings-form-actions">
              <button
                class="btn btn-primary btn-sm"
                onClick={() => void saveEdit()}
                disabled={savingEdit || !editName.trim()}
              >
                {savingEdit ? 'Saving…' : 'Save'}
              </button>
              <button class="btn btn-ghost btn-sm" onClick={() => setEditId(null)} disabled={savingEdit}>
                Cancel
              </button>
            </div>
          </div>
        ) : (
          <div
            key={folder.id}
            class={`settings-item${overIdx === idx ? ' drag-over' : ''}`}
            draggable
            onDragStart={() => { dragIdxRef.current = idx; }}
            onDragOver={e => { e.preventDefault(); setOverIdx(idx); }}
            onDragLeave={() => setOverIdx(null)}
            onDrop={e => { e.preventDefault(); void handleDrop(idx); }}
          >
            <Icon name="grip-vertical" size={18} class="settings-drag-handle" title="Drag to reorder" />
            <div class="settings-item-info">
              <div class="settings-item-name">{folder.name}</div>
            </div>
            <button class="btn btn-ghost btn-sm" onClick={() => startEdit(folder)}>Rename</button>
            <button class="btn btn-danger btn-sm" onClick={() => void deleteFolder(folder.id, folder.name)}>
              Delete
            </button>
          </div>
        )
      ))}
    </div>
  );
}
