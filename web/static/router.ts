export type Route =
  | { type: 'inbox' }
  | { type: 'folder'; slug: string }
  | { type: 'message'; id: number }
  | { type: 'compose'; replyId?: number; replyAllId?: number; forwardId?: number; draftId?: number }
  | { type: 'search'; query: string }
  | { type: 'settings'; tab?: string };

function parseRoute(hash: string): Route {
  const raw = hash.startsWith('#') ? hash.slice(1) : hash;
  const qIdx = raw.indexOf('?');
  const path = qIdx >= 0 ? raw.slice(0, qIdx) : raw;
  const params = new URLSearchParams(qIdx >= 0 ? raw.slice(qIdx + 1) : '');
  const parts = path.split('/').filter(Boolean);
  const seg = parts[0] ?? '';

  if (!seg || seg === 'inbox') return { type: 'inbox' };

  if (seg === 'folder' && parts[1]) return { type: 'folder', slug: parts[1] };

  if (seg === 'message' && parts[1]) {
    const id = Number(parts[1]);
    return { type: 'message', id };
  }

  if (seg === 'compose') {
    const replyId = params.get('reply') ? Number(params.get('reply')) : undefined;
    const replyAllId = params.get('replyall') ? Number(params.get('replyall')) : undefined;
    const forwardId = params.get('forward') ? Number(params.get('forward')) : undefined;
    const draftId = params.get('draft') ? Number(params.get('draft')) : undefined;
    return { type: 'compose', replyId, replyAllId, forwardId, draftId };
  }

  if (seg === 'search') return { type: 'search', query: params.get('q') ?? '' };

  if (seg === 'settings') return { type: 'settings', tab: parts[1] };

  return { type: 'inbox' };
}

export function initRouter(): Route {
  const hash = window.location.hash;
  if (!hash || hash === '#' || hash === '#/') {
    const saved = localStorage.getItem('selectedFolder');
    const target = saved ?? '#/inbox';
    window.location.hash = target;
    return parseRoute(target);
  }
  return parseRoute(hash);
}

export function navigate(hash: string): void {
  window.location.hash = hash;
}

export function currentRoute(): Route {
  return parseRoute(window.location.hash);
}

export function onRouteChange(cb: (route: Route) => void): () => void {
  const handler = () => cb(parseRoute(window.location.hash));
  window.addEventListener('hashchange', handler);
  return () => window.removeEventListener('hashchange', handler);
}
