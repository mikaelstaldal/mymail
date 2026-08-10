import { useState, useEffect } from 'preact/hooks';
import { getMycalUrl, getWrapColumn, WRAP_COLUMN_KEY } from '../../util/config.js';
import { normalizeWrapColumn, WRAP_COLUMN, WRAP_OFF, MAX_WRAP_COLUMN } from '../../util/wrap.js';
import { getTheme, onThemeChange, setTheme } from '../../util/theme.js';

type Density = 'compact' | 'normal' | 'relaxed';
type BodyView = 'html' | 'text';

function applyDensity(density: Density) {
  const el = document.documentElement;
  el.classList.remove('density-compact', 'density-normal', 'density-relaxed');
  el.classList.add(`density-${density}`);
}

export function Preferences() {
  const [darkMode, setDarkMode] = useState(() => getTheme() === 'dark');
  const [density, setDensity] = useState<Density>(
    () => (localStorage.getItem('density') as Density | null) ?? 'normal',
  );
  const [bodyView, setBodyView] = useState<BodyView>(
    () => (localStorage.getItem('preferredBodyView') as BodyView | null) ?? 'html',
  );
  const [notifEnabled, setNotifEnabled] = useState(() => localStorage.getItem('notificationsEnabled') === 'true');
  const [notifMessage, setNotifMessage] = useState<string | null>(null);
  const serverMycalUrl = window.__serverConfig?.mycalUrl ?? '';
  const [mycalUrl, setMycalUrl] = useState(() => getMycalUrl());
  // Kept as the raw string so the field shows what is being typed; only what is
  // stored is normalised. Blur then snaps the field to the column in effect.
  const [wrapColumn, setWrapColumn] = useState(() => String(getWrapColumn()));

  // The sidebar carries the same toggle, so follow it rather than going stale
  // while this tab is open.
  useEffect(() => onThemeChange(t => setDarkMode(t === 'dark')), []);

  useEffect(() => {
    // Auto-disable if notification permission was revoked since last visit
    if (
      localStorage.getItem('notificationsEnabled') === 'true' &&
      'Notification' in window &&
      Notification.permission !== 'granted'
    ) {
      setNotifEnabled(false);
      localStorage.setItem('notificationsEnabled', 'false');
    }
  }, []);

  function toggleDarkMode(value: boolean) {
    setDarkMode(value);
    setTheme(value ? 'dark' : 'light');
  }

  function changeDensity(value: Density) {
    setDensity(value);
    localStorage.setItem('density', value);
    applyDensity(value);
  }

  function changeBodyView(value: BodyView) {
    setBodyView(value);
    localStorage.setItem('preferredBodyView', value);
  }

  function changeMycalUrl(value: string) {
    const trimmed = value.trim();
    setMycalUrl(trimmed);
    if (trimmed) {
      localStorage.setItem('mycalUrl', trimmed);
    } else {
      localStorage.removeItem('mycalUrl');
    }
  }

  function changeWrapColumn(value: string) {
    setWrapColumn(value);
    if (value.trim()) {
      localStorage.setItem(WRAP_COLUMN_KEY, String(normalizeWrapColumn(value)));
    } else {
      localStorage.removeItem(WRAP_COLUMN_KEY);
    }
  }

  async function toggleNotifications(enable: boolean) {
    setNotifMessage(null);
    if (!enable) {
      setNotifEnabled(false);
      localStorage.setItem('notificationsEnabled', 'false');
      return;
    }
    if (!('Notification' in window)) {
      setNotifMessage('Browser notifications are not supported in this browser.');
      return;
    }
    if (Notification.permission === 'denied') {
      setNotifMessage('Notifications are blocked. Allow them in your browser settings first.');
      return;
    }
    try {
      const result = await Notification.requestPermission();
      if (result === 'granted') {
        setNotifEnabled(true);
        localStorage.setItem('notificationsEnabled', 'true');
      } else {
        setNotifEnabled(false);
        localStorage.setItem('notificationsEnabled', 'false');
        setNotifMessage('Notification permission was not granted.');
      }
    } catch {
      setNotifEnabled(false);
      localStorage.setItem('notificationsEnabled', 'false');
    }
  }

  return (
    <div>
      {notifMessage && <div class="settings-error">{notifMessage}</div>}

      <div class="pref-row">
        <div class="pref-row-info">
          <div class="pref-label">Dark Mode</div>
          <div class="pref-description">Switch to a dark color scheme.</div>
        </div>
        <label class="pref-toggle" aria-label="Toggle dark mode">
          <input
            type="checkbox"
            checked={darkMode}
            onChange={e => toggleDarkMode((e.target as HTMLInputElement).checked)}
          />
          <span class="pref-toggle-track" />
        </label>
      </div>

      <div class="pref-row">
        <div class="pref-row-info">
          <div class="pref-label">Message List Density</div>
          <div class="pref-description">Controls row spacing in the message list.</div>
        </div>
        <div class="pref-radio-group">
          {(['compact', 'normal', 'relaxed'] as Density[]).map(d => (
            <button
              key={d}
              class={`pref-radio-btn${density === d ? ' active' : ''}`}
              onClick={() => changeDensity(d)}
            >
              {d.charAt(0).toUpperCase() + d.slice(1)}
            </button>
          ))}
        </div>
      </div>

      <div class="pref-row">
        <div class="pref-row-info">
          <div class="pref-label">Default Body View</div>
          <div class="pref-description">Initial rendering format for message bodies.</div>
        </div>
        <div class="pref-radio-group">
          <button
            class={`pref-radio-btn${bodyView === 'html' ? ' active' : ''}`}
            onClick={() => changeBodyView('html')}
          >
            HTML
          </button>
          <button
            class={`pref-radio-btn${bodyView === 'text' ? ' active' : ''}`}
            onClick={() => changeBodyView('text')}
          >
            Plain Text
          </button>
        </div>
      </div>

      <div class="pref-row">
        <div class="pref-row-info">
          <div class="pref-label">Compose Line Width</div>
          <div class="pref-description">
            Column at which lines are broken while composing. Set to {WRAP_OFF} to
            wrap nothing; leave empty for the default of {WRAP_COLUMN}.
          </div>
        </div>
        <input
          type="number"
          class="pref-text-input pref-number-input"
          min={WRAP_OFF}
          max={MAX_WRAP_COLUMN}
          value={wrapColumn}
          placeholder={String(WRAP_COLUMN)}
          onInput={e => changeWrapColumn((e.target as HTMLInputElement).value)}
          onBlur={() => setWrapColumn(String(getWrapColumn()))}
        />
      </div>

      <div class="pref-row">
        <div class="pref-row-info">
          <div class="pref-label">Browser Notifications</div>
          <div class="pref-description">Show a notification when new messages arrive.</div>
        </div>
        <label class="pref-toggle" aria-label="Toggle browser notifications">
          <input
            type="checkbox"
            checked={notifEnabled}
            onChange={e => void toggleNotifications((e.target as HTMLInputElement).checked)}
          />
          <span class="pref-toggle-track" />
        </label>
      </div>

      <div class="pref-row">
        <div class="pref-row-info">
          <div class="pref-label">MyCal URL</div>
          <div class="pref-description">
            Base URL of your MyCal instance. When set, .ics email attachments and calendar links in an HTML message
            can be imported directly into MyCal.
            {serverMycalUrl && !localStorage.getItem('mycalUrl') && (
              <span> Auto-configured from server.</span>
            )}
          </div>
        </div>
        <input
          type="url"
          class="pref-text-input"
          value={mycalUrl}
          placeholder={serverMycalUrl || 'http://localhost:8081'}
          onInput={e => changeMycalUrl((e.target as HTMLInputElement).value)}
        />
      </div>
    </div>
  );
}
