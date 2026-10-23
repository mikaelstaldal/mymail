import { test, expect } from '@playwright/test';

// Browser consent UI is outside Playwright's page; stub only the registration API.
// Exercise the actual handler URL, editor escaping and no-save-on-open behavior.
test('registers a handler and opens a mailto composition without sending or saving', async ({ page }) => {
  await page.addInitScript(() => {
    navigator.registerProtocolHandler = (scheme, url) => {
      (window as unknown as { handler: string[] }).handler = [scheme, String(url)];
    };
  });
  await page.goto('/#/settings/preferences');
  await page.getByRole('button', { name: 'Handle email links' }).click();
  const handler = await page.evaluate(() => (window as unknown as { handler: string[] }).handler);
  expect(handler[0]).toBe('mailto');
  expect(handler[1]).toContain('/#/compose?mailto=%s');
  await page.evaluate(() => localStorage.setItem('composeDraft', JSON.stringify({
    id: null, to: ['unrelated@example.org'], cc: [], bcc: [], subject: 'Existing local draft',
    bodyHtml: '<p>Keep this draft</p>', sendAt: '', savedAt: new Date().toISOString(),
  })));
  let writes = 0;
  page.on('request', req => {
    if (req.method() !== 'GET' && /\/api\/v1\/(drafts|messages)/.test(req.url())) writes++;
  });
  const uri = 'mailto:alice+tag@example.org?cc=bob@example.org&bcc=carol@example.org&subject=Hello%20world&body=%3Cb%3Eplain%3C%2Fb%3E%0D%0Asecond';
  await page.goto(handler[1].replace('%s', encodeURIComponent(uri)));
  await expect(page.locator('.cf-subject-input')).toHaveValue('Hello world');
  await expect(page.locator('.compose-form')).toContainText('alice+tag@example.org');
  await expect(page.locator('.compose-form')).toContainText('bob@example.org');
  await expect(page.locator('.compose-form')).toContainText('carol@example.org');
  await expect(page.locator('.ql-editor')).toContainText('<b>plain</b>');
  await expect(page.locator('.ql-editor b')).toHaveCount(0);
  // A second handler navigation must reinitialize even while compose is mounted.
  await page.evaluate(() => { window.location.hash = '#/compose?mailto=' + encodeURIComponent('mailto:next@example.org?subject=Second'); });
  await expect(page.locator('.cf-subject-input')).toHaveValue('Second');
  await expect(page.locator('.compose-form')).toContainText('next@example.org');
  await page.getByRole('button', { name: 'Discard', exact: true }).click();
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Preferences', exact: true })).toBeVisible();
  expect(writes).toBe(0);
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('composeDraft')!).subject)).toBe('Existing local draft');
});

test('saving an edited mailto composition preserves the ordinary draft resume entry', async ({ page }) => {
  await page.goto('/#/inbox');
  await page.evaluate(() => localStorage.setItem('composeDraft', JSON.stringify({ subject: 'Unrelated draft' })));
  await page.goto('/#/compose?mailto=' + encodeURIComponent('mailto:alice@example.org?subject=Link'));
  await expect(page.locator('.cf-subject-input')).toHaveValue('Link');
  await page.locator('.cf-subject-input').fill('Edited link');
  const saved = page.waitForResponse(res => res.request().method() === 'POST' && res.url().endsWith('/api/v1/drafts'));
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  expect((await saved).status()).toBe(201);
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('composeDraft')!).subject)).toBe('Unrelated draft');
});
