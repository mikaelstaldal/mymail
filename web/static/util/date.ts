export function formatDateAdaptive(dateStr: string): { display: string; title: string } {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMins = Math.floor((now.getTime() - date.getTime()) / 60_000);

  const title = date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZoneName: 'short',
    hour12: false,
  });

  const hhmm = date.toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });

  const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const msgDayStart = new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
  const calDiff = Math.round((todayStart - msgDayStart) / 86_400_000);

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
