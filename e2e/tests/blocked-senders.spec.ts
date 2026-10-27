// The real junk actions update the Settings list; manual additions, search and
// removal work through the web UI. Delivery precedence is covered by Go tests.
import { test, expect } from '@playwright/test';

test('mark junk blocks a sender and Not junk removes the Settings entry', async ({ page, request }) => {
  const messages = await (await request.get('/api/v1/folders/1/messages')).json();
  const sample = messages.items.find((m: { from_addr: string }) => m.from_addr.includes('alice@'));
  expect(sample).toBeDefined();
  const id = sample.id;
  let address = '';
  try {
    await page.goto(`/#/message/${id}`);
    await page.getByRole('button', { name: 'Mark as junk', exact: true }).click();
    const list = await (await request.get('/api/v1/blocked-senders')).json();
    expect(list.total).toBe(1);
    address = list.items[0].address;
    await expect(page.getByText(`Future mail from ${address} goes to Junk`, { exact: true })).toBeVisible();
    await page.goto('/#/settings/blocked-senders');
    await expect(page.locator('.settings-item-name', { hasText: address })).toBeVisible();
    await page.goto(`/#/message/${id}`);
    await page.getByRole('button', { name: 'Not junk', exact: true }).click();
    await expect(page.getByText(`${address} unblocked`, { exact: true })).toBeVisible();
    await page.goto('/#/settings/blocked-senders');
    await expect(page.getByText('No blocked senders.', { exact: true })).toBeVisible();
  } finally {
    await request.post(`/api/v1/messages/${id}/mark-not-junk`);
    const list = await (await request.get('/api/v1/blocked-senders')).json();
    for (const row of list.items) if (row.address === address) await request.delete(`/api/v1/blocked-senders/${row.id}`);
  }
});

test('Settings adds, searches, and removes an exact blocked address', async ({ page, request }) => {
  const address = 'blocked-e2e@example.com';
  try {
    await page.goto('/#/settings/blocked-senders');
    await page.getByLabel('Email address to block').fill(address);
    await page.getByRole('button', { name: 'Block sender', exact: true }).click();
    await expect(page.locator('.settings-item-name', { hasText: address })).toBeVisible();
    await page.getByRole('searchbox', { name: 'Search blocked senders' }).fill('unmatched');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByText('No matching blocked senders.', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Clear', exact: true }).click();
    const row = page.locator('.settings-item').filter({ hasText: address });
    await row.getByRole('button', { name: 'Remove', exact: true }).click();
    await expect(row).toHaveCount(0);
  } finally {
    const list = await (await request.get('/api/v1/blocked-senders')).json();
    for (const row of list.items) if (row.address === address) await request.delete(`/api/v1/blocked-senders/${row.id}`);
  }
});
