"use strict";

(() => {
  const model = MonitorPreferences;
  let storage;
  try { storage = window.localStorage; } catch {}
  let preferences = model.load(storage);
  let activeGroup = null;
  const menu = document.getElementById("settings-menu");
  const editor = document.getElementById("settings-editor");
  const options = document.getElementById("settings-options");
  const status = document.getElementById("settings-status");
  const visibilityStyles = document.querySelector('link[rel="stylesheet"][href^="/styles.css"]').sheet;
  const firstVisibilityRule = visibilityStyles.cssRules.length;
  let visibilityRuleCount = 0;
  const panels = Object.fromEntries(model.systemOrder.map(key => [key, document.querySelector(model.groups.system.options.find(option => option[0] === key)[2])]));
  const grid = document.querySelector(".metric-grid");
  const sections = {
    system: [
      ["Page Indicators", ["heading", "status", "build"]],
      ["Disk Usage", ["disk", "disk.heading", "disk.values", "disk.bars"]],
      ["CPU", ["cpu", "cpu.heading", "cpu.value", "cpu.detail", "cpu.chart", "cpu.scale", "cpu.time", "cpu.processes"]],
      ["Bandwidth", ["network", "network.heading", "network.download", "network.upload", "network.detail", "network.chart", "network.scale", "network.time"]],
      ["Memory", ["memory", "memory.value", "memory.detail", "memory.chart", "memory.scale", "memory.time"]],
    ],
    playing: [
      ["Page Indicators", ["heading", "count", "badge"]],
      ["Session Appearance", ["cards", "posters", "title"]],
      ["Media Details", ["subtitle", "resolution", "tonemap", "audio", "bitrate"]],
      ["Playback Details", ["source", "method", "client", "node", "profile", "progress", "user", "time"]],
    ],
    infrastructure: [
      ["Page Indicators", ["heading", "summary", "badge"]],
      ["Node Identity", ["cards", "name", "role"]],
      ["Health & Activity", ["health", "routes", "jobs", "checked"]],
      ["Resources", ["egress", "accelerator", "resources"]],
    ],
  };

  function make(tag, className, text) {
    const node = document.createElement(tag);
    node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function makeSection(title) {
    const fields = make("fieldset", "settings-fieldset settings-group");
    const legend = make("legend", "");
    legend.append(make("h4", "settings-group-heading", title));
    fields.append(legend);
    return fields;
  }

  function apply() {
    const rules = [];
    for (const [group, definition] of Object.entries(model.groups)) {
      for (const [key, , selector] of definition.options) {
        if (!model.enabled(preferences, group, key)) rules.push(`${selector} { display: none !important; }`);
      }
    }
    if (!model.enabled(preferences, "playing", "posters")) rules.push(".session-card { grid-template-columns: minmax(0, 1fr); }");
    while (visibilityRuleCount > 0) {
      visibilityStyles.deleteRule(firstVisibilityRule);
      visibilityRuleCount--;
    }
    for (const rule of rules) {
      visibilityStyles.insertRule(rule, firstVisibilityRule + visibilityRuleCount);
      visibilityRuleCount++;
    }
    for (const key of preferences.systemOrder) grid.append(panels[key]);
    const tabs = document.querySelector(".tab-list");
    for (const key of preferences.navOrder) {
      const tab = tabs.querySelector(`[data-tab="${key}"]`);
      if (tab) tabs.append(tab);
    }
    document.documentElement.style.setProperty("--chart-height", `${preferences.chartHeight}px`);
    window.dispatchEvent(new CustomEvent("monitor-settings-change", { detail: { chartMinutes: preferences.chartMinutes, historyFilter: preferences.historyFilter } }));
  }

  function commit() {
    apply();
    status.textContent = model.save(storage, preferences) ? "Saved on this device" : "Storage unavailable. Changes last until this page closes.";
  }

  function updateDisabled() {
    for (const input of options.querySelectorAll("input[data-key]")) {
      const option = model.groups[activeGroup].options.find(option => option[0] === input.dataset.key);
      input.disabled = !!option[3] && !model.enabled(preferences, activeGroup, option[3]);
    }
  }

  function renderOptions(focusKey) {
    options.replaceChildren();
    if (activeGroup === "navigation") {
      const order = makeSection("Bottom navigation");
      preferences.navOrder.forEach((key, index) => {
        const label = ({ system: "System", playing: "Playing", infrastructure: "Nodes", settings: "Settings", library: "Library" })[key];
        const row = make("div", "settings-order-row");
        row.append(make("span", "", label));
        for (const [direction, delta] of [["up", -1], ["down", 1]]) {
          const button = make("button", "icon-button");
          button.type = "button";
          button.title = `Move ${label} ${direction}`;
          button.setAttribute("aria-label", button.title);
          button.dataset.order = `${key}-${direction}`;
          const icon = make("span", `ui-icon icon-move-${direction}`);
          icon.setAttribute("aria-hidden", "true");
          button.append(icon);
          button.disabled = index + delta < 0 || index + delta >= preferences.navOrder.length;
          button.addEventListener("click", () => {
            const target = index + delta;
            [preferences.navOrder[index], preferences.navOrder[target]] = [preferences.navOrder[target], preferences.navOrder[index]];
            commit();
            renderOptions(`${key}-${target === 0 ? "down" : target === preferences.navOrder.length - 1 ? "up" : direction}`);
          });
          row.append(button);
        }
        order.append(row);
      });
      options.append(order);
      if (focusKey) options.querySelector(`[data-order="${focusKey}"]`)?.focus();
      return;
    }
    const definition = model.groups[activeGroup];
    if (activeGroup === "system") {
      const order = makeSection("Layout");
      preferences.systemOrder.forEach((key, index) => {
        const label = definition.options.find(option => option[0] === key)[1];
        const row = make("div", "settings-order-row");
        row.append(make("span", "", label));
        for (const [direction, delta, symbol] of [["up", -1, "\u2191"], ["down", 1, "\u2193"]]) {
          const button = make("button", "icon-button", symbol);
          button.type = "button";
          button.title = `Move ${label} ${direction}`;
          button.setAttribute("aria-label", button.title);
          button.dataset.order = `${key}-${direction}`;
          button.disabled = index + delta < 0 || index + delta >= preferences.systemOrder.length;
          button.addEventListener("click", () => {
            const target = index + delta;
            [preferences.systemOrder[index], preferences.systemOrder[target]] = [preferences.systemOrder[target], preferences.systemOrder[index]];
            commit();
            renderOptions(`${key}-${target === 0 ? "down" : target === preferences.systemOrder.length - 1 ? "up" : direction}`);
          });
          row.append(button);
        }
        order.append(row);
      });
      options.append(order);
      const graphs = makeSection("Graph Settings");
      const heightLabel = make("label", "settings-height");
      const height = make("input", "");
      height.type = "range";
      height.min = "100";
      height.max = "220";
      height.step = "10";
      height.value = preferences.chartHeight;
      const output = make("output", "", `${preferences.chartHeight}px`);
      height.setAttribute("aria-label", "Graph height");
      height.addEventListener("input", () => {
        preferences.chartHeight = Number(height.value);
        output.textContent = `${height.value}px`;
        commit();
      });
      heightLabel.append(make("span", "", "Height"), output, height);
      graphs.append(heightLabel);
      const durationLabel = make("label", "settings-duration");
      const duration = make("select", "");
      duration.setAttribute("aria-label", "Graph time window");
      for (const minutes of [2, 3, 4, 5]) {
        const option = make("option", "", `${minutes} minutes`);
        option.value = String(minutes);
        duration.append(option);
      }
      duration.value = String(preferences.chartMinutes);
      duration.addEventListener("change", () => {
        preferences.chartMinutes = Number(duration.value);
        commit();
      });
      durationLabel.append(make("span", "", "Time window"), duration);
      graphs.append(durationLabel);
      options.append(graphs);
    }
    for (const [title, keys] of sections[activeGroup]) {
      const fields = makeSection(title);
      for (const key of keys) {
        const [, label, , parent] = definition.options.find(option => option[0] === key);
        const row = make("label", `settings-toggle${parent ? " settings-child" : ""}`);
        const input = make("input", "");
        input.type = "checkbox";
        input.dataset.key = key;
        input.checked = preferences.visibility[`${activeGroup}.${key}`];
        input.addEventListener("change", () => {
          preferences.visibility[`${activeGroup}.${key}`] = input.checked;
          commit();
          updateDisabled();
        });
        const displayLabel = key === "heading" ? "Page heading"
          : key === "cards" ? `Show ${activeGroup === "playing" ? "session" : "node"} cards`
          : ["disk", "cpu", "network", "memory"].includes(key) ? "Show panel"
          : key === "cpu.processes" ? "Top processes" : label;
        row.append(make("span", "", displayLabel), input);
        fields.append(row);
      }
      options.append(fields);
    }
    updateDisabled();
    if (focusKey) options.querySelector(`[data-order="${focusKey}"]`)?.focus();
  }

  for (const [key, definition] of Object.entries({ ...model.groups, navigation: { label: "Navigation" }, transcoder: { label: "Transcoder" }, notifications: { label: "Notifications" } })) {
    const button = make("button", "settings-menu-item");
    button.type = "button";
    const arrow = make("span", "", "\u203a");
    arrow.setAttribute("aria-hidden", "true");
    button.append(make("span", "", definition.label), arrow);
    button.dataset.group = key;
    button.addEventListener("click", () => {
      activeGroup = key;
      menu.hidden = true;
      editor.hidden = false;
      const heading = document.getElementById("settings-group-heading");
      heading.textContent = definition.label;
      document.getElementById("reset-tab-settings").hidden = key === "transcoder" || key === "notifications";
      document.getElementById("reset-settings").hidden = key === "transcoder" || key === "notifications";
      if (key === "transcoder") TranscoderSettings.open(options, status);
      else if (key === "notifications") NotificationSettings.open(options, status);
      else renderOptions();
      heading.focus();
    });
    menu.append(button);
  }
  document.getElementById("settings-back").addEventListener("click", async () => {
    if (activeGroup === "notifications") {
      if (!await NotificationSettings.canLeave() || activeGroup !== "notifications") return;
      NotificationSettings.leave();
      status.textContent = "";
    }
    if (activeGroup === "transcoder") {
      if (!TranscoderSettings.canLeave()) return;
      TranscoderSettings.leave();
      status.textContent = "";
    }
    editor.hidden = true;
    menu.hidden = false;
    menu.querySelector(`[data-group="${activeGroup}"]`).focus();
    activeGroup = null;
    document.getElementById("reset-settings").hidden = false;
  });
  document.getElementById("reset-settings").addEventListener("click", () => {
    preferences = model.normalize(null);
    commit();
    if (activeGroup && activeGroup !== "transcoder" && activeGroup !== "notifications") renderOptions();
  });
  document.getElementById("reset-tab-settings").addEventListener("click", () => {
    if (!activeGroup || activeGroup === "transcoder" || activeGroup === "notifications") return;
    for (const [key] of model.groups[activeGroup]?.options || []) preferences.visibility[`${activeGroup}.${key}`] = true;
    if (activeGroup === "navigation") preferences.navOrder = [...model.navOrder];
    if (activeGroup === "system") {
      preferences.systemOrder = [...model.systemOrder];
      preferences.chartHeight = 140;
      preferences.chartMinutes = 5;
    }
    commit();
    renderOptions();
  });
  window.addEventListener("storage", event => {
    if (event.key !== model.storageKey && event.key !== null) return;
    preferences = model.load(storage);
    apply();
    if (activeGroup && activeGroup !== "transcoder" && activeGroup !== "notifications") renderOptions();
  });
  window.addEventListener("history-filter-change", event => {
    preferences.historyFilter = model.normalize({ historyFilter: event.detail }).historyFilter;
    commit();
  });
  apply();
})();