import { test, expect } from '@playwright/test';

// Real form actions and confirmations: Escape must act once, on the current layer.
for (const mode of ['compose', 'reply', 'forward']) {
  test(`Escape discards ${mode} and cancels its discard confirmation`, async ({ page, request }) => {
    await page.clock.install();
    const inbox = await (await request.get('/api/v1/folders/1/messages')).json();
    const hash = mode === 'compose' ? '/#/compose' : `/#/compose?${mode}=${inbox.items[0].id}`;
    await page.goto(hash);
    await expect(page.locator('.compose-loading-overlay')).toHaveCount(0);
    const subject = page.locator('.cf-subject-input');
    await subject.fill(`Escape test ${mode}`);
    await page.clock.runFor(31_000);
    await expect(page.locator('.compose-save-status')).toHaveText('Draft saved');
    await page.keyboard.press('Escape');
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toContainText('Discard draft');
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(subject).toHaveValue(`Escape test ${mode}`);
    // The next Escape can open the question again; accepting it uses the real delete.
    await page.keyboard.press('Escape');
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Delete', exact: true }).click();
    await expect(page).toHaveURL(/#\/inbox$/);

    await page.goto(hash);
    await expect(page.locator('.compose-loading-overlay')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Discard', exact: true })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page).toHaveURL(/#\/inbox$/);
  });
}

test('Escape cancels send later before discarding compose', async ({ page }) => {
  await page.goto('/#/compose');
  await page.getByRole('button', { name: 'Send later', exact: true }).click();
  await expect(page.locator('.compose-send-later')).toBeVisible();
  await page.locator('.compose-send-later input').focus();
  await page.keyboard.press('Escape');
  await expect(page.locator('.compose-send-later')).toHaveCount(0);
  await expect(page).toHaveURL(/#\/compose$/);
});

test('Escape cancels a settings form from its Cancel button', async ({ page }) => {
  await page.goto('/#/settings/folders');
  await page.getByRole('button', { name: '+ New Folder', exact: true }).click();
  const cancel = page.getByRole('button', { name: 'Cancel', exact: true });
  await cancel.focus();
  await page.keyboard.press('Escape');
  await expect(cancel).toHaveCount(0);
});

test('Escape cancels a message delete confirmation', async ({ page, request }) => {
  const inbox = await (await request.get('/api/v1/folders/1/messages')).json();
  const id = inbox.items[0].id;
  await page.goto(`/#/message/${id}`);
  await page.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.getByRole('alertdialog')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('alertdialog')).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`#/message/${id}$`));
  expect((await (await request.get(`/api/v1/messages/${id}`)).json()).folder_id).toBe(1);
});
