process.env.TZ = "Africa/Johannesburg";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const PlaybackTime = require("../static/playback-time.js");
const now = Date.parse("2026-11-18T19:00:00+02:00");

test("finish time uses remaining seconds in the viewer's time zone", () => {
  for (const source of ["silo", "plex"]) {
    const session = { source, file_duration: 7200, position_seconds: 1380 };
    assert.equal(PlaybackTime.remainingSeconds(session), 5820);
    assert.equal(PlaybackTime.formatClock(PlaybackTime.endTime(session, now)), "20:37");
  }
  assert.equal(PlaybackTime.formatClock(PlaybackTime.endTime({ file_duration: 18000, position_seconds: 0 }, now)), "00:00");
});

test("paused and unknown or invalid playback measurements have no estimate", () => {
  assert.equal(PlaybackTime.endTime({ file_duration: 3600, position_seconds: 60, is_paused: true }, now), null);
  for (const value of [undefined, null, "", "60", false, NaN, Infinity, -1]) {
    assert.equal(PlaybackTime.endTime({ file_duration: value, position_seconds: 0 }, now), null);
    assert.equal(PlaybackTime.endTime({ file_duration: 3600, position_seconds: value }, now), null);
  }
  assert.equal(PlaybackTime.endTime({ file_duration: 0, position_seconds: 0 }, now), null);
  assert.equal(PlaybackTime.endTime(null, now), null);
  assert.equal(PlaybackTime.endTime({ file_duration: 100, position_seconds: 0 }, NaN), null);
  assert.equal(PlaybackTime.endTime({ file_duration: Number.MAX_VALUE, position_seconds: 0 }, now), null);
});

test("an overrun clamps to now and zero position remains valid", () => {
  assert.equal(PlaybackTime.endTime({ file_duration: 100, position_seconds: 160 }, now).getTime(), now);
  assert.equal(PlaybackTime.endTime({ file_duration: 100, position_seconds: 0 }, now).getTime(), now + 100000);
});
