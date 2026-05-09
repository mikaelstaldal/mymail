import { render } from 'preact';
import { useState, useEffect } from 'preact/hooks';
import { Sidebar } from './layout/Sidebar.js';
import { Toolbar } from './layout/Toolbar.js';
import { initRouter, onRouteChange, type Route } from './router.js';
import { startPolling } from './poll.js';
import type { components } from './api/types.js';

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

  useEffect(() => {
    return onRouteChange(setRoute);
  }, []);

  useEffect(() => {
    return startPolling(setFolders);
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

  const slug =
    route.type === 'inbox' ? 'inbox' :
    route.type === 'folder' ? route.slug :
    route.type === 'message' ? lastFolderSlug :
    '';

  const label = routeLabel(route, folders);

  return (
    <div class="app">
      <Sidebar folders={folders} activeSlug={slug} />
      <header class="topbar">
        <span class="topbar-title">{label}</span>
      </header>
      <main class="content">
        <div class="placeholder-view">
          <p>{label}</p>
        </div>
      </main>
      <Toolbar />
    </div>
  );
}

const root = document.getElementById('app');
if (root) {
  render(<App />, root);
}
