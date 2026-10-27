import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import { api } from '../../api/client.js';
import type { components } from '../../api/types.js';

type BlockedSender = components['schemas']['BlockedSender'];
const PAGE_SIZE = 50;

export function BlockedSenders() {
  const [items, setItems] = useState<BlockedSender[]>([]);
  const [total, setTotal] = useState(0);
  const [query, setQuery] = useState('');
  const [inputQuery, setInputQuery] = useState('');
  const [offset, setOffset] = useState(0);
  const [address, setAddress] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const generation = useRef(0);

  const load = useCallback(async () => {
    const current = ++generation.current;
    setLoading(true);
    setError(null);
    try {
      const result = await api.blockedSenders.list({ q: query, limit: PAGE_SIZE, offset });
      if (generation.current !== current) return;
      setItems(result.items);
      setTotal(result.total);
    } catch (e) {
      if (generation.current === current) setError(e instanceof Error ? e.message : 'Failed to load blocked senders');
    } finally {
      if (generation.current === current) setLoading(false);
    }
  }, [query, offset]);
  useEffect(() => { void load(); return () => { ++generation.current; }; }, [load]);

  async function add(e: Event) {
    e.preventDefault();
    if (saving) return;
    setSaving(true);
    setError(null);
    try {
      await api.blockedSenders.add(address.trim());
      setAddress('');
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to block sender');
    } finally { setSaving(false); }
  }

  async function remove(id: number) {
    setSaving(true);
    setError(null);
    try {
      await api.blockedSenders.remove(id);
      if (offset > 0 && items.length === 1) setOffset(offset - PAGE_SIZE);
      else await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to unblock sender');
    } finally { setSaving(false); }
  }

  return <div>
    <p>Future mail from these exact addresses goes to Junk. Your filters can override this.
      Mark as junk blocks a sender; Not junk unblocks them.</p>
    <p>From addresses can be forged. If someone was blocked by mistake, remove them here.</p>
    {error && <div class="settings-error" role="alert">{error}</div>}
    <form class="settings-form" onSubmit={e => void add(e)}>
      <div class="settings-field">
        <label for="blocked-address">Email address to block</label>
        <input id="blocked-address" type="email" required maxLength={254} value={address}
          onInput={e => setAddress((e.target as HTMLInputElement).value)} />
      </div>
      <button class="btn btn-primary btn-sm" disabled={saving || loading}>Block sender</button>
    </form>
    <form class="settings-search-form" onSubmit={e => { e.preventDefault(); setQuery(inputQuery); setOffset(0); }}>
      <input type="search" aria-label="Search blocked senders" placeholder="Search blocked senders…"
        maxLength={500} value={inputQuery} onInput={e => setInputQuery((e.target as HTMLInputElement).value)} />
      <button class="btn btn-ghost btn-sm">Search</button>
      {query && <button type="button" class="btn btn-ghost btn-sm"
        onClick={() => { setInputQuery(''); setQuery(''); setOffset(0); }}>Clear</button>}
    </form>
    {loading ? <div class="settings-loading">Loading…</div> : <>
      {items.length === 0 && <div class="settings-empty">{query ? 'No matching blocked senders.' : 'No blocked senders.'}</div>}
      {items.map(item => <div key={item.id} class="settings-item">
        <div class="settings-item-info"><div class="settings-item-name">{item.address}</div></div>
        <button class="btn btn-ghost btn-sm" disabled={saving} onClick={() => void remove(item.id)}>Remove</button>
      </div>)}
    </>}
    {total > PAGE_SIZE && <div class="settings-pagination">
      <button class="btn btn-ghost btn-sm" disabled={loading || saving || offset === 0}
        onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>Prev</button>
      <span>{offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}</span>
      <button class="btn btn-ghost btn-sm" disabled={loading || saving || offset + PAGE_SIZE >= total}
        onClick={() => setOffset(offset + PAGE_SIZE)}>Next</button>
    </div>}
  </div>;
}
