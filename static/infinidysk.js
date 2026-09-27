"use strict";

const InfiniDysk = (() => {
  function bytes(value, rate = false) {
    if (value === null || value === undefined || !Number.isFinite(value) || value < 0) return "--";
    const units = ["B", "KB", "MB", "GB", "TB"];
    let index = 0;
    while (value >= 1000 && index < units.length - 1) { value /= 1000; index++; }
    return `${value.toFixed(index ? 1 : 0)} ${units[index]}${rate ? "/s" : ""}`;
  }
  return { bytes };
})();

if (typeof module !== "undefined") module.exports = InfiniDysk;

if (typeof document !== "undefined") (() => {
  const panel = document.querySelector(".ifd-panel");
  const element = id => document.getElementById(`ifd-${id}`);
  const chart = element("chart");
  let snapshot = null;
  let minutes = 5;
  let pending = false;
  let selectedX = null;
  let points = [];
  let start = 0;
  let now = 0;
  let maximum = 1;
  const colors = [["download", "#79b8ed"], ["upload", "#e6bd72"]];

  function metric(id, value) {
    const target = element(id);
    const formatted = InfiniDysk.bytes(value, true).split(" ");
    target.textContent = formatted[0];
    if (formatted[1]) {
      const unit = document.createElement("small");
      unit.textContent = ` ${formatted[1]}`;
      target.append(unit);
    }
  }

  function readout(point) {
    metric("fetched", point?.download);
    metric("served", point?.upload);
  }

  function scrub() {
    chart.querySelectorAll(".chart-crosshair, .chart-scrub-point").forEach(node => node.remove());
    if (selectedX === null || !points.length) {
      readout(snapshot?.available ? snapshot.history?.at(-1) : null);
      element("chart-time").textContent = "Now";
      return;
    }
    const bounds = chart.getBoundingClientRect();
    const ratio = Math.max(0, Math.min(1, (selectedX - bounds.left) / bounds.width));
    const timestamp = start + ratio * (now - start);
    const point = points.reduce((nearest, candidate) => Math.abs(candidate.t - timestamp) < Math.abs(nearest.t - timestamp) ? candidate : nearest);
    const position = (point.t - start) / (now - start) * 600;
    chart.append(svgElement("line", { x1: position, x2: position, y1: 0, y2: 170, class: "chart-crosshair", stroke: "#b8b8b8" }));
    for (const [key, color] of colors) {
      if (point[key] != null) chart.append(svgElement("ellipse", { cx: position, cy: 170 - point[key] / maximum * 170, rx: circularMarkerRadiusX(chart, 6), ry: 6, fill: color, class: "chart-scrub-point" }));
    }
    readout(point);
    element("chart-time").textContent = formatSampleTime(point.t);
  }

  function renderChart() {
    now = Date.now();
    const history = snapshot?.history || [];
    const duration = Math.max(5000, Math.min(minutes * 60_000, now - (history[0]?.t || now)));
    start = now - duration;
    points = history.filter(point => point.t >= start && point.t <= now);
    maximum = bandwidthCeiling(points);
    chart.replaceChildren();
    chart.setAttribute("aria-label", `InfiniDysk fetched and served throughput over the last ${minutes} minutes`);
    for (const percent of [0, 25, 50, 75, 100]) {
      const position = 170 - percent / 100 * 170;
      chart.append(svgElement("line", { x1: 0, x2: 600, y1: position, y2: position, class: "chart-grid" }));
    }
    for (const [key, color] of colors) {
      const paths = seriesPaths(points, now, start, maximum, key);
      appendChartFill(chart, paths, color, key);
      for (const segment of paths) chart.append(svgElement("path", { d: smoothPath(segment), class: "chart-line", stroke: color }));
    }
    chart.append(svgElement("rect", { x: 0, y: 0, width: 600, height: 170, class: "chart-hit-area" }));
    element("scale-max").textContent = InfiniDysk.bytes(maximum, true);
    element("scale-mid").textContent = InfiniDysk.bytes(maximum / 2, true);
    element("range-start").textContent = history.length ? historyLabel(duration) : "Starting now";
    scrub();
  }

  function render() {
    panel.hidden = !snapshot?.configured;
    if (panel.hidden) return;
    const available = snapshot.available;
    const filesAvailable = snapshot.files_available;
    const status = element("status");
    status.textContent = available && filesAvailable ? "Live" : available ? "Partial" : "Unavailable";
    status.className = `status ${available && filesAvailable ? "status-live" : "status-waiting"}`;
    panel.classList.toggle("ifd-stale", !available);
    const message = element("message");
    message.hidden = available;
    message.textContent = snapshot.sampled_at ? `Last sample ${formatSampleTime(snapshot.sampled_at)}` : "Awaiting InfiniDysk statistics";
    element("connections").textContent = available && Number.isFinite(snapshot.connections) ? snapshot.connections : "--";
    const files = filesAvailable ? snapshot.files || [] : [];
    element("file-count").textContent = filesAvailable ? String(files.length) : "--";
    element("files-message").hidden = filesAvailable && files.length > 0;
    element("files-message").textContent = filesAvailable ? "No active files" : "File activity unavailable";
    const list = element("file-list");
    const existing = new Map([...list.children].map(node => [node.dataset.id, node]));
    for (const file of files) {
      let row = existing.get(file.id);
      if (!row) {
        row = document.createElement("li");
        row.dataset.id = file.id;
        const title = document.createElement("span");
        title.className = "ifd-file-name";
        const details = document.createElement("div");
        details.className = "ifd-file-details";
        for (const className of ["ifd-file-speed", "ifd-file-bytes"]) {
          const detail = document.createElement("span");
          detail.className = className;
          details.append(detail);
        }
        row.append(title, details);
        list.append(row);
      }
      existing.delete(file.id);
      row.querySelector(".ifd-file-name").textContent = file.fileName || "Unnamed file";
      row.querySelector(".ifd-file-speed").textContent = `Fetching ${InfiniDysk.bytes(file.fetch_bps, true)}`;
      row.querySelector(".ifd-file-bytes").textContent = `${InfiniDysk.bytes(file.bytesFetched)} fetched`;
    }
    for (const row of existing.values()) row.remove();
    renderChart();
  }

  async function refresh() {
    if (pending || document.hidden) return;
    pending = true;
    try {
      const response = await fetch("/api/ifd", { signal: AbortSignal.timeout(8000), cache: "no-store" });
      if (!response.ok) throw new Error("Unavailable");
      snapshot = await response.json();
    } catch {
      if (snapshot) snapshot = { ...snapshot, available: false, files_available: false };
    } finally { pending = false; }
    render();
  }

  chart.addEventListener("pointermove", event => { if (event.pointerType === "mouse" || selectedX !== null) { selectedX = event.clientX; scrub(); } });
  chart.addEventListener("pointerdown", event => { selectedX = event.clientX; chart.setPointerCapture(event.pointerId); scrub(); });
  for (const name of ["pointerleave", "pointerup", "pointercancel"]) chart.addEventListener(name, () => { selectedX = null; scrub(); });
  window.addEventListener("monitor-settings-change", event => { minutes = event.detail.chartMinutes || 5; if (snapshot) renderChart(); });
  document.addEventListener("visibilitychange", () => { if (!document.hidden) refresh(); });
  refresh();
  setInterval(refresh, 2000);
})();