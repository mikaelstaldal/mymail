// Browser-local persistence for the demo backend: the counterpart of
// internal/repository and its SQLite database.
//
// Storage is IndexedDB rather than localStorage, which a service worker cannot
// reach — localStorage is a synchronous API and is simply not exposed to worker
// contexts. IndexedDB is the same kind of thing from the user's point of view
// (per-origin storage that lives in the browser, survives a reload, and goes
// away when the site's data is cleared), and it also holds attachment bytes
// without base64-inflating them into a ~5 MB string quota.
//
// Everything except attachment bytes is one record, cached in memory and
// rewritten wholesale on every mutation. For a demo-sized mailbox that is far
// simpler than mirroring the relational schema, and every read is then a plain
// array scan. Attachment bytes live in their own store so editing a draft does
// not rewrite the files hanging off it.
//
// See model.ts for why these are globals rather than module exports.

const DB_NAME = 'mymail-demo';
const DB_VERSION = 1;
const KV_STORE = 'kv';
const ATTACHMENT_STORE = 'attachment-data';
const STATE_KEY = 'state';

/** An attachment's bytes, keyed by the same id as its metadata row. */
interface StoredAttachmentData {
  id: number;
  data: ArrayBuffer;
}

function promisifyRequest<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error('IndexedDB request failed'));
  });
}

let dbPromise: Promise<IDBDatabase> | null = null;

function openDatabase(): Promise<IDBDatabase> {
  if (dbPromise === null) {
    dbPromise = new Promise((resolve, reject) => {
      const request = indexedDB.open(DB_NAME, DB_VERSION);
      request.onupgradeneeded = () => {
        const db = request.result;
        if (!db.objectStoreNames.contains(KV_STORE)) db.createObjectStore(KV_STORE);
        if (!db.objectStoreNames.contains(ATTACHMENT_STORE)) {
          db.createObjectStore(ATTACHMENT_STORE, { keyPath: 'id' });
        }
      };
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error ?? new Error('cannot open the demo database'));
    });
  }
  return dbPromise;
}

async function readRecord<T>(store: string, key: IDBValidKey): Promise<T | undefined> {
  const db = await openDatabase();
  const tx = db.transaction(store, 'readonly');
  return promisifyRequest<T | undefined>(tx.objectStore(store).get(key) as IDBRequest<T | undefined>);
}

async function writeRecord(store: string, value: unknown, key?: IDBValidKey): Promise<void> {
  const db = await openDatabase();
  const tx = db.transaction(store, 'readwrite');
  const objectStore = tx.objectStore(store);
  await promisifyRequest(key === undefined ? objectStore.put(value) : objectStore.put(value, key));
  await transactionDone(tx);
}

async function deleteRecords(store: string, keys: IDBValidKey[]): Promise<void> {
  if (keys.length === 0) return;
  const db = await openDatabase();
  const tx = db.transaction(store, 'readwrite');
  const objectStore = tx.objectStore(store);
  for (const key of keys) await promisifyRequest(objectStore.delete(key));
  await transactionDone(tx);
}

function transactionDone(tx: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error ?? new Error('IndexedDB transaction failed'));
    tx.onabort = () => reject(tx.error ?? new Error('IndexedDB transaction aborted'));
  });
}

/**
 * A write that exceeds the origin's storage quota. Reported as 507 so the UI
 * shows the message rather than a bare network failure — the one failure mode
 * the real server does not have.
 */
function quotaError(err: unknown): ApiError {
  const name = (err as { name?: string } | null)?.name;
  if (name === 'QuotaExceededError') {
    return new ApiError(507, 'browser storage is full — delete a message or an attachment to free space');
  }
  return new ApiError(500, 'storage error: ' + String(err));
}

let cachedState: DemoState | null = null;

/**
 * The dataset, loading it (and seeding on first ever run) if needed. Callers
 * must hold the mutex — see withStore.
 */
async function loadState(): Promise<DemoState> {
  if (cachedState === null) {
    const stored = await readRecord<DemoState>(KV_STORE, STATE_KEY);
    cachedState = stored ?? (await seedStore());
  }
  return cachedState;
}

/**
 * Drops the cached dataset so the next read reloads it from IndexedDB.
 *
 * Every handler mutates the cached object in place and then persists it, so a
 * handler that throws part-way through leaves the cache holding changes that
 * were never written. Discarding it is how those are rolled back — the
 * equivalent of the transaction the server would not have committed.
 */
function invalidateState(): void {
  cachedState = null;
}

/** Persists the in-memory dataset. */
async function saveState(state: DemoState): Promise<void> {
  cachedState = state;
  try {
    await writeRecord(KV_STORE, state, STATE_KEY);
  } catch (err) {
    // The cache no longer reflects storage; drop it so the next read reloads.
    cachedState = null;
    throw quotaError(err);
  }
}

/** The state an unreachable seed document leaves behind: empty, but usable. */
function emptyState(): DemoState {
  return {
    nextMessageId: 1,
    nextAttachmentId: 1,
    nextFolderId: FIRST_USER_FOLDER,
    nextIdentityId: 1,
    nextContactId: 1,
    nextFilterId: 1,
    folders: [],
    identities: [],
    contacts: [],
    filters: [],
    spamFilter: { enabled: false, scoreHeader: 'X-Spam-Score', scoreThreshold: 5 },
    messages: [],
    attachments: [],
    pendingReplies: [],
  };
}

/**
 * Fills an empty store from demo-data.json, the same content `mymail -demo`
 * writes into SQLite (see internal/demo). Runs once: after this the mailbox is
 * the user's, and clearing the site's data is what brings the demo content back.
 */
async function seedStore(): Promise<DemoState> {
  const state = emptyState();
  const seed = await fetchSeed();
  if (seed !== null) {
    state.folders = seed.folders;
    state.identities = seed.identities;
    state.contacts = seed.contacts;
    state.filters = seed.filters;
    state.spamFilter = seed.spamFilter;
    state.messages = seed.messages.map((m) => ({
      ...m,
      raw: m.raw === null ? null : base64ToText(m.raw),
    }));
    state.attachments = seed.attachments.map((a) => ({
      id: a.id,
      messageId: a.messageId,
      filename: a.filename,
      contentType: a.contentType,
      size: a.size,
    }));
    for (const a of seed.attachments) {
      const bytes = base64ToBytes(a.data);
      const data = new ArrayBuffer(bytes.length);
      new Uint8Array(data).set(bytes);
      await writeRecord(ATTACHMENT_STORE, { id: a.id, data } satisfies StoredAttachmentData);
    }
    // The seed carries real primary keys, so the counters continue from them
    // rather than starting over and colliding.
    state.nextMessageId = nextIdAfter(state.messages);
    state.nextAttachmentId = nextIdAfter(state.attachments);
    state.nextIdentityId = nextIdAfter(state.identities);
    state.nextContactId = nextIdAfter(state.contacts);
    state.nextFilterId = nextIdAfter(state.filters);
    // User folders start at 100 regardless of the built-in ids 1–7, exactly as
    // `SELECT COALESCE(MAX(id), 99) + 1 FROM folders WHERE id >= 100` does.
    state.nextFolderId = Math.max(
      FIRST_USER_FOLDER,
      ...state.folders.filter((f) => f.id >= FIRST_USER_FOLDER).map((f) => f.id + 1),
    );
  }
  await writeRecord(KV_STORE, state, STATE_KEY);
  return state;
}

function nextIdAfter(rows: Array<{ id: number }>): number {
  return rows.reduce((max, row) => Math.max(max, row.id + 1), 1);
}

/**
 * Loads demo-data.json from alongside the worker, or null when it is missing.
 * A missing seed is not fatal: the demo then simply starts with an empty
 * mailbox and no folders, which is visibly broken but recoverable by a reload
 * — better than a worker that refuses to answer anything.
 */
async function fetchSeed(): Promise<DemoSeed | null> {
  try {
    const response = await fetch(seedURL(), { cache: 'no-cache' });
    if (!response.ok) return null;
    return (await response.json()) as DemoSeed;
  } catch {
    return null;
  }
}

// ── Attachment bytes ─────────────────────────────────────────────────────────

async function getAttachmentData(id: number): Promise<ArrayBuffer | null> {
  const record = await readRecord<StoredAttachmentData>(ATTACHMENT_STORE, id);
  return record === undefined ? null : record.data;
}

async function putAttachmentData(id: number, data: ArrayBuffer): Promise<void> {
  try {
    await writeRecord(ATTACHMENT_STORE, { id, data } satisfies StoredAttachmentData);
  } catch (err) {
    throw quotaError(err);
  }
}

async function removeAttachmentData(ids: number[]): Promise<void> {
  await deleteRecords(ATTACHMENT_STORE, ids);
}

// ── Serialization ────────────────────────────────────────────────────────────

let mutex: Promise<unknown> = Promise.resolve();

/**
 * Runs fn with exclusive access to the store. Fetch events arrive concurrently
 * and every mutation is read-modify-write over the whole dataset, so they are
 * serialized here — the transactional guarantee the SQLite writes have on the
 * server.
 */
function withStore<T>(fn: (state: DemoState) => Promise<T>): Promise<T> {
  const run = mutex.then(async () => fn(await loadState()));
  // Keep the chain alive after a rejection, or one failed request would wedge
  // every later one.
  mutex = run.catch(() => undefined);
  return run;
}
