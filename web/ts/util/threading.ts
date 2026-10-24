/** Keep received mail and old drafts replyable within the compose API limits. */
export function composeThreading(inReplyTo: string, references: string[]): {
  in_reply_to: string | undefined; references: string[];
} {
  return {
    // Drop oversized identifiers rather than truncating them into different IDs.
    in_reply_to: Array.from(inReplyTo).length <= 998 ? inReplyTo || undefined : undefined,
    references: references.filter(ref => Array.from(ref).length <= 998).slice(-1000),
  };
}
