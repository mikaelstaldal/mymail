import { useEffect, useRef, useState } from 'preact/hooks';
import { api } from '../api/client.js';
import type { components } from '../api/types.js';

type DeviceRequest = components['schemas']['DeviceRequest'];
interface Props { resources: { id: number; name: string }[]; scopePrefix: string; onDecision?: () => void; }

export function DeviceApproval({ resources, scopePrefix, onDecision }: Props) {
  const sectionRef = useRef<HTMLElement | null>(null);
  const initial = new URLSearchParams(window.location.search).get('user_code') ?? '';
  const [code, setCode] = useState(initial);
  const [request, setRequest] = useState<DeviceRequest | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [lifetime, setLifetime] = useState('30d');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => { if (request) sectionRef.current?.scrollIntoView({ block: 'start' }); }, [request]);

  async function lookup(value: string) {
    setError(''); setMessage(''); setRequest(null); setBusy(true);
    try {
      const pending = await api.device.get(value);
      setRequest(pending); setSelected([]); setLifetime('30d');
    } catch (e) { setError(e instanceof Error && e.message !== 'Not found' ? e.message : 'Unknown, expired, or already decided user code.'); }
    finally { setBusy(false); }
  }
  useEffect(() => { if (initial) {
    const url = new URL(window.location.href); url.searchParams.delete('user_code'); url.searchParams.delete('settings');
    window.history.replaceState(null, '', url);
    void lookup(initial);
  } }, []);

  async function decide(approve: boolean) {
    if (!request) return;
    setError(''); setBusy(true);
    try {
      await api.device.decide(request.user_code, approve ? { approve, scope: selected, lifetime } : { approve });
      setRequest(null); setCode('');
      setMessage(approve ? 'Approved. The waiting client will receive its token.' : 'Denied. No token will be issued.');
      onDecision?.();
    } catch (e) { setError(e instanceof Error ? e.message : 'Could not record decision'); }
    finally { setBusy(false); }
  }

  return <section class="device-approval" ref={sectionRef}>
    <h3>Approve device</h3>
    <p>Enter the code shown by your token CLI. Approve only a request you started, and compare the code with your CLI.</p>
    <div class="settings-field"><label>User code<input type="text" aria-label="User code" value={code} maxLength={32} disabled={busy} placeholder="XXXX-XXXX" onInput={e => { setCode((e.target as HTMLInputElement).value); setRequest(null); setMessage(''); }} /></label></div>
    <button class="btn btn-secondary" type="button" disabled={busy || !code.trim()} onClick={() => void lookup(code.trim())}>Review request</button>
    {error && <p role="alert">{error}</p>}
    {message && <p role="status">{message}</p>}
    {request && <div>
      <dl>
        <dt>User code</dt><dd><strong>{request.user_code}</strong></dd>
        <dt>Client</dt><dd>{request.client_id}</dd>
        <dt>Token name</dt><dd>{request.name}</dd>
        <dt>Requester (IP | User-Agent)</dt><dd class="device-requester">{request.requester}</dd>
        <dt>Requested</dt><dd>{Math.max(0, Math.floor((Date.now() - new Date(request.created_at).getTime()) / 60000))} minutes ago ({new Date(request.created_at).toLocaleString()})</dd>
        <dt>Requested scope</dt><dd>{request.scope.length ? request.scope.map(scope => resources.find(r => `${scopePrefix}:${r.id}` === scope)?.name ?? scope).join(', ') : 'No suggestion — choose below'}</dd>
        <dt>Requested lifetime</dt><dd>{request.lifetime}</dd>
      </dl>
      <fieldset disabled={busy}><legend>Granted resources</legend>{resources.map(resource => {
        const scope = `${scopePrefix}:${resource.id}`;
        return <label key={scope}><input type="checkbox" checked={selected.includes(scope)} onChange={e => setSelected((e.target as HTMLInputElement).checked ? [...selected, scope] : selected.filter(s => s !== scope))} />{resource.name}</label>;
      })}</fieldset>
      <div class="settings-field"><label>Granted lifetime<input type="text" aria-label="Granted lifetime" value={lifetime} maxLength={20} disabled={busy} placeholder="30d" onInput={e => setLifetime((e.target as HTMLInputElement).value)} /></label></div>
      <p>Lifetime: a positive number followed by s, m, h, or d, up to ten years.</p>
      <div class="device-actions">
        <button class="btn btn-primary" type="button" disabled={busy || !selected.length || selected.some(scope => !resources.some(resource => `${scopePrefix}:${resource.id}` === scope)) || !/^[1-9][0-9]*[smhd]$/.test(lifetime)} onClick={() => void decide(true)}>Approve</button>
        <button class="btn btn-danger" type="button" disabled={busy} onClick={() => void decide(false)}>Deny</button>
      </div>
    </div>}
  </section>;
}
