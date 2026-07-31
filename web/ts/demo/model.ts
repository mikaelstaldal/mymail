// Shared shapes and constants for the demo backend — the browser-side stand-in
// for the Go server, running inside a service worker (see ../demo-sw.ts).
//
// These files are NOT ES modules: the demo tsconfig compiles them as global
// scripts and the worker loads them with importScripts(), so every declaration
// here is a plain global shared with the other demo files. A classic worker is
// deliberate — module service workers are still uneven across browsers, and the
// script has to run wherever the static bundle ends up hosted.
//
// Adding a single `import` or `export` to any file in this directory silently
// turns it into a module, and every declaration in it vanishes from the shared
// scope.

// ── Built-in folder ids ──────────────────────────────────────────────────────
// Mirrors the table in AGENTS.md; user folders start at 100.

const FOLDER_INBOX = 1;
const FOLDER_SENT = 2;
const FOLDER_DRAFTS = 3;
const FOLDER_TRASH = 4;
const FOLDER_SCHEDULED = 5;
const FOLDER_SNOOZED = 6;
const FOLDER_JUNK = 7;
const FIRST_USER_FOLDER = 100;

/** Folders a message may neither be moved out of nor into. Mirrors the repeated
 *  `3, 5, 6` guards in internal/repository/message_repo.go. */
const MANAGED_FOLDERS = [FOLDER_DRAFTS, FOLDER_SCHEDULED, FOLDER_SNOOZED];

// ── Limits, mirroring internal/handler and internal/repository ───────────────

const DEFAULT_LIMIT = 50;
const MAX_LIMIT = 200;
/** Bulk endpoints cap at this many message ids (handler/messages.go). */
const MAX_BULK_IDS = 1000;
const MAX_ADDR_LIST_LEN = 8192;
const MAX_SUBJECT_LEN = 998;
const MAX_NAME_LEN = 200;
const MAX_ADDRESS_LEN = 254;
const MAX_QUERY_LEN = 500;
const MAX_SIGNATURE_LEN = 51_200;
const MAX_SCORE_HEADER_LEN = 200;
/** References header budget; oldest entries are dropped first. */
const MAX_REFS_BYTES = 16 * 1024;
/** Transitive-closure cap for threading (repository.GetMessageThread). */
const THREAD_CAP = 1000;
/** How much of body_text the search snippet is built from. */
const SNIPPET_SOURCE_LIMIT = 64 * 1024;
const SNIPPET_CONTEXT_TOKENS = 15;

/**
 * A send is deferred rather than immediate when send_at is more than this far
 * ahead, and a snooze must be at least this far ahead. Both thresholds are 60 s
 * on the server (handler.isScheduled, repository.SnoozeMessage).
 */
const SCHEDULE_THRESHOLD_MS = 60_000;

/**
 * How long after sending a message its fake reply arrives. Demo-only: there is
 * no correspondent on the other end, so the demo invents one (see reply.ts).
 * Long enough that the reply is visibly a reply rather than an echo, short
 * enough to see without waiting — and well inside the UI's 30 s folder poll, so
 * one poll after that is when it shows up.
 */
const AUTO_REPLY_DELAY_MS = 20_000;

/**
 * An upload ceiling that does not exist on the server, which caps the whole
 * request at 32 MiB instead. Browser storage is a shared, modest quota rather
 * than a disk, so an oversized attachment is refused up front with a clear
 * message rather than failing later as an opaque quota error.
 */
const MAX_ATTACHMENT_BYTES = 8 << 20; // 8 MiB

// ── Stored shapes ────────────────────────────────────────────────────────────
//
// These mirror the SQLite tables one field per column, in camelCase. Columns
// that are nullable in the schema are `| null` here, never `undefined`: the
// difference between NULL and '' decides folder membership (a draft has
// raw = NULL, a scheduled message has send_at != NULL) and threading.

interface DemoFolder {
  id: number;
  name: string;
  slug: string;
  position: number;
  createdAt: string;
}

interface DemoIdentity {
  id: number;
  name: string;
  address: string;
  isDefault: boolean;
  position: number;
  signature: string;
}

interface DemoContact {
  id: number;
  address: string;
  name: string;
  createdAt: string;
  updatedAt: string;
}

type FilterAction = 'move' | 'trash' | 'mark_read' | 'drop';

interface DemoFilter {
  id: number;
  position: number;
  name: string;
  matchFrom: string;
  matchTo: string;
  matchSubject: string;
  action: FilterAction;
  folderId: number | null;
  stop: boolean;
}

interface DemoSpamFilter {
  enabled: boolean;
  scoreHeader: string;
  scoreThreshold: number;
}

interface DemoMessage {
  id: number;
  folderId: number;
  identityId: number | null;
  messageId: string | null;
  inReplyTo: string | null;
  /** Newline-separated message-ids without angle brackets, as the column stores them. */
  references: string | null;
  fromAddr: string;
  toAddr: string;
  ccAddr: string;
  bccAddr: string;
  replyToAddr: string;
  subject: string;
  date: string;
  bodyText: string;
  bodyHtml: string;
  /** The RFC 5322 source, or null for a draft — the `raw IS NULL` invariant. */
  raw: string | null;
  read: boolean;
  flagged: boolean;
  hasAttachments: boolean;
  hasExternalImages: boolean;
  sendAt: string | null;
  snoozedUntil: string | null;
  snoozeFolder: number | null;
  sendError: string | null;
  sendFailureCount: number;
  createdAt: string;
  updatedAt: string;
}

/** Attachment metadata. The bytes live in their own object store (store.ts). */
interface DemoAttachment {
  id: number;
  messageId: number;
  filename: string;
  contentType: string;
  size: number;
}

/**
 * A fake reply waiting to be delivered. Queued rather than sent by a timer: a
 * service worker is stopped whenever it is idle, so a setTimeout spanning the
 * delay would frequently never fire. Instead every request that reaches the
 * store delivers whatever has come due (see deliverDueReplies in api.ts).
 */
interface PendingReply {
  /** When the reply becomes deliverable, epoch milliseconds. */
  dueAt: number;
  /** The id of the sent message being replied to. */
  sourceMessageId: number;
}

/**
 * The whole dataset, persisted as one IndexedDB record. Attachment *bytes* are
 * excluded — they are far larger and are read one at a time — so editing a
 * draft never rewrites the files hanging off it.
 */
interface DemoState {
  nextMessageId: number;
  nextAttachmentId: number;
  nextFolderId: number;
  nextIdentityId: number;
  nextContactId: number;
  nextFilterId: number;
  folders: DemoFolder[];
  identities: DemoIdentity[];
  contacts: DemoContact[];
  filters: DemoFilter[];
  spamFilter: DemoSpamFilter;
  messages: DemoMessage[];
  attachments: DemoAttachment[];
  pendingReplies: PendingReply[];
}

/**
 * demo-data.json, produced by internal/demo.BuildSeed. The two byte-valued
 * columns — a message's `raw` and an attachment's contents — travel base64
 * encoded, so neither is usable as-is; store.ts decodes both while seeding.
 */
interface DemoSeed {
  folders: DemoFolder[];
  identities: DemoIdentity[];
  contacts: DemoContact[];
  filters: DemoFilter[];
  spamFilter: DemoSpamFilter;
  messages: Array<Omit<DemoMessage, 'raw'> & { raw: string | null }>;
  attachments: Array<DemoAttachment & { data: string }>;
}

// ── Errors ───────────────────────────────────────────────────────────────────

/**
 * A failure that maps to an HTTP status, mirroring how handler.NewError and the
 * per-endpoint `errors.Is` ladders turn the repository's sentinel errors into
 * status codes. The message travels in the API's `{"error": "…"}` body.
 */
class ApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
    this.name = 'ApiError';
  }
}

function validationError(message: string): ApiError {
  return new ApiError(400, message);
}

function notFoundError(message: string): ApiError {
  return new ApiError(404, message);
}

function conflictError(message: string): ApiError {
  return new ApiError(409, message);
}

// ── Small shared helpers ─────────────────────────────────────────────────────

/** The UTC RFC 3339 form the server stores and the API emits. */
function nowTimestamp(): string {
  return new Date().toISOString().replace(/\.\d+Z$/, 'Z');
}

/** Second-resolution RFC 3339, as `time.Format(time.RFC3339)` produces. */
function toTimestamp(date: Date): string {
  return date.toISOString().replace(/\.\d+Z$/, 'Z');
}

/** Code-point count, matching Go's utf8.RuneCountInString. */
function runeLength(s: string): number {
  let n = 0;
  for (const _ of s) n++;
  return n;
}

/** UTF-8 byte length, matching Go's len() on a string. */
function byteLength(s: string): number {
  return new TextEncoder().encode(s).length;
}

/** The `references` column split into its entries, dropping empties. Mirrors repository.splitNL. */
function splitNL(s: string | null): string[] {
  if (s === null || s === '') return [];
  return s.split('\n').filter((p) => p !== '');
}

/**
 * The UTF-8 text a base64 payload encodes. Not atob() on its own: that yields
 * one character per *byte*, so any non-ASCII in a message would come out as
 * mojibake in the headers view and the .eml download.
 */
function base64ToText(base64: string): string {
  return new TextDecoder().decode(base64ToBytes(base64));
}

function base64ToBytes(base64: string): Uint8Array {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}
