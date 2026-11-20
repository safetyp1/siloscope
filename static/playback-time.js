"use strict";

// Estimates when an active playback session will finish, in the viewer's
// local time zone. The calculation runs in the browser, so the times shown
// follow the device viewing the dashboard rather than the container or server.
const PlaybackTime = (() => {
  function remainingSeconds(session) {
    const duration = Number(session?.file_duration);
    const position = Number(session?.position_seconds);
    if (!Number.isFinite(duration) || !Number.isFinite(position) || duration <= 0 || position < 0) return null;
    return Math.max(0, duration - position);
  }

  function endTime(session, now = Date.now()) {
    if (!session || session.is_paused) return null;
    const remaining = remainingSeconds(session);
    if (remaining === null) return null;
    return new Date(now + remaining * 1000);
  }

  function formatClock(date) {
    const pad = (value) => String(value).padStart(2, "0");
    return `${pad(date.getHours())}:${pad(date.getMinutes())}`;
  }

  function endsLabel(session, now = Date.now()) {
    const end = endTime(session, now);
    return end ? `Ends ${formatClock(end)}` : "";
  }

  return { remainingSeconds, endTime, formatClock, endsLabel };
})();

if (typeof module !== "undefined" && module.exports) module.exports = PlaybackTime;
