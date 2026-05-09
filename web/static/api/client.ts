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
  },

  messages: {
    list: (params: {
      folder_id?: number;
      search?: string;
      page?: number;
      per_page?: number;
    }) => {
      const q = new URLSearchParams();
      if (params.folder_id !== undefined) q.set('folder_id', String(params.folder_id));
      if (params.search) q.set('search', params.search);
      if (params.page !== undefined) q.set('page', String(params.page));
      if (params.per_page !== undefined) q.set('per_page', String(params.per_page));
      return request<{ total: number; items: MessageSummary[] }>('GET', `/messages?${q}`);
    },

    get: (id: number) =>
      request<components['schemas']['MessageDetail']>('GET', `/messages/${id}`),

    markRead: (ids: number[], read: boolean) =>
      request<void>('POST', '/messages/read', { ids, read }),

    markFlagged: (ids: number[], flagged: boolean) =>
      request<void>('POST', '/messages/flagged', { ids, flagged }),

    move: (ids: number[], folder_id: number) =>
      request<void>('POST', '/messages/move', { ids, folder_id }),

    delete: (ids: number[]) =>
      request<void>('DELETE', '/messages', { ids }),
  },
};
