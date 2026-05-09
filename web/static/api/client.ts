import type { components } from './types.js';

type Folder = components['schemas']['Folder'];
type MessageSummary = components['schemas']['MessageSummary'];

const BASE = '/api/v1';

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
};
