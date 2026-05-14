import { navigate } from '../router.js';
import { Identities } from './settings/Identities.js';
import { Folders } from './settings/Folders.js';
import { Filters } from './settings/Filters.js';
import { SpamFilter } from './settings/SpamFilter.js';
import { Contacts } from './settings/Contacts.js';
import { Preferences } from './settings/Preferences.js';

interface SettingsPageProps {
  tab?: string;
}

const TABS = [
  { slug: 'identities', label: 'Identities' },
  { slug: 'folders', label: 'Folders' },
  { slug: 'filters', label: 'Filters' },
  { slug: 'spam', label: 'Spam Filter' },
  { slug: 'contacts', label: 'Contacts' },
  { slug: 'preferences', label: 'Preferences' },
] as const;

export function SettingsPage({ tab }: SettingsPageProps) {
  const activeTab = tab ?? TABS[0].slug;

  return (
    <div class="settings-page">
      <div class="settings-tabs">
        {TABS.map(t => (
          <button
            key={t.slug}
            class={`settings-tab${activeTab === t.slug ? ' active' : ''}`}
            onClick={() => navigate(`#/settings/${t.slug}`)}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div class="settings-content">
        {activeTab === 'identities' && <Identities />}
        {activeTab === 'folders' && <Folders />}
        {activeTab === 'filters' && <Filters />}
        {activeTab === 'spam' && <SpamFilter />}
        {activeTab === 'contacts' && <Contacts />}
        {activeTab === 'preferences' && <Preferences />}
      </div>
    </div>
  );
}
