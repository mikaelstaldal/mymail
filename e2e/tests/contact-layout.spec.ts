// Contact provenance is an icon beside Edit, never a third line of row text.
import { test, expect } from '@playwright/test';

test('contact provenance tooltip sits before Edit and all rows have equal height', async ({ page, request }) => {
  const ids: number[] = [];
  try {
    for (const [suffix, name] of [['named', 'Named contact'], ['unnamed', '']]) {
      const response = await request.post('/api/v1/contacts', {
        data: { address: `layout-${suffix}-${Date.now()}@example.org`, name },
      });
      expect(response.status()).toBe(201);
      ids.push((await response.json()).id);
    }
    await page.goto('/#/settings/contacts');
    await expect(page.locator('.settings-loading')).toHaveCount(0);
    const rows = page.locator('.settings-item');
    await expect(rows.first()).toBeVisible();
    const trusted = rows.filter({ has: page.getByRole('img', { name: 'Added or edited by you; sender authenticity is not verified' }) });
    await expect(trusted).toHaveCount(2);
    for (const row of await trusted.all()) {
      const icon = row.getByRole('img');
      await expect(icon.locator('title')).toHaveText('Added or edited by you; sender authenticity is not verified');
      const iconBox = await icon.boundingBox();
      const editBox = await row.getByRole('button', { name: 'Edit', exact: true }).boundingBox();
      expect(iconBox!.x + iconBox!.width).toBeLessThanOrEqual(editBox!.x);
      await expect(row.locator('.settings-item-info')).not.toContainText('Added or edited by you');
    }
    const heights = await rows.evaluateAll(nodes => nodes.map(node => node.getBoundingClientRect().height));
    expect(heights.length).toBeGreaterThan(2);
    expect(Math.max(...heights) - Math.min(...heights)).toBeLessThan(1);
    await expect(page.locator('.contact-provenance')).toHaveCount(heights.length);
  } finally {
    for (const id of ids) await request.delete(`/api/v1/contacts/${id}`);
  }
});
