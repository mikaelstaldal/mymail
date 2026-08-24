import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import { api, type SearchSort } from '../api/client.js';
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

/**
 * The sort is deliberately not a Refinement: it does not narrow the result set,
 * so it neither belongs in the form's submit-to-apply group nor needs one. It
 * takes effect on change, from the results toolbar, and is passed to runSearch
 * separately.
 */
const DEFAULT_SORT: SearchSort = 'relevance';

/**
 * The options, in the order the dropdown offers them. A list rather than a
 * Record keyed by sort: the order is then something this file states, not
 * something it inherits from object key insertion order, which a reformat or an
 * alphabetising refactor would silently change — including which option the
 * dropdown shows first.
 */
const SORT_OPTIONS: ReadonlyArray<readonly [SearchSort, string]> = [
  ['relevance', 'Relevance'],
  ['date_desc', 'Newest first'],
  ['date_asc', 'Oldest first'],
];

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
  const [sort, setSort] = useState<SearchSort>(DEFAULT_SORT);

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

  // Which search is the current one. Responses can arrive out of order — two
  // quick changes of the Sort dropdown are enough — and the later request is
  // the one the controls now describe, so an earlier response landing after it
  // must be dropped rather than painted. Without this a slower earlier response
  // can overwrite a faster later one, leaving the list in an ordering the
  // dropdown does not name.
  //
  // This covers out-of-order *successes* only. A sort change that fails is a
  // separate case, handled by handleSortChange putting the dropdown back.
  const latestRequest = useRef(0);

  // 'stale' is not 'error': a superseded request must not make the caller undo
  // anything, because the request that superseded it is still deciding.
  type SearchOutcome = 'ok' | 'error' | 'stale';

  const runSearch = useCallback(async (
    q: string, r: Refinements, off: number, s: SearchSort,
  ): Promise<SearchOutcome> => {
    const request = ++latestRequest.current;
    setLoading(true);
    setError(null);
    try {
      const opts: Parameters<typeof api.messages.search>[1] = {
        limit: PAGE_SIZE,
        offset: off,
        sort: s,
      };
      if (r.folderId != null) opts.folder_id = r.folderId;
      if (r.dateFrom) opts.date_from = dateInputToISO(r.dateFrom);
      if (r.dateTo) opts.date_to = dateInputToISO(r.dateTo, 1);
      const fromAddr = r.fromAddr.trim();
      const toAddr = r.toAddr.trim();
      if (fromAddr) opts.from_addr = fromAddr;
      if (toAddr) opts.to_addr = toAddr;
      const result = await api.messages.search(q, opts);
      if (request !== latestRequest.current) return 'stale';
      setItems(result.items);
      const map: Record<number, string> = {};
      for (const item of result.items) {
        if (item.snippet) map[item.id] = item.snippet;
      }
      setSnippets(map);
      setTotal(result.total);
      setOffset(off);
      setHasSearched(true);
      return 'ok';
    } catch (e) {
      if (request !== latestRequest.current) return 'stale';
      setError(e instanceof Error ? e.message : 'Search failed');
      return 'error';
    } finally {
      // Only the newest request owns the spinner: an older one finishing must
      // not clear it while the newer is still in flight.
      if (request === latestRequest.current) setLoading(false);
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
    setSort(DEFAULT_SORT);
    if (q) {
      void runSearch(q, NO_REFINEMENTS, 0, DEFAULT_SORT);
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
    // The sort survives a resubmit: it is not part of what the form edits, and
    // silently reverting it to relevance would look like the control had been
    // forgotten. Only a query arriving from the toolbar resets it, alongside the
    // refinements.
    void runSearch(q, refine, 0, sort);
  }

  function handleSortChange(next: SearchSort) {
    const previous = sort;
    setSort(next);
    // Back to the first page: the same offset means a different slice under a
    // new ordering, so keeping it would drop the user somewhere arbitrary.
    // Re-runs the last *submitted* refinements, like the pagination buttons —
    // unsubmitted form edits must not take effect through the sort control.
    void runSearch(activeQ, active, 0, next).then(outcome => {
      // The dropdown moved before the request was made, so a failure would
      // otherwise leave it naming an ordering the list is not in — beside an
      // error banner, with nothing to say the two disagree. Only on 'error':
      // a 'stale' outcome means a newer change is still in flight and owns the
      // control.
      if (outcome === 'error') setSort(previous);
    });
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
      ) : (
        <>
          {/* Present from the first *completed* search onwards — hasSearched is
              set when a response lands, so the toolbar is absent during the
              first search of a mounted view, and stays absent if that first
              search fails. Later searches keep it, which is the point:
              unmounting it would destroy the control the user just operated,
              moving keyboard focus to the body mid-interaction, and for the
              Sort dropdown that means being unable to pick a second option
              without navigating back to it. Gating on hasSearched rather than
              items.length is what covers the empty case: Sort must not vanish
              at the moment a user reaches for it to widen a search that found
              nothing. The counts read the previous page's numbers while
              loading; the re-render corrects them. */}
          {hasSearched && (
            <div class="folder-toolbar">
              <div class="search-field">
                <label class="search-field-label" htmlFor="search-sort">Sort</label>
                <select
                  id="search-sort"
                  class="search-sort-select"
                  value={sort}
                  onChange={(e) => handleSortChange((e.target as HTMLSelectElement).value as SearchSort)}
                  aria-label="Sort results"
                >
                  {SORT_OPTIONS.map(([value, label]) => (
                    <option key={value} value={value}>{label}</option>
                  ))}
                </select>
              </div>
              <span class="ml-auto" />
              <span class="pagination-info">{pageStart}–{pageEnd} of {total}</span>
              {/* Disabled while loading, unlike the Sort dropdown beside them:
                  `offset` only advances when a response lands, so a second click
                  mid-flight would re-request the page already being fetched and
                  appear to do nothing. The dropdown has no such state to outrun,
                  and keeping it operable is the point of the mounted toolbar. */}
              <button
                class="btn btn-ghost btn-sm btn-icon"
                disabled={!hasPrev || loading}
                onClick={() => void runSearch(activeQ, active, offset - PAGE_SIZE, sort)}
                aria-label="Previous page"
              >
                <Icon name="chevron-left" />
              </button>
              <button
                class="btn btn-ghost btn-sm btn-icon"
                disabled={!hasNext || loading}
                onClick={() => void runSearch(activeQ, active, offset + PAGE_SIZE, sort)}
                aria-label="Next page"
              >
                <Icon name="chevron-right" />
              </button>
            </div>
          )}
          {loading ? (
            <div class="folder-status">Searching…</div>
          ) : items.length === 0 ? (
            <div class="folder-status folder-empty">No results</div>
          ) : (
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
          )}
        </>
      )}
    </div>
  );
}
