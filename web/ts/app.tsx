import { render } from 'preact';
import { useState, useEffect, useRef, useCallback } from 'preact/hooks';
import { Sidebar } from './layout/Sidebar.js';
import { Toolbar } from './layout/Toolbar.js';
import { Toast } from './components/Toast.js';
import { ConfirmDialog } from './components/ConfirmDialog.js';
import { FolderView } from './views/FolderView.js';
import { MessageDetail } from './views/MessageDetail.js';
import { ComposeForm } from './views/ComposeForm.js';
import { SearchView } from './views/SearchView.js';
import { SettingsPage } from './views/SettingsPage.js';
import { initRouter, onRouteChange, type Route } from './router.js';
import { startPolling } from './poll.js';
import { isDemo } from './util/config.js';
import { applyTheme, getTheme } from './util/theme.js';
import { DemoDialog, demoNoticeSeen, markDemoNoticeSeen } from './components/DemoDialog.js';
import type { components } from './api/types.js';

// Apply persisted preferences on startup
(function initPrefs() {
  // `data-theme` rather than a class: the same attribute MyCal's and MyNotes'
  // stylesheets switch on, so the shared palette is selected the same way in
  // all three apps.
  applyTheme(getTheme());
  const density = localStorage.getItem('density');
  const VALID_DENSITIES = ['compact', 'normal', 'relaxed'];
  if (density && VALID_DENSITIES.includes(density)) {
    document.documentElement.classList.add(`density-${density}`);
  }
}());

type Folder = components['schemas']['Folder'];

function routeLabel(route: Route, folders: Folder[]): string {
  switch (route.type) {
    case 'inbox': return 'Inbox';
    case 'folder': return folders.find(f => f.slug === route.slug)?.name ?? route.slug;
    case 'message': return 'Message';
    case 'compose': return 'Compose';
    case 'search': return `Search: ${route.query}`;
    case 'settings': return route.tab ? `Settings — ${route.tab}` : 'Settings';
    default: {
      const _exhaustive: never = route;
      void _exhaustive;
      return '';
    }
  }
}

function App() {
  const [route, setRoute] = useState<Route>(() => initRouter());
  const [folders, setFolders] = useState<Folder[]>([]);
  // Tracks the last inbox/folder slug so the sidebar stays highlighted when
  // the user opens a message (which has no folder context of its own).
  const [lastFolderSlug, setLastFolderSlug] = useState<string>('inbox');
  // The one-time "this is a demo" notice, shown before anything is typed.
  const [showDemoNotice, setShowDemoNotice] = useState(() => isDemo() && !demoNoticeSeen());

  useEffect(() => {
    return onRouteChange(setRoute);
  }, []);

  const dismissDemoNotice = useCallback(() => {
    markDemoNoticeSeen();
    setShowDemoNotice(false);
  }, []);

  const pollRefreshRef = useRef<() => void>(() => {});
  const folderSlugRef = useRef<string | null>(null);
  folderSlugRef.current =
    route.type === 'inbox' ? 'inbox' :
    route.type === 'folder' ? route.slug :
    null;
  const prevFoldersRef = useRef<Folder[]>([]);

  useEffect(() => {
    const { stop, refresh } = startPolling((folders) => {
      const slug = folderSlugRef.current;
      if (slug !== null && prevFoldersRef.current.length > 0) {
        const prev = prevFoldersRef.current.find(f => f.slug === slug);
        const next = folders.find(f => f.slug === slug);
        if (prev !== undefined && next !== undefined && next.unread_count > prev.unread_count) {
          window.dispatchEvent(new CustomEvent('folder-reload'));
        }
      }
      prevFoldersRef.current = folders;
      setFolders(folders);
    });
    pollRefreshRef.current = refresh;
    return stop;
  }, []);

  useEffect(() => {
    const handler = () => pollRefreshRef.current();
    window.addEventListener('folder-reload', handler);
    return () => window.removeEventListener('folder-reload', handler);
  }, []);

  // Persist selected folder across sessions and keep lastFolderSlug current.
  useEffect(() => {
    if (route.type === 'inbox') {
      setLastFolderSlug('inbox');
      localStorage.setItem('selectedFolder', '#/inbox');
    } else if (route.type === 'folder') {
      setLastFolderSlug(route.slug);
      localStorage.setItem('selectedFolder', `#/folder/${route.slug}`);
    }
  }, [route]);

  const activeSlug =
    route.type === 'inbox' ? 'inbox' :
    route.type === 'folder' ? route.slug :
    route.type === 'message' ? lastFolderSlug :
    '';

  const label = routeLabel(route, folders);

  const folderSlug =
    route.type === 'inbox' ? 'inbox' :
    route.type === 'folder' ? route.slug :
    null;
  const currentFolder = folderSlug !== null
    ? folders.find(f => f.slug === folderSlug)
    : undefined;

  function renderContent() {
    if (route.type === 'inbox' || route.type === 'folder') {
      if (folders.length === 0) {
        return <div class="placeholder-view"><p>Loading…</p></div>;
      }
      if (!currentFolder) {
        return <div class="placeholder-view"><p>Folder not found</p></div>;
      }
      return <FolderView folder={currentFolder} folders={folders} />;
    }
    if (route.type === 'message') {
      return <MessageDetail id={route.id} folders={folders} />;
    }
    if (route.type === 'compose') {
      return (
        <ComposeForm
          replyId={route.replyId}
          replyAllId={route.replyAllId}
          forwardId={route.forwardId}
          draftId={route.draftId}
        />
      );
    }
    if (route.type === 'search') {
      return <SearchView query={route.query} folders={folders} />;
    }
    if (route.type === 'settings') {
      return <SettingsPage tab={route.tab} />;
    }
    return <div class="placeholder-view"><p>{label}</p></div>;
  }

  return (
    <div class="app">
      <Sidebar folders={folders} activeSlug={activeSlug} />
      <header class="topbar">
        <span class="topbar-title">{label}</span>
        {isDemo() && (
          <span class="demo-badge" role="status">
            Demo — mail is stored in this browser only
          </span>
        )}
      </header>
      <main class="content">
        {renderContent()}
      </main>
      <Toolbar />
      <Toast />
      <ConfirmDialog />
      {showDemoNotice && <DemoDialog onClose={dismissDemoNotice} />}
    </div>
  );
}

// In demo mode the backend is a service worker that has to be installed and in
// control before the app makes its first request, so rendering waits for it.
// The import is dynamic so a normal build never fetches the demo code at all.
async function start(root: HTMLElement): Promise<void> {
  if (isDemo()) {
    try {
      const { startDemoBackend } = await import('./demo-client.js');
      await startDemoBackend();
    } catch (e) {
      root.textContent = e instanceof Error ? e.message : 'The demo backend failed to start.';
      return;
    }
  }
  render(<App />, root);
}

const root = document.getElementById('app');
if (root) {
  void start(root);
}
