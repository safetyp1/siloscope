"use strict";

const PlaybackTime = (() => {
  function remainingSeconds(session) {
    const duration = session?.file_duration;
    const position = session?.position_seconds;
    if (!Number.isFinite(duration) || !Number.isFinite(position) || duration <= 0 || position < 0) return null;
    return Math.max(0, duration - position);
  }

  function endTime(session, now = Date.now()) {
    if (!session || session.is_paused) return null;
    const remaining = remainingSeconds(session);
    if (remaining === null || !Number.isFinite(now)) return null;
    const end = new Date(now + remaining * 1000);
    return Number.isFinite(end.getTime()) ? end : null;
  }

  function formatClock(date) {
    const pad = value => String(value).padStart(2, "0");
    return `${pad(date.getHours())}:${pad(date.getMinutes())}`;
  }

  return { remainingSeconds, endTime, formatClock };
})();

if (typeof module !== "undefined" && module.exports) module.exports = PlaybackTime;
