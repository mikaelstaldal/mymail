// PoC: a cross-site page that opens MyMail at #/compose?forward=ID makes the
// SPA create a draft on mount, and the server copies every attachment BLOB of
// the source message into it. The attacker page keeps the window handle (no
// Cross-Origin-Opener-Policy) and re-triggers the copy by toggling the hash.
//
// Asserts the secure behaviour (navigation alone creates no server state), so
// it FAILS while the bug exists.
//
// Place in e2e/tests/ and run: ./build.sh && ./test-e2e.sh tests/12_forward_navigation.spec.ts
import { test, expect, type APIRequestContext } from '@playwright/test';

const BASE = 'http://localhost:8090';
const ATTACKER = 'https://evil.example/';

async function drafts(request: APIRequestContext): Promise<number> {
  return (await (await request.get('/api/v1/folders/3/messages?limit=1')).json()).total;
}

async function attachmentBytesInDrafts(request: APIRequestContext): Promise<number> {
  const list = await (await request.get('/api/v1/folders/3/messages?limit=200')).json();
  let total = 0;
  for (const item of list.items) {
    const detail = await (await request.get(`/api/v1/messages/${item.id}`)).json();
    for (const a of detail.attachments ?? []) total += a.size;
  }
  return total;
}

test('cross-site navigation to #/compose?forward=ID must not create drafts', async ({ page, request }) => {
  // Any received message with attachments; the e2e database has demo data.
  const inbox = await (await request.get('/api/v1/folders/1/messages?limit=200')).json();
  const source = inbox.items.find((m: { has_attachments: boolean }) => m.has_attachments);
  expect(source, 'need a message with attachments').toBeDefined();
  const sourceBytes = ((await (await request.get(`/api/v1/messages/${source.id}`)).json()).attachments as { size: number }[])
    .reduce((s, a) => s + a.size, 0);

  const draftsBefore = await drafts(request);
  const bytesBefore = await attachmentBytesInDrafts(request);

  // The attacker's page, on a different origin.
  await page.route(ATTACKER, route => route.fulfill({
    contentType: 'text/html',
    body: `<button id="go" onclick="window.w = window.open('${BASE}/#/compose?forward=${source.id}')">Click me</button>`,
  }));
  await page.goto(ATTACKER);
  const [popup] = await Promise.all([page.waitForEvent('popup'), page.click('#go')]);
  await popup.waitForLoadState();
  await popup.waitForTimeout(1500);

  // From the cross-origin opener, toggle the popup's hash to remount ComposeForm.
  const rounds = 5;
  for (let i = 0; i < rounds; i++) {
    await page.evaluate(u => { (window as any).w.location = u; }, `${BASE}/#/inbox`);
    await popup.waitForTimeout(500);
    await page.evaluate(u => { (window as any).w.location = u; }, `${BASE}/#/compose?forward=${source.id}`);
    await popup.waitForTimeout(1000);
  }

  const draftsAfter = await drafts(request);
  const bytesAfter = await attachmentBytesInDrafts(request);
  const report = `source message ${source.id} has ${sourceBytes} attachment bytes; ` +
    `drafts ${draftsBefore} -> ${draftsAfter}; attachment bytes held by drafts ${bytesBefore} -> ${bytesAfter}`;
  console.log(report);

  expect(draftsAfter, `navigation alone must not create drafts (${report})`).toBe(draftsBefore);
  expect(bytesAfter, `navigation alone must not copy attachments (${report})`).toBe(bytesBefore);
});

test('untouched forward survives autosave interval without a write; editing copies attachments', async ({ page, request }) => {
  const inbox = await (await request.get('/api/v1/folders/1/messages?limit=200')).json();
  const source = inbox.items.find((m: { has_attachments: boolean }) => m.has_attachments);
  const original = await (await request.get(`/api/v1/messages/${source.id}`)).json();
  await page.clock.install();
  await page.goto(`/#/compose?forward=${source.id}`);
  await expect(page.locator('.compose-loading-overlay')).toHaveCount(0);
  const writes: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && req.url().includes('/drafts')) writes.push(req.url());
  });
  await page.clock.runFor(31_000);
  expect(writes).toEqual([]);
  await expect(page.locator('.compose-attachments')).toContainText(original.attachments[0].filename);

  await page.locator('.cf-subject-input').fill('Edited forward navigation regression');
  const saved = page.waitForResponse(res => res.request().method() === 'POST' && res.url().endsWith('/drafts'));
  await page.clock.runFor(30_000);
  const { id } = await (await saved).json();
  await expect(page.locator('.compose-save-status')).toHaveText('Draft saved');
  const draft = await (await request.get(`/api/v1/messages/${id}`)).json();
  expect(draft.subject).toBe('Edited forward navigation regression');
  expect(draft.body_html).not.toBe('');
  expect(draft.attachments.map((a: { filename: string; size: number }) => [a.filename, a.size]))
    .toEqual(original.attachments.map((a: { filename: string; size: number }) => [a.filename, a.size]));
  await page.goto('/#/inbox');
  await request.delete(`/api/v1/drafts/${id}`);
});

test('removing a preview attachment creates a draft and leaves the source intact', async ({ page, request }) => {
  const inbox = await (await request.get('/api/v1/folders/1/messages?limit=200')).json();
  const source = inbox.items.find((m: { has_attachments: boolean }) => m.has_attachments);
  const original = await (await request.get(`/api/v1/messages/${source.id}`)).json();
  await page.goto(`/#/compose?forward=${source.id}`);
  await expect(page.locator('.compose-loading-overlay')).toHaveCount(0);
  const created = page.waitForResponse(res => res.request().method() === 'POST' && res.url().endsWith('/drafts'));
  const removed = page.waitForResponse(res => res.request().method() === 'DELETE' && res.url().includes('/attachments/'));
  await page.getByRole('button', { name: 'Remove attachment', exact: true }).first().click();
  const { id } = await (await created).json();
  expect((await removed).ok()).toBeTruthy();
  await expect(page.locator('.cf-attached-file')).toHaveCount(original.attachments.length - 1);
  const unchanged = await (await request.get(`/api/v1/messages/${source.id}`)).json();
  expect(unchanged.attachments).toEqual(original.attachments);
  await page.goto('/#/inbox');
  await request.delete(`/api/v1/drafts/${id}`);
});

test('untouched new and reply routes do not write on autosave or navigation away', async ({ page, request }) => {
  const inbox = await (await request.get('/api/v1/folders/1/messages?limit=1')).json();
  const writes: string[] = [];
  page.on('request', req => {
    if (['POST', 'PUT'].includes(req.method()) && req.url().includes('/drafts')) writes.push(req.url());
  });
  await page.clock.install();
  for (const hash of ['#/compose', `#/compose?reply=${inbox.items[0].id}`]) {
    await page.goto('/' + hash);
    await expect(page.locator('.compose-loading-overlay')).toHaveCount(0);
    await page.clock.runFor(31_000);
    await page.evaluate(() => { location.hash = '#/inbox'; });
    await expect(page.locator('.compose-form')).toHaveCount(0);
  }
  expect(writes).toEqual([]);
});

test('Discard waits for an in-flight first forward save and deletes its copy', async ({ page, request }) => {
  const inbox = await (await request.get('/api/v1/folders/1/messages?limit=200')).json();
  const source = inbox.items.find((m: { has_attachments: boolean }) => m.has_attachments);
  const before = await drafts(request);
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  let created!: (id: number) => void;
  const creation = new Promise<number>(resolve => { created = resolve; });
  await page.route('**/api/v1/drafts', async route => {
    const response = await route.fetch();
    created((await response.json()).id);
    await held;
    await route.fulfill({ response });
  });
  await page.clock.install();
  await page.goto(`/#/compose?forward=${source.id}`);
  await expect(page.locator('.compose-loading-overlay')).toHaveCount(0);
  await page.locator('.cf-subject-input').fill('Discard in-flight forward');
  await page.clock.runFor(31_000);
  const id = await creation;
  await page.getByRole('button', { name: 'Discard', exact: true }).click();
  release();
  await page.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.locator('.compose-form')).toHaveCount(0);
  expect((await request.get(`/api/v1/messages/${id}`)).status()).toBe(404);
  expect(await drafts(request)).toBe(before);
});

test('leaving during Send does not save or recreate the draft', async ({ page, request }) => {
  const source = (await (await request.get('/api/v1/folders/1/messages?limit=1')).json()).items[0];
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  let started!: () => void;
  const sending = new Promise<void>(resolve => { started = resolve; });
  await page.route('**/api/v1/drafts/*/send', async route => {
    started();
    await held;
    const id = route.request().url().split('/').at(-2);
    await request.delete(`/api/v1/drafts/${id}`);
    await route.fulfill({ status: 200, json: {} });
  });
  const writes: string[] = [];
  page.on('request', req => {
    if (['POST', 'PUT'].includes(req.method()) && /\/drafts(?:$|\/\d+$)/.test(req.url())) {
      writes.push(req.method());
    }
  });
  await page.goto(`/#/compose?reply=${source.id}`);
  await expect(page.locator('.compose-loading-overlay')).toHaveCount(0);
  await page.locator('.cf-subject-input').fill('Leave while sending');
  const sentResponse = page.waitForResponse(res => res.url().endsWith('/send'));
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await sending;
  await page.evaluate(() => { location.hash = '#/inbox'; });
  await expect(page.locator('.compose-form')).toHaveCount(0);
  release();
  await sentResponse;
  await expect.poll(() => writes).toEqual(['POST']);
});

test('rapid preview removals keep the third attachment and preserve the source', async ({ page, request }) => {
  await page.goto('/#/inbox');
  const sourceId = await page.evaluate(async () => {
    const data = new FormData();
    data.append('message', JSON.stringify({ subject: 'Three attachment source' }));
    for (const name of ['a.txt', 'b.txt', 'c.txt']) {
      data.append('attachments', new Blob([name], { type: 'text/plain' }), name);
    }
    const response = await fetch('/api/v1/drafts-with-attachments', { method: 'POST', body: data });
    if (!response.ok) throw new Error(await response.text());
    return (await response.json()).id;
  });
  await page.goto(`/#/compose?forward=${sourceId}`);
  await expect(page.locator('.cf-attached-file')).toHaveCount(3);
  const creation = page.waitForResponse(res => res.request().method() === 'POST' && res.url().endsWith('/drafts'));
  // Dispatch both gestures before a render can replace the preview IDs.
  await page.locator('.compose-attachments').evaluate(el => {
    const buttons = el.querySelectorAll<HTMLButtonElement>('button');
    buttons[0].click();
    buttons[1].click();
  });
  const id = (await (await creation).json()).id;
  await expect(page.locator('.cf-attached-file')).toHaveCount(1);
  await expect(page.locator('.cf-attached-file')).toContainText('c.txt');
  const source = await (await request.get(`/api/v1/messages/${sourceId}`)).json();
  expect(source.attachments).toHaveLength(3);
  await page.goto('/#/inbox');
  await request.delete(`/api/v1/drafts/${id}`);
  await request.delete(`/api/v1/drafts/${sourceId}`);
});
