const { test } = require("node:test");
const assert = require("node:assert/strict");
const { bytes } = require("../static/infinidysk.js");

test("IFD formatting distinguishes missing values from idle and uses decimal byte units", () => {
  for (const value of [null, undefined, NaN, Infinity, -1]) assert.equal(bytes(value, true), "--");
  assert.equal(bytes(0, true), "0 B/s");
  assert.equal(bytes(42800000, true), "42.8 MB/s");
  assert.equal(bytes(2100000000), "2.1 GB");
});