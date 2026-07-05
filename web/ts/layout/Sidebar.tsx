import type { components } from '../api/types.js';

type Folder = components['schemas']['Folder'];

const FOLDER_ICONS: Record<string, string> = {
  inbox: '📥',
  sent: '📤',
  drafts: '📝',
  scheduled: '🕐',
  snoozed: '💤',
  trash: '🗑️',
  junk: '⚠️',
};

const MAIN_SLUGS = ['inbox', 'sent', 'drafts', 'scheduled', 'snoozed'];
const SYSTEM_SLUGS = ['trash', 'junk'];
const BUILTIN_SLUGS = new Set([...MAIN_SLUGS, ...SYSTEM_SLUGS]);

interface FolderItemProps {
  folder: Folder;
  active: boolean;
}

function FolderItem({ folder, active }: FolderItemProps) {
  const href = folder.slug === 'inbox' ? '#/inbox' : `#/folder/${folder.slug}`;
  const icon = FOLDER_ICONS[folder.slug] ?? '📁';
  return (
    <li class={active ? 'folder-item active' : 'folder-item'}>
      <a href={href}>
        <span class="folder-icon">{icon}</span>
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
}

export function Sidebar({ folders, activeSlug }: SidebarProps) {
  const main = folders.filter(f => MAIN_SLUGS.includes(f.slug));
  const system = folders.filter(f => SYSTEM_SLUGS.includes(f.slug));
  const user = folders.filter(f => !BUILTIN_SLUGS.has(f.slug));

  return (
    <nav class="sidebar">
      <div class="sidebar-header">
        <div class="logo-icon">✉</div>
        MyMail
        <button
          class="sidebar-reload-btn"
          onClick={() => window.dispatchEvent(new CustomEvent('folder-reload'))}
          aria-label="Reload"
          title="Reload"
        >
          ↻
        </button>
      </div>

      <div class="sidebar-section-label">Mailboxes</div>
      <ul class="folder-list">
        {main.map(f => (
          <FolderItem key={f.id} folder={f} active={f.slug === activeSlug} />
        ))}
      </ul>

      <div class="sidebar-divider" />

      <ul class="folder-list">
        {system.map(f => (
          <FolderItem key={f.id} folder={f} active={f.slug === activeSlug} />
        ))}
      </ul>

      {user.length > 0 && (
        <>
          <div class="sidebar-section-label">My Folders</div>
          <ul class="folder-list">
            {user.map(f => (
              <FolderItem key={f.id} folder={f} active={f.slug === activeSlug} />
            ))}
          </ul>
        </>
      )}

      <div class="sidebar-spacer" />

      <div class="sidebar-footer">
        <a href="#/settings" class="sidebar-settings-link">
          <span class="folder-icon">⚙</span>
          Settings
        </a>
      </div>
    </nav>
  );
}
