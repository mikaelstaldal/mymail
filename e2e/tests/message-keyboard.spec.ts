import { test, expect } from '@playwright/test';

// Exercise real Tab traversal and keyboard activation against seeded messages.
test('message rows support Tab, Space selection and Enter opening', async ({ page }) => {
  await page.goto('/#/inbox');
  const rows = page.locator('.msg-row');
  const first = rows.nth(0);
  const second = rows.nth(1);
  const checkbox = first.getByRole('checkbox');
  await expect(first).toBeVisible();

  await page.getByRole('checkbox', { name: 'Select all messages' }).focus();
  await page.keyboard.press('Tab');
  await expect(first).toBeFocused();
  await expect(first).toHaveCSS('outline-style', 'solid');
  await expect(checkbox).not.toBeChecked();
  await page.keyboard.press('Space');
  await expect(checkbox).toBeChecked();
  await expect(first).toBeFocused();
  await expect(page).toHaveURL(/#\/inbox$/);
  await page.keyboard.press('Space');
  await expect(checkbox).not.toBeChecked();

  await page.keyboard.press('Tab');
  await expect(second).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(first).toBeFocused();

  await page.keyboard.press('ArrowUp');
  await expect(first).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(second).toBeFocused();
  await expect(second.getByRole('checkbox')).not.toBeChecked();
  await page.keyboard.press('ArrowUp');
  await expect(first).toBeFocused();
  await rows.last().focus();
  await page.keyboard.press('ArrowDown');
  await expect(rows.last()).toBeFocused();

  // Pointer selection still works and does not open the message.
  await checkbox.click();
  await expect(checkbox).toBeChecked();
  await expect(page).toHaveURL(/#\/inbox$/);
  await first.focus();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/#\/message\/\d+$/);
  await expect(page.getByRole('button', { name: 'back to list', exact: true })).toBeVisible();
  const search = page.locator('.search-bar input');
  await search.fill('abc');
  await page.keyboard.press('Backspace');
  await expect(search).toHaveValue('ab');
  await expect(page).toHaveURL(/#\/message\/\d+$/);
  await page.getByRole('button', { name: 'back to list', exact: true }).focus();
  await page.keyboard.press('Backspace');
  await expect(page).toHaveURL(/#\/inbox$/);
  await expect(first).toBeFocused();
});
