const { test } = require("node:test");
const assert = require("node:assert/strict");
const preferences = require("../static/preferences.js");

test("defaults show all existing content", () => {
  const value = preferences.normalize(null);
  assert.ok(Object.values(value.visibility).every(Boolean));
  assert.equal(value.chartHeight, 140);
  assert.equal(value.chartMinutes, 5);
  assert.deepEqual(value.systemOrder, ["disk", "cpu", "network", "ifd", "memory"]);
});

test("saved values are bounded and order is repaired", () => {
  const value = preferences.normalize({ visibility: { "system.cpu": false, "playing.cards": "false" }, systemOrder: ["memory", "memory", "bad"], chartHeight: 999 });
  assert.equal(value.visibility["system.cpu"], false);
  assert.equal(value.visibility["playing.cards"], true);
  assert.deepEqual(value.systemOrder, ["memory", "disk", "cpu", "network", "ifd"]);
  assert.equal(value.chartHeight, 220);
});

test("hidden parents suppress children without changing saved child values", () => {
  const value = preferences.normalize({ visibility: { "system.cpu": false } });
  assert.equal(preferences.enabled(value, "system", "cpu.processes"), false);
  assert.equal(value.visibility["system.cpu.processes"], true);
  value.visibility["system.cpu"] = true;
  assert.equal(preferences.enabled(value, "system", "cpu.processes"), true);
});

test("IFD preferences preserve old layout and respect nested visibility", () => {
  const value = preferences.normalize({ systemOrder: ["memory", "cpu", "disk", "network"], visibility: { "system.ifd.files": false } });
  assert.deepEqual(value.systemOrder, ["memory", "cpu", "disk", "network", "ifd"]);
  assert.equal(preferences.enabled(value, "system", "ifd.speed"), false);
  assert.equal(preferences.enabled(value, "system", "ifd.chart"), true);
  value.visibility["system.ifd"] = false;
  assert.equal(preferences.enabled(value, "system", "ifd.chart"), false);
});

test("storage failures and corrupt JSON fall back safely", () => {
  assert.deepEqual(preferences.load({ getItem() { return "{"; } }), preferences.normalize(null));
  assert.deepEqual(preferences.load({ getItem() { throw Error("blocked"); } }), preferences.normalize(null));
  assert.equal(preferences.save({ setItem() { throw Error("blocked"); } }, preferences.normalize(null)), false);
});

test("preferences round trip", () => {
  let stored;
  const storage = { getItem: () => stored, setItem: (_, value) => { stored = value; } };
  const value = preferences.normalize({ visibility: { "system.cpu.processes": false }, systemOrder: ["cpu", "memory", "disk", "network"], chartMinutes: 3 });
  assert.equal(preferences.save(storage, value), true);
  assert.deepEqual(preferences.load(storage), value);
});

test("graph duration accepts only supported minute options", () => {
  for (const minutes of [2, 3, 4, 5]) assert.equal(preferences.normalize({ chartMinutes: minutes }).chartMinutes, minutes);
  for (const minutes of [0, 1, 6, 2.5, "3", null, NaN]) assert.equal(preferences.normalize({ chartMinutes: minutes }).chartMinutes, 5);
});
test("navigation order and history filter normalize and persist", () => {
  const value = preferences.normalize({ navOrder: ["settings", "bad", "settings"], historyFilter: "plex" });
  assert.deepEqual(value.navOrder, ["settings", "system", "playing", "library", "infrastructure"]);
  assert.equal(value.historyFilter, "plex");
  assert.equal(preferences.normalize({ historyFilter: "bad" }).historyFilter, "all");
});