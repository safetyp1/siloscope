"use strict";

const Library = (() => {
  const section = document.querySelector('[data-view="library"]');
  const form = document.getElementById("library-search");
  const query = document.getElementById("library-query");
  const list = document.getElementById("library-results");
  const status = document.getElementById("library-status");
  const more = document.getElementById("library-more");
  const back = document.getElementById("library-back");
  const seriesHeading = document.getElementById("library-series");
  const operations = document.getElementById("library-operations");
  const dialog = document.getElementById("replacement-dialog");
  const confirm = document.getElementById("replacement-confirm");
  const cancel = document.getElementById("replacement-cancel");
  const dialogStatus = document.getElementById("replacement-status");
  let source = "radarr", series = null, previousQuery = "", offset = 0;
  let initialized = false, enabled = false, loadingConfig = false, preview = null, submitting = false, previewPending = false;
  let searchController, debounce, pollTimer, pollUntil = 0, polling = false, generation = 0;

  function node(tag, className, text) {
    const element = document.createElement(tag);
    element.className = className;
    if (text !== undefined) element.textContent = text;
    return element;
  }
  async function api(endpoint, payload, signal) {
    const options = { cache: "no-store", signal: signal || AbortSignal.timeout(10000) };
    if (payload !== undefined) Object.assign(options, { method: "POST", headers: { "Content-Type": "application/json", "X-Siloscope-Settings": "1" }, body: JSON.stringify(payload) });
    const response = await fetch(endpoint, options);
    let value;
    try { value = await response.json(); }
    catch { throw new Error(`Library returned an invalid response (HTTP ${response.status}). Check the connection or proxy and try again.`); }
    if (!response.ok) throw new Error(value?.error || `Library request failed (HTTP ${response.status})`);
    return value;
  }
  async function configure() {
    if (loadingConfig) return;
    loadingConfig = true;
    try {
      const config = await api("/api/library");
      if (!Array.isArray(config.sources)) throw new Error("Library configuration unavailable");
      enabled = config.replacements_enabled === true;
      for (const input of form.querySelectorAll('[name="library-source"]')) input.disabled = !config.sources.includes(input.value);
      if (!config.sources.includes(source)) source = config.sources[0] || "radarr";
      form.querySelector(`[value="${source}"]`).checked = true;
      query.disabled = config.sources.length === 0;
      status.textContent = config.sources.length ? "" : "No library integrations configured";
      initialized = true;
      if (!query.disabled && (series || query.value.trim().length >= 2)) void search();
    } catch (error) { status.textContent = error.message; }
    finally { loadingConfig = false; }
  }
  function resetSeries() {
    series = null;
    back.hidden = true;
    seriesHeading.textContent = "";
  }
  async function search(append = false) {
    searchController?.abort();
    searchController = new AbortController();
    const revision = ++generation;
    const searchSource = source;
    more.hidden = true;
    if (!append) { offset = 0; list.replaceChildren(); }
    if (!initialized || query.disabled) return;
    if (!series && query.value.trim().length < 2) { status.textContent = ""; return; }
    status.textContent = "Searching...";
    const params = new URLSearchParams({ source, q: query.value.trim(), offset: String(offset) });
    if (series) params.set("series_id", String(series.id));
    try {
      const result = await api(`/api/library/items?${params}`, undefined, AbortSignal.any([searchController.signal, AbortSignal.timeout(40000)]));
      if (revision !== generation) return;
      if (!Array.isArray(result.items)) throw new Error("Invalid library response");
      for (const item of result.items) {
        const row = node("li", "library-row");
        const copy = node("div", "library-copy");
        copy.append(node("strong", "", item.kind === "episode" ? `S${item.season} E${item.episode} - ${item.title}` : item.title));
        copy.append(node("span", "metric-detail", item.kind === "series" ? `${item.year || ""}` : [item.series_title, item.year, item.has_file ? "File available" : "No file"].filter(Boolean).join(" · ")));
        const action = node("button", "settings-command", item.kind === "series" ? "Episodes" : "Replace");
        action.type = "button";
        action.disabled = item.kind !== "series" && (!enabled || !item.has_file);
        if (!enabled && item.kind !== "series") action.title = "Replacements disabled";
        action.addEventListener("click", () => {
          if (item.kind === "series") {
            previousQuery = query.value;
            series = item;
            query.value = result.episode_query || "";
            back.hidden = false;
            seriesHeading.textContent = item.title;
            void search();
          } else void showPreview(searchSource, item, action);
        });
        row.append(copy, action);
        list.append(row);
      }
      offset = result.next_offset;
      more.hidden = !result.has_more;
      status.textContent = list.childElementCount ? "" : "No matching items";
    } catch (error) { if (revision === generation && !searchController.signal.aborted) status.textContent = error.message; }
  }
  async function showPreview(selectedSource, item, button) {
    if (previewPending || dialog.open) return;
    previewPending = true;
    button.disabled = true;
    try {
      preview = await api("/api/library/preview", { source: selectedSource, item_id: item.id });
      if (!preview.token || !preview.target) throw new Error("File preview unavailable");
      if (section.hidden) return;
      const target = preview.target;
      document.getElementById("replacement-title").textContent = target.title;
      document.getElementById("replacement-file").textContent = [target.filename, target.quality, Number.isFinite(target.size) ? `${(target.size / 1073741824).toFixed(2)} GiB` : ""].filter(Boolean).join(" · ");
      document.getElementById("replacement-release").textContent = `Blocklist: ${target.release_title}`;
      dialogStatus.textContent = "";
      confirm.disabled = false;
      dialog.showModal();
      document.getElementById("replacement-heading").focus();
    } catch (error) { status.textContent = error.message; }
    finally { button.disabled = false; previewPending = false; }
  }
  cancel.addEventListener("click", () => dialog.close());
  confirm.addEventListener("click", async () => {
    if (!preview || submitting) return;
    submitting = true;
    confirm.disabled = true;
    cancel.disabled = true;
    dialogStatus.textContent = "Submitting replacement...";
    try {
      await api("/api/library/replace", { token: preview.token });
      dialog.close();
      pollUntil = Date.now() + 120000;
      void refreshOperations();
      void search();
    } catch (error) {
      dialogStatus.textContent = `${error.message}. Check recent operations before trying again.`;
      confirm.disabled = false;
      pollUntil = Date.now() + 120000;
      void refreshOperations();
    } finally { submitting = false; cancel.disabled = false; }
  });
  dialog.addEventListener("cancel", event => { if (submitting) event.preventDefault(); });
  async function refreshOperations() {
    if (polling || section.hidden || document.visibilityState !== "visible") return;
    polling = true;
    clearTimeout(pollTimer);
    try {
      let jobs = await api("/api/library/operations");
      if (!Array.isArray(jobs)) throw new Error("Operation history unavailable");
      let checked = 0;
      for (let index = 0; index < jobs.length && checked < 5; index++) {
        if (jobs[index].state === "search_queued") {
          checked++;
          try { jobs[index] = await api(`/api/library/operations/${encodeURIComponent(jobs[index].id)}`); } catch {}
        }
      }
      operations.replaceChildren();
      for (const job of jobs) {
        const row = node("li", "library-row");
        const copy = node("div", "library-copy");
        copy.append(node("strong", "", job.target.title), node("span", "metric-detail", job.message));
        row.append(copy);
        if (job.state === "search_failed" && enabled) {
          const retry = node("button", "settings-command", "Retry search");
          retry.type = "button";
          retry.addEventListener("click", async () => {
            retry.disabled = true;
            try { await api("/api/library/retry-search", { id: job.id }); pollUntil = Date.now() + 120000; void refreshOperations(); }
            catch (error) { status.textContent = error.message; }
            finally { retry.disabled = false; }
          });
          row.append(retry);
        }
        operations.append(row);
      }
      document.getElementById("library-operation-status").textContent = jobs.length ? "" : "No replacement operations";
      if (Date.now() < pollUntil && jobs.some(job => ["preparing", "deleting", "deleted", "blocklisting", "searching", "search_queued"].includes(job.state))) pollTimer = setTimeout(refreshOperations, 5000);
    } catch { document.getElementById("library-operation-status").textContent = "Operation history unavailable"; }
    finally { polling = false; }
  }
  form.addEventListener("submit", event => { event.preventDefault(); clearTimeout(debounce); void search(); });
  query.addEventListener("input", () => { clearTimeout(debounce); searchController?.abort(); generation++; debounce = setTimeout(() => search(), 300); });
  form.addEventListener("change", event => {
    if (event.target.name !== "library-source") return;
    source = event.target.value;
    resetSeries();
    void search();
  });
  back.addEventListener("click", () => { resetSeries(); query.value = previousQuery; void search(); });
  more.addEventListener("click", () => search(true));
  document.getElementById("library-refresh").addEventListener("click", () => { pollUntil = Date.now() + 120000; void refreshOperations(); void configure(); });
  function onView() {
    clearTimeout(pollTimer);
    if (section.hidden || document.visibilityState !== "visible") { searchController?.abort(); return; }
    if (!initialized) void configure();
    pollUntil = Date.now() + 120000;
    void refreshOperations();
  }
  window.addEventListener("monitor-view-change", onView);
  document.addEventListener("visibilitychange", onView);
  onView();
  return { refreshOperations };
})();