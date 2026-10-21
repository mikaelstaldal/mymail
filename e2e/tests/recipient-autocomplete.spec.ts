// Real compose events guard against sender-controlled suggestions replacing typed recipients.
import { test, expect } from '@playwright/test';

test('recipient suggestions require deliberate selection and pills expose addresses', async ({ page }) => {
  await page.route('**/api/v1/contacts?*', route => route.fulfill({ json: {
    total: 1, items: [{ id: 999, name: 'bob@corp.com', address: 'thief@evil.example', explicit: false }],
  } }));
  await page.goto('/#/compose');
  const input = page.locator('.cf-tags-input input').first();
  const pills = page.locator('.cf-tags-input').first().locator('.cf-addr-tag');
  for (const key of ['Enter', ',']) {
    await input.fill('bob@corp.com');
    await expect(page.locator('.cf-ac-item')).toBeVisible();
    await expect(page.locator('.cf-ac-item.selected')).toHaveCount(0);
    await input.press(key);
    await expect(pills.last()).toHaveText('bob@corp.com');
  }
  await input.fill('bob');
  await expect(page.locator('.cf-ac-item')).toBeVisible();
  await input.press('ArrowDown');
  await input.press('Enter');
  await expect(pills.last()).toHaveText('"bob@corp.com" <thief@evil.example>');
  await input.fill('bob');
  await expect(page.locator('.cf-ac-item')).toBeVisible();
  await input.press('ArrowDown');
  await input.fill('alice@corp.com');
  await input.press('Enter');
  await expect(pills.last()).toHaveText('alice@corp.com');
});
