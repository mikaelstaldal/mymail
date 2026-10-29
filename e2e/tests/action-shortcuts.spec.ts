import { test, expect } from '@playwright/test';

// Check component wiring and letter underlines; unit tests cover dispatch guards.
test('letter shortcuts activate message buttons and compose', async ({ page, request }) => {
  const inbox = await (await request.get('/api/v1/folders/1/messages')).json();
  const id = inbox.items[0].id;
  const open = async () => {
    await page.goto(`/#/message/${id}`);
    await expect(page.locator('button[data-shortcut="r"]')).toBeVisible();
    await page.getByRole('button', { name: 'back to list', exact: true }).focus();
  };
  await open();
  for (const key of ['c', 'r', 'a', 'f', 'd', 's', 'm']) {
    await expect(page.locator(`button[data-shortcut="${key}"] u`)).toHaveText(new RegExp(key, 'i'));
  }
  const search = page.locator('.search-bar input');
  await search.focus();
  await page.keyboard.type('cra fdsm');
  await expect(search).toHaveValue('cra fdsm');
  await expect(page).toHaveURL(new RegExp(`#/message/${id}$`));
  for (const [key, query] of [['r', 'reply'], ['a', 'replyall'], ['f', 'forward']]) {
    await open();
    await page.keyboard.press(key);
    await expect(page).toHaveURL(new RegExp(`#/compose\\?${query}=${id}$`));
  }
  await open();
  await page.keyboard.press('s');
  await expect(page.locator('.snooze-panel')).toBeVisible();
  await page.keyboard.press('s');
  await expect(page.locator('.snooze-panel')).toHaveCount(0);
  await page.keyboard.press('d');
  await expect(page.getByRole('alertdialog')).toBeVisible();
  await page.keyboard.press('c');
  await expect(page).toHaveURL(new RegExp(`#/message/${id}$`));
  await page.keyboard.press('Escape');
  await page.keyboard.press('c');
  await expect(page).toHaveURL(/#\/compose$/);
});
