import { Icon } from '../components/Icon.js';
import type { components } from '../api/types.js';

type Folder = components['schemas']['Folder'];

/** Lucide icon name per built-in folder slug; user folders fall back to `folder`. */
const FOLDER_ICONS: Record<string, string> = {
  inbox: 'inbox',
  sent: 'send',
  drafts: 'file-pen',
  scheduled: 'clock',
  snoozed: 'alarm-clock',
  trash: 'trash-2',
  junk: 'triangle-alert',
};

const MAIN_SLUGS = ['inbox', 'sent', 'drafts', 'scheduled', 'snoozed'];
const SYSTEM_SLUGS = ['trash', 'junk'];
const BUILTIN_SLUGS = new Set([...MAIN_SLUGS, ...SYSTEM_SLUGS]);

interface FolderItemProps {
  folder: Folder;
  active: boolean;
  onFolderEnter: (folder: Folder) => void;
}

function FolderItem({ folder, active, onFolderEnter }: FolderItemProps) {
  const href = folder.slug === 'inbox' ? '#/inbox' : `#/folder/${folder.slug}`;
  const icon = FOLDER_ICONS[folder.slug] ?? 'folder';
  return (
    <li class={active ? 'folder-item active' : 'folder-item'}>
      <a href={href} title={`${folder.slug} (id ${folder.id})`}
        onKeyDown={event => {
          if (event.key === 'Enter' && !event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey) {
            event.preventDefault();
            onFolderEnter(folder);
            return;
          }
          if (event.key === 'ArrowRight') {
            event.preventDefault();
            document.querySelector<HTMLElement>('.msg-table .msg-row')?.focus();
            return;
          }
          if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return;
          event.preventDefault();
          const links = Array.from(event.currentTarget.closest('.sidebar')!
            .querySelectorAll<HTMLAnchorElement>('.folder-item a'));
          const index = links.indexOf(event.currentTarget);
          links[index + (event.key === 'ArrowUp' ? -1 : 1)]?.focus();
        }}>
        <Icon name={icon} class="folder-icon" />
        {folder.name}
        {folder.unread_count > 0 && (
          <span class="folder-badge">{folder.unread_count}</span>
        )}
      </a>
    </li>
  );
}

interface SidebarProps {
  folders: Folder[];
  activeSlug: string;
  onFolderEnter: (folder: Folder) => void;
}

export function Sidebar({ folders, activeSlug, onFolderEnter }: SidebarProps) {
  const main = folders.filter(f => MAIN_SLUGS.includes(f.slug));
  const system = folders.filter(f => SYSTEM_SLUGS.includes(f.slug));
  const user = folders.filter(f => !BUILTIN_SLUGS.has(f.slug));

  return (
    <nav class="sidebar">
      <div class="sidebar-header">
        <div class="logo-icon"><Icon name="mail" size={17} /></div>
        MyMail
        <button
          class="sidebar-reload-btn"
          onClick={() => window.dispatchEvent(new CustomEvent('folder-reload'))}
          aria-label="Reload"
          title="Reload"
        >
          <Icon name="refresh-cw" />
        </button>
      </div>

      <div class="sidebar-section-label">Mailboxes</div>
      <ul class="folder-list">
        {main.map(f => (
          <FolderItem key={f.id} folder={f} active={f.slug === activeSlug} onFolderEnter={onFolderEnter} />
        ))}
      </ul>

      <div class="sidebar-divider" />

      <ul class="folder-list">
        {system.map(f => (
          <FolderItem key={f.id} folder={f} active={f.slug === activeSlug} onFolderEnter={onFolderEnter} />
        ))}
      </ul>

      {user.length > 0 && (
        <>
          <div class="sidebar-section-label">My Folders</div>
          <ul class="folder-list">
            {user.map(f => (
              <FolderItem key={f.id} folder={f} active={f.slug === activeSlug} onFolderEnter={onFolderEnter} />
            ))}
          </ul>
        </>
      )}

      <div class="sidebar-spacer" />

      <div class="sidebar-footer">
        <a href="#/settings" class="sidebar-settings-link">
          <Icon name="settings" />
          Settings
        </a>
      </div>
    </nav>
  );
}
