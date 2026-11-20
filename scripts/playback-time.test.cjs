process.env.TZ = "Africa/Johannesburg";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const PlaybackTime = require("../static/playback-time.js");

const now = Date.parse("2026-11-18T19:00:00+02:00");

test("end time is now plus the remaining duration, in local time", () => {
  const session = { file_duration: 7200, position_seconds: 1380 };
  assert.equal(PlaybackTime.remainingSeconds(session), 5820);
  assert.equal(PlaybackTime.endsLabel(session, now), "Ends 20:37");
});

test("minutes and hours are zero padded", () => {
  assert.equal(PlaybackTime.endsLabel({ file_duration: 18000, position_seconds: 0 }, now), "Ends 00:00");
  assert.equal(PlaybackTime.formatClock(new Date(Date.parse("2026-11-18T07:05:00+02:00"))), "07:05");
});

test("paused, unknown, or invalid sessions show no end time", () => {
  assert.equal(PlaybackTime.endsLabel({ file_duration: 3600, position_seconds: 60, is_paused: true }, now), "");
  assert.equal(PlaybackTime.endsLabel({ file_duration: 0, position_seconds: 0 }, now), "");
  assert.equal(PlaybackTime.endsLabel({ position_seconds: 10 }, now), "");
  assert.equal(PlaybackTime.endsLabel({ file_duration: 3600, position_seconds: -1 }, now), "");
  assert.equal(PlaybackTime.endsLabel(null, now), "");
});

test("a position past the end clamps to the current time", () => {
  assert.equal(PlaybackTime.endsLabel({ file_duration: 100, position_seconds: 160 }, now), "Ends 19:00");
});
