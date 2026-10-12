// API Tokens keeps listing, confirmed revocation and device consent, without creation.
import { test, expect } from '@playwright/test';

test('settings offers token listing and revocation without a creation form', async ({ page, request }) => {
  const created = await request.post('/api/v1/tokens', { data: { name: 'Settings token', expires_at: new Date(Date.now() + 3600000).toISOString(), folder_ids: [1] } });
  expect(created.status()).toBe(201);
  const token = await created.json();
  await page.goto('/#/settings/api-tokens');
  const settings = page.locator('.settings-content');
  await expect(settings.getByRole('button', { name: 'Create token' })).toHaveCount(0);
  await expect(settings.getByLabel('Name', { exact: true })).toHaveCount(0);
  await expect(settings.getByLabel('Expires at', { exact: true })).toHaveCount(0);
  await expect(settings.getByRole('group', { name: 'Readable folders' })).toHaveCount(0);
  const row = settings.getByRole('listitem').filter({ hasText: token.slug });
  await expect(row).toContainText('Settings token');
  await expect(row).toContainText('expires');
  await expect(row).toContainText('Inbox');
  await expect(settings.getByLabel('User code', { exact: true })).toBeVisible();
  await expect(settings.getByRole('button', { name: 'Review request' })).toBeDisabled();
  await settings.getByLabel('User code', { exact: true }).fill('   ');
  await expect(settings.getByRole('button', { name: 'Review request' })).toBeDisabled();
  await row.getByRole('button', { name: 'Revoke', exact: true }).click();
  const confirmation = page.getByRole('alertdialog', { name: 'Revoke API token' });
  await confirmation.getByRole('button', { name: 'Keep' }).click();
  await expect(row).toBeVisible();
  await page.route('**/api/v1/tokens/*', async route => {
    if (route.request().method() === 'DELETE') await route.fulfill({ status: 400, contentType: 'application/json', body: '{"error":"revoke failed"}' });
    else await route.continue();
  });
  await row.getByRole('button', { name: 'Revoke', exact: true }).click();
  await confirmation.getByRole('button', { name: 'Revoke', exact: true }).click();
  await expect(settings.getByRole('alert')).toContainText('revoke failed');
  await expect(row).toBeVisible();
  await page.unroute('**/api/v1/tokens/*');
  await row.getByRole('button', { name: 'Revoke', exact: true }).click();
  await confirmation.getByRole('button', { name: 'Revoke', exact: true }).click();
  await expect(row).toHaveCount(0);
  const access = await request.get('/api/v1/folders', { headers: { Authorization: `Bearer ${token.token}` } });
  expect(access.status()).toBe(401);
});

test('token list failure does not claim that no tokens exist', async ({ page }) => {
  await page.route('**/api/v1/tokens', route => route.fulfill({ status: 400, contentType: 'application/json', body: '{"error":"list failed"}' }));
  await page.goto('/#/settings/api-tokens');
  await expect(page.locator('.settings-content').getByRole('alert')).toContainText('list failed');
  await expect(page.getByText('No API tokens.', { exact: true })).toHaveCount(0);
});

test('revoking a token removed elsewhere removes the stale row', async ({ page, request }) => {
  const created = await request.post('/api/v1/tokens', { data: { name: 'Stale token', expires_at: new Date(Date.now() + 3600000).toISOString(), folder_ids: [1] } });
  expect(created.status()).toBe(201);
  const token = await created.json();
  await page.goto('/#/settings/api-tokens');
  const row = page.locator('.settings-content').getByRole('listitem').filter({ hasText: token.slug });
  await expect(row).toBeVisible();
  expect((await request.delete('/api/v1/tokens/' + token.slug)).status()).toBe(204);
  await row.getByRole('button', { name: 'Revoke', exact: true }).click();
  await page.getByRole('alertdialog', { name: 'Revoke API token' }).getByRole('button', { name: 'Revoke', exact: true }).click();
  await expect(row).toHaveCount(0);
  await expect(page.locator('.settings-content').getByRole('alert')).toHaveCount(0);
});
