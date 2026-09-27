const { chromium, webkit } = require('playwright');
const assert = require('node:assert/strict');
const preferences = require('../static/preferences.js');
const transcoderFields = require('../static/transcoder-fields.json');
const baseURL = process.env.NOTIFICATION_PREVIEW_URL || 'http://127.0.0.1:18198';
const headings = {
  system: ['Layout', 'Graph Settings', 'Page Indicators', 'Disk Usage', 'CPU', 'Bandwidth', 'InfiniDysk', 'Memory'],
  playing: ['Page Indicators', 'Session Appearance', 'Media Details', 'Playback Details'],
  infrastructure: ['Page Indicators', 'Node Identity', 'Health & Activity', 'Resources'],
};

(async () => {
  for (const engine of [chromium, webkit]) {
    const browser = await engine.launch();
    try {
      for (const width of [320, 390, 1280]) {
        const page = await browser.newPage({ viewport: { width, height: width === 320 ? 568 : 900 } });
        const errors = [];
        page.on('pageerror', error => errors.push(error.message));
        await page.route('**/api/**', route => {
          if (route.request().url().includes('/api/transcoder')) {
            return route.fulfill({ headers: { ETag: '"settings-1"' }, json: {
              fields: transcoderFields,
              values: Object.fromEntries(transcoderFields.map(field => [field.key, field.type === 'boolean' ? 'true' : field.type === 'select' ? field.options[0][0] : '120'])),
              restart_keys: [],
            } });
          }
          return route.fulfill({ json: [] });
        });
        await page.goto(baseURL);
        await page.locator('[data-tab="settings"]').click();
        for (const [group, expected] of Object.entries(headings)) {
          await page.locator(`[data-group="${group}"]`).click();
          assert.deepEqual(await page.locator('#settings-options h4').allTextContents(), expected);
          const keys = await page.locator('input[data-key]').evaluateAll(inputs => inputs.map(input => input.dataset.key));
          assert.deepEqual(keys.sort(), preferences.groups[group].options.map(option => option[0]).sort());
          const parent = group === 'system' ? 'cpu' : 'cards';
          await page.locator(`input[data-key="${parent}"]`).uncheck();
          for (const [key, , , dependency] of preferences.groups[group].options) {
            if (dependency === parent) assert.ok(await page.locator(`input[data-key="${key}"]`).isDisabled());
          }
          await page.locator(`input[data-key="${parent}"]`).check();
          for (const [key, , , dependency] of preferences.groups[group].options) {
            if (dependency === parent) assert.ok(await page.locator(`input[data-key="${key}"]`).isEnabled());
          }
          if (group === 'system') {
            await page.locator('input[data-key="cpu.chart"]').uncheck();
            assert.ok(await page.locator('input[data-key="cpu.scale"]').isDisabled());
            await page.locator('[data-order="disk-down"]').click();
            assert.equal(await page.locator('.settings-order-row').first().locator('span').textContent(), 'CPU');
            assert.ok(await page.locator('[data-order="disk-down"]').evaluate(element => element === document.activeElement));
            await page.getByLabel('Graph time window', { exact: true }).selectOption('3');
            await page.getByLabel('Graph height', { exact: true }).press('End');
            const stored = await page.evaluate(key => JSON.parse(localStorage.getItem(key)), preferences.storageKey);
            assert.equal(stored.chartHeight, 220);
            assert.equal(stored.chartMinutes, 3);
            assert.equal(stored.visibility['system.cpu.chart'], false);
            await page.locator('#reset-tab-settings').click();
            assert.ok(await page.locator('input[data-key="cpu.chart"]').isChecked());
            assert.equal(await page.getByLabel('Graph time window', { exact: true }).inputValue(), '5');
          }
          assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
          await page.locator('.page-shell').evaluate(element => { element.scrollTop = 0; });
          await page.screenshot({ path: `/tmp/siloscope-settings-${group}-${engine.name()}-${width}.png` });
          await page.locator('.page-shell').evaluate(element => { element.scrollTop = element.scrollHeight; });
          assert.ok(await page.evaluate(() => document.querySelector('#settings-options').getBoundingClientRect().bottom <= document.querySelector('.bottom-tabs').getBoundingClientRect().top));
          await page.screenshot({ path: `/tmp/siloscope-settings-${group}-bottom-${engine.name()}-${width}.png` });
          await page.getByRole('button', { name: 'Back to settings', exact: true }).click();
          assert.ok(await page.locator('#settings-menu').isVisible());
        }
        await page.locator('[data-group="transcoder"]').click();
        await page.locator('.transcoder-form').waitFor();
        assert.deepEqual(await page.locator('.transcoder-form h4').allTextContents(), [...new Set(transcoderFields.map(field => field.group))]);
        assert.equal(await page.locator('.transcoder-form [name]').count(), transcoderFields.length);
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
        await page.locator('.page-shell').evaluate(element => { element.scrollTop = 0; });
        await page.screenshot({ path: `/tmp/siloscope-settings-transcoder-${engine.name()}-${width}.png` });
        await page.getByRole('button', { name: 'Back to settings', exact: true }).click();
        assert.ok(await page.locator('#settings-menu').isVisible());
        assert.deepEqual(errors, []);
        await page.close();
      }
      console.log(`${engine.name()}: grouped settings, dependencies, ordering, persistence, reset, navigation and layout passed`);
    } finally { await browser.close(); }
  }
})().catch(error => { console.error(error); process.exit(1); });