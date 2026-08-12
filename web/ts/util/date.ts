function fullTitle(date: Date): string {
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZoneName: 'short',
    hour12: false,
  });
}

function timeOfDay(date: Date): string {
  return date.toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
}

/** Whole calendar days from `date`'s day to `now`'s day — positive in the past. */
function calendarDaysAgo(date: Date, now: Date): number {
  const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const dayStart = new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
  return Math.round((todayStart - dayStart) / 86_400_000);
}

export function formatDateAdaptive(dateStr: string): { display: string; title: string } {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMins = Math.floor((now.getTime() - date.getTime()) / 60_000);

  const title = fullTitle(date);
  const hhmm = timeOfDay(date);
  const calDiff = calendarDaysAgo(date, now);

  let display: string;
  if (diffMins < 60) {
    display = diffMins <= 1 ? '1 minute ago' : `${diffMins} minutes ago`;
  } else if (calDiff === 0) {
    display = hhmm;
  } else if (calDiff === 1) {
    display = `Yesterday ${hhmm}`;
  } else if (calDiff <= 6) {
    display = `${date.toLocaleDateString(undefined, { weekday: 'short' })} ${hhmm}`;
  } else if (date.getFullYear() === now.getFullYear()) {
    display = `${date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}, ${hhmm}`;
  } else {
    display = date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
  }

  return { display, title };
}

/**
 * Formats a `send_at` or `snoozed_until` value for the Scheduled / Snoozed list
 * columns. These are normally in the *future*, which is exactly what
 * formatDateAdaptive cannot express: its `diffMins` goes negative for a future
 * time and every one of them renders as "1 minute ago".
 *
 * A past value is still formatted rather than treated as impossible — the
 * scheduler polls once a minute, so a due message sits in the folder with a
 * `send_at` behind it, and a scheduled send that keeps failing is retried with
 * the original time left in place.
 *
 * The time of day is kept at every distance (unlike formatDateAdaptive, which
 * drops it beyond a year): when a message will be sent is the point of the
 * column, not incidental to it.
 */
export function formatDateSchedule(dateStr: string): { display: string; title: string } {
  const date = new Date(dateStr);
  const now = new Date();
  // Positive is the future here — the opposite sign to formatDateAdaptive,
  // which measures how long ago a message arrived. The sign decides the wording
  // and the magnitude the number, separately: rounding a half-minute across
  // zero would otherwise word a time still ahead as one already past.
  const diffMs = date.getTime() - now.getTime();
  // Floored, so the last seconds before the hour do not read as "60 minutes",
  // and floored to at least 1 so the minute either side of now is not "0".
  const diffMins = Math.max(1, Math.floor(Math.abs(diffMs) / 60_000));

  const title = fullTitle(date);
  const hhmm = timeOfDay(date);
  const daysAgo = calendarDaysAgo(date, now);

  let display: string;
  if (Math.abs(diffMs) < 3_600_000) {
    const minutes = diffMins === 1 ? '1 minute' : `${diffMins} minutes`;
    display = diffMs >= 0 ? `in ${minutes}` : `${minutes} ago`;
  } else if (daysAgo === 0) {
    display = `Today ${hhmm}`;
  } else if (daysAgo === -1) {
    display = `Tomorrow ${hhmm}`;
  } else if (daysAgo === 1) {
    display = `Yesterday ${hhmm}`;
  } else if (Math.abs(daysAgo) <= 6) {
    // Symmetric on purpose: a `send_at` days behind is what a scheduled send
    // that keeps failing looks like, and it deserves the same named day a
    // pending one gets. Beyond a week the weekday stops identifying a day.
    display = `${date.toLocaleDateString(undefined, { weekday: 'short' })} ${hhmm}`;
  } else if (date.getFullYear() === now.getFullYear()) {
    display = `${date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}, ${hhmm}`;
  } else {
    display = `${date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}, ${hhmm}`;
  }

  return { display, title };
}

export function formatDateFull(dateStr: string): string {
  return new Date(dateStr).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZoneName: 'short',
    hour12: false,
  });
}
