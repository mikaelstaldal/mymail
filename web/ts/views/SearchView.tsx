import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import { api } from '../api/client.js';
import { navigate } from '../router.js';
import { MessageList } from '../components/MessageList.js';
import { Icon } from '../components/Icon.js';
import type { components } from '../api/types.js';

type Folder = components['schemas']['Folder'];
type MessageSummary = components['schemas']['MessageSummary'];

const PAGE_SIZE = 50;

interface SearchViewProps {
  query: string;
  folders: Folder[];
}

/**
 * Everything that narrows a query beyond the query text itself. Kept as one
 * value so the form state and the last-submitted state stay in step, and so
 * pagination re-runs exactly what was submitted.
 */
interface Refinements {
  folderId: number | undefined;
  dateFrom: string;
  dateTo: string;
  fromAddr: string;
  toAddr: string;
}

const NO_REFINEMENTS: Refinements = {
  folderId: undefined,
  dateFrom: '',
  dateTo: '',
  fromAddr: '',
  toAddr: '',
};

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
  const [refine, setRefine] = useState<Refinements>(NO_REFINEMENTS);

  // Last-submitted params — used for pagination so live form edits don't affect prev/next
  const [activeQ, setActiveQ] = useState(query);
  const [active, setActive] = useState<Refinements>(NO_REFINEMENTS);

  const [items, setItems] = useState<MessageSummary[]>([]);
  const [snippets, setSnippets] = useState<Record<number, string>>({});
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hasSearched, setHasSearched] = useState(false);

  // The query text the view has already searched for, so the effect below can
  // tell an external navigation apart from the one handleSubmit just made.
  //
  // A "skip the next effect" boolean cannot do that job: submitting a
  // refinement without editing the query text assigns the identical hash, which
  // fires no hashchange, so the query prop never changes, the effect never
  // runs, and the flag stays set — swallowing the next query that does arrive
  // from the toolbar.
  const handledQuery = useRef<string | null>(null);

  const runSearch = useCallback(async (q: string, r: Refinements, off: number) => {
    setLoading(true);
    setError(null);
    try {
      const opts: Parameters<typeof api.messages.search>[1] = {
        limit: PAGE_SIZE,
        offset: off,
      };
      if (r.folderId != null) opts.folder_id = r.folderId;
      if (r.dateFrom) opts.date_from = dateInputToISO(r.dateFrom);
      if (r.dateTo) opts.date_to = dateInputToISO(r.dateTo, 1);
      const fromAddr = r.fromAddr.trim();
      const toAddr = r.toAddr.trim();
      if (fromAddr) opts.from_addr = fromAddr;
      if (toAddr) opts.to_addr = toAddr;
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
    const q = query.trim();
    if (handledQuery.current === q) return;
    handledQuery.current = q;
    setInputQ(query);
    setRefine(NO_REFINEMENTS);
    setActiveQ(q);
    setActive(NO_REFINEMENTS);
    if (q) {
      void runSearch(q, NO_REFINEMENTS, 0);
    }
  }, [query, runSearch]);

  function handleSubmit(e: Event) {
    e.preventDefault();
    const q = inputQ.trim();
    if (!q) return;
    setActiveQ(q);
    setActive(refine);
    handledQuery.current = q;
    // URLSearchParams, not encodeURIComponent: the route parser reads q back
    // with URLSearchParams, which decodes "+" as a space. Encoding the two the
    // same way keeps the round-trip exact, so a query containing "+" still
    // compares equal to handledQuery above.
    navigate(`#/search?${new URLSearchParams({ q }).toString()}`);
    void runSearch(q, refine, 0);
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
          value={refine.folderId ?? ''}
          onChange={(e) => {
            const v = (e.target as HTMLSelectElement).value;
            setRefine(r => ({ ...r, folderId: v === '' ? undefined : Number(v) }));
          }}
          aria-label="Search in folder"
        >
          <option value="">All mail</option>
          {folders.map(f => (
            <option key={f.id} value={String(f.id)}>{f.name}</option>
          ))}
        </select>
        {/* Each label stays glued to its own control: the row wraps, and a pair
            split across two lines reads as belonging to the wrong neighbour. */}
        <div class="search-field">
          <label class="search-field-label" htmlFor="search-from-addr">From</label>
          <input
            id="search-from-addr"
            class="search-addr-input"
            type="search"
            placeholder="sender@example.com"
            value={refine.fromAddr}
            onInput={(e) => setRefine(r => ({ ...r, fromAddr: (e.target as HTMLInputElement).value }))}
            aria-label="From address"
          />
        </div>
        <div class="search-field">
          <label class="search-field-label" htmlFor="search-to-addr">To</label>
          <input
            id="search-to-addr"
            class="search-addr-input"
            type="search"
            placeholder="recipient@example.com"
            value={refine.toAddr}
            onInput={(e) => setRefine(r => ({ ...r, toAddr: (e.target as HTMLInputElement).value }))}
            aria-label="To address"
          />
        </div>
        <div class="search-field">
          <label class="search-field-label" htmlFor="search-date-from">From date</label>
          <input
            id="search-date-from"
            class="search-date-input"
            type="date"
            value={refine.dateFrom}
            onChange={(e) => setRefine(r => ({ ...r, dateFrom: (e.target as HTMLInputElement).value }))}
            aria-label="From date"
          />
        </div>
        <div class="search-field">
          <label class="search-field-label" htmlFor="search-date-to">To date</label>
          <input
            id="search-date-to"
            class="search-date-input"
            type="date"
            value={refine.dateTo}
            onChange={(e) => setRefine(r => ({ ...r, dateTo: (e.target as HTMLInputElement).value }))}
            aria-label="To date"
          />
        </div>
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
              onClick={() => void runSearch(activeQ, active, offset - PAGE_SIZE)}
              aria-label="Previous page"
            >
              <Icon name="chevron-left" />
            </button>
            <button
              class="btn btn-ghost btn-sm btn-icon"
              disabled={!hasNext}
              onClick={() => void runSearch(activeQ, active, offset + PAGE_SIZE)}
              aria-label="Next page"
            >
              <Icon name="chevron-right" />
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
              folders={active.folderId == null ? folders : undefined}
            />
          </div>
        </>
      )}
    </div>
  );
}
