import { navigate } from '../router.js';
import { Identities } from './settings/Identities.js';
import { Folders } from './settings/Folders.js';
import { Filters } from './settings/Filters.js';
import { SpamFilter } from './settings/SpamFilter.js';
import { BlockedSenders } from './settings/BlockedSenders.js';
import { Contacts } from './settings/Contacts.js';
import { Preferences } from './settings/Preferences.js';
import { ApiTokens } from './settings/ApiTokens.js';
import { isDemo } from '../util/config.js';

interface SettingsPageProps {
  tab?: string;
  senderMessageId?: number;
}

const TABS = [
  { slug: 'identities', label: 'Identities' },
  { slug: 'folders', label: 'Folders' },
  { slug: 'filters', label: 'Filters' },
  { slug: 'spam', label: 'Spam Filter' },
  { slug: 'blocked-senders', label: 'Blocked senders' },
  { slug: 'contacts', label: 'Contacts' },
  { slug: 'preferences', label: 'Preferences' },
  { slug: 'api-tokens', label: 'API Tokens' },
] as const;

export function SettingsPage({ tab, senderMessageId }: SettingsPageProps) {
  const activeTab = tab ?? TABS[0].slug;

  return (
    <div class="settings-page">
      <div class="settings-tabs">
        {TABS.filter(t => t.slug !== 'api-tokens' || !isDemo()).map(t => (
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
        {activeTab === 'blocked-senders' && <BlockedSenders />}
        {activeTab === 'contacts' && <Contacts key={senderMessageId ?? 'list'} senderMessageId={senderMessageId} />}
        {activeTab === 'preferences' && <Preferences />}
        {activeTab === 'api-tokens' && !isDemo() && <ApiTokens />}
      </div>
    </div>
  );
}
