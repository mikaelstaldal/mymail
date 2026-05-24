import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import { api } from '../api/client.js';
import { navigate } from '../router.js';
import { MessageList } from '../components/MessageList.js';
import type { components } from '../api/types.js';

type Folder = components['schemas']['Folder'];
type MessageSummary = components['schemas']['MessageSummary'];

const PAGE_SIZE = 50;

interface SearchViewProps {
  query: string;
  folders: Folder[];
}

function dateInputToISO(dateStr: string, addDays = 0): string {
  const [y, m, d] = dateStr.split('-').map(Number);
  const date = new Date(y, m - 1, d + addDays, 0, 0, 0);
  const off = -date.getTimezoneOffset();
  const sign = off >= 0 ? '+' : '-';
  const absOff = Math.abs(off);
  const hh = String(Math.floor(absOff / 60)).padStart(2, '0');
  const mm = String(absOff % 60).padStart(2, '0');
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T00:00:00${sign}${hh}:${mm}`;
}

export function SearchView({ query, folders }: SearchViewProps) {
  const [inputQ, setInputQ] = useState(query);
  const [folderId, setFolderId] = useState<number | undefined>(undefined);
  const [dateFrom, setDateFrom] = useState('');
  const [dateTo, setDateTo] = useState('');

  // Last-submitted params — used for pagination so live form edits don't affect prev/next
  const [activeQ, setActiveQ] = useState(query);
  const [activeFolderId, setActiveFolderId] = useState<number | undefined>(undefined);
  const [activeDateFrom, setActiveDateFrom] = useState('');
  const [activeDateTo, setActiveDateTo] = useState('');

  const [items, setItems] = useState<MessageSummary[]>([]);
  const [snippets, setSnippets] = useState<Record<number, string>>({});
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hasSearched, setHasSearched] = useState(false);

  // Prevents useEffect from double-firing when we navigate internally on submit
  const skipQueryEffect = useRef(false);

  const runSearch = useCallback(async (
    q: string,
    fId: number | undefined,
    df: string,
    dt: string,
    off: number,
  ) => {
    setLoading(true);
    setError(null);
    try {
      const opts: Parameters<typeof api.messages.search>[1] = {
        limit: PAGE_SIZE,
        offset: off,
      };
      if (fId != null) opts.folder_id = fId;
      if (df) opts.date_from = dateInputToISO(df);
      if (dt) opts.date_to = dateInputToISO(dt, 1);
      const result = await api.messages.search(q, opts);
      setItems(result.items);
      const map: Record<number, string> = {};
      for (const item of result.items) {
        if (item.snippet) map[item.id] = item.snippet;
      }
      setSnippets(map);
      setTotal(result.total);
      setOffset(off);
      setHasSearched(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Search failed');
    } finally {
      setLoading(false);
    }
  }, []);

  // Run initial search when query prop changes (e.g. toolbar navigation)
  useEffect(() => {
    if (skipQueryEffect.current) {
      skipQueryEffect.current = false;
      return;
    }
    const q = query.trim();
    setInputQ(query);
    setFolderId(undefined);
    setDateFrom('');
    setDateTo('');
    setActiveQ(q);
    setActiveFolderId(undefined);
    setActiveDateFrom('');
    setActiveDateTo('');
    if (q) {
      void runSearch(q, undefined, '', '', 0);
    }
  }, [query, runSearch]);

  function handleSubmit(e: Event) {
    e.preventDefault();
    const q = inputQ.trim();
    if (!q) return;
    setActiveQ(q);
    setActiveFolderId(folderId);
    setActiveDateFrom(dateFrom);
    setActiveDateTo(dateTo);
    skipQueryEffect.current = true;
    navigate(`#/search?q=${encodeURIComponent(q)}`);
    void runSearch(q, folderId, dateFrom, dateTo, 0);
  }

  const hasPrev = offset > 0;
  const hasNext = offset + PAGE_SIZE < total;
  const pageStart = total === 0 ? 0 : offset + 1;
  const pageEnd = Math.min(offset + PAGE_SIZE, total);

  return (
    <div class="search-view">
      <form class="search-form" onSubmit={handleSubmit}>
        <input
          class="search-query-input"
          type="search"
          placeholder="Search…"
          value={inputQ}
          onInput={(e) => setInputQ((e.target as HTMLInputElement).value)}
          aria-label="Search query"
        />
        <select
          class="search-folder-select"
          value={folderId ?? ''}
          onChange={(e) => {
            const v = (e.target as HTMLSelectElement).value;
            setFolderId(v === '' ? undefined : Number(v));
          }}
          aria-label="Search in folder"
        >
          <option value="">All mail</option>
          {folders.map(f => (
            <option key={f.id} value={String(f.id)}>{f.name}</option>
          ))}
        </select>
        <label class="search-date-label" htmlFor="search-date-from">From</label>
        <input
          id="search-date-from"
          class="search-date-input"
          type="date"
          value={dateFrom}
          onChange={(e) => setDateFrom((e.target as HTMLInputElement).value)}
          aria-label="Date from"
        />
        <label class="search-date-label" htmlFor="search-date-to">To</label>
        <input
          id="search-date-to"
          class="search-date-input"
          type="date"
          value={dateTo}
          onChange={(e) => setDateTo((e.target as HTMLInputElement).value)}
          aria-label="Date to"
        />
        <button class="btn btn-primary btn-sm" type="submit">Search</button>
      </form>

      {error && <div class="folder-error">{error}</div>}

      {!hasSearched && !loading ? (
        <div class="search-blank" />
      ) : loading ? (
        <div class="folder-status">Searching…</div>
      ) : items.length === 0 ? (
        <div class="folder-status folder-empty">No results</div>
      ) : (
        <>
          <div class="folder-toolbar">
            <span class="ml-auto" />
            <span class="pagination-info">{pageStart}–{pageEnd} of {total}</span>
            <button
              class="btn btn-ghost btn-sm btn-icon"
              disabled={!hasPrev}
              onClick={() => void runSearch(activeQ, activeFolderId, activeDateFrom, activeDateTo, offset - PAGE_SIZE)}
              aria-label="Previous page"
            >
              ‹
            </button>
            <button
              class="btn btn-ghost btn-sm btn-icon"
              disabled={!hasNext}
              onClick={() => void runSearch(activeQ, activeFolderId, activeDateFrom, activeDateTo, offset + PAGE_SIZE)}
              aria-label="Next page"
            >
              ›
            </button>
          </div>
          <div class="msg-list-wrap">
            <MessageList
              items={items}
              selectedIds={new Set()}
              onToggleSelect={() => undefined}
              onToggleSelectAll={() => undefined}
              onRowClick={(id) => navigate(`#/message/${id}`)}
              snippets={snippets}
              folders={activeFolderId == null ? folders : undefined}
            />
          </div>
        </>
      )}
    </div>
  );
}
