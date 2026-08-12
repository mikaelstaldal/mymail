// node:test coverage for web/ts/util/date.ts (exercised via its compiled
// output, web/static/util/date.js). Run via build.sh or directly:
//   node --test web/ts/date.test.mjs
//
// formatDateSchedule renders the Scheduled folder's `send_at` and the Snoozed
// folder's `snoozed_until` in the message list. It exists because
// formatDateAdaptive cannot: that function measures how long ago a message
// arrived, so every future time falls into its "less than an hour" branch with
// a negative difference and renders as "1 minute ago". What these tests pin is
// the sign — a future time reads as future — and the boundaries between the
// relative, named-day and dated forms, in both directions, since a scheduled
// or snoozed time can be in the past while the once-a-minute scheduler catches
// up with it.
//
// No DOM is involved. Assertions avoid the parts that vary with the host
// locale (weekday and month names, and the time separator): what is asserted is
// the prefix that names the day and the presence of the time of day, never the
// spelling of either.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const { formatDateSchedule, formatDateAdaptive } = await import(
  path.resolve(__dirname, '../static/util/date.js')
);

const MINUTE = 60_000;
const DAY = 86_400_000;

/**
 * An ISO string `mins` minutes from now — the inputs are always relative.
 *
 * Two seconds of slack away from now, since both formatters floor the
 * magnitude: without it the milliseconds spent getting into the function turn
 * "in 30 minutes" into "in 29 minutes", intermittently.
 */
function inMinutes(mins) {
  return new Date(Date.now() + mins * MINUTE + (mins >= 0 ? 2000 : -2000)).toISOString();
}

/**
 * Noon on the day `days` from today, so a case about *calendar* days is not
 * decided by the hour the test happens to run at.
 */
function noonInDays(days) {
  const d = new Date(Date.now() + days * DAY);
  d.setHours(12, 0, 0, 0);
  return d.toISOString();
}

// The time of day the display carries, in whatever form this host writes it.
function hhmm(iso) {
  return new Date(iso).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
}

// ---------------------------------------------------------------------------
// Within the hour: the direction is what formatDateAdaptive got wrong
// ---------------------------------------------------------------------------

test('a time within the next hour reads as future, not as past', () => {
  assert.equal(formatDateSchedule(inMinutes(30)).display, 'in 30 minutes');
  assert.equal(formatDateSchedule(inMinutes(59)).display, 'in 59 minutes');
});

test('the nearest future minute is singular', () => {
  assert.equal(formatDateSchedule(inMinutes(1)).display, 'in 1 minute');
});

test('a time just past reads as the past', () => {
  // The scheduler polls every 60 seconds, so a due message sits in its folder
  // with the time already behind it — that is a normal state, not an error.
  assert.equal(formatDateSchedule(inMinutes(-1)).display, '1 minute ago');
  assert.equal(formatDateSchedule(inMinutes(-30)).display, '30 minutes ago');
  assert.equal(formatDateSchedule(inMinutes(-59)).display, '59 minutes ago');
});

test('the minute either side of now is worded by its own side', () => {
  // Half a minute out is under a minute in either direction; what must not
  // happen is a rounded magnitude carrying a time still ahead into "ago".
  assert.equal(formatDateSchedule(inMinutes(0.5)).display, 'in 1 minute');
  assert.equal(formatDateSchedule(inMinutes(-0.5)).display, '1 minute ago');
});

test('the last seconds before the hour stay under sixty minutes', () => {
  // Flooring, not rounding: "in 60 minutes" would name an hour the next
  // branch is there to write as a day and a time.
  assert.equal(formatDateSchedule(inMinutes(59.9)).display, 'in 59 minutes');
  assert.equal(formatDateSchedule(inMinutes(-59.9)).display, '59 minutes ago');
});

// ---------------------------------------------------------------------------
// Beyond the hour: named days, then dates
// ---------------------------------------------------------------------------

test('an hour or more ahead switches from minutes to a day and a time', () => {
  const iso = inMinutes(90);
  const { display } = formatDateSchedule(iso);
  assert.ok(!display.includes('minute'), display);
  assert.ok(display.includes(hhmm(iso)), display);
});

test('the next and the previous day are named', () => {
  const tomorrow = noonInDays(1);
  assert.equal(formatDateSchedule(tomorrow).display, `Tomorrow ${hhmm(tomorrow)}`);
  const yesterday = noonInDays(-1);
  assert.equal(formatDateSchedule(yesterday).display, `Yesterday ${hhmm(yesterday)}`);
});

test('a later hour today is Today, not a bare time', () => {
  // Unlike the Date column, this one is read against a folder full of other
  // future times, so the day is never left implicit.
  const today = new Date();
  today.setHours(23, 59, 0, 0);
  const iso = today.toISOString();
  const { display } = formatDateSchedule(iso);
  // Only assert the Today form when 23:59 is genuinely more than an hour off;
  // a run started at 23:30 correctly gets the relative form instead, and a run
  // that crosses midnight mid-test gets the past one.
  if (today.getTime() - Date.now() >= 60 * MINUTE) {
    assert.equal(display, `Today ${hhmm(iso)}`);
  } else {
    assert.ok(display.includes('minute'), display);
  }
});

test('the rest of the week is a weekday and a time, in both directions', () => {
  // Backwards as well as forwards: a scheduled send that keeps failing is
  // retried with its original send_at, so a value days behind is a state the
  // Scheduled folder really shows.
  for (const days of [3, 6, -3, -6]) {
    const iso = noonInDays(days);
    const { display } = formatDateSchedule(iso);
    const weekday = new Date(iso).toLocaleDateString(undefined, { weekday: 'short' });
    assert.equal(display, `${weekday} ${hhmm(iso)}`, `${days} days`);
  }
});

test('beyond a week the day is given as a date, keeping the time', () => {
  for (const days of [30, -30]) {
    const iso = noonInDays(days);
    const { display } = formatDateSchedule(iso);
    assert.ok(display.endsWith(`, ${hhmm(iso)}`), display);
    const weekday = new Date(iso).toLocaleDateString(undefined, { weekday: 'short' });
    assert.ok(!display.startsWith(weekday), display);
  }
});

/**
 * Noon roughly half a year away, in the current year whatever today is — six
 * months on the other side of the calendar, so this never lands inside the
 * named-day week and never crosses into a neighbouring year.
 */
function noonHalfAYearAway() {
  const d = new Date();
  d.setMonth((d.getMonth() + 6) % 12, 15);
  d.setHours(12, 0, 0, 0);
  return d.toISOString();
}

test('a year is only shown when it differs from the current one', () => {
  const nextYear = new Date();
  nextYear.setFullYear(nextYear.getFullYear() + 1, 5, 15);
  nextYear.setHours(12, 0, 0, 0);
  const iso = nextYear.toISOString();
  const { display } = formatDateSchedule(iso);
  assert.ok(display.includes(String(nextYear.getFullYear())), display);
  // The time survives the longest distance — unlike formatDateAdaptive, which
  // drops it past a year, since when a message sends is the point here.
  assert.ok(display.endsWith(`, ${hhmm(iso)}`), display);

  const thisYear = formatDateSchedule(noonHalfAYearAway()).display;
  assert.ok(!thisYear.includes(String(new Date().getFullYear())), thisYear);
});

// ---------------------------------------------------------------------------
// The tooltip
// ---------------------------------------------------------------------------

test('the title carries the full date whatever the display shows', () => {
  const iso = inMinutes(5);
  const { title } = formatDateSchedule(iso);
  assert.ok(title.includes(String(new Date(iso).getFullYear())), title);
  assert.ok(title.includes(hhmm(iso)), title);
});

// ---------------------------------------------------------------------------
// formatDateAdaptive — the Date column, which the two share their parts with
// ---------------------------------------------------------------------------
//
// The two functions were given a common fullTitle / timeOfDay /
// calendarDaysAgo when the second was written, so the older one is the half of
// date.ts the change actually put at risk. These cases are its own ladder, not
// a re-test of the shared helpers.

test('a message from within the hour is given as elapsed minutes', () => {
  assert.equal(formatDateAdaptive(inMinutes(-30)).display, '30 minutes ago');
  assert.equal(formatDateAdaptive(inMinutes(0)).display, '1 minute ago');
});

test('a message from earlier today is a bare time, and yesterday is named', () => {
  const earlier = new Date();
  earlier.setHours(0, 1, 0, 0);
  const iso = earlier.toISOString();
  // Only meaningful when 00:01 is over an hour ago; a run started at 00:30
  // correctly gets the relative form instead.
  if (Date.now() - earlier.getTime() >= 60 * MINUTE) {
    assert.equal(formatDateAdaptive(iso).display, hhmm(iso));
  }
  const yesterday = noonInDays(-1);
  assert.equal(formatDateAdaptive(yesterday).display, `Yesterday ${hhmm(yesterday)}`);
});

test('the rest of the past week is a weekday, then a date', () => {
  const recent = noonInDays(-3);
  const weekday = new Date(recent).toLocaleDateString(undefined, { weekday: 'short' });
  assert.equal(formatDateAdaptive(recent).display, `${weekday} ${hhmm(recent)}`);

  const older = noonInDays(-30);
  assert.ok(formatDateAdaptive(older).display.endsWith(`, ${hhmm(older)}`));
});

test('a message from another year drops the time, unlike the schedule column', () => {
  // The one place the two ladders deliberately disagree: when a message
  // arrived years ago the hour is noise, while when one will be sent it is
  // the whole point.
  const old = new Date();
  old.setFullYear(old.getFullYear() - 2, 5, 15);
  old.setHours(12, 0, 0, 0);
  const iso = old.toISOString();
  const { display } = formatDateAdaptive(iso);
  assert.ok(display.includes(String(old.getFullYear())), display);
  assert.ok(!display.includes(hhmm(iso)), display);
  assert.ok(formatDateSchedule(iso).display.includes(hhmm(iso)));
});
