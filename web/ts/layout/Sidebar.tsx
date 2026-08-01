import { useState, useEffect } from 'preact/hooks';
import { Icon } from '../components/Icon.js';
import { getTheme, onThemeChange, toggleTheme, type Theme } from '../util/theme.js';
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
}

function FolderItem({ folder, active }: FolderItemProps) {
  const href = folder.slug === 'inbox' ? '#/inbox' : `#/folder/${folder.slug}`;
  const icon = FOLDER_ICONS[folder.slug] ?? 'folder';
  return (
    <li class={active ? 'folder-item active' : 'folder-item'}>
      <a href={href}>
        <Icon name={icon} class="folder-icon" />
        {folder.name}
        {folder.unread_count > 0 && (
          <span class="folder-badge">{folder.unread_count}</span>
        )}
      </a>
    </li>
  );
}

// The same control MyNotes puts in its sidebar footer: the icon shows the theme
// you would switch *to*, not the one in effect. It subscribes rather than
// reading the stored value once, so flipping the equivalent switch in
// Preferences moves this button too.
function ThemeToggle() {
  const [theme, setThemeState] = useState<Theme>(getTheme);
  useEffect(() => onThemeChange(setThemeState), []);

  const dark = theme === 'dark';
  const label = dark ? 'Switch to light mode' : 'Switch to dark mode';
  return (
    <button
      class="sidebar-theme-toggle"
      title={label}
      aria-label={label}
      aria-pressed={dark}
      onClick={() => setThemeState(toggleTheme())}
    >
      <Icon name={dark ? 'sun' : 'moon'} class="folder-icon" />
      {/* One word, not "Dark mode": the two buttons share a 220px sidebar and
          the full phrase leaves single-digit pixels of slack, which the first
          slightly wider font wipes out. Both words sit in the same grid cell so
          the button is the width of the longer one either way, and toggling
          cannot shift the Settings link beside it. `aria-label` above already
          names the button, so this text is decoration. */}
      <span class="sidebar-theme-label" aria-hidden="true">
        <span class={dark ? 'is-shown' : ''}>Light</span>
        <span class={dark ? '' : 'is-shown'}>Dark</span>
      </span>
    </button>
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
        <ThemeToggle />
        <a href="#/settings" class="sidebar-settings-link">
          <Icon name="settings" class="folder-icon" />
          Settings
        </a>
      </div>
    </nav>
  );
}
