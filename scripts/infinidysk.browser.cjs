const { chromium, webkit } = require("playwright");
const assert = require("node:assert/strict");
const baseURL = process.env.NOTIFICATION_PREVIEW_URL || "http://127.0.0.1:18198";

(async () => {
  for (const engine of [chromium, webkit]) {
    const browser = await engine.launch();
    try {
      for (const width of [320, 390, 1280]) {
        const page = await browser.newPage({ viewport: { width, height: 900 } });
        const errors = [];
        page.on("pageerror", error => errors.push(error.message));
        const now = Date.now();
        let data = {
          configured: true, available: true, files_available: true, sampled_at: now, connections: 24,
          history: Array.from({ length: 61 }, (_, index) => ({ t: now - (60 - index) * 5000, download: 20000000 + Math.sin(index) * 10000000, upload: 12000000 + Math.cos(index) * 6000000 })),
          files: [{ id: "one", fileName: "Example.Movie.2026.A.Very.Long.Release.Name.2160p.UHD.mkv", bytesFetched: 2100000000, fetch_bps: 28400000, providers: [{ host: "hidden-provider" }] }],
        };
        await page.route("**/api/**", route => route.fulfill({ json: route.request().url().includes("/api/ifd") ? data : {} }));
        await page.goto(baseURL);
        await page.locator(".ifd-panel").waitFor();
        await page.locator(".ifd-panel").scrollIntoViewIfNeeded();
        assert.equal(await page.locator("#ifd-status").textContent(), "Live");
        assert.equal(await page.locator("#ifd-connections").textContent(), "24");
        assert.equal(await page.locator(".ifd-file-speed").textContent(), "Fetching 28.4 MB/s");
        assert.equal(await page.locator(".ifd-file-bytes").textContent(), "2.1 GB fetched");
        assert.ok(!(await page.locator(".ifd-panel").textContent()).includes("hidden-provider"));
        assert.ok(await page.locator("#ifd-chart .chart-line").count() >= 2);
        assert.ok(await page.locator(".ifd-panel").evaluate(node => node.scrollWidth <= node.clientWidth));
        await page.screenshot({ path: `/tmp/siloscope-ifd-${engine.name()}-${width}.png` });
        await page.locator('[data-tab="settings"]').click();
        await page.locator('[data-group="system"]').click();
        await page.locator('[data-order="ifd-up"]').click();
        await page.locator('input[data-key="ifd.bytes"]').uncheck();
        await page.locator('input[data-key="ifd.chart"]').uncheck();
        assert.ok(await page.locator('input[data-key="ifd.scale"]').isDisabled());
        await page.locator('input[data-key="ifd.chart"]').check();
        await page.getByLabel("Graph height", { exact: true }).press("End");
        await page.getByLabel("Graph time window", { exact: true }).selectOption("3");
        await page.reload();
        await page.locator(".ifd-panel").waitFor();
        assert.ok(await page.locator(".ifd-file-bytes").isHidden());
        assert.equal(await page.locator("#ifd-chart").getAttribute("aria-label"), "InfiniDysk fetched and served throughput over the last 3 minutes");
        assert.equal(await page.locator("#ifd-chart").evaluate(node => Math.round(node.getBoundingClientRect().height)), 220);
        const order = await page.locator(".metric-grid").evaluate(node => [...node.children].map(child => child.className));
        assert.ok(order.findIndex(name => name.includes("ifd-panel")) < order.findIndex(name => name.includes("network-panel")));
        data = { ...data, available: false, files_available: false };
        await page.waitForFunction(() => document.querySelector("#ifd-status").textContent === "Unavailable");
        assert.equal(await page.locator("#ifd-fetched").textContent(), "--");
        assert.equal(await page.locator("#ifd-file-list li").count(), 0);
        data = { ...data, available: true, files_available: true, files: [] };
        await page.waitForFunction(() => document.querySelector("#ifd-files-message").textContent === "No active files");
        assert.deepEqual(errors, []);
        await page.close();
      }
      console.log(`${engine.name()}: IFD mobile/desktop, appearance, order, persistence, idle and stale states passed`);
    } finally { await browser.close(); }
  }
})().catch(error => { console.error(error); process.exit(1); });