// Real API threads retain cross-folder replies; the UI must preserve their
// Junk/Trash routing signal before and after explicit inline expansion.
import { test, expect } from '@playwright/test';

test('Junk and Trash replies are marked and initially collapsed', async ({ page, request }) => {
  const ids: number[] = [];
  try {
    const sent = await request.post('/api/v1/messages/send', {
      data: { to_addr: 'e2e@example.com', subject: 'Thread folder markers', body_text: 'Parent' },
    });
    expect(sent.status()).toBe(201);
    const parent = await sent.json();
    ids.push(parent.id);
    const detail = await (await request.get(`/api/v1/messages/${parent.id}`)).json();
    expect((await request.patch(`/api/v1/messages/${parent.id}`, { data: { folder_id: 1 } })).status()).toBe(200);
    for (const folder of [7, 4]) {
      const response = await request.post('/api/v1/messages/send', {
        data: {
          to_addr: 'e2e@example.com', subject: 'Re: Thread folder markers',
          body_text: `Reply in folder ${folder}`, in_reply_to: detail.message_id,
        },
      });
      expect(response.status()).toBe(201);
      const reply = await response.json();
      ids.push(reply.id);
      expect((await request.patch(`/api/v1/messages/${reply.id}`, { data: { folder_id: folder } })).status()).toBe(200);
    }
    await page.goto(`/#/message/${parent.id}`);
    const entries = page.locator('.thread-entry');
    await expect(entries).toHaveCount(3);
    await expect(page.locator('.thread-expanded')).toHaveCount(0);
    await expect(page.locator('.thread-current .thread-folder-label')).toHaveCount(0);
    for (const label of ['Junk', 'Trash']) {
      const entry = entries.filter({ has: page.locator('.thread-folder-label', { hasText: label }) });
      await expect(entry.locator('.thread-folder-label')).toBeVisible();
      await expect(entry.locator('.thread-from')).toHaveCSS('font-weight', '400');
      const secondaryColor = await entry.locator('.thread-subject').evaluate(el => getComputedStyle(el).color);
      await expect(entry.locator('.thread-from')).toHaveCSS('color', secondaryColor);
      await entry.locator('.thread-entry-btn').click();
      await expect(entry.locator('.thread-expanded .body-text')).toBeVisible();
      await expect(entry.locator('.thread-folder-label')).toBeVisible();
      await entry.locator('.thread-entry-btn').click();
      await expect(entry.locator('.thread-expanded')).toHaveCount(0);
    }
  } finally {
    for (const id of ids) {
      await request.patch(`/api/v1/messages/${id}`, { data: { folder_id: 4 } });
      await request.delete(`/api/v1/messages/${id}`);
    }
  }
});
