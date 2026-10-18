import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import { api } from '../../api/client.js';
import { confirmDialog } from '../../util/confirm.js';
import { foldContactAddress } from '../../util/casefold.js';
import { senderParts } from '../../util/phishing.js';
import { Icon } from '../../components/Icon.js';
import type { components } from '../../api/types.js';

type Contact = components['schemas']['Contact'];

const PAGE_SIZE = 50;
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

interface ContactFormProps {
  address: string;
  name: string;
  onAddress: (v: string) => void;
  onName: (v: string) => void;
  error: string | null;
  saving: boolean;
  onSave: () => void;
  onCancel: () => void;
}

function ContactForm({ address, name, onAddress, onName, error, saving, onSave, onCancel }: ContactFormProps) {
  return (
    <div class="settings-form">
      {error && <div class="settings-error">{error}</div>}
      <div class="settings-field">
        <label for="contact-address">Email Address</label>
        <input
          id="contact-address"
          type="email"
          value={address}
          onInput={e => onAddress((e.target as HTMLInputElement).value)}
          placeholder="contact@example.com"
        />
      </div>
      <div class="settings-field">
        <label for="contact-name">Name (optional)</label>
        <input
          id="contact-name"
          type="text"
          value={name}
          onInput={e => onName((e.target as HTMLInputElement).value)}
          placeholder="Contact name"
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

export function Contacts({ senderMessageId }: { senderMessageId?: number }) {
  const [items, setItems] = useState<Contact[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [pageLoading, setPageLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [inputQuery, setInputQuery] = useState('');
  const [offset, setOffset] = useState(0);
  const [formMode, setFormMode] = useState<null | 'create' | 'sender' | number>(null);
  const senderContactId = useRef<number | null>(null);
  const [senderLoading, setSenderLoading] = useState(senderMessageId !== undefined);
  const [formAddress, setFormAddress] = useState('');
  const [formName, setFormName] = useState('');
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const prevQueryRef = useRef<string>('__init__');

  const load = useCallback(async () => {
    const queryChanged = prevQueryRef.current !== query;
    prevQueryRef.current = query;
    if (queryChanged) {
      setLoading(true);
      setItems([]);
    } else {
      setPageLoading(true);
    }
    setError(null);
    try {
      const res = await api.contacts.list({ q: query || undefined, limit: PAGE_SIZE, offset });
      setItems(res.items);
      setTotal(res.total);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load contacts');
    } finally {
      setLoading(false);
      setPageLoading(false);
    }
  }, [query, offset]);

  useEffect(() => { void load(); }, [load]);

  useEffect(() => {
    if (senderMessageId === undefined) return;
    let cancelled = false;
    async function openSender() {
      try {
        const message = await api.messages.get(senderMessageId!);
        const sender = senderParts(message.from_addr);
        // Use the stored addr-spec, never a display-only decoded IDN address.
        const address = message.from_addr.includes('<')
          ? message.from_addr.slice(message.from_addr.lastIndexOf('<') + 1, message.from_addr.lastIndexOf('>')).trim()
          : message.from_addr.trim();
        let contact: Contact | undefined;
        // Contacts can span many pages; an existing explicit contact must open for
        // editing too, rather than fail as a duplicate on save.
        for (let offset = 0; ; offset += 100) {
          const page = await api.contacts.list({ q: foldContactAddress(address), limit: 100, offset });
          if (cancelled) return;
          contact = page.items.find(c => c.address === foldContactAddress(address));
          if (contact || offset + page.items.length >= page.total || page.items.length === 0) break;
        }
        if (cancelled) return;
        senderContactId.current = contact?.id ?? null;
        setFormAddress(contact?.address ?? address);
        setFormName(contact && (contact.explicit || contact.name) ? contact.name : sender.name);
        setFormMode('sender');
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to open sender contact');
      } finally {
        if (!cancelled) setSenderLoading(false);
      }
    }
    void openSender();
    return () => { cancelled = true; };
  }, [senderMessageId]);

  function search(e: Event) {
    e.preventDefault();
    setQuery(inputQuery);
    setOffset(0);
  }

  function clearSearch() {
    setInputQuery('');
    setQuery('');
    setOffset(0);
  }

  function startCreate() {
    setFormMode('create');
    setFormAddress('');
    setFormName('');
    setFormError(null);
  }

  function startEdit(c: Contact) {
    setFormMode(c.id);
    setFormAddress(c.address);
    setFormName(c.name);
    setFormError(null);
  }

  function clearSenderRoute() {
    if (senderMessageId !== undefined) {
      window.history.replaceState(window.history.state, '', '#/settings/contacts');
    }
  }

  function cancelForm() {
    clearSenderRoute();
    setFormMode(null);
    setFormError(null);
  }

  async function submitForm() {
    const addr = formAddress.trim();
    if (!EMAIL_RE.test(addr)) {
      setFormError('Please enter a valid email address.');
      return;
    }
    setSaving(true);
    setFormError(null);
    try {
      if (formMode === 'sender' && senderContactId.current !== null) {
        await api.contacts.update(senderContactId.current, { address: addr, name: formName.trim() });
      } else if (formMode === 'create' || formMode === 'sender') {
        await api.contacts.create({ address: addr, name: formName.trim() });
      } else if (typeof formMode === 'number') {
        await api.contacts.update(formMode, { address: addr, name: formName.trim() });
      }
      setFormMode(null);
      clearSenderRoute();
      await load();
    } catch (e) {
      setFormError(e instanceof Error ? e.message : 'Failed to save contact');
    } finally {
      setSaving(false);
    }
  }

  async function deleteContact(id: number) {
    if (!await confirmDialog({
      title: 'Delete contact',
      body: 'Delete this contact? This cannot be undone.',
      confirmLabel: 'Delete',
      cancelLabel: 'Keep',
      destructive: true,
    })) return;
    setError(null);
    try {
      await api.contacts.delete(id);
      if (offset > 0 && items.length === 1) {
        setOffset(offset - PAGE_SIZE);
      } else {
        await load();
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete contact');
    }
  }

  return (
    <div>
      {error && <div class="settings-error">{error}</div>}
      <div class="settings-toolbar">
        <form class="settings-search-form" onSubmit={search}>
          <input
            type="text"
            class="settings-search-input"
            value={inputQuery}
            onInput={e => setInputQuery((e.target as HTMLInputElement).value)}
            placeholder="Search contacts…"
          />
          <button class="btn btn-ghost btn-sm" type="submit">Search</button>
          {query && (
            <button class="btn btn-ghost btn-sm" type="button" onClick={clearSearch}>
              Clear
            </button>
          )}
        </form>
        <button class="btn btn-primary btn-sm" onClick={startCreate} disabled={senderLoading || saving || formMode === 'create'}>
          + Add Contact
        </button>
      </div>
      {senderLoading && <div class="settings-loading">Opening sender contact…</div>}
      {(formMode === 'create' || formMode === 'sender') && (
        <ContactForm
          address={formAddress}
          name={formName}
          onAddress={setFormAddress}
          onName={setFormName}
          error={formError}
          saving={saving}
          onSave={() => void submitForm()}
          onCancel={cancelForm}
        />
      )}
      {loading && <div class="settings-loading">Loading…</div>}
      {!loading && items.length === 0 && !pageLoading && (
        <div class="settings-empty">
          {query ? 'No contacts match your search.' : 'No contacts yet.'}
        </div>
      )}
      <div style={pageLoading ? 'opacity:0.5;pointer-events:none' : ''}>
      {!loading && items.map(c => (
        formMode === c.id ? (
          <ContactForm
            key={c.id}
            address={formAddress}
            name={formName}
            onAddress={setFormAddress}
            onName={setFormName}
            error={formError}
            saving={saving}
            onSave={() => void submitForm()}
            onCancel={cancelForm}
          />
        ) : (
          <div key={c.id} class="settings-item">
            <div class="settings-item-info">
              <div class="settings-item-name">
                {c.name || <em style="color:var(--text-muted);font-style:italic">No name</em>}
              </div>
              <div class="settings-item-meta">{c.address}</div>
              {c.explicit && <small class="settings-item-meta" title="Added or edited by you; sender authenticity is not verified">Added or edited by you</small>}
            </div>
            <button class="btn btn-ghost btn-sm" onClick={() => startEdit(c)} disabled={senderLoading || saving}>Edit</button>
            <button class="btn btn-danger btn-sm" onClick={() => void deleteContact(c.id)} disabled={senderLoading || saving}>Delete</button>
          </div>
        )
      ))}
      </div>
      {total > PAGE_SIZE && (
        <div class="settings-pagination">
          <button
            class="btn btn-ghost btn-sm"
            onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
            disabled={offset === 0}
          >
            <Icon name="arrow-left" size={14} /> Prev
          </button>
          <span class="settings-pagination-info">
            {offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}
          </span>
          <button
            class="btn btn-ghost btn-sm"
            onClick={() => setOffset(offset + PAGE_SIZE)}
            disabled={offset + PAGE_SIZE >= total}
          >
            Next <Icon name="arrow-right" size={14} />
          </button>
        </div>
      )}
    </div>
  );
}
