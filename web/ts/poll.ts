import { api } from './api/client.js';
import type { components } from './api/types.js';

type Folder = components['schemas']['Folder'];

const POLL_INTERVAL = 30_000;

export function startPolling(cb: (folders: Folder[]) => void): { stop: () => void; refresh: () => void } {
  let timerId: ReturnType<typeof setTimeout> | null = null;
  let lastInboxUnread = -1; // -1 = unknown; skip notification on first poll
  let stopped = false;

  async function poll(): Promise<void> {
    if (stopped) return;
    try {
      const { items } = await api.folders.list();
      if (stopped) return;

      cb(items);

      const inbox = items.find(f => f.slug === 'inbox');
      const count = inbox?.unread_count ?? 0;

      document.title = count > 0 ? `(${count}) mymail` : 'mymail';

      if (lastInboxUnread >= 0 && count > lastInboxUnread && Notification.permission === 'granted') {
        new Notification('MyMail', {
          body: `${count} unread message${count !== 1 ? 's' : ''}`,
        });
      }

      lastInboxUnread = count;
    } catch {
      // ignore transient poll errors
    }
  }

  function stopTimer(): void {
    if (timerId !== null) {
      clearTimeout(timerId);
      timerId = null;
    }
  }

  function scheduleNext(): void {
    stopTimer();
    if (!stopped && document.visibilityState !== 'hidden') {
      timerId = setTimeout(() => {
        timerId = null;
        void poll().then(scheduleNext);
      }, POLL_INTERVAL);
    }
  }

  function visibilityHandler(): void {
    if (stopped) return;
    if (document.visibilityState === 'visible') {
      void poll().then(scheduleNext);
    } else {
      stopTimer();
    }
  }

  void poll().then(scheduleNext);
  document.addEventListener('visibilitychange', visibilityHandler);

  function stop(): void {
    stopped = true;
    stopTimer();
    document.removeEventListener('visibilitychange', visibilityHandler);
  }

  function refresh(): void {
    stopTimer();
    void poll().then(scheduleNext);
  }

  return { stop, refresh };
}
