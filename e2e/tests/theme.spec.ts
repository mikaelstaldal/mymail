import { test, expect, type Page } from '@playwright/test';

// Preference behavior for ../mysuite/spec/sidebar-footer.md §7.
async function openSettings(page: Page) {
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  await page.getByRole('button', { name: 'Preferences', exact: true }).click();
  await expect(page.getByLabel('Theme', { exact: true })).toBeVisible();
}

for (const scheme of ['light', 'dark'] as const) {
  test(`Auto follows the browser at startup and live: ${scheme}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await page.goto('/');
    await openSettings(page);
    const selector = page.getByLabel('Theme', { exact: true });
    await expect(selector).toHaveValue('auto');
    await expect(selector.locator('option')).toHaveText(['Auto', 'Light', 'Dark']);
    await expect(page.locator('html')).toHaveAttribute('data-theme', scheme);
    const next = scheme === 'dark' ? 'light' : 'dark';
    await page.emulateMedia({ colorScheme: next });
    await expect(page.locator('html')).toHaveAttribute('data-theme', next);
    await expect(selector).toHaveValue('auto');
    await expect(page.locator('.sidebar-footer').getByRole('button', { name: /Switch to/ })).toHaveCount(0);
  });
}

test('explicit preferences persist, override the browser, and can return to Auto', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.goto('/');
  await openSettings(page);
  await page.getByLabel('Theme', { exact: true }).selectOption('light');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await openSettings(page);
  await expect(page.getByLabel('Theme', { exact: true })).toHaveValue('light');
  await page.getByLabel('Theme', { exact: true }).selectOption('dark');
  await page.emulateMedia({ colorScheme: 'light' });
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.getByLabel('Theme', { exact: true }).selectOption('auto');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.reload();
  await openSettings(page);
  await expect(page.getByLabel('Theme', { exact: true })).toHaveValue('auto');
  await page.emulateMedia({ colorScheme: 'dark' });
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

for (const value of ['light', 'dark'] as const) {
  test(`retains an existing ${value} preference`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: value === 'dark' ? 'light' : 'dark' });
    await page.addInitScript(value => { localStorage.setItem('darkMode', value === 'dark' ? 'true' : 'false'); }, value);
    await page.goto('/');
    await openSettings(page);
    await expect(page.getByLabel('Theme', { exact: true })).toHaveValue(value);
    await expect(page.locator('html')).toHaveAttribute('data-theme', value);
  });
}

test('invalid preference falls back to Auto', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.addInitScript(() => { localStorage.setItem('theme', 'invalid'); });
  await page.goto('/');
  await openSettings(page);
  await expect(page.getByLabel('Theme', { exact: true })).toHaveValue('auto');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});
