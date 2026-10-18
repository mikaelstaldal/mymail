// Real seeded warnings and download bytes, with fresh assets/server provided by
// test-e2e.sh. No warning verdict is invented by a browser route mock.
import { test, expect } from '@playwright/test';

async function sample(request: import('@playwright/test').APIRequestContext) {
  const messages = await (await request.get('/api/v1/folders/1/messages')).json();
  const message = messages.items.find((m: { subject: string }) => m.subject === 'Phishing warning examples (safe demo)');
  expect(message).toBeDefined();
  return (await request.get(`/api/v1/messages/${message.id}`)).json();
}

test('warnings explain the server signals and sender address stays visible', async ({ page, request }) => {
  const message = await sample(request);
  expect(message.warnings).toHaveLength(6);
  await page.goto(`/#/message/${message.id}`);
  const banner = page.getByRole('note', { name: 'Message warnings' });
  await expect(banner.locator('li')).toHaveCount(6);
  for (const detail of ['security@example.com', 'xn--exmple-4nf.com', 'help@unrelated.example', 'never sent', 'https://unrelated.example/', 'invoice.pdf.exe']) {
    await expect(banner).toContainText(detail);
  }
  await expect(banner.locator('.phishing-warning-strong')).toHaveCount(1);
  await expect(page.locator('.msg-detail-header .sender-name')).toHaveText('security@example.com');
  await expect(page.locator('.msg-detail-header .sender-address')).toBeVisible();
  await expect(page.locator('.msg-detail-header .sender-address')).not.toContainText('xn--');
  await expect(page.locator('.sender-idn').first()).toContainText('IDN');
  const plain = page.getByRole('button', { name: 'Plain', exact: true });
  await plain.click();
  await expect(banner).toBeVisible();
});

test('risky attachment requires confirmation and Cancel makes no download request', async ({ page, request }) => {
  const message = await sample(request);
  await page.goto(`/#/message/${message.id}`);
  const attachment = page.getByRole('button', { name: /invoice.pdf.exe/ });
  await expect(attachment).toContainText('Warning: has a double extension');
  let requests = 0;
  await page.route(`**/api/v1/attachments/${message.attachments[0].id}`, async route => { requests++; await route.continue(); });
  await attachment.click();
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toContainText('invoice.pdf.exe');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(requests).toBe(0);
  await attachment.focus();
  await page.keyboard.press('Enter');
  const downloadPromise = page.waitForEvent('download');
  await page.getByRole('alertdialog').getByRole('button', { name: 'Download', exact: true }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe('invoice.pdf.exe');
  const { readFile } = await import('node:fs/promises');
  expect(await readFile((await download.path())!, 'utf8')).toContain('Safe demo placeholder');
});

for (const action of ['Reply', 'Reply All']) {
  test(`${action} shows the server's Reply-To mismatch by To`, async ({ page, request }) => {
    const message = await sample(request);
    await page.goto(`/#/message/${message.id}`);
    await page.getByRole('button', { name: action, exact: true }).click();
    await expect(page.locator('.compose-reply-warning')).toContainText('help@unrelated.example');
    await expect(page.locator('.compose-reply-warning')).toContainText('different domain');
  });
}

test('thread entries show the full sender address', async ({ page, request }) => {
  const messages = await (await request.get('/api/v1/folders/1/messages')).json();
  for (const message of messages.items) {
    const thread = await (await request.get(`/api/v1/messages/${message.id}/thread`)).json();
    if (thread.items.length < 2) continue;
    await page.goto(`/#/message/${message.id}`);
    await expect(page.locator('.thread-from .sender-address')).toHaveCount(thread.items.length);
    for (const address of await page.locator('.thread-from .sender-address').all()) {
      await expect(address).toBeVisible();
      await expect(address).toContainText('@');
    }
    return;
  }
  throw new Error('Demo seed must include a thread');
});
