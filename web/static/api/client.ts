import type { components } from './types.js';

type Folder = components['schemas']['Folder'];
type MessageSummary = components['schemas']['MessageSummary'];
type Identity = components['schemas']['Identity'];
type Contact = components['schemas']['Contact'];
type DraftRequest = components['schemas']['DraftRequest'];

const BASE = '/api/v1';

async function requestStatus(method: string, path: string): Promise<{ status: number; data: unknown }> {
  const res = await fetch(BASE + path, { method, headers: { 'Content-Type': 'application/json' } });
  if (res.status === 401) { window.location.reload(); throw new Error('Unauthorized'); }
  const data = await res.json() as unknown;
  if (!res.ok) throw new Error((data as { error?: string }).error ?? res.statusText);
  return { status: res.status, data };
}

async function requestMultipart<T>(method: string, path: string, body: unknown, files: File[]): Promise<T> {
  const fd = new FormData();
  fd.append('message', JSON.stringify(body));
  for (const f of files) fd.append('attachments', f);
  const res = await fetch(BASE + path, { method, body: fd });
  if (res.status === 401) { window.location.reload(); throw new Error('Unauthorized'); }
  if (res.status === 204) return undefined as T;
  const data = await res.json() as unknown;
  if (!res.ok) throw new Error((data as { error?: string }).error ?? res.statusText);
  return data as T;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method };
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  } else {
    init.headers = { 'Content-Type': 'application/json' };
  }

  const res = await fetch(BASE + path, init);

  if (res.status === 401) {
    window.location.reload();
    throw new Error('Unauthorized');
  }

  if (res.status === 204) return undefined as T;

  const data = await res.json() as unknown;
  if (!res.ok) {
    const err = data as { error?: string };
    throw new Error(err.error ?? res.statusText);
  }

  return data as T;
}

export const api = {
  folders: {
    list: () =>
      request<{ total: number; items: Folder[] }>('GET', '/folders'),

    create: (name: string) =>
      request<Folder>('POST', '/folders', { name }),

    delete: (id: number) =>
      request<void>('DELETE', `/folders/${id}`),

    listMessages: (folderId: number, limit: number, offset: number) => {
      const q = new URLSearchParams({ limit: String(limit), offset: String(offset) });
      return request<{ total: number; items: MessageSummary[] }>(
        'GET', `/folders/${folderId}/messages?${q}`
      );
    },

    markAllRead: (folderId: number) =>
      request<{ updated: number }>('POST', `/folders/${folderId}/mark-all-read`),

    deleteAllMessages: (folderId: number) =>
      request<{ moved_to_trash: number; permanently_deleted: number }>(
        'DELETE', `/folders/${folderId}/messages`
      ),
  },

  messages: {
    get: (id: number) =>
      request<components['schemas']['MessageDetail']>('GET', `/messages/${id}`),

    patch: (id: number, updates: { folder_id?: number; read?: boolean; flagged?: boolean }) =>
      request<components['schemas']['MessageSummary']>('PATCH', `/messages/${id}`, updates),

    deleteSingle: (id: number) =>
      request<void>('DELETE', `/messages/${id}`),

    bulkUpdate: (ids: number[], updates: { read?: boolean; flagged?: boolean }) =>
      request<{ updated: number }>('PATCH', '/messages', { ids, ...updates }),

    move: (ids: number[], folder_id: number) =>
      request<{ updated: number }>('POST', '/messages/move', { ids, folder_id }),

    delete: (ids: number[]) =>
      request<{ moved_to_trash: number; permanently_deleted: number }>(
        'DELETE', '/messages', { ids }
      ),

    search: (q: string, opts: { folder_id?: number; date_from?: string; date_to?: string; limit?: number; offset?: number } = {}) => {
      const p = new URLSearchParams({ q });
      if (opts.folder_id != null) p.set('folder_id', String(opts.folder_id));
      if (opts.date_from) p.set('date_from', opts.date_from);
      if (opts.date_to) p.set('date_to', opts.date_to);
      if (opts.limit != null) p.set('limit', String(opts.limit));
      if (opts.offset != null) p.set('offset', String(opts.offset));
      return request<{ total: number; items: (MessageSummary & { snippet?: string })[] }>('GET', `/messages/search?${p}`);
    },

    thread: (id: number) =>
      request<{ total: number; truncated: boolean; items: components['schemas']['MessageSummary'][] }>(
        'GET', `/messages/${id}/thread`
      ),

    snooze: (id: number, until: string) =>
      request<{ id: number; folder_id: number; snoozed_until: string; snooze_folder_id: number | null }>(
        'POST', `/messages/${id}/snooze`, { until }
      ),

    markJunk: (id: number) =>
      request<{ id: number; folder_id: number }>('POST', `/messages/${id}/mark-junk`),

    markNotJunk: (id: number) =>
      request<{ id: number; folder_id: number }>('POST', `/messages/${id}/mark-not-junk`),

    cancelSnooze: (id: number) =>
      request<{ id: number; folder_id: number }>('DELETE', `/messages/${id}/snooze`),
  },

  scheduled: {
    cancel: (id: number) =>
      request<{ id: number; folder_id: number }>('DELETE', `/scheduled/${id}`),
  },

  identities: {
    list: () =>
      request<{ total: number; items: Identity[] }>('GET', '/identities'),
  },

  contacts: {
    autocomplete: (q: string, limit = 10) => {
      const p = new URLSearchParams({ q, limit: String(limit) });
      return request<{ total: number; items: Contact[] }>('GET', `/contacts?${p}`);
    },
  },

  drafts: {
    create: (body: DraftRequest) =>
      request<{ id: number; updated_at: string }>('POST', '/drafts', body),

    createWithAttachments: (body: DraftRequest, files: File[]) =>
      requestMultipart<{ id: number; updated_at: string }>('POST', '/drafts-with-attachments', body, files),

    update: (id: number, body: DraftRequest) =>
      request<{ id: number; updated_at: string }>('PUT', `/drafts/${id}`, body),

    updateWithAttachments: (id: number, body: DraftRequest, files: File[]) =>
      requestMultipart<{ id: number; updated_at: string }>('PUT', `/drafts-with-attachments/${id}`, body, files),

    delete: (id: number) =>
      request<void>('DELETE', `/drafts/${id}`),

    deleteAttachment: (id: number, attachmentId: number) =>
      request<void>('DELETE', `/drafts/${id}/attachments/${attachmentId}`),

    send: (id: number) =>
      requestStatus('POST', `/drafts/${id}/send`),
  },
};
