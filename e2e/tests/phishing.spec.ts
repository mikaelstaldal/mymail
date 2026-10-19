// Real seeded warnings and download bytes, with fresh assets/server provided by
// test-e2e.sh. No warning verdict is invented by a browser route mock.
// Layout coverage uses private sent messages moved to Inbox: long link values,
// both body views, expanded thread warnings and divider movement in both directions.
import { test, expect } from '@playwright/test';

async function sample(request: import('@playwright/test').APIRequestContext) {
  const messages = await (await request.get('/api/v1/folders/1/messages')).json();
  const message = messages.items.find((m: { subject: string }) => m.subject === 'Phishing warning examples (safe demo)');
  expect(message).toBeDefined();
  return (await request.get(`/api/v1/messages/${message.id}`)).json();
}

test('long link warnings fit one line and body resizing stays above the thread', async ({ page, request }) => {
  const text = `https://example.com/${'visible-path/'.repeat(80)}`;
  const href = `https://unrelated.example/${'destination-path/'.repeat(80)}`;
  const ids: number[] = [];
  try {
    let parent = '';
    for (let i = 0; i < 2; i++) {
      const response = await request.post('/api/v1/messages/send', {
        data: {
          to_addr: 'e2e@example.com', subject: 'Long link warning layout',
          body_text: 'Message body line\n'.repeat(80),
          body_html: `<a href="${href}">${text}</a><p>${'Message body line<br>'.repeat(80)}</p>`,
          in_reply_to: parent,
        },
      });
      expect(response.status()).toBe(201);
      const message = await response.json();
      ids.push(message.id);
      const detail = await (await request.get(`/api/v1/messages/${message.id}`)).json();
      parent = detail.message_id;
      expect((await request.patch(`/api/v1/messages/${message.id}`, { data: { folder_id: 1 } })).status()).toBe(200);
    }
    await page.goto(`/#/message/${ids[1]}`);
    const body = page.locator('.msg-detail-body');
    const divider = page.locator('.thread-resize-handle');
    const thread = page.locator('.thread-strip');
    await expect(thread).toBeVisible();

    const checkLink = async (container: import('@playwright/test').Locator) => {
      const line = container.locator('.phishing-link-warning');
      await expect(line).toHaveCount(1);
      const values = line.locator('.phishing-warning-url');
      await expect(values.nth(0)).toHaveAttribute('title', text);
      await expect(values.nth(1)).toHaveAttribute('title', href);
      await expect(values.nth(2)).toHaveAttribute('title', 'unrelated.example');
      const geometry = await line.evaluate(el => {
        const values = [...el.querySelectorAll<HTMLElement>('.phishing-warning-url')];
        return {
          height: el.getBoundingClientRect().height,
          lineHeight: parseFloat(getComputedStyle(el).lineHeight),
          width: el.clientWidth, scrollWidth: el.scrollWidth,
          truncated: values.slice(0, 2).every(value => value.scrollWidth > value.clientWidth),
        };
      });
      expect(geometry.height).toBeLessThanOrEqual(geometry.lineHeight + 1);
      expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width + 1);
      expect(geometry.truncated).toBe(true);
    };

    for (const width of [1280, 800]) {
      await page.setViewportSize({ width, height: 800 });
      await checkLink(body);
      for (const view of ['HTML', 'Plain']) {
        await page.getByRole('button', { name: view, exact: true }).click();
        const before = (await body.boundingBox())!;
        const handle = (await divider.boundingBox())!;
        await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
        await page.mouse.down();
        await page.mouse.move(handle.x + handle.width / 2, handle.y - 90, { steps: 5 });
        await page.mouse.up();
        await expect.poll(async () => (await body.boundingBox())!.height).toBeLessThan(before.height - 60);
        const shrunk = (await body.boundingBox())!;
        expect((await thread.boundingBox())!.y).toBeGreaterThanOrEqual(shrunk.y + shrunk.height);
        // Even when warnings fill the pane, scrolling reaches the selected body
        // before the thread; overflowing content must never paint beneath it.
        await body.evaluate(el => { el.scrollTop = el.scrollHeight; });
        const content = (await body.locator(view === 'HTML' ? '.body-iframe' : '.body-text').boundingBox())!;
        expect(content.y).toBeLessThan(shrunk.y + shrunk.height);
        expect(content.y + content.height).toBeGreaterThan(shrunk.y);

        const moved = (await divider.boundingBox())!;
        await page.mouse.move(moved.x + moved.width / 2, moved.y + moved.height / 2);
        await page.mouse.down();
        await page.mouse.move(moved.x + moved.width / 2, moved.y + 90, { steps: 5 });
        await page.mouse.up();
        await expect.poll(async () => (await body.boundingBox())!.height).toBeGreaterThan(shrunk.height + 60);
        await body.evaluate(el => { el.scrollTop = 0; });
      }
    }
    const handle = (await divider.boundingBox())!;
    await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
    await page.mouse.down();
    await page.mouse.move(handle.x + handle.width / 2, 0);
    await page.mouse.up();
    await expect.poll(async () => (await body.boundingBox())!.height).toBeGreaterThanOrEqual(60);
    await body.evaluate(el => { el.scrollTop = el.scrollHeight; });
    const pane = (await body.boundingBox())!;
    const plain = (await body.locator('.body-text').boundingBox())!;
    expect(plain.height).toBeGreaterThanOrEqual(200);
    expect(plain.y).toBeLessThan(pane.y + pane.height);
    expect(plain.y + plain.height).toBeGreaterThan(pane.y);
    await thread.locator('.thread-entry-btn:not(:disabled)').click();
    await checkLink(thread.locator('.thread-expanded'));
  } finally {
    for (const id of ids) {
      await request.patch(`/api/v1/messages/${id}`, { data: { folder_id: 4 } });
      await request.delete(`/api/v1/messages/${id}`);
    }
  }
});

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
