// Real contact editor/API: opening and cancelling must not confer trust. The
// shortcut remains available for collected contacts and disappears after Save.
import { test, expect } from '@playwright/test';

for (const collected of [false, true]) {
  test(`sender shortcut hides after saving a ${collected ? 'collected' : 'new'} contact`, async ({ page, request }) => {
    // Private fixture: changing contact trust must not race the shared phishing
    // sample's warning assertions in another worker. The suite stubs sendmail.
    const address = `shortcut-${Date.now()}@example.org`;
    const identityResponse = await request.post('/api/v1/identities', {
      data: { name: 'Sender name', address: collected ? address.toUpperCase() : address },
    });
    expect(identityResponse.status()).toBe(201);
    const identity = await identityResponse.json();
    const sent = await request.post('/api/v1/messages/send', {
      data: { identity_id: identity.id, to_addr: 'e2e@example.com', subject: 'Sender contact shortcut', body_text: 'Test' },
    });
    expect(sent.status()).toBe(201);
    const message = await sent.json();
    const messageIds = [message.id];
    expect((await request.patch(`/api/v1/messages/${message.id}`, { data: { folder_id: 1 } })).status()).toBe(200);
    let createdId: number | undefined;
    try {
      if (collected) {
        const outgoing = await request.post('/api/v1/messages/send', {
          data: { to_addr: address, subject: 'Collect recipient', body_text: 'Test' },
        });
        expect(outgoing.status()).toBe(201);
        messageIds.push((await outgoing.json()).id);
      }
      let contacts = await (await request.get('/api/v1/contacts?limit=100')).json();
      const initial = contacts.items.find((c: { address: string }) => c.address === address);
      if (collected) {
        expect(initial.explicit).toBe(false);
        createdId = initial.id;
      } else {
        expect(initial).toBeUndefined();
      }

      await page.goto(`/#/message/${message.id}`);
      await page.getByRole('button', { name: 'Add sender to contacts', exact: true }).click();
      await expect(page.getByLabel('Email Address')).toHaveValue(address);
      const name = await page.getByLabel('Name (optional)').inputValue();
      await page.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(page).toHaveURL(/#\/settings\/contacts$/);
      contacts = await (await request.get('/api/v1/contacts?limit=100')).json();
      expect(contacts.items.find((c: { address: string }) => c.address === address)).toEqual(initial);

      await page.goto(`/#/message/${message.id}`);
      await page.getByRole('button', { name: 'Add sender to contacts', exact: true }).click();
      await expect(page.getByLabel('Name (optional)')).toHaveValue(name);
      await page.getByLabel('Name (optional)').fill('Chosen sender name');
      await page.getByRole('button', { name: 'Save', exact: true }).click();
      await expect(page.getByLabel('Name (optional)')).toHaveCount(0);
      await expect(page).toHaveURL(/#\/settings\/contacts$/);
      contacts = await (await request.get('/api/v1/contacts?limit=100')).json();
      const saved = contacts.items.find((c: { address: string }) => c.address === address);
      expect(saved.name).toBe('Chosen sender name');
      expect(saved.explicit).toBe(true);
      createdId = saved.id;
      const refreshed = await (await request.get(`/api/v1/messages/${message.id}`)).json();
      expect(refreshed.warnings.some((w: { kind: string }) => w.kind === 'first_time_sender')).toBe(false);

      const lookup = page.waitForResponse(response => {
        const url = new URL(response.url());
        return url.pathname === '/api/v1/contacts' && url.searchParams.get('q') === address;
      });
      await page.goto(`/#/message/${message.id}`);
      await (await lookup).finished();
      await expect(page.locator('.msg-detail-header .sender-address')).toBeVisible();
      await expect(page.getByRole('button', { name: 'Add sender to contacts', exact: true })).toHaveCount(0);

      // Existing links still open the explicit contact for editing in Settings.
      await page.goto(`/#/settings/contacts?sender=${message.id}`);
      await expect(page.getByLabel('Name (optional)')).toHaveValue('Chosen sender name');
      await page.getByRole('button', { name: 'Save', exact: true }).click();
      await expect(page.getByLabel('Name (optional)')).toHaveCount(0);
      await expect(page).toHaveURL(/#\/settings\/contacts$/);
      expect((await request.delete(`/api/v1/contacts/${createdId}`)).status()).toBe(204);
      createdId = undefined;
      await page.goto(`/#/message/${message.id}`);
      await expect(page.getByRole('button', { name: 'Add sender to contacts', exact: true })).toBeVisible();
    } finally {
      if (createdId !== undefined) await request.delete(`/api/v1/contacts/${createdId}`);
      for (const id of messageIds) {
        await request.patch(`/api/v1/messages/${id}`, { data: { folder_id: 4 } });
        await request.delete(`/api/v1/messages/${id}`);
      }
      await request.delete(`/api/v1/identities/${identity.id}`);
    }
  });
}
