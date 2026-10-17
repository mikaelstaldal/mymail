import { useEffect, useState } from 'preact/hooks';
import { api } from '../api/client.js';
import type { components } from '../api/types.js';

type Warning = components['schemas']['MessageWarning'];
export function LinkConfirmation({ id, index }: { id: number; index: number }) {
  const [warning, setWarning] = useState<Warning | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    if (!Number.isSafeInteger(id) || id < 1 || !Number.isSafeInteger(index) || index < 0) {
      setError('Link not found'); return;
    }
    void api.messages.get(id).then(message => {
      if (!active) return;
      const found = message.warnings?.find(w => w.kind === 'deceptive_link' && w.link_index === index);
      if (found) setWarning(found); else setError('Link not found');
    }).catch(() => { if (active) setError('Message unavailable'); });
    return () => { active = false; };
  }, [id, index]);
  let destination: URL | null = null;
  try { const url = new URL(warning?.href ?? '', 'https://invalid.invalid'); if (['http:', 'https:'].includes(url.protocol) && warning?.href) destination = url; } catch { /* unavailable */ }
  return <section class="link-confirmation">
    <h1>Check this link before continuing</h1>
    {error ? <p role="alert">{error}</p> : !warning ? <p>Loading…</p> : <>
      <p>This link was flagged because its text names a different domain, or its destination uses an IP address or an internationalised domain.</p>
      <p>Visible text: <bdi>{warning.text}</bdi></p>
      <p>Destination host: <strong><bdi>{warning.decoded_host}</bdi></strong></p>
      {destination && destination.hostname !== warning.decoded_host && <p>ASCII host: <strong><bdi>{destination.hostname}</bdi></strong></p>}
      <p>Full destination: <bdi>{warning.href}</bdi></p>
    </>}
    <p><a href={`#/message/${id}`}>Go back</a></p>
    {!error && warning && destination && <p><a href={destination.href} rel="noopener noreferrer" referrerPolicy="no-referrer">Continue to destination</a></p>}
  </section>;
}
