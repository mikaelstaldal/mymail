import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import { api } from '../../api/client.js';
import type { components } from '../../api/types.js';

type Identity = components['schemas']['Identity'];
type IdentityRequest = components['schemas']['IdentityRequest'];

interface FormState {
  name: string;
  address: string;
  is_default: boolean;
  signature: string;
}

const EMPTY_FORM: FormState = { name: '', address: '', is_default: false, signature: '' };

interface FormProps {
  form: FormState;
  onChange: (f: FormState) => void;
  showDefaultOption: boolean;
  error: string | null;
  saving: boolean;
  onSave: () => void;
  onCancel: () => void;
}

function IdentityForm({ form, onChange, showDefaultOption, error, saving, onSave, onCancel }: FormProps) {
  return (
    <div class="settings-form">
      {error && <div class="settings-error">{error}</div>}
      <div class="settings-field">
        <label>Name</label>
        <input
          type="text"
          value={form.name}
          onInput={e => onChange({ ...form, name: (e.target as HTMLInputElement).value })}
          placeholder="Display name"
        />
      </div>
      <div class="settings-field">
        <label>Email Address</label>
        <input
          type="email"
          value={form.address}
          onInput={e => onChange({ ...form, address: (e.target as HTMLInputElement).value })}
          placeholder="user@example.com"
        />
      </div>
      {showDefaultOption && (
        <div class="settings-field-check">
          <input
            type="checkbox"
            id="idf-default"
            checked={form.is_default}
            onChange={e => onChange({ ...form, is_default: (e.target as HTMLInputElement).checked })}
          />
          <label for="idf-default">Set as default identity</label>
        </div>
      )}
      <div class="settings-field">
        <label>Signature</label>
        <textarea
          value={form.signature}
          onInput={e => onChange({ ...form, signature: (e.target as HTMLTextAreaElement).value })}
          placeholder="Plain text signature (optional)"
          rows={4}
        />
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

export function Identities() {
  const [items, setItems] = useState<Identity[]>([]);
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
      const res = await api.identities.list();
      setItems(res.items);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load identities');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  function startCreate() {
    setFormMode('create');
    setForm(EMPTY_FORM);
    setFormError(null);
  }

  function startEdit(item: Identity) {
    setFormMode(item.id);
    setForm({ name: item.name, address: item.address, is_default: item.is_default, signature: item.signature });
    setFormError(null);
  }

  function cancelForm() {
    setFormMode(null);
    setFormError(null);
  }

  async function submitForm() {
    setSaving(true);
    setFormError(null);
    try {
      if (formMode === 'create') {
        const body: IdentityRequest = {
          name: form.name,
          address: form.address,
          signature: form.signature,
          ...(form.is_default ? { is_default: true } : {}),
        };
        await api.identities.create(body);
      } else if (typeof formMode === 'number') {
        // Omit is_default on edit: the API leaves default status unchanged when absent.
        const body: IdentityRequest = {
          name: form.name,
          address: form.address,
          signature: form.signature,
        };
        await api.identities.update(formMode, body);
      }
      setFormMode(null);
      await load();
    } catch (e) {
      setFormError(e instanceof Error ? e.message : 'Failed to save identity');
    } finally {
      setSaving(false);
    }
  }

  async function makeDefault(item: Identity) {
    setError(null);
    try {
      await api.identities.update(item.id, {
        name: item.name,
        address: item.address,
        signature: item.signature,
        is_default: true,
      });
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to update identity');
    }
  }

  async function deleteIdentity(id: number) {
    if (!confirm('Delete this identity?')) return;
    setError(null);
    try {
      await api.identities.delete(id);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete identity');
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
      await api.identities.reorder(newItems.map(i => i.id));
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
          onClick={startCreate}
          disabled={formMode === 'create'}
        >
          + Add Identity
        </button>
      </div>
      {formMode === 'create' && (
        <IdentityForm
          form={form}
          onChange={setForm}
          showDefaultOption={true}
          error={formError}
          saving={saving}
          onSave={() => void submitForm()}
          onCancel={cancelForm}
        />
      )}
      {items.length === 0 && formMode !== 'create' && (
        <div class="settings-empty">No identities configured. Add one to start sending mail.</div>
      )}
      {items.map((item, idx) => (
        typeof formMode === 'number' && formMode === item.id ? (
          <IdentityForm
            key={item.id}
            form={form}
            onChange={setForm}
            showDefaultOption={false}
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
            <span class="settings-drag-handle" title="Drag to reorder">⠿</span>
            <div class="settings-item-info">
              <div class="settings-item-name">{item.name}</div>
              <div class="settings-item-meta">{item.address}</div>
            </div>
            {item.is_default && <span class="settings-badge">Default</span>}
            {!item.is_default && (
              <button class="btn btn-ghost btn-sm" onClick={() => void makeDefault(item)}>
                Make default
              </button>
            )}
            <button class="btn btn-ghost btn-sm" onClick={() => startEdit(item)}>Edit</button>
            <button class="btn btn-danger btn-sm" onClick={() => void deleteIdentity(item.id)}>Delete</button>
          </div>
        )
      ))}
    </div>
  );
}
