import { test, expect } from '@playwright/test';

// Traverse every rendered folder, including group boundaries, with real key events.
test('sidebar arrows move focus between folders without wrapping or opening them', async ({ page, request }) => {
  const response = await request.post('/api/v1/folders', { data: { name: 'Keyboard navigation test' } });
  expect(response.status()).toBe(201);
  const folder = await response.json();
  try {
    await page.goto('/#/inbox');
    const links = page.locator('.sidebar .folder-item a');
    await expect(links.filter({ hasText: 'Keyboard navigation test' })).toBeVisible();
    const count = await links.count();
    await links.first().focus();
    await page.keyboard.press('ArrowUp');
    await expect(links.first()).toBeFocused();
    for (let i = 1; i < count; i++) {
      await page.keyboard.press('ArrowDown');
      await expect(links.nth(i)).toBeFocused();
    }
    await page.keyboard.press('ArrowDown');
    await expect(links.last()).toBeFocused();
    await expect(page).toHaveURL(/#\/inbox$/);
    for (let i = count - 2; i >= 0; i--) {
      await page.keyboard.press('ArrowUp');
      await expect(links.nth(i)).toBeFocused();
    }
    await page.keyboard.press('ArrowUp');
    await expect(links.first()).toBeFocused();
    await page.keyboard.press('ArrowDown');
    const href = await links.nth(1).getAttribute('href');
    // Moving focus across panes does not activate the focused folder.
    const firstMessage = page.locator('.msg-row').first();
    await page.keyboard.press('ArrowRight');
    await expect(firstMessage).toBeFocused();
    await expect(page).toHaveURL(/#\/inbox$/);
    await page.keyboard.press('ArrowLeft');
    await expect(links.first()).toBeFocused();
    await page.keyboard.press('ArrowDown');
    const loaded = page.waitForResponse(response => response.url().includes('/folders/2/messages')
      && response.request().method() === 'GET');
    await page.keyboard.press('Enter');
    await expect.poll(() => page.evaluate(() => location.hash)).toBe(href);
    const sent = await (await loaded).json();
    await expect(firstMessage).toHaveAttribute('data-message-id', String(sent.items[0].id));
    await expect(firstMessage).toBeFocused();
    // Repeated Enter on the current folder also transfers focus.
    for (let i = 0; i < 2; i++) {
      await links.nth(1).focus();
      await page.keyboard.press('Enter');
      await expect(firstMessage).toBeFocused();
    }
    await links.nth(1).focus();
    await page.keyboard.press('ArrowRight');
    await expect(firstMessage).toBeFocused();
    await page.keyboard.press('ArrowLeft');
    await expect(links.nth(1)).toBeFocused();

    // The new custom folder is empty: Right must leave focus on its link.
    const custom = links.filter({ hasText: 'Keyboard navigation test' });
    await custom.focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('.folder-empty')).toBeVisible();
    await expect(custom).toBeFocused();
    await custom.focus();
    await page.keyboard.press('ArrowRight');
    await expect(custom).toBeFocused();
  } finally {
    await request.delete(`/api/v1/folders/${folder.id}`);
  }
});

// Hold the response so the old folder's rows cannot satisfy the focus request.
test('Enter waits for the activated folder and consumes failed requests', async ({ page }) => {
  await page.goto('/#/inbox');
  await expect(page.locator('.msg-row').first()).toBeVisible();
  const sentLink = page.locator('.sidebar .folder-item a[href="#/folder/sent"]');
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  let intercepted!: () => void;
  const started = new Promise<void>(resolve => { intercepted = resolve; });
  await page.route('**/folders/2/messages?*', async route => {
    intercepted();
    await held;
    await route.continue();
  });
  await sentLink.focus();
  await page.keyboard.press('Enter');
  await started;
  await expect(page.locator('.folder-status')).toHaveText('Loading…');
  await expect(sentLink).toBeFocused();
  release();
  await expect(page.locator('.msg-row').first()).toBeFocused();
  await page.unroute('**/folders/2/messages?*');

  // A click keeps focus on the link; a failed Enter request must not survive a reload.
  const inboxLink = page.locator('.sidebar .folder-item a[href="#/inbox"]');
  await page.route('**/folders/1/messages?*', route => route.fulfill({
    status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'Test loading error' }),
  }));
  await inboxLink.focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('.folder-error')).toBeVisible();
  await expect(inboxLink).toBeFocused();
  await page.unroute('**/folders/1/messages?*');
  await page.evaluate(() => window.dispatchEvent(new CustomEvent('folder-reload')));
  await expect(page.locator('.msg-row').first()).toBeVisible();
  await expect(inboxLink).toBeFocused();
  await sentLink.click();
  await expect(page.locator('.msg-row').first()).toBeVisible();
  await expect(sentLink).toBeFocused();
});
