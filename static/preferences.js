"use strict";

const MonitorPreferences = (() => {
  const storageKey = "silo-monitor.ui.v1";
  const systemOrder = ["disk", "cpu", "network", "ifd", "memory"];
  const navOrder = ["system", "playing", "library", "infrastructure", "settings"];
  const groups = {
    system: {
      label: "System",
      options: [
        ["heading", "Section heading", ".resource-section > .section-heading > div"],
        ["status", "Connection light", "#connection-status"],
        ["build", "Server build", "#server-build"],
        ["disk", "Disk usage", ".disk-panel"],
        ["disk.heading", "Heading", ".disk-heading", "disk"],
        ["disk.values", "Usage values", ".disk-summary", "disk"],
        ["disk.bars", "Usage bars", ".disk-track", "disk"],
        ["cpu", "CPU", ".cpu-panel"],
        ["cpu.heading", "Heading", ".cpu-panel .metric-section-heading", "cpu"],
        ["cpu.value", "Current usage", ".cpu-panel .metric-header > div", "cpu"],
        ["cpu.detail", "Cores and load", "#cpu-detail", "cpu"],
        ["cpu.chart", "Graph", ".cpu-panel .chart-wrap", "cpu"],
        ["cpu.scale", "Graph scale", ".cpu-panel .chart-scale", "cpu.chart"],
        ["cpu.time", "Time labels", ".cpu-panel .chart-footer", "cpu.chart"],
        ["cpu.processes", "Top CPU processes", ".cpu-processes", "cpu"],
        ["network", "Bandwidth", ".network-panel"],
        ["network.heading", "Heading", ".network-panel .metric-section-heading", "network"],
        ["network.download", "Download value", ".network-download", "network"],
        ["network.upload", "Upload value", ".network-upload", "network"],
        ["network.detail", "Bandwidth detail", "#network-detail", "network"],
        ["network.chart", "Graph", ".network-panel .chart-wrap", "network"],
        ["network.scale", "Graph scale", ".network-panel .chart-scale", "network.chart"],
        ["network.time", "Time labels", ".network-panel .chart-footer", "network.chart"],
        ["ifd", "InfiniDysk", ".ifd-panel"],
        ["ifd.heading", "Heading", ".ifd-heading", "ifd"],
        ["ifd.status", "Connection status", "#ifd-status", "ifd"],
        ["ifd.fetched", "Fetched throughput", ".ifd-fetched", "ifd"],
        ["ifd.served", "Served throughput", ".ifd-served", "ifd"],
        ["ifd.connections", "Connections in use", ".ifd-connections", "ifd"],
        ["ifd.chart", "Graph", ".ifd-panel .chart-wrap", "ifd"],
        ["ifd.scale", "Graph scale", ".ifd-panel .chart-scale", "ifd.chart"],
        ["ifd.time", "Time labels", ".ifd-panel .chart-footer", "ifd.chart"],
        ["ifd.files", "Active files", ".ifd-files", "ifd"],
        ["ifd.speed", "File fetching speed", ".ifd-file-speed", "ifd.files"],
        ["ifd.bytes", "File bytes fetched", ".ifd-file-bytes", "ifd.files"],
        ["memory", "Memory", ".ram-panel"],
        ["memory.value", "Current usage", ".ram-panel .metric-header > div", "memory"],
        ["memory.detail", "Capacity", "#ram-detail", "memory"],
        ["memory.chart", "Graph", ".ram-panel .chart-wrap", "memory"],
        ["memory.scale", "Graph scale", ".ram-panel .chart-scale", "memory.chart"],
        ["memory.time", "Time labels", ".ram-panel .chart-footer", "memory.chart"],
      ],
    },
    playing: {
      label: "Playing",
      options: [
        ["heading", "Section heading", ".sessions-section .section-heading > div"],
        ["count", "Stream count", "#session-count"],
        ["badge", "Tab badge", "#playing-badge"],
        ["cards", "Session cards", "#sessions"],
        ["posters", "Posters", ".session-card .poster", "cards"],
        ["title", "Title", ".session-title", "cards"],
        ["subtitle", "Episode and media details", ".session-subtitle", "cards"],
        ["source", "Playback source", ".session-source", "cards"],
        ["resolution", "Video resolution", ".session-resolution", "cards"],
        ["tonemap", "Tone mapping", ".session-tonemap", "cards"],
        ["audio", "Audio codec", ".session-audio", "cards"],
        ["bitrate", "File bitrate", ".session-bitrate", "cards"],
        ["method", "Play method", ".session-tags .tag-method, .session-tags .tag-transcode", "cards"],
        ["client", "Client", ".session-client", "cards"],
        ["node", "Playback node", ".session-node", "cards"],
        ["profile", "Profile", ".session-profile", "cards"],
        ["progress", "Playback progress", ".session-card .progress-track", "cards"],
        ["user", "User", ".session-footer .avatar, .session-user", "cards"],
        ["time", "Start time / playback state", ".session-time", "cards"],
      ],
    },
    infrastructure: {
      label: "Nodes",
      options: [
        ["heading", "Section heading", ".nodes-section .section-heading > div"],
        ["summary", "Health summary", "#node-summary"],
        ["badge", "Transcode job badge", "#nodes-badge"],
        ["cards", "Node cards", "#nodes"],
        ["name", "Name", ".node-name", "cards"],
        ["role", "Role", ".node-role", "cards"],
        ["health", "Health", ".node-status", "cards"],
        ["routes", "Routed streams", ".node-route", "cards"],
        ["jobs", "Jobs", ".node-stat-jobs", "cards"],
        ["egress", "Egress", ".node-stat-egress", "cards"],
        ["checked", "Last check", ".node-stat-checked", "cards"],
        ["accelerator", "Accelerator", ".node-stat-accelerator", "cards"],
        ["resources", "Resource summary", ".node-resources", "cards"],
      ],
    },
  };

  function normalize(raw) {
    const result = { visibility: {}, systemOrder: [...systemOrder], navOrder: [...navOrder], historyFilter: "all", chartHeight: 140, chartMinutes: 5 };
    for (const [group, definition] of Object.entries(groups)) {
      for (const [key] of definition.options) {
        const fullKey = `${group}.${key}`;
        result.visibility[fullKey] = typeof raw?.visibility?.[fullKey] === "boolean" ? raw.visibility[fullKey] : true;
      }
    }
    if (Array.isArray(raw?.systemOrder)) {
      result.systemOrder = [...new Set([...raw.systemOrder.filter(key => systemOrder.includes(key)), ...systemOrder])];
    }
    if (Array.isArray(raw?.navOrder)) result.navOrder = [...new Set([...raw.navOrder.filter(key => navOrder.includes(key)), ...navOrder])];
    if (["all", "silo", "plex"].includes(raw?.historyFilter)) result.historyFilter = raw.historyFilter;
    if (Number.isFinite(raw?.chartHeight)) result.chartHeight = Math.min(220, Math.max(100, raw.chartHeight));
    if ([2, 3, 4, 5].includes(raw?.chartMinutes)) result.chartMinutes = raw.chartMinutes;
    return result;
  }

  function enabled(preferences, group, key) {
    const option = groups[group]?.options.find(option => option[0] === key);
    return !!option && preferences.visibility[`${group}.${key}`] !== false
      && (!option[3] || enabled(preferences, group, option[3]));
  }

  function load(storage) {
    try { return normalize(JSON.parse(storage.getItem(storageKey))); }
    catch { return normalize(null); }
  }

  function save(storage, preferences) {
    try { storage.setItem(storageKey, JSON.stringify(normalize(preferences))); return true; }
    catch { return false; }
  }

  return { storageKey, systemOrder, navOrder, groups, normalize, enabled, load, save };
})();

if (typeof module !== "undefined" && module.exports) module.exports = MonitorPreferences;