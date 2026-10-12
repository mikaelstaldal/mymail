import { DeviceApproval } from '../../components/DeviceApproval.js';
import { useEffect, useState } from 'preact/hooks';
import { api, NotFoundError, type ApiToken } from '../../api/client.js';
import type { components } from '../../api/types.js';
import { confirmDialog } from '../../util/confirm.js';

type Folder = components['schemas']['Folder'];

export function ApiTokens() {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [folders, setFolders] = useState<Folder[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [tokensLoading, setTokensLoading] = useState(true);
  const [foldersLoading, setFoldersLoading] = useState(true);

  async function reload() {
    setError('');
    setTokensLoading(true);
    setFoldersLoading(true);
    try {
      const [t, f] = await Promise.all([api.tokens.list(), api.folders.list()]);
      setTokens(t.items);
      setFolders(f.items);
    } catch (e) { setError(e instanceof Error ? e.message : 'Could not load API tokens'); }
    finally { setFoldersLoading(false); setTokensLoading(false); }
  }
  useEffect(() => { void reload(); }, []);

  async function revoke(slug: string) {
    if (busy) return;
    setBusy(true);
    try {
      const item = tokens.find(t => t.slug === slug);
      if (!await confirmDialog({
        title: 'Revoke API token',
        body: `Revoke "${item?.name ?? 'this token'}"? Any client using it will immediately lose access.`,
        confirmLabel: 'Revoke', cancelLabel: 'Keep', destructive: true,
      })) return;
      setError('');
      await api.tokens.revoke(slug);
      setTokens(current => current.filter(token => token.slug !== slug));
      await reload();
    } catch (e) {
      if (e instanceof NotFoundError) {
        setTokens(current => current.filter(token => token.slug !== slug));
        await reload();
      } else setError(e instanceof Error ? e.message : 'Could not revoke token');
    } finally { setBusy(false); }
  }

  return <div>
    <h2>API tokens</h2>
    <p>Tokens can read mail in their granted folders until they expire. Use mymail-token login or create to obtain a token; existing token secrets cannot be shown again.</p>
    {error && <div class="settings-error" role="alert">{error}</div>}
    <DeviceApproval resources={folders} scopePrefix="folder" resourcesLoading={foldersLoading} onDecision={() => void reload()} />
    <h3>Existing tokens</h3>
    {tokensLoading ? <p role="status">Loading API tokens…</p> : !error && tokens.length === 0 ? <p>No API tokens.</p> : <ul>{tokens.map(t => <li key={t.slug}>
      <strong>{t.name}</strong> (<code>{t.slug}</code>) {new Date(t.expires_at).getTime() <= Date.now() ? '(expired)' : ''} — expires {new Date(t.expires_at).toLocaleString()} — {t.folder_ids.map(id => folders.find(f => f.id === id)?.name ?? `Folder ${id}`).join(', ')}
      {' '}<button class="btn btn-secondary" disabled={busy} onClick={() => void revoke(t.slug)}>Revoke</button>
    </li>)}</ul>}
  </div>;
}
