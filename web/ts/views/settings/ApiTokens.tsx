import { useEffect, useState } from 'preact/hooks';
import { api, type ApiToken } from '../../api/client.js';
import type { components } from '../../api/types.js';
import { confirmDialog } from '../../util/confirm.js';

type Folder = components['schemas']['Folder'];

export function ApiTokens() {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [name, setName] = useState('');
  const [expiresAt, setExpiresAt] = useState('');
  const [selected, setSelected] = useState<number[]>([]);
  const [secret, setSecret] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function reload() {
    try {
      const [t, f] = await Promise.all([api.tokens.list(), api.folders.list()]);
      setTokens(t.items);
      setFolders(f.items);
    } catch (e) { setError(e instanceof Error ? e.message : 'Could not load API tokens'); }
  }
  useEffect(() => { void reload(); }, []);

  async function create() {
    setError('');
    setSecret('');
    setBusy(true);
    try {
      const created = await api.tokens.create({ name, expires_at: new Date(expiresAt).toISOString(), folder_ids: selected });
      setSecret(created.token);
      setName('');
      setExpiresAt('');
      setSelected([]);
      await reload();
    } catch (e) { setError(e instanceof Error ? e.message : 'Could not create token'); }
    finally { setBusy(false); }
  }

  async function revoke(slug: string) {
    const item = tokens.find(t => t.slug === slug);
    if (!await confirmDialog({
      title: 'Revoke API token',
      body: `Revoke "${item?.name ?? 'this token'}"? Any client using it will immediately lose access.`,
      confirmLabel: 'Revoke',
      cancelLabel: 'Keep',
      destructive: true,
    })) return;
    setError('');
    try { await api.tokens.revoke(slug); await reload(); }
    catch (e) { setError(e instanceof Error ? e.message : 'Could not revoke token'); }
  }

  return <div>
    <h2>API tokens</h2>
    <p>Tokens can read mail in the selected folders until they expire. Copy a new token now; it cannot be shown again.</p>
    {error && <div class="settings-error" role="alert">{error}</div>}
    {secret && <div class="settings-success"><label for="new-api-token">New token — copy now</label><input id="new-api-token" type="text" readOnly value={secret} onFocus={e => (e.target as HTMLInputElement).select()} /></div>}
    <div class="settings-field"><label for="token-name">Name</label><input id="token-name" value={name} maxLength={200} onInput={e => setName((e.target as HTMLInputElement).value)} /></div>
    <div class="settings-field"><label for="token-expiry">Expires at</label><input id="token-expiry" type="datetime-local" value={expiresAt} onInput={e => setExpiresAt((e.target as HTMLInputElement).value)} /></div>
    <fieldset><legend>Readable folders</legend>{folders.map(f => <div key={f.id} class="settings-field-check"><input id={`token-folder-${f.id}`} type="checkbox" checked={selected.includes(f.id)} onChange={e => setSelected((e.target as HTMLInputElement).checked ? [...selected, f.id] : selected.filter(id => id !== f.id))} /><label for={`token-folder-${f.id}`}>{f.name}</label></div>)}</fieldset>
    <div class="settings-form-actions"><button class="btn btn-primary" disabled={busy || !name.trim() || !expiresAt || selected.length === 0} onClick={() => void create()}>Create token</button></div>
    <h3>Existing tokens</h3>
    {tokens.length === 0 ? <p>No API tokens.</p> : <ul>{tokens.map(t => <li key={t.slug}>
      <strong>{t.name}</strong> (<code>{t.slug}</code>) {new Date(t.expires_at).getTime() <= Date.now() ? '(expired)' : ''} — expires {new Date(t.expires_at).toLocaleString()} — {t.folder_ids.map(id => folders.find(f => f.id === id)?.name ?? `Folder ${id}`).join(', ')}
      {' '}<button class="btn btn-secondary" onClick={() => void revoke(t.slug)}>Revoke</button>
    </li>)}</ul>}
  </div>;
}
