import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import { api } from '../../api/client.js';
import { confirmDialog } from '../../util/confirm.js';
import { Icon } from '../../components/Icon.js';
import type { components } from '../../api/types.js';

type Filter = components['schemas']['Filter'];
type FilterRequest = components['schemas']['FilterRequest'];
type Folder = components['schemas']['Folder'];

const ACTION_LABELS: Record<Filter['action'], string> = {
  move: 'Move to folder',
  trash: 'Move to Trash',
  mark_read: 'Mark as read',
  drop: 'Drop (discard)',
};

function isMoveTarget(f: Folder): boolean {
  return f.id === 1 || f.id === 4 || f.id === 7 || f.id >= 100;
}

interface FormState {
  name: string;
  match_from: string;
  match_to: string;
  match_subject: string;
  action: Filter['action'];
  folder_id: number | null;
  stop: boolean;
}

const EMPTY_FORM: FormState = {
  name: '',
  match_from: '',
  match_to: '',
  match_subject: '',
  action: 'move',
  folder_id: null,
  stop: true,
};

interface FormProps {
  form: FormState;
  onChange: (f: FormState) => void;
  folders: Folder[];
  error: string | null;
  saving: boolean;
  onSave: () => void;
  onCancel: () => void;
}

function FilterForm({ form, onChange, folders, error, saving, onSave, onCancel }: FormProps) {
  const moveTargets = folders.filter(isMoveTarget);

  return (
    <div class="settings-form">
      {error && <div class="settings-error">{error}</div>}
      <div class="settings-field">
        <label>Name (optional)</label>
        <input
          type="text"
          value={form.name}
          onInput={e => onChange({ ...form, name: (e.target as HTMLInputElement).value })}
          placeholder="Filter name"
        />
      </div>
      <div class="settings-field">
        <label>From</label>
        <input
          type="text"
          value={form.match_from}
          onInput={e => onChange({ ...form, match_from: (e.target as HTMLInputElement).value })}
          placeholder="Match sender address or domain"
        />
      </div>
      <div class="settings-field">
        <label>To / Cc</label>
        <input
          type="text"
          value={form.match_to}
          onInput={e => onChange({ ...form, match_to: (e.target as HTMLInputElement).value })}
          placeholder="Match recipient address or domain"
        />
      </div>
      <div class="settings-field">
        <label>Subject</label>
        <input
          type="text"
          value={form.match_subject}
          onInput={e => onChange({ ...form, match_subject: (e.target as HTMLInputElement).value })}
          placeholder="Match subject text"
        />
      </div>
      <div class="settings-field">
        <label>Action</label>
        <select
          value={form.action}
          onChange={e => {
            const action = (e.target as HTMLSelectElement).value as Filter['action'];
            onChange({ ...form, action, folder_id: action === 'move' ? (moveTargets[0]?.id ?? null) : null });
          }}
        >
          {(Object.entries(ACTION_LABELS) as [Filter['action'], string][]).map(([value, label]) => (
            <option key={value} value={value}>{label}</option>
          ))}
        </select>
      </div>
      {form.action === 'move' && (
        <div class="settings-field">
          <label>Destination Folder</label>
          <select
            value={form.folder_id ?? ''}
            onChange={e => {
              const v = (e.target as HTMLSelectElement).value;
              onChange({ ...form, folder_id: v ? Number(v) : null });
            }}
          >
            <option value="">— select folder —</option>
            {moveTargets.map(f => (
              <option key={f.id} value={f.id}>{f.name}</option>
            ))}
          </select>
        </div>
      )}
      <div class="settings-field-check">
        <input
          type="checkbox"
          id="filter-stop"
          checked={form.stop}
          onChange={e => onChange({ ...form, stop: (e.target as HTMLInputElement).checked })}
        />
        <label for="filter-stop">Stop processing further filters after this one matches</label>
      </div>
      <div class="settings-form-actions">
        <button class="btn btn-primary btn-sm" onClick={onSave} disabled={saving}>
          {saving ? 'Saving…' : 'Save'}
        </button>
        <button class="btn btn-ghost btn-sm" onClick={onCancel} disabled={saving}>Cancel</button>
      </div>
    </div>
  );
}

export function Filters() {
  const [items, setItems] = useState<Filter[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [formMode, setFormMode] = useState<null | 'create' | number>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [overIdx, setOverIdx] = useState<number | null>(null);
  const dragIdxRef = useRef<number | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [filtersRes, foldersRes] = await Promise.all([
        api.filters.list(),
        api.folders.list(),
      ]);
      setItems(filtersRes.items);
      setFolders(foldersRes.items);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  function startCreate() {
    const firstTarget = folders.find(isMoveTarget);
    setFormMode('create');
    setForm({ ...EMPTY_FORM, folder_id: firstTarget?.id ?? null });
    setFormError(null);
  }

  function startEdit(item: Filter) {
    setFormMode(item.id);
    setForm({
      name: item.name,
      match_from: item.match_from,
      match_to: item.match_to,
      match_subject: item.match_subject,
      action: item.action,
      folder_id: item.folder_id ?? null,
      stop: item.stop,
    });
    setFormError(null);
  }

  function cancelForm() {
    setFormMode(null);
    setFormError(null);
  }

  async function submitForm() {
    if (!form.match_from.trim() && !form.match_to.trim() && !form.match_subject.trim()) {
      setFormError('At least one of From, To/Cc, or Subject must be specified.');
      return;
    }
    if (form.action === 'move' && !form.folder_id) {
      setFormError('Select a destination folder for the Move action.');
      return;
    }
    setSaving(true);
    setFormError(null);
    try {
      const body: FilterRequest = {
        name: form.name,
        match_from: form.match_from,
        match_to: form.match_to,
        match_subject: form.match_subject,
        action: form.action,
        folder_id: form.action === 'move' ? form.folder_id : null,
        stop: form.stop,
      };
      if (formMode === 'create') {
        await api.filters.create(body);
      } else if (typeof formMode === 'number') {
        await api.filters.update(formMode, body);
      }
      setFormMode(null);
      await load();
    } catch (e) {
      setFormError(e instanceof Error ? e.message : 'Failed to save filter');
    } finally {
      setSaving(false);
    }
  }

  async function deleteFilter(id: number) {
    if (!await confirmDialog({
      title: 'Delete filter',
      body: 'Delete this filter? This cannot be undone.',
      confirmLabel: 'Delete',
      cancelLabel: 'Keep',
      destructive: true,
    })) return;
    setError(null);
    try {
      await api.filters.delete(id);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete filter');
    }
  }

  async function handleDrop(toIdx: number) {
    const fromIdx = dragIdxRef.current;
    dragIdxRef.current = null;
    setOverIdx(null);
    if (fromIdx === null || fromIdx === toIdx) return;
    const newItems = [...items];
    const [moved] = newItems.splice(fromIdx, 1);
    newItems.splice(toIdx, 0, moved);
    setItems(newItems);
    try {
      await api.filters.reorder(newItems.map(i => i.id));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to reorder');
      void load();
    }
  }

  function getFolderName(id: number | null | undefined): string {
    if (!id) return '';
    return folders.find(f => f.id === id)?.name ?? '';
  }

  function isFolderMissing(id: number | null | undefined): boolean {
    if (!id) return false;
    return !folders.some(f => f.id === id);
  }

  if (loading) return <div class="settings-loading">Loading…</div>;

  return (
    <div>
      {error && <div class="settings-error">{error}</div>}
      <div class="settings-toolbar">
        <button class="btn btn-primary btn-sm" onClick={startCreate} disabled={formMode === 'create'}>
          + Add Filter
        </button>
      </div>
      {formMode === 'create' && (
        <FilterForm
          form={form}
          onChange={setForm}
          folders={folders}
          error={formError}
          saving={saving}
          onSave={() => void submitForm()}
          onCancel={cancelForm}
        />
      )}
      {items.length === 0 && formMode !== 'create' && (
        <div class="settings-empty">No filters configured.</div>
      )}
      {items.map((item, idx) => (
        typeof formMode === 'number' && formMode === item.id ? (
          <FilterForm
            key={item.id}
            form={form}
            onChange={setForm}
            folders={folders}
            error={formError}
            saving={saving}
            onSave={() => void submitForm()}
            onCancel={cancelForm}
          />
        ) : (
          <div
            key={item.id}
            class={`settings-item${overIdx === idx ? ' drag-over' : ''}`}
            draggable
            onDragStart={() => { dragIdxRef.current = idx; }}
            onDragOver={e => { e.preventDefault(); setOverIdx(idx); }}
            onDragLeave={() => setOverIdx(null)}
            onDrop={e => { e.preventDefault(); void handleDrop(idx); }}
          >
            <Icon name="grip-vertical" size={18} class="settings-drag-handle" title="Drag to reorder" />
            <div class="settings-item-info">
              <div class="settings-item-name">{item.name || `Filter #${item.id}`}</div>
              <div class="settings-item-meta">
                {[
                  item.match_from && `From: ${item.match_from}`,
                  item.match_to && `To/Cc: ${item.match_to}`,
                  item.match_subject && `Subject: ${item.match_subject}`,
                ].filter(Boolean).join(' · ')}
                {' '}<Icon name="arrow-right" size={13} class="settings-meta-arrow" />{' '}
                {ACTION_LABELS[item.action]}
                {item.action === 'move' && item.folder_id && (
                  isFolderMissing(item.folder_id)
                    ? <span class="filter-folder-missing"> (folder deleted)</span>
                    : ` (${getFolderName(item.folder_id)})`
                )}
              </div>
            </div>
            <button class="btn btn-ghost btn-sm" onClick={() => startEdit(item)}>Edit</button>
            <button class="btn btn-danger btn-sm" onClick={() => void deleteFilter(item.id)}>Delete</button>
          </div>
        )
      ))}
    </div>
  );
}
