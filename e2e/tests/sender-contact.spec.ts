// Real contact editor/API: opening and cancelling must not confer trust.
import { test, expect } from '@playwright/test';

test('sender shortcut creates a contact only on Save and can then edit it', async ({ page, request }) => {
  // Private fixture: changing contact trust must not race the shared phishing
  // sample's warning assertions in another worker. The suite stubs sendmail.
  const address = `shortcut-${Date.now()}@example.org`;
  const identityResponse = await request.post('/api/v1/identities', {
    data: { name: 'Sender name', address },
  });
  expect(identityResponse.status()).toBe(201);
  const identity = await identityResponse.json();
  const sent = await request.post('/api/v1/messages/send', {
    data: { identity_id: identity.id, to_addr: 'e2e@example.com', subject: 'Sender contact shortcut', body_text: 'Test' },
  });
  expect(sent.status()).toBe(201);
  const message = await sent.json();
  expect((await request.patch(`/api/v1/messages/${message.id}`, { data: { folder_id: 1 } })).status()).toBe(200);
  let createdId: number | undefined;
  try {
    await page.goto(`/#/message/${message.id}`);
    await page.getByRole('button', { name: 'Add sender to contacts', exact: true }).click();
    await expect(page.getByLabel('Email Address')).toHaveValue(address);
    const name = await page.getByLabel('Name (optional)').inputValue();
    let contacts = await (await request.get('/api/v1/contacts?limit=100')).json();
    expect(contacts.items.find((c: { address: string }) => c.address === address)).toBeUndefined();
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(page).toHaveURL(/#\/settings\/contacts$/);
    contacts = await (await request.get('/api/v1/contacts?limit=100')).json();
    expect(contacts.items.find((c: { address: string }) => c.address === address)).toBeUndefined();

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

    // A second visit edits the now-explicit row, avoiding duplicate-create 409.
    await page.goto(`/#/message/${message.id}`);
    await page.getByRole('button', { name: 'Add sender to contacts', exact: true }).click();
    await expect(page.getByLabel('Name (optional)')).toHaveValue('Chosen sender name');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(page.getByLabel('Name (optional)')).toHaveCount(0);
    await expect(page).toHaveURL(/#\/settings\/contacts$/);
  } finally {
    if (createdId !== undefined) await request.delete(`/api/v1/contacts/${createdId}`);
    await request.patch(`/api/v1/messages/${message.id}`, { data: { folder_id: 4 } });
    await request.delete(`/api/v1/messages/${message.id}`);
    await request.delete(`/api/v1/identities/${identity.id}`);
  }
});
