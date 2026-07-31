// The demo backend's REST layer: the counterpart of internal/handler and the
// use-case half of internal/repository, answering the same /api/v1 routes the
// Go server does, from browser-local storage.
//
// Everything the web UI sends goes through here — including the requests that
// never touch web/ts/api/client.ts, namely the <iframe src> that loads a
// message body and the <a href> that downloads an attachment. Intercepting at
// the network layer rather than swapping the API client out is what keeps the
// frontend byte-for-byte the same between the demo and the real thing.
//
// Parity with the Go server is the contract: each function below names the Go
// original it mirrors, and the divergences that remain are listed in
// spec/REQUIREMENTS.md § Demo Mode. Don't add one silently.
//
// See model.ts for why these are globals rather than module exports.

// ── Response helpers ─────────────────────────────────────────────────────────

function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', ...headers },
  });
}

function errorResponse(err: unknown): Response {
  if (err instanceof ApiError) return jsonResponse(err.status, { error: err.message });
  return jsonResponse(500, { error: 'internal server error: ' + String(err) });
}

function noContentResponse(): Response {
  return new Response(null, { status: 204, headers: { 'Cache-Control': 'no-store' } });
}

/**
 * Mirrors handler.contentDispositionFilename: CR, LF, NUL and " are stripped in
 * a single pass, and a non-ASCII name switches to the RFC 8187 form.
 */
function contentDisposition(filename: string): string {
  let clean = '';
  let hasNonASCII = false;
  for (const ch of filename) {
    if (ch === '\r' || ch === '\n' || ch === '\0' || ch === '"') continue;
    if (ch.codePointAt(0)! > 127) hasNonASCII = true;
    clean += ch;
  }
  if (hasNonASCII) return `attachment; filename*=UTF-8''${rfc8187Encode(clean)}`;
  return `attachment; filename="${clean}"`;
}

/** Mirrors handler.rfc8187Encode: percent-encode everything outside attr-char. */
function rfc8187Encode(s: string): string {
  const attrChar = /[A-Za-z0-9!#$&+\-.^_`|~]/;
  let out = '';
  for (const byte of new TextEncoder().encode(s)) {
    const ch = String.fromCharCode(byte);
    out += attrChar.test(ch) ? ch : '%' + byte.toString(16).toUpperCase().padStart(2, '0');
  }
  return out;
}

// ── Projections ──────────────────────────────────────────────────────────────

function toFolderDTO(folder: DemoFolder, state: DemoState): unknown {
  return {
    id: folder.id,
    name: folder.name,
    slug: folder.slug,
    position: folder.position,
    unread_count: state.messages.filter((m) => m.folderId === folder.id && !m.read).length,
    created_at: folder.createdAt,
  };
}

/**
 * The summary projection. `send_failed` is suppressed in Trash exactly as
 * repository.scanMessageSummary does: the count is preserved there but the
 * message is no longer actionable, so the badge would only be noise.
 */
function toSummaryDTO(msg: DemoMessage): unknown {
  return {
    id: msg.id,
    folder_id: msg.folderId,
    message_id: msg.messageId,
    from_addr: msg.fromAddr,
    to_addr: msg.toAddr,
    subject: msg.subject,
    date: msg.date,
    read: msg.read,
    flagged: msg.flagged,
    has_attachments: msg.hasAttachments,
    send_failed: msg.sendFailureCount > 0 && msg.folderId !== FOLDER_TRASH,
    created_at: msg.createdAt,
  };
}

/** Mirrors model.DBMessage.ToOASMessage, including the angle brackets it re-adds. */
function toDetailDTO(msg: DemoMessage, attachments: DemoAttachment[]): unknown {
  return {
    id: msg.id,
    folder_id: msg.folderId,
    message_id: msg.messageId,
    in_reply_to: msg.inReplyTo,
    references: splitNL(msg.references).map((r) => `<${r}>`),
    from_addr: msg.fromAddr,
    to_addr: msg.toAddr,
    cc_addr: msg.ccAddr,
    bcc_addr: msg.bccAddr,
    reply_to_addr: msg.replyToAddr,
    subject: msg.subject,
    date: msg.date,
    body_text: msg.bodyText,
    body_html: msg.bodyHtml,
    has_external_images: msg.hasExternalImages,
    // Unlike the summary, the detail projection does not suppress this in
    // Trash — model.ToOASMessage does not either; the UI applies the folder
    // rule itself when it renders the badge.
    send_failed: msg.sendFailureCount > 0,
    read: msg.read,
    flagged: msg.flagged,
    send_at: msg.sendAt,
    send_error: msg.sendError,
    snoozed_until: msg.snoozedUntil,
    snooze_folder_id: msg.snoozeFolder,
    created_at: msg.createdAt,
    updated_at: msg.updatedAt,
    attachments: attachments.map(toAttachmentMetaDTO),
  };
}

function toAttachmentMetaDTO(att: DemoAttachment): unknown {
  return { id: att.id, filename: att.filename, content_type: att.contentType, size: att.size };
}

function toIdentityDTO(identity: DemoIdentity): unknown {
  return {
    id: identity.id,
    name: identity.name,
    address: identity.address,
    is_default: identity.isDefault,
    position: identity.position,
    signature: identity.signature,
  };
}

function toContactDTO(contact: DemoContact): unknown {
  return {
    id: contact.id,
    address: contact.address,
    name: contact.name,
    created_at: contact.createdAt,
    updated_at: contact.updatedAt,
  };
}

function toFilterDTO(filter: DemoFilter): unknown {
  return {
    id: filter.id,
    position: filter.position,
    name: filter.name,
    match_from: filter.matchFrom,
    match_to: filter.matchTo,
    match_subject: filter.matchSubject,
    action: filter.action,
    folder_id: filter.folderId,
    stop: filter.stop,
  };
}

function toSpamFilterDTO(spam: DemoSpamFilter): unknown {
  return {
    enabled: spam.enabled,
    score_header: spam.scoreHeader,
    score_threshold: spam.scoreThreshold,
  };
}

// ── Lookups ──────────────────────────────────────────────────────────────────

function findMessage(state: DemoState, id: number): DemoMessage {
  const msg = state.messages.find((m) => m.id === id);
  if (msg === undefined) throw notFoundError('message not found');
  return msg;
}

/** A message that is in Drafts, which is what every /drafts route requires. */
function findDraft(state: DemoState, id: number): DemoMessage {
  const msg = state.messages.find((m) => m.id === id && m.folderId === FOLDER_DRAFTS);
  if (msg === undefined) throw notFoundError('draft not found');
  return msg;
}

function findFolder(state: DemoState, id: number): DemoFolder {
  const folder = state.folders.find((f) => f.id === id);
  if (folder === undefined) throw notFoundError('folder not found');
  return folder;
}

function attachmentsOf(state: DemoState, messageId: number): DemoAttachment[] {
  return state.attachments.filter((a) => a.messageId === messageId).sort((a, b) => a.id - b.id);
}

/** Mirrors handler.parsePagination: limit defaults to 50 and is capped at 200. */
function pagination(url: URL): { limit: number; offset: number } {
  let limit = DEFAULT_LIMIT;
  const rawLimit = Number(url.searchParams.get('limit'));
  if (Number.isFinite(rawLimit) && rawLimit > 0) limit = Math.min(rawLimit, MAX_LIMIT);
  let offset = 0;
  const rawOffset = Number(url.searchParams.get('offset'));
  if (Number.isFinite(rawOffset) && rawOffset > 0) offset = rawOffset;
  return { limit, offset };
}

function optionalBool(url: URL, name: string): boolean | null {
  const raw = url.searchParams.get(name);
  if (raw === null) return null;
  return raw === 'true' || raw === '1';
}

/** A path segment that has to be a message/folder/… id. */
function pathId(segment: string): number {
  const id = Number(segment);
  if (!Number.isInteger(id)) throw notFoundError('not found');
  return id;
}

async function readJSON(request: Request): Promise<Record<string, unknown>> {
  try {
    const body = (await request.json()) as unknown;
    if (body === null || typeof body !== 'object') return {};
    return body as Record<string, unknown>;
  } catch {
    throw validationError('invalid JSON body');
  }
}

function optString(body: Record<string, unknown>, key: string): string | undefined {
  const value = body[key];
  return typeof value === 'string' ? value : undefined;
}

function optNumber(body: Record<string, unknown>, key: string): number | undefined {
  const value = body[key];
  return typeof value === 'number' ? value : undefined;
}

function optBool(body: Record<string, unknown>, key: string): boolean | undefined {
  const value = body[key];
  return typeof value === 'boolean' ? value : undefined;
}

function idList(body: Record<string, unknown>, label: string): number[] {
  const raw = body.ids;
  if (!Array.isArray(raw)) throw validationError(`${label} must contain at least one id`);
  const ids = raw.filter((v): v is number => typeof v === 'number');
  if (ids.length === 0) throw validationError(`${label} must contain at least one id`);
  if (ids.length > MAX_BULK_IDS) throw validationError(`${label} must not exceed ${MAX_BULK_IDS}`);
  return ids;
}

// ── Contacts, as the send paths maintain them ────────────────────────────────

/**
 * Mirrors ContactRepository.UpsertContact: insert the address, or fill in the
 * name if the stored one is still empty. An existing name is never overwritten.
 */
function upsertContact(state: DemoState, address: string, name: string): void {
  const folded = foldAddress(address);
  const now = nowTimestamp();
  const existing = state.contacts.find((c) => c.address === folded);
  if (existing === undefined) {
    state.contacts.push({
      id: state.nextContactId++,
      address: folded,
      name,
      createdAt: now,
      updatedAt: now,
    });
    return;
  }
  if (existing.name === '') {
    existing.name = name;
    existing.updatedAt = now;
  }
}

/** Mirrors handler.upsertRecipients: a malformed list is silently skipped. */
function upsertRecipients(state: DemoState, addrList: string): void {
  if (addrList === '') return;
  try {
    for (const addr of parseAddressList(addrList)) upsertContact(state, addr.address, addr.name);
  } catch {
    // Not parseable: the server's mail.ParseAddressList fails the same way and
    // the send still succeeds.
  }
}

// ── The scheduler, run on demand ─────────────────────────────────────────────

/**
 * Everything the server's 60-second background goroutine does
 * (service.Scheduler), plus the demo's own auto-reply delivery.
 *
 * It runs at the start of every request instead of on a timer: a service worker
 * is stopped whenever it goes idle, so a periodic timer would stop with it. The
 * observable difference is that a scheduled send or an expiring snooze lands on
 * the next request rather than within a minute — and since the UI polls the
 * folder list every 30 seconds, that is the same thing in practice.
 *
 * Returns whether anything changed, so the caller knows to persist.
 */
function runScheduler(state: DemoState, now: number): boolean {
  let changed = deliverDueReplies(state, now);
  if (processDeferredSends(state, now)) changed = true;
  if (processSnoozeExpiry(state, now)) changed = true;
  return changed;
}

/**
 * Delivers the fake replies that have come due (see reply.ts). Each arrives the
 * way an inbound message does — through the filter chain, into Inbox unless a
 * filter says otherwise, and with its sender upserted as a contact.
 */
function deliverDueReplies(state: DemoState, now: number): boolean {
  const due = state.pendingReplies.filter((p) => p.dueAt <= now);
  if (due.length === 0) return false;
  state.pendingReplies = state.pendingReplies.filter((p) => p.dueAt > now);

  const folderIds = new Set(state.folders.map((f) => f.id));
  for (const pending of due) {
    const source = state.messages.find((m) => m.id === pending.sourceMessageId);
    if (source === undefined) continue; // the sent message was deleted meanwhile
    const date = new Date(now);
    const reply = buildAutoReply(source, date);
    if (reply === null) continue;

    const outcome = applyFilters(
      { fromAddr: reply.fromAddr, toAddr: reply.toAddr, ccAddr: '', subject: reply.subject },
      state.filters,
      folderIds,
    );
    if (outcome.folderId === null) continue; // a `drop` filter matched

    const messageId = newMessageId(reply.fromAddr);
    const timestamp = toTimestamp(date);
    const raw = buildRawMessage({
      fromName: '',
      fromAddr: reply.fromAddr,
      toAddr: reply.toAddr,
      ccAddr: '',
      bccAddr: '',
      replyToAddr: '',
      subject: reply.subject,
      bodyText: reply.bodyText,
      bodyHtml: '',
      messageId,
      inReplyTo: reply.inReplyTo ?? '',
      references: splitNL(reply.references),
      date,
    }, []);

    state.messages.push({
      id: state.nextMessageId++,
      folderId: outcome.folderId,
      identityId: null,
      messageId,
      inReplyTo: reply.inReplyTo,
      references: reply.references,
      fromAddr: reply.fromAddr,
      toAddr: reply.toAddr,
      ccAddr: '',
      bccAddr: '',
      replyToAddr: '',
      subject: reply.subject,
      date: timestamp,
      bodyText: reply.bodyText,
      bodyHtml: '',
      raw,
      read: outcome.markRead,
      flagged: false,
      hasAttachments: false,
      hasExternalImages: false,
      sendAt: null,
      snoozedUntil: null,
      snoozeFolder: null,
      sendError: null,
      sendFailureCount: 0,
      createdAt: timestamp,
      updatedAt: timestamp,
    });

    const sender = firstAddress(reply.fromAddr);
    if (sender !== null) upsertContact(state, sender.address, sender.name);
  }
  return true;
}

/**
 * Sends the scheduled messages whose send_at has passed. Mirrors
 * Scheduler.processDeferredSends, minus the failure handling: there is no
 * sendmail to fail, so send_failure_count and send_error stay at their initial
 * values and a demo message never falls back to Drafts.
 */
function processDeferredSends(state: DemoState, now: number): boolean {
  const due = state.messages.filter(
    (m) => m.folderId === FOLDER_SCHEDULED && m.sendAt !== null && Date.parse(m.sendAt) <= now,
  );
  if (due.length === 0) return false;
  for (const msg of due) deliverSentMessage(state, msg, new Date(now));
  return true;
}

/** Mirrors Scheduler.processSnoozeExpiry, including the `read = 0` it forces. */
function processSnoozeExpiry(state: DemoState, now: number): boolean {
  const due = state.messages.filter(
    (m) => m.folderId === FOLDER_SNOOZED && m.snoozedUntil !== null && Date.parse(m.snoozedUntil) <= now,
  );
  if (due.length === 0) return false;
  for (const msg of due) {
    msg.folderId = msg.snoozeFolder ?? FOLDER_INBOX;
    msg.snoozedUntil = null;
    msg.snoozeFolder = null;
    msg.read = false;
    msg.updatedAt = nowTimestamp();
  }
  return true;
}

// ── Sending ──────────────────────────────────────────────────────────────────

/**
 * Moves a message that was waiting in Scheduled into Sent: assign a Message-ID,
 * build the RFC 5322 source, record the recipients as contacts, and queue the
 * reply. Mirrors DraftRepository.MarkSent plus the surrounding work in
 * Scheduler.sendScheduledMessage — with the sendmail pipe left out, since a
 * demo has nothing to pipe to.
 */
function deliverSentMessage(state: DemoState, msg: DemoMessage, date: Date): void {
  const identity = msg.identityId !== null
    ? state.identities.find((i) => i.id === msg.identityId)
    : undefined;
  const attachments = attachmentsOf(state, msg.id);

  msg.messageId = newMessageId(msg.fromAddr);
  msg.raw = buildRawMessageFor(msg, identity?.name ?? '', attachments, date);
  msg.folderId = FOLDER_SENT;
  msg.read = true;
  msg.sendAt = null;
  msg.updatedAt = toTimestamp(date);

  upsertRecipients(state, msg.toAddr);
  upsertRecipients(state, msg.ccAddr);
  upsertRecipients(state, msg.bccAddr);
  queueAutoReply(state, msg, date);
}

/** Builds `raw` for a stored message; attachment bytes are read separately. */
function buildRawMessageFor(
  msg: DemoMessage,
  fromName: string,
  attachments: DemoAttachment[],
  date: Date,
): string {
  return buildRawMessage(
    {
      fromName,
      fromAddr: msg.fromAddr,
      toAddr: msg.toAddr,
      ccAddr: msg.ccAddr,
      bccAddr: msg.bccAddr,
      replyToAddr: msg.replyToAddr,
      subject: msg.subject,
      bodyText: msg.bodyText,
      bodyHtml: msg.bodyHtml,
      messageId: msg.messageId ?? '',
      inReplyTo: msg.inReplyTo ?? '',
      references: splitNL(msg.references),
      date,
    },
    // The bytes are in a separate object store and `raw` is only ever shown
    // back to the user, so attachments appear in the MIME structure by name and
    // type with an empty body rather than forcing an extra read on every send.
    attachments.map((a) => ({ filename: a.filename, contentType: a.contentType, data: new Uint8Array(0) })),
  );
}

function queueAutoReply(state: DemoState, sent: DemoMessage, date: Date): void {
  state.pendingReplies.push({
    dueAt: date.getTime() + AUTO_REPLY_DELAY_MS,
    sourceMessageId: sent.id,
  });
}

// ── Compose-field validation ─────────────────────────────────────────────────

interface SendFields {
  toAddr: string;
  ccAddr: string;
  bccAddr: string;
  replyToAddr: string;
  subject: string;
  bodyText: string;
  bodyHtml: string;
  inReplyTo: string;
  references: string;
}

/** Mirrors handler.validateAndStripAddrList. */
function validateAddrList(raw: string, field: string): string {
  const clean = stripHeaderControls(raw);
  if (byteLength(clean) > MAX_ADDR_LIST_LEN) {
    throw validationError(`${field} must not exceed ${MAX_ADDR_LIST_LEN} characters`);
  }
  if (clean === '') return '';
  try {
    for (const addr of parseAddressList(clean)) {
      if (addr.address === '') throw new Error('empty address');
    }
  } catch (err) {
    throw validationError(`invalid ${field}: ${err instanceof Error ? err.message : String(err)}`);
  }
  return clean;
}

/** Mirrors handler.validateSendFields, shared by the send and draft endpoints. */
function validateSendFields(body: Record<string, unknown>): SendFields {
  const subject = stripHeaderControls(optString(body, 'subject') ?? '');
  if (byteLength(subject) > MAX_SUBJECT_LEN) {
    throw validationError(`subject must not exceed ${MAX_SUBJECT_LEN} characters`);
  }
  const rawRefs = Array.isArray(body.references)
    ? (body.references as unknown[]).filter((r): r is string => typeof r === 'string')
    : [];
  return {
    toAddr: validateAddrList(optString(body, 'to_addr') ?? '', 'to_addr'),
    ccAddr: validateAddrList(optString(body, 'cc_addr') ?? '', 'cc_addr'),
    bccAddr: validateAddrList(optString(body, 'bcc_addr') ?? '', 'bcc_addr'),
    replyToAddr: validateAddrList(optString(body, 'reply_to_addr') ?? '', 'reply_to_addr'),
    subject,
    bodyText: optString(body, 'body_text') ?? '',
    bodyHtml: optString(body, 'body_html') ?? '',
    inReplyTo: stripAngleBrackets(stripHeaderControls(optString(body, 'in_reply_to') ?? '')),
    references: normalizeReferences(rawRefs),
  };
}

/** Mirrors handler.isScheduled: deferred only when more than 60 s out. */
function scheduledFor(body: Record<string, unknown>, now: number): Date | null {
  const raw = body.send_at;
  if (typeof raw !== 'string' || raw === '') return null;
  const at = Date.parse(raw);
  if (Number.isNaN(at)) return null;
  return at > now + SCHEDULE_THRESHOLD_MS ? new Date(at) : null;
}

/** The raw send_at a draft stores verbatim, acted on only when it is sent. */
function draftSendAt(body: Record<string, unknown>): string | null {
  const raw = body.send_at;
  if (typeof raw !== 'string' || raw === '') return null;
  const at = Date.parse(raw);
  return Number.isNaN(at) ? null : toTimestamp(new Date(at));
}

/** Mirrors handler.resolveIdentityForSend: an explicit id, else the default. */
function resolveIdentityForSend(state: DemoState, identityId: number | undefined): DemoIdentity {
  if (identityId !== undefined) {
    const identity = state.identities.find((i) => i.id === identityId);
    if (identity === undefined) throw validationError('identity not found');
    return identity;
  }
  const fallback = state.identities.find((i) => i.isDefault);
  if (fallback === undefined) {
    throw validationError('no identity configured; create one in Settings → Identities first');
  }
  return fallback;
}

/**
 * Mirrors handler.resolveIdentityForDraft plus DraftRepository.resolveFromAddr:
 * an unknown id is a 400, an absent one leaves identity_id NULL and takes
 * from_addr from the default identity — or leaves it empty when there is none,
 * so a draft can be saved before any identity exists.
 */
function resolveIdentityForDraft(
  state: DemoState,
  identityId: number | undefined,
): { identityId: number | null; fromAddr: string } {
  if (identityId !== undefined) {
    const identity = state.identities.find((i) => i.id === identityId);
    if (identity === undefined) throw validationError('identity not found');
    return { identityId: identity.id, fromAddr: identity.address };
  }
  const fallback = state.identities.find((i) => i.isDefault);
  return { identityId: null, fromAddr: fallback?.address ?? '' };
}

// ── Attachments arriving over multipart ──────────────────────────────────────

interface UploadedAttachment {
  filename: string;
  contentType: string;
  data: ArrayBuffer;
}

/**
 * The `message` JSON part and the `attachments` files of a multipart request,
 * mirroring how ogen presents them to handler.readMultipartAttachments.
 */
async function readMultipart(
  request: Request,
): Promise<{ body: Record<string, unknown>; files: UploadedAttachment[] }> {
  let form: FormData;
  try {
    form = await request.formData();
  } catch {
    throw validationError('invalid multipart body');
  }

  let body: Record<string, unknown> = {};
  const messagePart = form.get('message');
  if (typeof messagePart === 'string' && messagePart !== '') {
    try {
      const parsed = JSON.parse(messagePart) as unknown;
      if (parsed !== null && typeof parsed === 'object') body = parsed as Record<string, unknown>;
    } catch {
      throw validationError('invalid message part');
    }
  }

  const files: UploadedAttachment[] = [];
  for (const entry of form.getAll('attachments')) {
    if (typeof entry === 'string') continue;
    const data = await entry.arrayBuffer();
    if (data.byteLength > MAX_ATTACHMENT_BYTES) {
      throw new ApiError(413,
        `attachment "${entry.name}" is larger than the demo's ${MAX_ATTACHMENT_BYTES >> 20} MiB limit`);
    }
    files.push({
      filename: entry.name === '' ? 'untitled' : entry.name,
      contentType: entry.type === '' ? 'application/octet-stream' : entry.type,
      data,
    });
  }
  return { body, files };
}

async function storeAttachments(
  state: DemoState,
  messageId: number,
  files: UploadedAttachment[],
): Promise<void> {
  for (const file of files) {
    const id = state.nextAttachmentId++;
    await putAttachmentData(id, file.data);
    state.attachments.push({
      id,
      messageId,
      filename: file.filename,
      contentType: file.contentType,
      size: file.data.byteLength,
    });
  }
  if (files.length > 0) {
    const msg = state.messages.find((m) => m.id === messageId);
    if (msg !== undefined) msg.hasAttachments = true;
  }
}

/** Deletes messages and everything that hangs off them (the ON DELETE CASCADE). */
async function deleteMessages(state: DemoState, ids: Set<number>): Promise<void> {
  const orphaned = state.attachments.filter((a) => ids.has(a.messageId));
  state.attachments = state.attachments.filter((a) => !ids.has(a.messageId));
  state.messages = state.messages.filter((m) => !ids.has(m.id));
  state.pendingReplies = state.pendingReplies.filter((p) => !ids.has(p.sourceMessageId));
  await removeAttachmentData(orphaned.map((a) => a.id));
}

// ── Folders ──────────────────────────────────────────────────────────────────

function listFolders(state: DemoState): Response {
  const folders = [...state.folders].sort((a, b) => a.position - b.position || a.id - b.id);
  return jsonResponse(200, {
    total: folders.length,
    items: folders.map((f) => toFolderDTO(f, state)),
  });
}

async function createFolder(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const name = (optString(body, 'name') ?? '').trim();
  if (name === '') throw validationError('name is required');
  if (runeLength(name) > MAX_NAME_LEN) {
    throw validationError(`name must not exceed ${MAX_NAME_LEN} characters`);
  }
  if (state.folders.some((f) => f.name === name)) throw conflictError('folder name already exists');

  const position = optNumber(body, 'position') ??
    state.folders.reduce((max, f) => Math.max(max, f.position + 1), 0);
  const folder: DemoFolder = {
    id: state.nextFolderId++,
    name,
    slug: uniqueSlug(new Set(state.folders.map((f) => f.slug)), toSlug(name)),
    position,
    createdAt: nowTimestamp(),
  };
  state.folders.push(folder);
  await saveState(state);
  return jsonResponse(201, toFolderDTO(folder, state));
}

async function patchFolder(state: DemoState, id: number, body: Record<string, unknown>): Promise<Response> {
  const rawName = optString(body, 'name');
  if (id < FIRST_USER_FOLDER && rawName !== undefined) {
    throw validationError('built-in folders cannot be renamed');
  }
  const folder = findFolder(state, id);

  if (rawName !== undefined) {
    const name = rawName.trim();
    if (name === '') throw validationError('name must not be empty');
    if (runeLength(name) > MAX_NAME_LEN) {
      throw validationError(`name must not exceed ${MAX_NAME_LEN} characters`);
    }
    if (state.folders.some((f) => f.name === name && f.id !== id)) {
      throw conflictError('folder name already exists');
    }
    folder.name = name; // the slug is never updated, exactly as on the server
  }

  const position = optNumber(body, 'position');
  if (position !== undefined) folder.position = position;

  await saveState(state);
  return jsonResponse(200, toFolderDTO(folder, state));
}

async function deleteFolder(state: DemoState, id: number): Promise<Response> {
  if (id < FIRST_USER_FOLDER) throw validationError('cannot delete built-in folder');
  findFolder(state, id);
  for (const msg of state.messages) {
    if (msg.folderId === id) msg.folderId = FOLDER_TRASH;
  }
  state.folders = state.folders.filter((f) => f.id !== id);
  await saveState(state);
  return noContentResponse();
}

/** Mirrors FolderRepository.ReorderFolders: every existing id, exactly once. */
async function reorderFolders(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const ids = Array.isArray(body.ids) ? (body.ids as unknown[]).filter((v): v is number => typeof v === 'number') : [];
  validateReorder(ids, state.folders.map((f) => f.id));
  ids.forEach((id, position) => {
    const folder = state.folders.find((f) => f.id === id);
    if (folder !== undefined) folder.position = position;
  });
  await saveState(state);
  return jsonResponse(200, { updated: ids.length });
}

/** The reorder precondition shared by folders, filters and identities. */
function validateReorder(ids: number[], existing: number[]): void {
  const seen = new Set<number>();
  const known = new Set(existing);
  for (const id of ids) {
    if (seen.has(id)) throw validationError('duplicate id');
    seen.add(id);
    if (!known.has(id)) throw validationError('unknown id');
  }
  if (seen.size !== known.size) {
    throw validationError('incomplete reorder; all ids must be supplied');
  }
}

function listFolderMessages(state: DemoState, folderId: number, url: URL): Response {
  findFolder(state, folderId);
  const { limit, offset } = pagination(url);
  const unread = optionalBool(url, 'unread');
  const flagged = optionalBool(url, 'flagged');

  const matching = state.messages
    .filter((m) => m.folderId === folderId)
    .filter((m) => unread === null || m.read !== unread)
    .filter((m) => flagged === null || m.flagged === flagged)
    .sort((a, b) => compareDateDesc(a, b));

  return jsonResponse(200, {
    total: matching.length,
    items: matching.slice(offset, offset + limit).map(toSummaryDTO),
  });
}

/** `ORDER BY date DESC`, with the row id as SQLite's stable tiebreak. */
function compareDateDesc(a: DemoMessage, b: DemoMessage): number {
  if (a.date === b.date) return a.id - b.id;
  return a.date < b.date ? 1 : -1;
}

async function markFolderRead(state: DemoState, folderId: number): Promise<Response> {
  findFolder(state, folderId);
  let updated = 0;
  for (const msg of state.messages) {
    if (msg.folderId === folderId && !msg.read) {
      msg.read = true;
      msg.updatedAt = nowTimestamp();
      updated++;
    }
  }
  await saveState(state);
  return jsonResponse(200, { updated });
}

/**
 * Mirrors FolderRepository.DeleteAllMessagesInFolder: emptying Trash or Junk
 * deletes for good, emptying anything else moves to Trash.
 */
async function deleteFolderMessages(state: DemoState, folderId: number): Promise<Response> {
  if (MANAGED_FOLDERS.includes(folderId)) {
    throw validationError('cannot delete all messages in this folder');
  }
  findFolder(state, folderId);

  if (folderId === FOLDER_TRASH || folderId === FOLDER_JUNK) {
    const ids = new Set(state.messages.filter((m) => m.folderId === folderId).map((m) => m.id));
    await deleteMessages(state, ids);
    await saveState(state);
    return jsonResponse(200, { moved_to_trash: 0, permanently_deleted: ids.size });
  }

  let moved = 0;
  for (const msg of state.messages) {
    if (msg.folderId === folderId) {
      msg.folderId = FOLDER_TRASH;
      moved++;
    }
  }
  await saveState(state);
  return jsonResponse(200, { moved_to_trash: moved, permanently_deleted: 0 });
}

// ── Messages ─────────────────────────────────────────────────────────────────

function getMessage(state: DemoState, id: number): Response {
  const msg = findMessage(state, id);
  return jsonResponse(200, toDetailDTO(msg, attachmentsOf(state, id)));
}

async function patchMessage(state: DemoState, id: number, body: Record<string, unknown>): Promise<Response> {
  // The target-folder check comes before the lookup, as in handler.MessagesIDPatch:
  // a move to Drafts/Scheduled/Snoozed is a 400 even for an id that does not exist.
  const folderId = optNumber(body, 'folder_id');
  if (folderId !== undefined && MANAGED_FOLDERS.includes(folderId)) {
    throw validationError('cannot move to this folder');
  }
  const msg = findMessage(state, id);

  if (folderId !== undefined) {
    if (MANAGED_FOLDERS.includes(msg.folderId)) throw validationError('cannot move from this folder');
    msg.folderId = folderId;
    // Mirrors the extra SET clauses in MessageRepository.UpdateMessage.
    if (folderId === FOLDER_TRASH || folderId === FOLDER_JUNK) {
      msg.snoozedUntil = null;
      msg.snoozeFolder = null;
      msg.sendAt = null;
    }
  }
  const read = optBool(body, 'read');
  if (read !== undefined) msg.read = read;
  const flagged = optBool(body, 'flagged');
  if (flagged !== undefined) msg.flagged = flagged;

  msg.updatedAt = nowTimestamp();
  await saveState(state);
  return jsonResponse(200, toSummaryDTO(msg));
}

async function bulkPatchMessages(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const ids = idList(body, 'ids');
  const read = optBool(body, 'read');
  const flagged = optBool(body, 'flagged');

  const targets = ids.map((id) => findMessage(state, id)); // all-or-nothing: a missing id is a 404
  if (read === undefined && flagged === undefined) return jsonResponse(200, { updated: 0 });

  for (const msg of targets) {
    if (read !== undefined) msg.read = read;
    if (flagged !== undefined) msg.flagged = flagged;
    msg.updatedAt = nowTimestamp();
  }
  await saveState(state);
  return jsonResponse(200, { updated: targets.length });
}

/**
 * Mirrors MessageRepository.DeleteMessage: a message already in Trash or Junk
 * is deleted for good, anything else moves to Trash, and the managed folders
 * refuse outright.
 */
async function deleteMessage(state: DemoState, id: number): Promise<Response> {
  const msg = findMessage(state, id);
  if (MANAGED_FOLDERS.includes(msg.folderId)) {
    throw validationError('cannot delete a message from this folder');
  }
  if (msg.folderId === FOLDER_TRASH || msg.folderId === FOLDER_JUNK) {
    await deleteMessages(state, new Set([id]));
  } else {
    msg.folderId = FOLDER_TRASH;
    msg.snoozedUntil = null;
    msg.snoozeFolder = null;
    msg.sendAt = null;
  }
  await saveState(state);
  return noContentResponse();
}

async function bulkDeleteMessages(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const ids = idList(body, 'ids');
  const targets = ids.map((id) => findMessage(state, id));
  if (targets.some((m) => MANAGED_FOLDERS.includes(m.folderId))) {
    throw validationError('cannot delete a message from this folder');
  }

  const permanent = new Set(
    targets.filter((m) => m.folderId === FOLDER_TRASH || m.folderId === FOLDER_JUNK).map((m) => m.id),
  );
  let moved = 0;
  for (const msg of targets) {
    if (permanent.has(msg.id)) continue;
    msg.folderId = FOLDER_TRASH;
    msg.snoozedUntil = null;
    msg.snoozeFolder = null;
    msg.sendAt = null;
    moved++;
  }
  await deleteMessages(state, permanent);
  await saveState(state);
  return jsonResponse(200, { moved_to_trash: moved, permanently_deleted: permanent.size });
}

async function moveMessages(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const ids = idList(body, 'ids');
  const folderId = optNumber(body, 'folder_id');
  if (folderId === undefined || MANAGED_FOLDERS.includes(folderId)) {
    throw validationError('cannot move to this folder');
  }
  if (!state.folders.some((f) => f.id === folderId)) {
    throw validationError('target folder not found');
  }

  const targets = ids.map((id) => findMessage(state, id));
  if (targets.some((m) => MANAGED_FOLDERS.includes(m.folderId))) {
    throw validationError('cannot move a message from this folder');
  }
  for (const msg of targets) {
    msg.folderId = folderId;
    if (folderId === FOLDER_TRASH) {
      msg.snoozedUntil = null;
      msg.snoozeFolder = null;
      msg.sendAt = null;
    }
  }
  await saveState(state);
  return jsonResponse(200, { updated: targets.length });
}

/**
 * Full-text search. The server hands the query to FTS5 as a single quoted
 * phrase over from/to/cc/subject/body, so this matches the same way (see
 * phraseMatches). What it cannot reproduce is `ORDER BY rank`: bm25 needs
 * corpus statistics FTS5 maintains and this does not, so results are ordered
 * by a match-count score and then by date — the one search divergence.
 *
 * The from_addr/to_addr refinements narrow that result set the way
 * repository.SearchMessages does: a case-insensitive substring, with to_addr
 * matching either the To or the Cc header.
 */
function searchMessages(state: DemoState, url: URL): Response {
  const q = (url.searchParams.get('q') ?? '').trim();
  if (q === '') throw validationError('q must contain at least one non-whitespace character');
  if (runeLength(q) > MAX_QUERY_LEN) {
    throw validationError(`q must not exceed ${MAX_QUERY_LEN} characters`);
  }

  const rawFolder = url.searchParams.get('folder_id');
  const folderId = rawFolder === null ? null : Number(rawFolder);
  const dateFrom = parseDateParam(url, 'date_from');
  const dateTo = parseDateParam(url, 'date_to');
  const fromAddr = addressFilterParam(url, 'from_addr');
  const toAddr = addressFilterParam(url, 'to_addr');
  const { limit, offset } = pagination(url);
  const queryTokens = tokenizeText(q).map((t) => t.lower);

  const scored: Array<{ msg: DemoMessage; score: number }> = [];
  for (const msg of state.messages) {
    if (folderId !== null) {
      if (msg.folderId !== folderId) continue;
    } else if ([FOLDER_DRAFTS, FOLDER_SCHEDULED, FOLDER_JUNK].includes(msg.folderId)) {
      // The default scope excludes Drafts, Scheduled and Junk.
      continue;
    }
    if (dateFrom !== null && msg.date < dateFrom) continue;
    if (dateTo !== null && msg.date >= dateTo) continue;
    if (fromAddr !== null && !containsFold(msg.fromAddr, fromAddr)) continue;
    if (toAddr !== null && !containsFold(msg.toAddr, toAddr) && !containsFold(msg.ccAddr, toAddr)) {
      continue;
    }

    const score = searchScore(msg, queryTokens);
    if (score > 0) scored.push({ msg, score });
  }
  scored.sort((a, b) => b.score - a.score || compareDateDesc(a.msg, b.msg));

  const items = scored.slice(offset, offset + limit).map(({ msg }) => ({
    ...(toSummaryDTO(msg) as Record<string, unknown>),
    // Search does not apply the Trash suppression the folder listing does —
    // repository.SearchMessages sets this from the raw count.
    send_failed: msg.sendFailureCount > 0,
    snippet: htmlEscape(buildSnippet(msg.bodyText.slice(0, SNIPPET_SOURCE_LIMIT), q)),
  }));
  return jsonResponse(200, { total: scored.length, items });
}

function parseDateParam(url: URL, name: string): string | null {
  const raw = url.searchParams.get(name);
  if (raw === null || raw === '') return null;
  const at = Date.parse(raw);
  return Number.isNaN(at) ? null : toTimestamp(new Date(at));
}

/**
 * An optional address refinement. Mirrors handler.optAddressFilter: absent,
 * empty and whitespace-only all mean "no filter"; over the cap is a 400, which
 * on the server ogen raises from the schema's maxLength.
 */
function addressFilterParam(url: URL, name: string): string | null {
  const raw = url.searchParams.get(name);
  if (raw === null) return null;
  if (runeLength(raw) > MAX_ADDR_FILTER_LEN) {
    throw validationError(`${name} must not exceed ${MAX_ADDR_FILTER_LEN} characters`);
  }
  const trimmed = raw.trim();
  return trimmed === '' ? null : trimmed;
}

/** Weighted so a subject hit outranks a body hit, standing in for bm25. */
function searchScore(msg: DemoMessage, queryTokens: string[]): number {
  let score = 0;
  if (phraseMatches(tokenizeText(msg.subject), queryTokens)) score += 4;
  if (phraseMatches(tokenizeText(msg.fromAddr), queryTokens)) score += 2;
  if (phraseMatches(tokenizeText(msg.toAddr), queryTokens)) score += 1;
  if (phraseMatches(tokenizeText(msg.ccAddr), queryTokens)) score += 1;
  if (phraseMatches(tokenizeText(msg.bodyText), queryTokens)) score += 2;
  return score;
}

/**
 * The thread a message belongs to: the iterative transitive closure over
 * In-Reply-To and References, mirroring MessageRepository.GetMessageThread
 * including its 1000-message cap and its same-folder, same-normalised-subject
 * fallback for a message that links to nothing.
 */
function getThread(state: DemoState, id: number): Response {
  const seed = findMessage(state, id);

  const found = new Set<number>();
  const known = new Set<string>();
  const referenced = new Set<string>();
  const add = (msg: DemoMessage) => {
    found.add(msg.id);
    if (msg.messageId !== null && msg.messageId !== '') known.add(msg.messageId);
    if (msg.inReplyTo !== null && msg.inReplyTo !== '') referenced.add(msg.inReplyTo);
    for (const ref of splitNL(msg.references)) referenced.add(ref);
  };
  add(seed);

  let truncated = false;
  while (found.size < THREAD_CAP) {
    const before = found.size;

    // Forward: anything that links to a message we already have.
    for (const msg of state.messages) {
      if (found.has(msg.id)) continue;
      const links =
        (msg.inReplyTo !== null && known.has(msg.inReplyTo)) ||
        splitNL(msg.references).some((ref) => known.has(ref));
      if (!links) continue;
      if (found.size >= THREAD_CAP) { truncated = true; break; }
      add(msg);
    }
    if (truncated) break;

    // Backward: anything a message we already have links to.
    for (const msg of state.messages) {
      if (found.has(msg.id)) continue;
      if (msg.messageId === null || !referenced.has(msg.messageId)) continue;
      if (found.size >= THREAD_CAP) { truncated = true; break; }
      add(msg);
    }
    if (truncated) break;

    if (found.size === before) break; // fixed point
  }
  if (found.size >= THREAD_CAP) truncated = true;

  if (found.size === 1) {
    const normalized = normalizeSubject(seed.subject);
    if (normalized !== '') {
      for (const msg of state.messages) {
        if (msg.id === seed.id || msg.folderId !== seed.folderId) continue;
        if (normalizeSubject(msg.subject).toLowerCase() !== normalized.toLowerCase()) continue;
        if (found.size >= THREAD_CAP) { truncated = true; break; }
        found.add(msg.id);
      }
    }
  }

  const items = state.messages
    .filter((m) => found.has(m.id))
    .sort((a, b) => (a.date === b.date ? a.id - b.id : a.date < b.date ? -1 : 1))
    .map(toSummaryDTO);
  return jsonResponse(200, { total: items.length, truncated, items });
}

/** The `raw` download; a draft has none and answers with `{}`, as the server does. */
function getRawMessage(state: DemoState, id: number): Response {
  const msg = findMessage(state, id);
  if (msg.raw === null) return jsonResponse(200, {});
  return new Response(msg.raw, {
    status: 200,
    headers: {
      'Content-Type': 'message/rfc822',
      'Content-Disposition': `attachment; filename=${id}.eml`,
      'Cache-Control': 'no-store',
    },
  });
}

function getMessageHeaders(state: DemoState, id: number): Response {
  const msg = findMessage(state, id);
  if (msg.raw === null) throw notFoundError('no headers for draft');
  let headerBlock = msg.raw;
  const crlfEnd = msg.raw.indexOf('\r\n\r\n');
  const lfEnd = msg.raw.indexOf('\n\n');
  if (crlfEnd >= 0) headerBlock = msg.raw.slice(0, crlfEnd);
  else if (lfEnd >= 0) headerBlock = msg.raw.slice(0, lfEnd);
  return new Response(headerBlock, {
    status: 200,
    headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'no-store' },
  });
}

/**
 * The message body as a standalone document, mirroring
 * handler.MessagesIDBodyGet — including the CSP that keeps external images out
 * until the user asks for them.
 *
 * The one addition is that the policy is repeated as a <meta> inside the
 * document. A sandboxed iframe has an opaque origin, and a browser does not
 * consult a service worker for a navigation from one, so in demo mode this can
 * never be reached as an <iframe src>: the page fetches it and hands it over as
 * srcdoc instead (see BodyIframe in views/MessageDetail.tsx), and response
 * headers do not survive that trip. The header is still set, so the two
 * responses differ only by the meta the server has no need of.
 */
function getMessageBody(state: DemoState, id: number, url: URL): Response {
  const msg = findMessage(state, id);
  const imgSrc = url.searchParams.get('external') === '1' ? 'https: data:' : 'data:';
  // frame-ancestors is deliberately left out of the meta copy: browsers ignore
  // it there, and the iframe is same-document anyway.
  const csp = `default-src 'none'; img-src ${imgSrc}; style-src 'unsafe-inline'`;
  const body =
    '<!DOCTYPE html>\n<html>\n<head><meta charset="utf-8">' +
    `<meta http-equiv="Content-Security-Policy" content="${csp}">` +
    '<base target="_blank"></head>\n' +
    `<body>${msg.bodyHtml}</body>\n</html>`;
  return new Response(body, {
    status: 200,
    headers: {
      'Content-Type': 'text/html; charset=utf-8',
      'Content-Security-Policy': csp + "; frame-ancestors 'self'",
      'X-Frame-Options': 'SAMEORIGIN',
      'Referrer-Policy': 'no-referrer',
      'Cache-Control': 'no-store',
    },
  });
}

async function snoozeMessage(state: DemoState, id: number, body: Record<string, unknown>): Promise<Response> {
  const until = optString(body, 'until');
  const at = until === undefined ? NaN : Date.parse(until);
  if (Number.isNaN(at)) throw validationError('until must be a date-time');
  if (at < Date.now() + SCHEDULE_THRESHOLD_MS) {
    throw validationError('snooze time must be at least 60 seconds in the future');
  }

  const msg = findMessage(state, id);
  // Forbidden everywhere but Inbox, a user folder, or Snoozed itself.
  if ([FOLDER_SENT, FOLDER_DRAFTS, FOLDER_TRASH, FOLDER_SCHEDULED, FOLDER_JUNK].includes(msg.folderId)) {
    throw validationError('cannot snooze a message in this folder');
  }
  if (msg.folderId !== FOLDER_SNOOZED) {
    msg.snoozeFolder = msg.folderId; // re-snoozing preserves the original folder
    msg.folderId = FOLDER_SNOOZED;
  }
  msg.snoozedUntil = toTimestamp(new Date(at));
  msg.updatedAt = nowTimestamp();
  await saveState(state);

  return jsonResponse(200, {
    id: msg.id,
    folder_id: msg.folderId,
    snoozed_until: msg.snoozedUntil,
    snooze_folder_id: msg.snoozeFolder,
  });
}

async function cancelSnooze(state: DemoState, id: number): Promise<Response> {
  const msg = findMessage(state, id);
  if (msg.folderId !== FOLDER_SNOOZED) throw validationError('message is not snoozed');
  msg.folderId = msg.snoozeFolder ?? FOLDER_INBOX;
  msg.snoozedUntil = null;
  msg.snoozeFolder = null;
  msg.read = false;
  msg.updatedAt = nowTimestamp();
  await saveState(state);
  return jsonResponse(200, { id: msg.id, folder_id: msg.folderId });
}

async function markJunk(state: DemoState, id: number): Promise<Response> {
  const msg = findMessage(state, id);
  if ([FOLDER_DRAFTS, FOLDER_SCHEDULED, FOLDER_SNOOZED, FOLDER_JUNK].includes(msg.folderId)) {
    throw validationError('cannot mark junk from this folder');
  }
  msg.folderId = FOLDER_JUNK;
  msg.read = true;
  msg.updatedAt = nowTimestamp();
  await saveState(state);
  return jsonResponse(200, { id: msg.id, folder_id: msg.folderId });
}

async function markNotJunk(state: DemoState, id: number): Promise<Response> {
  const msg = findMessage(state, id);
  if (msg.folderId !== FOLDER_JUNK) throw validationError('message is not in junk folder');
  msg.folderId = FOLDER_INBOX;
  msg.read = false;
  msg.updatedAt = nowTimestamp();
  await saveState(state);
  return jsonResponse(200, { id: msg.id, folder_id: msg.folderId });
}

async function serveAttachment(state: DemoState, id: number): Promise<Response> {
  const att = state.attachments.find((a) => a.id === id);
  if (att === undefined) throw notFoundError('attachment not found');
  const data = await getAttachmentData(id);
  if (data === null) throw notFoundError('attachment not found');
  return new Response(data, {
    status: 200,
    headers: {
      // Always octet-stream, never the stored content_type: it comes from the
      // sender, and serving it back would let a message pick the type the
      // browser renders it as.
      'Content-Type': 'application/octet-stream',
      'Content-Disposition': contentDisposition(att.filename),
      'Cache-Control': 'no-store',
    },
  });
}

// ── Send ─────────────────────────────────────────────────────────────────────

/**
 * Sends, or schedules, a composed message.
 *
 * There is no sendmail here, so the send always succeeds: the message lands in
 * Sent with a Message-ID and an RFC 5322 source, its recipients become contacts,
 * and a reply is queued (reply.ts). The one thing the demo cannot show is a
 * delivery failure — send_failure_count only ever stays zero.
 */
async function sendMessage(
  state: DemoState,
  body: Record<string, unknown>,
  files: UploadedAttachment[],
): Promise<Response> {
  const fields = validateSendFields(body);
  if (fields.toAddr === '' && fields.ccAddr === '' && fields.bccAddr === '') {
    throw validationError('at least one of to_addr, cc_addr, bcc_addr must be non-empty');
  }
  const identity = resolveIdentityForSend(state, optNumber(body, 'identity_id'));
  const now = new Date();
  const deferUntil = scheduledFor(body, now.getTime());

  const msg = newComposedMessage(state, identity, fields, now);
  msg.hasExternalImages = hasExternalImages(fields.bodyHtml);
  state.messages.push(msg);
  await storeAttachments(state, msg.id, files);

  if (deferUntil !== null) {
    msg.folderId = FOLDER_SCHEDULED;
    msg.sendAt = toTimestamp(deferUntil);
    await saveState(state);
    return jsonResponse(202, { id: msg.id, send_at: msg.sendAt });
  }

  deliverSentMessage(state, msg, now);
  await saveState(state);
  return jsonResponse(201, { id: msg.id });
}

/** A composed message in its pre-send state; the caller decides its folder. */
function newComposedMessage(
  state: DemoState,
  identity: DemoIdentity,
  fields: SendFields,
  now: Date,
): DemoMessage {
  const timestamp = toTimestamp(now);
  return {
    id: state.nextMessageId++,
    folderId: FOLDER_SENT,
    identityId: identity.id,
    messageId: null,
    inReplyTo: fields.inReplyTo === '' ? null : fields.inReplyTo,
    references: fields.references === '' ? null : fields.references,
    fromAddr: identity.address,
    toAddr: fields.toAddr,
    ccAddr: fields.ccAddr,
    bccAddr: fields.bccAddr,
    replyToAddr: fields.replyToAddr,
    subject: fields.subject,
    date: timestamp,
    bodyText: fields.bodyText,
    bodyHtml: fields.bodyHtml,
    raw: null,
    read: true,
    flagged: false,
    hasAttachments: false,
    hasExternalImages: false,
    sendAt: null,
    snoozedUntil: null,
    snoozeFolder: null,
    sendError: null,
    sendFailureCount: 0,
    createdAt: timestamp,
    updatedAt: timestamp,
  };
}

// ── Drafts ───────────────────────────────────────────────────────────────────

async function createDraft(
  state: DemoState,
  body: Record<string, unknown>,
  files: UploadedAttachment[],
): Promise<Response> {
  const fields = validateSendFields(body);
  const identity = resolveIdentityForDraft(state, optNumber(body, 'identity_id'));

  // source_message_id copies the original's attachments, which is how forwarding
  // keeps them. Checked before anything is inserted: the server does the whole
  // of CreateDraftCopying in one transaction, so a bad source leaves no draft
  // behind, and a half-built one here would survive in the in-memory state.
  const sourceId = optNumber(body, 'source_message_id');
  if (sourceId !== undefined && !state.messages.some((m) => m.id === sourceId)) {
    throw validationError('source_message_id references a message that does not exist');
  }

  const now = nowTimestamp();
  const draft: DemoMessage = {
    id: state.nextMessageId++,
    folderId: FOLDER_DRAFTS,
    identityId: identity.identityId,
    messageId: null,
    inReplyTo: fields.inReplyTo === '' ? null : fields.inReplyTo,
    references: fields.references === '' ? null : fields.references,
    fromAddr: identity.fromAddr,
    toAddr: fields.toAddr,
    ccAddr: fields.ccAddr,
    bccAddr: fields.bccAddr,
    replyToAddr: fields.replyToAddr,
    subject: fields.subject,
    date: now,
    bodyText: fields.bodyText,
    bodyHtml: fields.bodyHtml,
    raw: null, // the `raw IS NULL` invariant that makes this a draft
    read: false,
    flagged: false,
    hasAttachments: false,
    hasExternalImages: false,
    sendAt: draftSendAt(body),
    snoozedUntil: null,
    snoozeFolder: null,
    sendError: null,
    sendFailureCount: 0,
    createdAt: now,
    updatedAt: now,
  };
  state.messages.push(draft);

  if (sourceId !== undefined) {
    // Mirrors DraftRepository.CreateDraftCopying.
    for (const att of attachmentsOf(state, sourceId)) {
      const data = await getAttachmentData(att.id);
      if (data === null) continue;
      const id = state.nextAttachmentId++;
      await putAttachmentData(id, data);
      state.attachments.push({ ...att, id, messageId: draft.id });
      draft.hasAttachments = true;
    }
  }

  await storeAttachments(state, draft.id, files);
  await saveState(state);
  return jsonResponse(201, { id: draft.id, updated_at: draft.updatedAt });
}

/**
 * PUT replaces the draft entirely: any field left out of the body is cleared.
 * Mirrors DraftRepository.UpdateDraft — including that source_message_id is
 * silently ignored here.
 */
async function updateDraft(
  state: DemoState,
  id: number,
  body: Record<string, unknown>,
  files: UploadedAttachment[] | null,
): Promise<Response> {
  const fields = validateSendFields(body);
  const identity = resolveIdentityForDraft(state, optNumber(body, 'identity_id'));
  const draft = findDraft(state, id);

  draft.identityId = identity.identityId;
  draft.fromAddr = identity.fromAddr;
  draft.toAddr = fields.toAddr;
  draft.ccAddr = fields.ccAddr;
  draft.bccAddr = fields.bccAddr;
  draft.replyToAddr = fields.replyToAddr;
  draft.subject = fields.subject;
  draft.bodyText = fields.bodyText;
  draft.bodyHtml = fields.bodyHtml;
  draft.inReplyTo = fields.inReplyTo === '' ? null : fields.inReplyTo;
  draft.references = fields.references === '' ? null : fields.references;
  draft.sendAt = draftSendAt(body);
  draft.date = nowTimestamp();
  draft.updatedAt = draft.date;

  if (files !== null) {
    // The multipart form replaces the attachment set wholesale
    // (AttachmentRepository.ReplaceAttachments), so what is not re-uploaded is gone.
    const existing = attachmentsOf(state, id);
    state.attachments = state.attachments.filter((a) => a.messageId !== id);
    await removeAttachmentData(existing.map((a) => a.id));
    draft.hasAttachments = false;
    await storeAttachments(state, id, files);
  }

  await saveState(state);
  return jsonResponse(200, { id: draft.id, updated_at: draft.updatedAt });
}

async function deleteDraft(state: DemoState, id: number): Promise<Response> {
  findDraft(state, id);
  await deleteMessages(state, new Set([id]));
  await saveState(state);
  return noContentResponse();
}

async function deleteDraftAttachment(state: DemoState, id: number, attachmentId: number): Promise<Response> {
  const draft = findDraft(state, id);
  const att = state.attachments.find((a) => a.id === attachmentId && a.messageId === id);
  if (att === undefined) throw notFoundError('attachment not found');
  state.attachments = state.attachments.filter((a) => a.id !== attachmentId);
  await removeAttachmentData([attachmentId]);

  // Mirrors the attachments_delete_flag trigger.
  draft.hasAttachments = attachmentsOf(state, id).length > 0;
  await saveState(state);
  return noContentResponse();
}

/**
 * Sends a draft. Its own send_at decides whether that is immediate or deferred,
 * and either way the draft row itself becomes the sent/scheduled message —
 * mirroring handler.DraftsIDSendPost, which builds a new row and deletes the
 * draft. Keeping the row means the attachments come along without being copied.
 */
async function sendDraft(state: DemoState, id: number): Promise<Response> {
  const draft = findDraft(state, id);
  if (draft.toAddr === '' && draft.ccAddr === '' && draft.bccAddr === '') {
    throw validationError('at least one of to_addr, cc_addr, bcc_addr must be non-empty');
  }

  // A draft whose identity was deleted falls back to the default.
  let identity = draft.identityId !== null
    ? state.identities.find((i) => i.id === draft.identityId)
    : undefined;
  if (identity === undefined) identity = state.identities.find((i) => i.isDefault);
  if (identity === undefined) {
    throw validationError('no identity configured; create one in Settings → Identities first');
  }
  draft.identityId = identity.id;
  draft.fromAddr = identity.address;
  draft.hasExternalImages = hasExternalImages(draft.bodyHtml);

  const now = new Date();
  if (draft.sendAt !== null && Date.parse(draft.sendAt) > now.getTime() + SCHEDULE_THRESHOLD_MS) {
    draft.folderId = FOLDER_SCHEDULED;
    draft.updatedAt = toTimestamp(now);
    await saveState(state);
    return jsonResponse(202, { id: draft.id, send_at: draft.sendAt });
  }

  // An immediate send does not go through MarkSent on the server:
  // handler.executeSend inserts a *new* row dated now and deletes the draft. So
  // the row has to be re-dated here, or a draft written three days ago would
  // land three days down a Sent list ordered by date — and disagree with the
  // Date: header of the source built for it a moment ago. The deferred branch
  // above and the scheduler both leave `date` alone, which is what their server
  // counterparts (MarkSent, and the scheduler's UPDATE) do.
  draft.sendAt = null;
  draft.date = toTimestamp(now);
  draft.createdAt = toTimestamp(now);
  deliverSentMessage(state, draft, now);
  await saveState(state);
  return jsonResponse(201, { id: draft.id });
}

// ── Scheduled ────────────────────────────────────────────────────────────────

async function rescheduleMessage(state: DemoState, id: number, body: Record<string, unknown>): Promise<Response> {
  const raw = optString(body, 'send_at');
  const at = raw === undefined ? NaN : Date.parse(raw);
  if (Number.isNaN(at) || at <= Date.now() + SCHEDULE_THRESHOLD_MS) {
    throw validationError('send_at must be more than 60 seconds in the future');
  }
  const msg = state.messages.find((m) => m.id === id && m.folderId === FOLDER_SCHEDULED);
  if (msg === undefined) {
    throw notFoundError('scheduled message not found or no longer in Scheduled folder');
  }
  msg.sendAt = toTimestamp(new Date(at));
  msg.updatedAt = nowTimestamp();
  await saveState(state);
  return jsonResponse(200, { id: msg.id, folder_id: msg.folderId, send_at: msg.sendAt });
}

async function sendScheduledNow(state: DemoState, id: number): Promise<Response> {
  const msg = state.messages.find(
    (m) => m.id === id && m.folderId === FOLDER_SCHEDULED && m.sendAt !== null,
  );
  if (msg === undefined) {
    throw notFoundError('scheduled message not found or no longer in Scheduled folder');
  }
  msg.sendAt = null;
  deliverSentMessage(state, msg, new Date());
  await saveState(state);
  return jsonResponse(200, { id: msg.id, folder_id: FOLDER_SENT });
}

/** Mirrors DraftRepository.CancelScheduled: back to Drafts, failure state cleared. */
async function cancelScheduled(state: DemoState, id: number): Promise<Response> {
  const msg = state.messages.find((m) => m.id === id && m.folderId === FOLDER_SCHEDULED);
  if (msg === undefined) throw notFoundError('scheduled message not found or already cancelled');
  msg.folderId = FOLDER_DRAFTS;
  msg.sendAt = null;
  msg.sendFailureCount = 0;
  msg.sendError = null;
  msg.updatedAt = nowTimestamp();
  await saveState(state);
  return jsonResponse(200, { id: msg.id, folder_id: msg.folderId });
}

// ── Identities ───────────────────────────────────────────────────────────────

function listIdentities(state: DemoState): Response {
  const items = sortByPosition(state.identities).map(toIdentityDTO);
  return jsonResponse(200, { total: items.length, items });
}

function sortByPosition<T extends { id: number; position: number }>(rows: T[]): T[] {
  return [...rows].sort((a, b) => a.position - b.position || a.id - b.id);
}

async function createIdentity(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const identity = identityFromRequest(state, body, state.nextIdentityId++);
  if (state.identities.some((i) => i.address === identity.address)) {
    throw conflictError('address already in use by another identity');
  }
  // The first identity is always the default, whatever the request said.
  if (state.identities.length === 0) identity.isDefault = true;
  if (identity.isDefault) for (const other of state.identities) other.isDefault = false;

  state.identities.push(identity);
  await saveState(state);
  return jsonResponse(201, toIdentityDTO(identity));
}

function identityFromRequest(state: DemoState, body: Record<string, unknown>, id: number): DemoIdentity {
  const signature = optString(body, 'signature') ?? '';
  if (runeLength(signature) > MAX_SIGNATURE_LEN) {
    throw validationError('signature must not exceed 51200 characters');
  }
  let address: string;
  try {
    address = parseAndFoldAddress(optString(body, 'address') ?? '');
  } catch {
    throw validationError('invalid address: must be a bare addr-spec (no display name)');
  }
  const position = optNumber(body, 'position') ??
    state.identities.reduce((max, i) => Math.max(max, i.position + 1), 0);
  return {
    id,
    name: (optString(body, 'name') ?? '').trim(),
    address,
    isDefault: optBool(body, 'is_default') ?? false,
    position,
    signature,
  };
}

/**
 * PUT replaces an identity. is_default=true promotes it; is_default=false leaves
 * the current default alone rather than demoting it, as
 * IdentityRepository.UpdateIdentity does.
 */
async function updateIdentity(state: DemoState, id: number, body: Record<string, unknown>): Promise<Response> {
  const identity = state.identities.find((i) => i.id === id);
  if (identity === undefined) throw notFoundError('identity not found');

  const replacement = identityFromRequest(state, body, id);
  if (state.identities.some((i) => i.address === replacement.address && i.id !== id)) {
    throw conflictError('address already in use by another identity');
  }

  const previousAddress = identity.address;
  if (replacement.isDefault) {
    for (const other of state.identities) other.isDefault = other.id === id;
  }
  identity.name = replacement.name;
  identity.address = replacement.address;
  identity.position = replacement.position;
  identity.signature = replacement.signature;

  // A renamed identity re-stamps the drafts that were composed under it.
  if (identity.address !== previousAddress) {
    for (const msg of state.messages) {
      if (msg.identityId === id && msg.folderId === FOLDER_DRAFTS) msg.fromAddr = identity.address;
    }
  }

  await saveState(state);
  return jsonResponse(200, toIdentityDTO(identity));
}

async function deleteIdentity(state: DemoState, id: number): Promise<Response> {
  const identity = state.identities.find((i) => i.id === id);
  if (identity === undefined) throw notFoundError('identity not found');
  if (state.identities.length <= 1) throw validationError('cannot delete the last identity');

  const wasDefault = identity.isDefault;
  const address = identity.address;
  state.identities = state.identities.filter((i) => i.id !== id);

  // The FK is ON DELETE SET NULL; a draft that pointed here loses its from_addr.
  for (const msg of state.messages) {
    if (msg.identityId !== id) continue;
    msg.identityId = null;
    if (msg.folderId === FOLDER_DRAFTS && msg.fromAddr === address) msg.fromAddr = '';
  }
  if (wasDefault) {
    const promoted = sortByPosition(state.identities)[0];
    if (promoted !== undefined) promoted.isDefault = true;
  }

  await saveState(state);
  return noContentResponse();
}

async function reorderIdentities(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const ids = Array.isArray(body.ids) ? (body.ids as unknown[]).filter((v): v is number => typeof v === 'number') : [];
  validateReorder(ids, state.identities.map((i) => i.id));
  ids.forEach((id, position) => {
    const identity = state.identities.find((i) => i.id === id);
    if (identity !== undefined) identity.position = position;
  });
  await saveState(state);
  return jsonResponse(200, { updated: ids.length });
}

// ── Filters ──────────────────────────────────────────────────────────────────

function listFilters(state: DemoState): Response {
  const items = sortByPosition(state.filters).map(toFilterDTO);
  return jsonResponse(200, { total: items.length, items });
}

const FILTER_ACTIONS: FilterAction[] = ['move', 'trash', 'mark_read', 'drop'];

/** Mirrors repository.validateFilter and handler.filterFromRequest. */
function filterFromRequest(state: DemoState, body: Record<string, unknown>, id: number): DemoFilter {
  const action = optString(body, 'action') as FilterAction | undefined;
  if (action === undefined || !FILTER_ACTIONS.includes(action)) {
    throw validationError('action must be one of: move, trash, mark_read, drop');
  }
  const matchFrom = optString(body, 'match_from') ?? '';
  const matchTo = optString(body, 'match_to') ?? '';
  const matchSubject = optString(body, 'match_subject') ?? '';
  if (matchFrom.trim() === '' && matchTo.trim() === '' && matchSubject.trim() === '') {
    throw validationError('at least one of match_from, match_to, match_subject must be non-empty');
  }
  const rawFolderId = optNumber(body, 'folder_id');
  const folderId = rawFolderId === undefined ? null : rawFolderId;
  if (action === 'move') {
    // Sent, Drafts, Scheduled and Snoozed are rejected: routing inbound mail
    // there conflicts with what those folders mean.
    const allowed = folderId !== null &&
      (folderId === FOLDER_INBOX || folderId === FOLDER_TRASH || folderId === FOLDER_JUNK ||
        folderId >= FIRST_USER_FOLDER);
    if (!allowed) {
      throw validationError(
        'folder_id for move action must be 1 (Inbox), 4 (Trash), 7 (Junk), or a user folder (id >= 100)');
    }
  }
  return {
    id,
    position: optNumber(body, 'position') ??
      state.filters.reduce((max, f) => Math.max(max, f.position + 1), 0),
    name: (optString(body, 'name') ?? '').trim(),
    matchFrom,
    matchTo,
    matchSubject,
    action,
    folderId,
    stop: optBool(body, 'stop') ?? true,
  };
}

async function createFilter(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const filter = filterFromRequest(state, body, state.nextFilterId++);
  state.filters.push(filter);
  await saveState(state);
  return jsonResponse(201, toFilterDTO(filter));
}

async function updateFilter(state: DemoState, id: number, body: Record<string, unknown>): Promise<Response> {
  const index = state.filters.findIndex((f) => f.id === id);
  if (index < 0) throw notFoundError('filter not found');
  const replacement = filterFromRequest(state, body, id);
  // PUT replaces, so an omitted position becomes 0 rather than "appended".
  replacement.position = optNumber(body, 'position') ?? 0;
  state.filters[index] = replacement;
  await saveState(state);
  return jsonResponse(200, toFilterDTO(replacement));
}

async function deleteFilter(state: DemoState, id: number): Promise<Response> {
  if (!state.filters.some((f) => f.id === id)) throw notFoundError('filter not found');
  state.filters = state.filters.filter((f) => f.id !== id);
  await saveState(state);
  return noContentResponse();
}

async function reorderFilters(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const ids = Array.isArray(body.ids) ? (body.ids as unknown[]).filter((v): v is number => typeof v === 'number') : [];
  validateReorder(ids, state.filters.map((f) => f.id));
  ids.forEach((id, position) => {
    const filter = state.filters.find((f) => f.id === id);
    if (filter !== undefined) filter.position = position;
  });
  await saveState(state);
  return jsonResponse(200, { updated: ids.length });
}

// ── Spam filter ──────────────────────────────────────────────────────────────

async function updateSpamFilter(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const scoreHeader = optString(body, 'score_header') ?? '';
  if (scoreHeader.length === 0 || scoreHeader.length > MAX_SCORE_HEADER_LEN) {
    throw validationError('score_header must be between 1 and 200 characters');
  }
  const scoreThreshold = optNumber(body, 'score_threshold') ?? 0;
  if (scoreThreshold < 0) throw validationError('score_threshold must be >= 0');

  state.spamFilter = { enabled: optBool(body, 'enabled') ?? false, scoreHeader, scoreThreshold };
  await saveState(state);
  return jsonResponse(200, toSpamFilterDTO(state.spamFilter));
}

// ── Contacts ─────────────────────────────────────────────────────────────────

function listContacts(state: DemoState, url: URL): Response {
  const { limit, offset } = pagination(url);
  const rawQuery = url.searchParams.get('q');
  const q = rawQuery === null ? null : rawQuery.trim().toLowerCase();

  const matching = state.contacts
    .filter((c) => q === null || c.name.toLowerCase().includes(q) || c.address.toLowerCase().includes(q))
    // Named contacts first, then by name, then by address — the ORDER BY in
    // ContactRepository.ListContacts.
    .sort((a, b) =>
      (a.name === '' ? 1 : 0) - (b.name === '' ? 1 : 0) ||
      a.name.toLowerCase().localeCompare(b.name.toLowerCase()) ||
      a.address.toLowerCase().localeCompare(b.address.toLowerCase()));

  return jsonResponse(200, {
    total: matching.length,
    items: matching.slice(offset, offset + limit).map(toContactDTO),
  });
}

function contactFromRequest(body: Record<string, unknown>): { address: string; name: string } {
  const name = (optString(body, 'name') ?? '').trim();
  if (runeLength(name) > MAX_NAME_LEN) {
    throw validationError(`name must not exceed ${MAX_NAME_LEN} characters`);
  }
  const rawAddress = optString(body, 'address') ?? '';
  if (rawAddress.length > MAX_ADDRESS_LEN) {
    throw validationError(`address must not exceed ${MAX_ADDRESS_LEN} characters`);
  }
  let address: string;
  try {
    address = parseAndFoldAddress(rawAddress);
  } catch {
    throw validationError('invalid address: must be a bare addr-spec (no display name)');
  }
  return { address, name };
}

async function createContact(state: DemoState, body: Record<string, unknown>): Promise<Response> {
  const { address, name } = contactFromRequest(body);
  if (state.contacts.some((c) => c.address === address)) throw conflictError('address already exists');
  const now = nowTimestamp();
  const contact: DemoContact = {
    id: state.nextContactId++,
    address,
    name,
    createdAt: now,
    updatedAt: now,
  };
  state.contacts.push(contact);
  await saveState(state);
  return jsonResponse(201, toContactDTO(contact));
}

async function updateContact(state: DemoState, id: number, body: Record<string, unknown>): Promise<Response> {
  const { address, name } = contactFromRequest(body);
  const contact = state.contacts.find((c) => c.id === id);
  if (contact === undefined) throw notFoundError('contact not found');
  if (state.contacts.some((c) => c.address === address && c.id !== id)) {
    throw conflictError('address already exists');
  }
  contact.address = address;
  contact.name = name;
  contact.updatedAt = nowTimestamp();
  await saveState(state);
  return jsonResponse(200, toContactDTO(contact));
}

async function deleteContact(state: DemoState, id: number): Promise<Response> {
  if (!state.contacts.some((c) => c.id === id)) throw notFoundError('contact not found');
  state.contacts = state.contacts.filter((c) => c.id !== id);
  await saveState(state);
  return noContentResponse();
}

// ── Routing ──────────────────────────────────────────────────────────────────

/**
 * Answers one /api/v1 request. `path` is what demo-sw.ts stripped the prefix
 * off, with a leading slash: "/messages/42/thread".
 *
 * The whole request runs under the store mutex, and the scheduler runs first —
 * so a request never observes a scheduled send or an expired snooze it should
 * already have seen.
 */
async function handleApiRequest(path: string, request: Request): Promise<Response> {
  try {
    const url = new URL(request.url);
    const method = request.method.toUpperCase();
    const segments = path.split('/').filter((s) => s !== '').map(decodeURIComponent);

    return await withStore(async (state) => {
      if (runScheduler(state, Date.now())) await saveState(state);
      return route(state, segments, method, url, request);
    });
  } catch (err) {
    // A handler that threw after mutating the state left changes in the cache
    // that it never persisted; drop the cache so they are rolled back rather
    // than riding along on the next successful write.
    //
    // Only for a method that could have mutated something. Every GET handler is
    // read-only — the scheduler is the one thing that writes during a GET, and
    // it runs and persists above, outside the handler — so re-reading the whole
    // dataset from IndexedDB on, say, a 404 for a message the UI just deleted
    // would be pure cost.
    if (request.method.toUpperCase() !== 'GET') invalidateState();
    return errorResponse(err);
  }
}

async function route(
  state: DemoState,
  segments: string[],
  method: string,
  url: URL,
  request: Request,
): Promise<Response> {
  const [head, second, third, fourth] = segments;

  switch (head) {
    case 'folders':
      if (segments.length === 1) {
        if (method === 'GET') return listFolders(state);
        if (method === 'POST') return createFolder(state, await readJSON(request));
      } else if (segments.length === 2) {
        if (second === 'reorder' && method === 'PATCH') {
          return reorderFolders(state, await readJSON(request));
        }
        if (method === 'PATCH') return patchFolder(state, pathId(second), await readJSON(request));
        if (method === 'DELETE') return deleteFolder(state, pathId(second));
      } else if (segments.length === 3) {
        if (third === 'messages' && method === 'GET') return listFolderMessages(state, pathId(second), url);
        if (third === 'messages' && method === 'DELETE') return deleteFolderMessages(state, pathId(second));
        if (third === 'mark-all-read' && method === 'POST') return markFolderRead(state, pathId(second));
      }
      break;

    case 'messages':
      if (segments.length === 1) {
        if (method === 'PATCH') return bulkPatchMessages(state, await readJSON(request));
        if (method === 'DELETE') return bulkDeleteMessages(state, await readJSON(request));
      } else if (segments.length === 2) {
        if (second === 'search' && method === 'GET') return searchMessages(state, url);
        if (second === 'move' && method === 'POST') return moveMessages(state, await readJSON(request));
        if (second === 'send' && method === 'POST') {
          return sendMessage(state, await readJSON(request), []);
        }
        if (second === 'send-with-attachments' && method === 'POST') {
          const { body, files } = await readMultipart(request);
          return sendMessage(state, body, files);
        }
        if (method === 'GET') return getMessage(state, pathId(second));
        if (method === 'PATCH') return patchMessage(state, pathId(second), await readJSON(request));
        if (method === 'DELETE') return deleteMessage(state, pathId(second));
      } else if (segments.length === 3) {
        const id = pathId(second);
        if (third === 'raw' && method === 'GET') return getRawMessage(state, id);
        if (third === 'headers' && method === 'GET') return getMessageHeaders(state, id);
        if (third === 'body' && method === 'GET') return getMessageBody(state, id, url);
        if (third === 'thread' && method === 'GET') return getThread(state, id);
        if (third === 'snooze' && method === 'POST') return snoozeMessage(state, id, await readJSON(request));
        if (third === 'snooze' && method === 'DELETE') return cancelSnooze(state, id);
        if (third === 'mark-junk' && method === 'POST') return markJunk(state, id);
        if (third === 'mark-not-junk' && method === 'POST') return markNotJunk(state, id);
      }
      break;

    case 'attachments':
      if (segments.length === 2 && method === 'GET') return serveAttachment(state, pathId(second));
      break;

    case 'drafts':
      if (segments.length === 1 && method === 'POST') {
        return createDraft(state, await readJSON(request), []);
      }
      if (segments.length === 2) {
        if (method === 'PUT') return updateDraft(state, pathId(second), await readJSON(request), null);
        if (method === 'DELETE') return deleteDraft(state, pathId(second));
      }
      if (segments.length === 3 && third === 'send' && method === 'POST') {
        return sendDraft(state, pathId(second));
      }
      if (segments.length === 4 && third === 'attachments' && method === 'DELETE') {
        return deleteDraftAttachment(state, pathId(second), pathId(fourth));
      }
      break;

    case 'drafts-with-attachments': {
      if (segments.length === 1 && method === 'POST') {
        const { body, files } = await readMultipart(request);
        return createDraft(state, body, files);
      }
      if (segments.length === 2 && method === 'PUT') {
        const { body, files } = await readMultipart(request);
        return updateDraft(state, pathId(second), body, files);
      }
      break;
    }

    case 'scheduled':
      if (segments.length === 2) {
        if (method === 'PATCH') return rescheduleMessage(state, pathId(second), await readJSON(request));
        if (method === 'DELETE') return cancelScheduled(state, pathId(second));
      }
      if (segments.length === 3 && third === 'send' && method === 'POST') {
        return sendScheduledNow(state, pathId(second));
      }
      break;

    case 'identities':
      if (segments.length === 1) {
        if (method === 'GET') return listIdentities(state);
        if (method === 'POST') return createIdentity(state, await readJSON(request));
      } else if (segments.length === 2) {
        if (second === 'reorder' && method === 'PATCH') {
          return reorderIdentities(state, await readJSON(request));
        }
        if (method === 'PUT') return updateIdentity(state, pathId(second), await readJSON(request));
        if (method === 'DELETE') return deleteIdentity(state, pathId(second));
      }
      break;

    case 'filters':
      if (segments.length === 1) {
        if (method === 'GET') return listFilters(state);
        if (method === 'POST') return createFilter(state, await readJSON(request));
      } else if (segments.length === 2) {
        if (second === 'reorder' && method === 'PATCH') {
          return reorderFilters(state, await readJSON(request));
        }
        if (method === 'PUT') return updateFilter(state, pathId(second), await readJSON(request));
        if (method === 'DELETE') return deleteFilter(state, pathId(second));
      }
      break;

    case 'spam-filter':
      if (segments.length === 1) {
        if (method === 'GET') return jsonResponse(200, toSpamFilterDTO(state.spamFilter));
        if (method === 'PUT') return updateSpamFilter(state, await readJSON(request));
      }
      break;

    case 'contacts':
      if (segments.length === 1) {
        if (method === 'GET') return listContacts(state, url);
        if (method === 'POST') return createContact(state, await readJSON(request));
      } else if (segments.length === 2) {
        if (method === 'PUT') return updateContact(state, pathId(second), await readJSON(request));
        if (method === 'DELETE') return deleteContact(state, pathId(second));
      }
      break;
  }

  return jsonResponse(404, { error: 'not found' });
}
