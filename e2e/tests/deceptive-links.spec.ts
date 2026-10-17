// Tests the real served body and popup navigation; no destination is visited.
import { test, expect } from '@playwright/test';

test('deceptive link opens confirmation and resolves the stored destination', async ({ page, request }) => {
  const messages = await (await request.get('/api/v1/folders/1/messages')).json();
  let selected: { id: number; index: number; href: string } | undefined;
  for (const message of messages.items) {
    const detail = await (await request.get(`/api/v1/messages/${message.id}`)).json();
    const warning = detail.warnings?.find((w: { kind: string }) => w.kind === 'deceptive_link');
    if (warning) { selected = { id: message.id, index: warning.link_index, href: warning.href }; break; }
  }
  expect(selected, 'demo seed includes a deceptive link').toBeDefined();
  await page.goto(`/#/message/${selected!.id}`);
  const link = page.frameLocator('iframe').first().locator(`a[href$="#/link/${selected!.id}/${selected!.index}"]`);
  const popupPromise = page.waitForEvent('popup');
  await link.click();
  const popup = await popupPromise;
  await expect(popup.getByRole('heading', { name: 'Check this link before continuing' })).toBeVisible();
  const proceed = popup.getByRole('link', { name: 'Continue to destination' });
  await expect(proceed).toHaveAttribute('href', selected!.href);
  await expect(proceed).toHaveAttribute('rel', 'noopener noreferrer');
  await expect(proceed).toHaveAttribute('referrerpolicy', 'no-referrer');
  await popup.goto(`/#/link/${selected!.id}/999999?url=https://attacker.example`);
  await expect(popup.getByRole('alert')).toContainText('Link not found');
  await expect(popup.getByRole('link', { name: 'Continue to destination' })).toHaveCount(0);
});

test('browser demo rewrites and confirms deceptive links through its worker', async ({ page, request }) => {
  const { spawn } = await import('node:child_process');
  const { once } = await import('node:events');
  const { createServer } = await import('node:net');
  const reservation = createServer();
  reservation.listen(0, '127.0.0.1');
  await once(reservation, 'listening');
  const port = (reservation.address() as { port: number }).port;
  await new Promise<void>(resolve => reservation.close(() => resolve()));
  const origin = `http://localhost:${port}`;
  const server = spawn('../mymail', ['-demo-server', '-port', String(port)], { stdio: 'ignore' });
  try {
    await expect.poll(async () => {
      if (server.exitCode !== null) throw new Error('Demo server exited');
      try { return (await request.get(origin + '/')).status(); } catch { return 0; }
    }).toBe(200);
    await page.goto(origin + '/');
    await page.getByRole('alertdialog').getByRole('button').click();
    await expect(page.locator('.demo-badge')).toBeVisible();
    const selected = await page.evaluate(async () => {
      const messages = await (await fetch('api/v1/folders/1/messages')).json();
      for (const message of messages.items) {
        const detail = await (await fetch(`api/v1/messages/${message.id}`)).json();
        const warning = detail.warnings?.find((w: { kind: string }) => w.kind === 'deceptive_link');
        if (warning) return { id: message.id, index: warning.link_index, href: warning.href };
      }
      throw new Error('No deceptive demo link');
    });
    await page.goto(`${origin}/#/message/${selected.id}`);
    const popupPromise = page.waitForEvent('popup');
    await page.frameLocator('iframe').first().locator(`a[href$="#/link/${selected.id}/${selected.index}"]`).click();
    const popup = await popupPromise;
    await expect(popup.getByRole('link', { name: 'Continue to destination' })).toHaveAttribute('href', selected.href);
  } finally {
    if (server.exitCode === null) {
      const exited = once(server, 'exit');
      server.kill();
      await exited;
    }
  }
});
