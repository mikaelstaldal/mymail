// The one thing the demo backend invents rather than mirrors: a reply.
//
// A real MyMail hands a sent message to sendmail and, some time later, an
// answer arrives through the LDA. The demo has neither, so a mailbox where
// everything you send disappears for good would show only half the app. Instead
// each sent message is answered once, by its first recipient, after
// AUTO_REPLY_DELAY_MS (see model.ts).
//
// Two properties are deliberate. The reply is *derived*, not random: which of
// the templates below is used is a pure function of the outgoing subject and
// body, so the same message always gets the same answer and a test can assert
// on it. And the delay is a named constant checked against the clock at request
// time, not a setTimeout — a service worker is stopped whenever it goes idle,
// so a timer spanning twenty seconds would usually never fire.
//
// See model.ts for why these are globals rather than module exports.

/**
 * The reply bodies, one of which is chosen per message. They are written to
 * make sense as an answer to anything: no template refers to the content of
 * what it is replying to, because nothing here understands it.
 */
const REPLY_TEMPLATES = [
  'Thanks for the note — this all looks right to me. I\'ll pick it up from here and come back to you if anything is unclear.',
  'Got it, thanks. Give me a day to go through this properly and I\'ll send you my thoughts.',
  'Appreciate you writing this up. One question before I reply in full: is there a deadline I should be working to?',
  'This is helpful, thank you. I\'ve forwarded it to the rest of the team and will let you know what they say.',
  'Perfect, that answers my question. Nothing further needed from my side for now.',
  'Thanks for getting back to me so quickly. I\'ve made a note of it and will follow up next week.',
];

/** The closing line, chosen alongside the template. */
const REPLY_SIGN_OFFS = ['Best', 'Thanks', 'Kind regards', 'Cheers'];

/**
 * FNV-1a over the message text. Any stable hash would do; the point is only
 * that the choice of template is reproducible from the message itself.
 */
function replyHash(text: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash;
}

/** The fields of a generated reply, before the store assigns it an id. */
interface AutoReply {
  fromAddr: string;
  toAddr: string;
  subject: string;
  bodyText: string;
  inReplyTo: string | null;
  /** Newline-separated, angle brackets stripped, as the column stores it. */
  references: string | null;
}

/**
 * The reply to a sent message, or null when there is nobody to reply from —
 * a message addressed only via Bcc, say, or one whose To list will not parse.
 *
 * `date` is the reply's timestamp, and is also what the quote attribution line
 * dates the original to, so a caller passing a fixed clock gets a fixed reply.
 */
function buildAutoReply(sent: DemoMessage, date: Date): AutoReply | null {
  const recipient = firstAddress(sent.toAddr);
  if (recipient === null) return null;

  const sender = firstAddress(sent.fromAddr);
  const senderAddress = sender !== null ? sender.address : sent.fromAddr;
  if (senderAddress === '') return null;

  const pick = replyHash(sent.subject + '\n' + sent.bodyText);
  const template = REPLY_TEMPLATES[pick % REPLY_TEMPLATES.length];
  const signOff = REPLY_SIGN_OFFS[(pick >>> 8) % REPLY_SIGN_OFFS.length];

  const greeting = firstName(sender) === '' ? 'Hi,' : `Hi ${firstName(sender)},`;
  const salutation = firstName(recipient);

  const bodyText =
    `${greeting}\n\n${template}\n\n${signOff},\n${salutation === '' ? recipient.address : salutation}\n\n` +
    quoteOriginal(sent, date);

  // The reply extends the thread the message it answers is in: its References
  // are that message's, plus that message's own id (RFC 5322 §3.6.4).
  const refs = splitNL(sent.references);
  if (sent.messageId !== null) refs.push(sent.messageId);

  return {
    fromAddr: recipient.name === '' ? recipient.address : `${recipient.name} <${recipient.address}>`,
    toAddr: sender !== null && sender.name !== '' ? `${sender.name} <${sender.address}>` : senderAddress,
    subject: replySubject(sent.subject),
    bodyText,
    inReplyTo: sent.messageId,
    references: refs.length > 0 ? refs.join('\n') : null,
  };
}

/** "Re: " once, never "Re: Re: ". Matches what the compose form produces. */
function replySubject(subject: string): string {
  const normalized = normalizeSubject(subject);
  if (normalized === '') return 'Re:';
  return `Re: ${normalized}`;
}

function firstName(addr: ParsedAddress | null): string {
  if (addr === null || addr.name === '') return '';
  return addr.name.split(/\s+/)[0];
}

/** The usual attribution line plus a `> ` quote of the outgoing body. */
function quoteOriginal(sent: DemoMessage, date: Date): string {
  const when = date.toISOString().slice(0, 10);
  const who = sent.fromAddr === '' ? 'you' : sent.fromAddr;
  const quoted = sent.bodyText
    .split('\n')
    .map((line) => (line === '' ? '>' : `> ${line}`))
    .join('\n');
  return `On ${when}, ${who} wrote:\n${quoted}\n`;
}

// ── Filter evaluation ────────────────────────────────────────────────────────

/**
 * Whether a filter's criteria all match. Mirrors lda.filterMatches, including
 * that match_to is checked against Cc as well as To, and that empty criteria
 * are skipped rather than treated as "matches nothing".
 */
function filterMatches(
  msg: { fromAddr: string; toAddr: string; ccAddr: string; subject: string },
  filter: DemoFilter,
): boolean {
  if (filter.matchFrom !== '' && !containsFold(msg.fromAddr, filter.matchFrom)) return false;
  if (filter.matchTo !== '' &&
      !containsFold(msg.toAddr, filter.matchTo) &&
      !containsFold(msg.ccAddr, filter.matchTo)) {
    return false;
  }
  if (filter.matchSubject !== '' && !containsFold(msg.subject, filter.matchSubject)) return false;
  return true;
}

function containsFold(haystack: string, needle: string): boolean {
  return haystack.toLowerCase().includes(needle.toLowerCase());
}

/** What the filter chain decided for an incoming message. */
interface FilterOutcome {
  /** Where it lands, or null when a `drop` filter matched and it is discarded. */
  folderId: number | null;
  markRead: boolean;
}

/**
 * Runs the filter chain over an incoming message, mirroring the loop in
 * lda.Run: filters apply in order, a matching `stop` filter ends evaluation,
 * `move` is ignored when its target folder no longer exists, and `drop` wins
 * immediately.
 *
 * Spam detection is not part of this. It reads headers the demo's generated
 * replies do not have, so `spam_filter_settings` is stored and editable but
 * never consulted — one of the divergences listed in spec/REQUIREMENTS.md.
 */
function applyFilters(
  msg: { fromAddr: string; toAddr: string; ccAddr: string; subject: string },
  filters: DemoFilter[],
  folderIds: Set<number>,
): FilterOutcome {
  let folderId = FOLDER_INBOX;
  let markRead = false;
  for (const filter of filters) {
    if (!filterMatches(msg, filter)) continue;
    switch (filter.action) {
      case 'drop':
        return { folderId: null, markRead: false };
      case 'move':
        if (filter.folderId !== null && folderIds.has(filter.folderId)) folderId = filter.folderId;
        break;
      case 'trash':
        folderId = FOLDER_TRASH;
        break;
      case 'mark_read':
        markRead = true;
        break;
    }
    if (filter.stop) break;
  }
  return { folderId, markRead };
}
