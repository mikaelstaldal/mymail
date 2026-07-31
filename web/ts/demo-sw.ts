// The demo service worker's entry point: lifecycle and request interception.
//
// Registered only by a demo build (see web/ts/demo-client.ts), it intercepts
// every same-scope /api/v1 request and answers it from browser-local storage,
// so the whole web UI runs with no server behind it. Requests for anything else
// — the app's own scripts, styles, and vendor bundles — are left alone and go
// to the network as usual.
//
// See demo/model.ts for why these are globals rather than module exports, and
// why this is a classic worker script.

// The demo backend, loaded synchronously into this worker's global scope. Paths
// are relative to this script, which sits at the deployment root — that is what
// gives the worker a scope covering the whole app.
importScripts(
  'demo/model.js',
  'demo/text.js',
  'demo/reply.js',
  'demo/store.js',
  'demo/api.js',
);

/** `self`, typed. lib.webworker declares it as the generic worker scope. */
const sw = self as unknown as ServiceWorkerGlobalScope;

/** The path prefix every emulated endpoint sits under, relative to the scope. */
const API_PREFIX = 'api/v1';

/** The message a client sends to ask this worker to take control of it. */
const CLAIM_MESSAGE = 'mymail-demo:claim';

/** Must match demo.SeedFileName in internal/demo/bundle.go. */
const SEED_FILE_NAME = 'demo-data.json';

/**
 * The registration scope: the deployment's base path, since the page registers
 * the worker from its own directory. The seed document is resolved relative to
 * this, so a bundle works at the origin root and under a path alike.
 */
function scopeURL(): URL {
  return new URL(sw.registration.scope);
}

function seedURL(): string {
  return new URL(SEED_FILE_NAME, scopeURL()).href;
}

// ── Lifecycle ────────────────────────────────────────────────────────────────

// Take over as soon as installed, then claim the pages that are already open:
// the page registers the worker and waits for it, so without claiming, the very
// first load would find no controller and have to reload itself.
sw.addEventListener('install', () => {
  sw.skipWaiting();
});

sw.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    await sw.clients.claim();
    // Seed now rather than on the first API call, so the initial folder list
    // does not wait on a fetch of the demo content.
    await withStore(async () => undefined);
  })());
});

// Claiming on activate covers a first load, but not a hard reload (Ctrl-Shift-R):
// the browser loads that navigation with this worker bypassed, so the page ends
// up uncontrolled while the worker stays activated and activate never fires
// again. The page notices and asks here (see web/ts/demo-client.ts).
sw.addEventListener('message', (event) => {
  const data = event.data as { type?: string } | null;
  if (data === null || data.type !== CLAIM_MESSAGE) return;
  event.waitUntil(sw.clients.claim());
});

// ── Interception ─────────────────────────────────────────────────────────────

sw.addEventListener('fetch', (event) => {
  const path = apiPath(event.request.url);
  if (path !== null) {
    event.respondWith(handleApiRequest(path, event.request));
  }
  // Anything else — the shell, scripts, styles, vendor bundles — goes to the
  // network. MyMail routes on the fragment (#/inbox), so every navigation is
  // for the deployment root and needs no rewriting to the SPA shell.
});

/**
 * The API path of a request, or null when it is not an API request this worker
 * serves. Returns the part after the /api/v1 prefix with a leading slash, so
 * "https://host/mail/api/v1/messages/7" under scope "/mail/" yields
 * "/messages/7".
 */
function apiPath(requestURL: string): string | null {
  const url = new URL(requestURL);
  const scope = scopeURL();
  if (url.origin !== scope.origin) return null;
  if (!url.pathname.startsWith(scope.pathname)) return null;
  const rest = url.pathname.slice(scope.pathname.length);
  if (rest !== API_PREFIX && !rest.startsWith(API_PREFIX + '/')) return null;
  return rest.slice(API_PREFIX.length);
}
