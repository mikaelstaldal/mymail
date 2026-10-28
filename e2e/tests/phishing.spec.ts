// Real seeded warnings and download bytes, with fresh assets/server provided by
// test-e2e.sh. No warning verdict is invented by a browser route mock.
// Layout coverage uses private sent messages moved to Inbox: long link values,
// visible hover/focus tooltips, both body views, expanded thread warnings and
// divider movement and window resizing, including fractional pane heights. Label
// allocation is checked against available row space; no minimum glyph count is specified.
import { test, expect } from '@playwright/test';

async function sample(request: import('@playwright/test').APIRequestContext) {
  const messages = await (await request.get('/api/v1/folders/1/messages')).json();
  const message = messages.items.find((m: { subject: string }) => m.subject === 'Phishing warning examples (safe demo)');
  expect(message).toBeDefined();
  return (await request.get(`/api/v1/messages/${message.id}`)).json();
}

test('long link warnings fit one line and body resizing stays above the thread', async ({ page, request }) => {
  // A much longer destination must not take space away from the label's share.
  const text = 'https://example.com/account/security';
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
      const geometry = await line.evaluate(el => {
        const values = [...el.querySelectorAll<HTMLElement>('.phishing-warning-url')];
        const children = [...el.children].filter(child => getComputedStyle(child).position !== 'fixed');
        const otherWidth = children.filter(child => child !== values[0] && child !== values[1])
          .reduce((sum, child) => sum + child.getBoundingClientRect().width, 0);
        const gaps = parseFloat(getComputedStyle(el).columnGap) * (children.length - 1);
        return {
          height: el.getBoundingClientRect().height,
          lineHeight: parseFloat(getComputedStyle(el).lineHeight),
          width: el.clientWidth, scrollWidth: el.scrollWidth,
          truncated: values.slice(0, 2).every(value => value.scrollWidth > value.clientWidth),
          valueWidths: values.slice(0, 2).map(value => value.getBoundingClientRect().width),
          availableWidth: el.getBoundingClientRect().width - otherWidth - gaps,
          font: getComputedStyle(el).font,
        };
      });
      expect(geometry.height).toBeLessThanOrEqual(geometry.lineHeight + 1);
      expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width + 1);
      expect(geometry.truncated).toBe(true);
      expect(geometry.valueWidths[0], JSON.stringify(geometry)).toBeGreaterThan(0);
      expect(Math.abs(geometry.valueWidths[0] - geometry.valueWidths[1])).toBeLessThanOrEqual(1);
      expect(Math.abs(geometry.valueWidths[0] + geometry.valueWidths[1] - geometry.availableWidth))
        .toBeLessThanOrEqual(1);

      await values.nth(0).hover();
      const tooltip = page.getByRole('tooltip');
      await expect(tooltip).toBeVisible();
      await expect(tooltip).toContainText(`Link text: ${text}`);
      await expect(tooltip).toContainText(`Destination: ${href}`);
      await expect(tooltip).toContainText('Host: unrelated.example');
      const box = (await tooltip.boundingBox())!;
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.y).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(page.viewportSize()!.width);
      expect(box.y + box.height).toBeLessThanOrEqual(page.viewportSize()!.height);
      // The tooltip must stay open when entered, including inside the clipped
      // thread pane, so long destinations can be selected and scrolled.
      await tooltip.hover();
      await expect(tooltip).toBeVisible();
      await tooltip.evaluate(el => { el.scrollTop = el.scrollHeight; });
      await expect(tooltip).toBeVisible();
      await page.mouse.move(0, 0);
      await expect(tooltip).toHaveCount(0);
      await line.focus();
      await expect(tooltip).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(tooltip).toHaveCount(0);
      await line.evaluate(el => (el as HTMLElement).blur());
    };

    for (const width of [1280, 800]) {
      await page.setViewportSize({ width, height: 800 });
      await checkLink(body);
      for (const view of ['HTML', 'Plain']) {
        await page.getByRole('switch', { name: 'Plain text', exact: true }).setChecked(view === 'Plain');
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
    // Force fractional available space independently of the runner's font metrics.
    // Rounding pane heights to whole pixels can otherwise leave the body < 60px.
    await page.locator('.msg-detail').evaluate(container => {
      const strip = container.querySelector<HTMLElement>('.thread-strip')!;
      const body = container.querySelector<HTMLElement>('.msg-detail-body')!;
      strip.style.height = '100px';
      const bodyHeight = body.getBoundingClientRect().height;
      (container as HTMLElement).style.height =
        `${container.getBoundingClientRect().height - bodyHeight + Math.floor(bodyHeight) + 0.609375}px`;
    });
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
    // A saved thread height must still leave body space after the window shrinks.
    await page.locator('.msg-detail').evaluate(el => { (el as HTMLElement).style.height = ''; });
    await page.setViewportSize({ width: 800, height: 680 });
    await expect.poll(async () => (await body.boundingBox())!.height).toBeGreaterThanOrEqual(60);
    const detailBox = (await page.locator('.msg-detail').boundingBox())!;
    const threadBox = (await thread.boundingBox())!;
    expect(threadBox.y + threadBox.height).toBeLessThanOrEqual(detailBox.y + detailBox.height + 1);
    await page.setViewportSize({ width: 800, height: 800 });
    await thread.locator('.thread-entry-btn:not(:disabled)').click();
    const expanded = thread.locator('.thread-expanded');
    await checkLink(expanded);
    // Reserve classic-scrollbar space even on systems with overlay scrollbars,
    // and exercise a narrower viewport and different glyph widths without
    // depending on installed faces or assuming a minimum number of visible glyphs.
    await page.setViewportSize({ width: 796, height: 800 });
    await thread.locator('.thread-list').evaluate(el => { (el as HTMLElement).style.scrollbarGutter = 'stable'; });
    for (const font of ['sans-serif', 'serif']) {
      await expanded.locator('.phishing-warnings').evaluate((el, font) => {
        (el as HTMLElement).style.fontFamily = font;
      }, font);
      await checkLink(expanded);
    }
  } finally {
    for (const id of ids) {
      await request.patch(`/api/v1/messages/${id}`, { data: { folder_id: 4 } });
      await request.delete(`/api/v1/messages/${id}`);
    }
  }
});

test('multiple deceptive links share one counted warning in both body views', async ({ page, request }) => {
  const response = await request.post('/api/v1/messages/send', {
    data: {
      to_addr: 'e2e@example.com', subject: 'Multiple deceptive links',
      body_text: 'Two links',
      body_html: '<a href="https://unrelated.example/one">example.com</a>'
        + '<a href="https://unrelated.example/two">example.org</a>',
    },
  });
  expect(response.status()).toBe(201);
  const { id } = await response.json();
  try {
    expect((await request.patch(`/api/v1/messages/${id}`, { data: { folder_id: 1 } })).status()).toBe(200);
    const detail = await (await request.get(`/api/v1/messages/${id}`)).json();
    expect(detail.warnings.filter((w: { kind: string }) => w.kind === 'deceptive_link')).toHaveLength(2);
    await page.goto(`/#/message/${id}`);
    const banner = page.getByRole('note', { name: 'Message warnings' });
    await expect(banner.locator('li')).toHaveCount(1);
    await expect(banner).toHaveText('Message contains 2 deceptive links');
    await page.getByRole('switch', { name: 'Plain text', exact: true }).check();
    await expect(banner.locator('li')).toHaveCount(1);
    await expect(banner).toContainText('Message contains 2 deceptive links');
  } finally {
    await request.patch(`/api/v1/messages/${id}`, { data: { folder_id: 4 } });
    await request.delete(`/api/v1/messages/${id}`);
  }
});

test('warnings explain the server signals and sender address stays visible', async ({ page, request }) => {
  const message = await sample(request);
  expect(message.warnings).toHaveLength(6);
  await page.goto(`/#/message/${message.id}`);
  const banner = page.getByRole('note', { name: 'Message warnings' });
  await expect(banner.locator('li')).toHaveCount(5);
  for (const detail of ['security@example.com', 'xn--exmple-4nf.com', 'help@unrelated.example', 'https://unrelated.example/', 'invoice.pdf.exe']) {
    await expect(banner).toContainText(detail);
  }
  // First-time sender sits beside the From address, not in the banner.
  await expect(banner).not.toContainText('never sent');
  await expect(page.locator('.msg-detail-header').getByRole('note', { name: 'Sender warning' })).toContainText('never sent');
  await expect(banner.locator('.phishing-warning-strong')).toHaveCount(1);
  await expect(page.locator('.msg-detail-header .sender-name')).toHaveText('security@example.com');
  await expect(page.locator('.msg-detail-header .sender-address')).toBeVisible();
  await expect(page.locator('.msg-detail-header .sender-address')).not.toContainText('xn--');
  await expect(page.locator('.sender-idn').first()).toContainText('IDN');
  const plain = page.getByRole('switch', { name: 'Plain text', exact: true });
  await plain.check();
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

// Renderer regression: the server's warning decisions are covered above; inject
// hostile display values here to exercise every affected presentation surface.
test('format controls are visible and isolated in warnings and download confirmation', async ({ page, request }) => {
  const message = await sample(request);
  const filename = 'invoice\u202Efdp.exe';
  const address = 'x@\u202Emoc.lapyap';
  message.attachments[0].filename = filename;
  message.warnings = [
    { kind: 'risky_attachment', filename, reason: 'executable_extension' },
    { kind: 'reply_to_mismatch', address },
    { kind: 'lookalike_domain', domain: 'עברית.example', punycode: 'xn--example', resembles: 'bank\u2066.example' },
    { kind: 'deceptive_link', text: 'example.com\u202E', href: 'https://evil.example/\u2067', decoded_host: 'evil.example' },
  ];
  await page.route(`**/api/v1/messages/${message.id}`, route => route.fulfill({ json: message }));
  await page.goto(`/#/message/${message.id}`);
  const banner = page.getByRole('note', { name: 'Message warnings' });
  await expect(banner.locator('bdi').filter({ hasText: 'invoice<U+202E>fdp.exe' })).toHaveCount(1);
  await expect(banner.locator('bdi').filter({ hasText: 'x@<U+202E>moc.lapyap' })).toHaveCount(1);
  await expect(banner).toContainText('on a different domain from the sender');
  await expect(banner.locator('bdi').filter({ hasText: 'עברית.example' })).toHaveCount(1);
  await expect(banner.locator('bdi').filter({ hasText: 'bank<U+2066>.example' })).toHaveCount(1);
  await expect(banner.locator('bdi').filter({ hasText: 'example.com<U+202E>' })).toHaveCount(1);
  await expect(banner.locator('bdi').filter({ hasText: 'https://evil.example/<U+2067>' })).toHaveCount(1);
  const attachment = page.getByRole('button', { name: /invoice<U\+202E>fdp.exe/ });
  await expect(attachment.locator('bdi')).toHaveText('invoice<U+202E>fdp.exe');
  await attachment.click();
  const dialog = page.getByRole('alertdialog');
  await expect(dialog.locator('bdi')).toHaveText('invoice<U+202E>fdp.exe');
  await expect(dialog).toContainText('Download only if you trust its source.');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.getByRole('button', { name: 'Forward', exact: true }).click();
  await expect(page.locator('.compose-attachments bdi')).toContainText(['invoice<U+202E>fdp.exe']);
});
