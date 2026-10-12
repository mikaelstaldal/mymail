// Exercises the dedicated consent page, requested defaults and close behavior.
import { test, expect } from '@playwright/test';

const form = { client_id: 'mymail-token', name: 'E2E device', scope: 'folder:1', lifetime: '1m' };
const grantType = 'urn:ietf:params:oauth:grant-type:device_code';

test('dedicated page prefills the request and attempts to close after approve or deny', async ({ page, request }) => {
  await page.addInitScript(() => {
    const state = window as Window & { closeAttempts: number };
    state.closeAttempts = 0;
    window.close = () => { state.closeAttempts++; };
  });
  const authorization = await request.post('/api/v1/oauth/device_authorization', { form });
  expect(authorization.status()).toBe(200);
  const grant = await authorization.json();
  expect(new URL(grant.verification_uri).pathname).toBe('/device-approval.html');
  await page.goto(grant.verification_uri_complete);
  await expect(page.getByRole('heading', { name: 'MyMail — device login' })).toBeVisible();
  await expect(page.locator('.sidebar, .app-layout, .settings-dialog, .toolbar')).toHaveCount(0);
  await expect(page.getByText('E2E device', { exact: true })).toBeVisible();
  await expect(page.getByText('Requester (IP | User-Agent)', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Granted lifetime')).toHaveValue('1m');
  const selected = page.locator('.device-approval input[type="checkbox"]').first();
  await expect(selected).toBeChecked();
  await expect(page.locator('.device-approval input[type="checkbox"]:checked')).toHaveCount(1);
  // Loading a link only inspects the request; consent still needs an explicit decision.
  expect((await request.get('/api/v1/oauth/device/' + grant.user_code)).status()).toBe(200);
  const approve = page.getByRole('button', { name: 'Approve', exact: true });
  await selected.uncheck();
  await expect(approve).toBeDisabled();
  await selected.check();
  await approve.click();
  await expect(page.getByRole('status')).toContainText('Approved.');
  await expect(page.getByText('You can close this tab.')).toBeVisible();
  expect(await page.evaluate(() => (window as Window & { closeAttempts: number }).closeAttempts)).toBe(1);
  const issued = await request.post('/api/v1/oauth/token', { form: { client_id: form.client_id, device_code: grant.device_code, grant_type: grantType } });
  expect(issued.status()).toBe(200);
  const token = await issued.json();
  expect(token.scope).toBe(form.scope);
  expect(token.expires_in).toBeGreaterThan(0);
  expect(token.expires_in).toBeLessThanOrEqual(60);
  const another = await request.post('/api/v1/oauth/device_authorization', { form: { client_id: form.client_id, name: 'Denied device' } });
  const denied = await another.json();
  await page.goto(denied.verification_uri);
  await page.getByLabel('User code').fill('  ' + denied.user_code.toLowerCase().replace('-', '') + '  ');
  await page.getByRole('button', { name: 'Review request' }).click();
  await expect(page.getByText('Denied device', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Approve', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: 'Deny', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Denied.');
  expect(await page.evaluate(() => (window as Window & { closeAttempts: number }).closeAttempts)).toBe(1);
  const result = await request.post('/api/v1/oauth/token', { form: { client_id: form.client_id, device_code: denied.device_code, grant_type: grantType } });
  expect((await result.json()).error).toBe('access_denied');
});

for (const approve of [true, false]) {
  test(`script-opened consent page closes after ${approve ? 'approval' : 'denial'}`, async ({ page, request }) => {
    const authorization = await request.post('/api/v1/oauth/device_authorization', { form });
    const grant = await authorization.json();
    await page.goto('/device-approval.html');
    const popupPromise = page.waitForEvent('popup');
    await page.evaluate(url => window.open(url, '_blank'), grant.verification_uri_complete);
    const popup = await popupPromise;
    await expect(popup.getByText('E2E device', { exact: true })).toBeVisible();
    await Promise.all([
      popup.waitForEvent('close'),
      popup.getByRole('button', { name: approve ? 'Approve' : 'Deny', exact: true }).click(),
    ]);
    const result = await request.post('/api/v1/oauth/token', { form: { client_id: form.client_id, device_code: grant.device_code, grant_type: grantType } });
    expect(result.status()).toBe(approve ? 200 : 400);
    if (!approve) expect((await result.json()).error).toBe('access_denied');
  });
}

test('approval retains prefilled values under a deployment path prefix', async ({ page, request }) => {
  const authorization = await request.post('/api/v1/oauth/device_authorization', { form });
  const grant = await authorization.json();
  await page.addInitScript(() => { window.close = () => {}; });
  await page.route('**/nested/**', async route => {
    const target = new URL(route.request().url());
    target.pathname = target.pathname.replace(/^\/nested/, '');
    await route.fulfill({ response: await route.fetch({ url: target.toString() }) });
  });
  const url = new URL(grant.verification_uri_complete);
  url.pathname = '/nested' + url.pathname;
  await page.goto(url.toString());
  await expect(page.getByText('E2E device', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Granted lifetime')).toHaveValue('1m');
  await expect(page.locator('.device-approval input[type="checkbox"]').first()).toBeChecked();
  await page.getByRole('button', { name: 'Deny', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Denied.');
  await request.post('/api/v1/oauth/token', { form: { client_id: form.client_id, device_code: grant.device_code, grant_type: grantType } });
});

// Prefilling cannot grant resources before their visible list loads or on a failed decision.
test('approval waits for resources and does not close after a failed decision', async ({ page, request }) => {
  const authorization = await request.post('/api/v1/oauth/device_authorization', { form });
  const grant = await authorization.json();
  await page.addInitScript(() => {
    const state = window as Window & { closeAttempts: number };
    state.closeAttempts = 0;
    window.close = () => { state.closeAttempts++; };
  });
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  await page.route('**/api/v1/folders', async route => { await gate; await route.continue(); });
  await page.goto(grant.verification_uri_complete);
  await expect(page.getByText('E2E device', { exact: true })).toBeVisible();
  const approve = page.getByRole('button', { name: 'Approve', exact: true });
  await expect(approve).toBeDisabled();
  await expect(page.locator('.device-approval input[type="checkbox"]')).toHaveCount(0);
  release();
  await expect(approve).toBeEnabled();
  await page.route('**/api/v1/oauth/device/*', async route => {
    if (route.request().method() === 'POST') await route.fulfill({ status: 400, contentType: 'application/json', body: '{"error":"decision failed"}' });
    else await route.continue();
  });
  await approve.click();
  await expect(page.getByRole('alert')).toContainText('decision failed');
  expect(await page.evaluate(() => (window as Window & { closeAttempts: number }).closeAttempts)).toBe(0);
  expect((await request.get('/api/v1/oauth/device/' + grant.user_code)).status()).toBe(200);
  await page.unroute('**/api/v1/oauth/device/*');
  await page.getByRole('button', { name: 'Deny', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Denied.');
  await request.post('/api/v1/oauth/token', { form: { client_id: form.client_id, device_code: grant.device_code, grant_type: grantType } });
});
