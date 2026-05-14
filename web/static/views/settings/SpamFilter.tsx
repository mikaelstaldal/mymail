import { useState, useEffect, useCallback } from 'preact/hooks';
import { api } from '../../api/client.js';
import type { components } from '../../api/types.js';

type SpamFilterSettings = components['schemas']['SpamFilterSettings'];

export function SpamFilter() {
  const [settings, setSettings] = useState<SpamFilterSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api.spamFilter.get();
      setSettings(data);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load spam filter settings');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  async function save() {
    if (!settings) return;
    if (!Number.isFinite(settings.score_threshold) || settings.score_threshold < 0) {
      setError('Score threshold must be a valid number ≥ 0.');
      return;
    }
    setSaving(true);
    setError(null);
    setSuccess(false);
    try {
      const updated = await api.spamFilter.update(settings);
      setSettings(updated);
      setSuccess(true);
      setTimeout(() => setSuccess(false), 3000);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save settings');
    } finally {
      setSaving(false);
    }
  }

  if (loading) return <div class="settings-loading">Loading…</div>;
  if (!settings) return <div class="settings-error">{error ?? 'Failed to load settings'}</div>;

  return (
    <div>
      {error && <div class="settings-error">{error}</div>}
      {success && <div class="settings-success">Settings saved.</div>}
      <div class="spam-filter-form">
        <div class="settings-field-check">
          <input
            type="checkbox"
            id="spam-enabled"
            checked={settings.enabled}
            onChange={e => setSettings({ ...settings, enabled: (e.target as HTMLInputElement).checked })}
          />
          <label for="spam-enabled">Enable spam filter</label>
        </div>
        <div class="settings-field">
          <label>Score Header</label>
          <input
            type="text"
            value={settings.score_header}
            onInput={e => setSettings({ ...settings, score_header: (e.target as HTMLInputElement).value })}
            placeholder="X-Spam-Score"
          />
        </div>
        <div class="settings-field">
          <label>Score Threshold</label>
          <input
            type="number"
            value={settings.score_threshold}
            min="0"
            step="0.1"
            onInput={e => setSettings({
              ...settings,
              score_threshold: Number((e.target as HTMLInputElement).value),
            })}
          />
        </div>
        <div class="settings-form-actions">
          <button class="btn btn-primary" onClick={() => void save()} disabled={saving}>
            {saving ? 'Saving…' : 'Save Settings'}
          </button>
        </div>
      </div>
    </div>
  );
}
