# Siloscope

A low-overhead, mobile-first wallboard for Silo CPU, memory, network bandwidth, disk usage, playback-node health, and active Silo and Plex playback sessions. A small Go server keeps API credentials out of the browser and retains five minutes of CPU, memory, download, and upload samples in memory so charts survive page reloads.

Includes recent playback activity, independent freshness indicators, free disk capacity, per-device display preferences, Silo transcoder controls, playback termination, and optional Web Push alerts for CPU, disk usage, playback, transcoding, and service outages/recovery.

The Library tab optionally searches existing Sonarr/Radarr items and replaces individual files after confirmation. Destructive actions are disabled by default.

## Compatible with

| Silo | Plex |
| --- | --- |
| <a href="https://github.com/Silo-Server/silo-server"><img src="docs/images/silo-logo.png" alt="Silo logo" width="64" height="64"></a> | <a href="https://www.plex.tv/"><img src="docs/images/plex-logo.svg" alt="Plex logo" width="64" height="64"></a> |
| Resource and node monitoring, playback, and transcoder settings | Optional playback monitoring and session termination |

Logos identify compatible services; no affiliation or endorsement is implied. See [logo sources](docs/images/LOGOS.md).

### Silo API compatibility

Siloscope requires Silo's native **v2 administrator API**. The integration was checked against upstream Silo server commit `032ca133a5f41b835a87a3e22076ca3c8f3381d6` and its `contracts/api/v2/openapi.json`. Older v1-only servers are not supported by this version; upgrade Silo before updating Siloscope. Set `SILO_URL` to the server base URL, not an `/api/v2` URL. Plex continues to use its own API unchanged.

| Operation | Silo endpoint |
| --- | --- |
| Resources and charts | `GET /api/v2/admin/system/resources` |
| API server build | `GET /api/v2/admin/system/build` |
| Playback, history, and playback alerts | `GET /api/v2/admin/sessions` |
| Nodes and node alerts | `GET /api/v2/admin/nodes` |
| Stop Silo playback | `POST /api/v2/admin/sessions/{session_id}/terminate` |
| Restart state | `GET /api/v2/admin/server/status` |
| Request server restart | `POST /api/v2/admin/server/restart` |
| Read transcoder configuration | `GET /api/v2/admin/settings/effective` |
| Read restart requirements | `GET /api/v2/admin/settings/restart-keys` |
| Save transcoder configuration | `PUT /api/v2/admin/settings` with `If-Match` |

Use an unscoped API key owned by an enabled administrator. Session and node collections are fetched in pages of up to 200 and combined before updating the dashboard, history, or alerts. Failed pages are never interpreted as missing sessions or nodes. Collection reads are bounded by the upstream timeout, 100 pages, and 4 MiB total response size; exceeding a bound reports the source unavailable rather than presenting a partial snapshot. Siloscope's own browser-facing endpoints remain unchanged.

Settings reads return a version (ETag), which each save sends back in `If-Match`. A concurrent change returns a conflict and requires **Reload settings**; Siloscope does not force an overwrite or replay the write. Playback termination requires a v2 receipt confirming durable revocation for the requested session before the UI reports the request accepted. Buffered media can still finish playing after revocation.

System shows the API server build number centered below **Server pulse**, with a revision fallback when available. Metadata is cached for five minutes; an unsupported or unavailable endpoint does not affect monitoring. Proxy/transcode node builds are not displayed because the current v2 node contract does not expose them. Node versions are never inferred from the API server build.

## Screenshots

Mobile views captured using live server data and a read-only settings preview. Screenshots predate activity history, freshness/free-space indicators, the Library tab, navigation ordering, and the latest grouped notification controls. No playback was terminated during capture.

| System | Playing |
| --- | --- |
| <img src="docs/images/system.png" alt="System view with CPU, bandwidth, memory, disk usage, and top processes" width="280"> | <img src="docs/images/playing.png" alt="Playing view with active sessions and playback progress" width="280"> |

| Nodes | Settings |
| --- | --- |
| <img src="docs/images/nodes.png" alt="Nodes view with health, routed streams, jobs, and resource usage" width="280"> | <img src="docs/images/settings.png" alt="System settings with item ordering, graph height, and visibility controls" width="280"> |

| Playback selection | Transcoder settings |
| --- | --- |
| <img src="docs/images/playback-selection.png" alt="Playing view with session selection circles and the batch Terminate button" width="280"> | <img src="docs/images/transcoder.png" alt="Transcoder settings with hardware acceleration, tone mapping, and buffer controls" width="280"> |

## Run with Docker Compose

1. Copy `.env.example` to `.env`.
2. Set `SILO_URL` to the Silo address reachable from the container.
3. Set `SILO_API_KEY` to an unscoped API key owned by an enabled Silo admin.
4. On the Linux Docker host, run `ip route show default`. Set `HOST_NETWORK_INTERFACE` in `.env` to the interface shown after `dev`, such as `eth0` or `enp0s6`.
5. To include Plex playback, set `PLEX_URL` to your Plex Media Server base URL (for example, `http://plex:32400`) and `PLEX_TOKEN` to its API token. Leave both blank for Silo-only playback.
6. To enable notifications, set `PUSH_CONTACT` in `.env` to your real contact URI, such as `mailto:admin@example.com` or `https://example.com/contact`. Leave it blank to disable push. This identifies the sender to the push provider; it is not a notification recipient.
7. Start the service with host bandwidth and process monitoring:

   ```sh
    docker compose -f compose.yaml -f compose.host-network.yaml -f compose.host-processes.yaml up -d
   ```

Use this same command after changing `.env`; a container restart alone does not apply environment changes. For image updates, pull the published image first, then recreate with the same files:

```sh
docker compose -f compose.yaml -f compose.host-network.yaml -f compose.host-processes.yaml pull
docker compose -f compose.yaml -f compose.host-network.yaml -f compose.host-processes.yaml up -d
```

The default local URL is `http://127.0.0.1:8091`. The health endpoint is `/healthz`. Host metrics require a Linux Docker host; process CPU usage becomes available after two samples, approximately 10 seconds after startup.

The Silo admin resource and session routes are not covered by the currently available scoped API-key permissions. Treat the key as a secret: keep `.env` out of source control and use it only in Siloscope's server-side configuration, never in browser settings.

## Run with Portainer

Create a stack using the following complete Compose definition. In Portainer's stack environment variables, set `SILO_URL`, `SILO_API_KEY`, and `HOST_NETWORK_INTERFACE` as described above. Set both `PLEX_URL` and `PLEX_TOKEN` to enable Plex, or leave both unset. Set `PUSH_CONTACT` to your real `mailto:` address or HTTPS contact URL to enable push. `MONITOR_BIND` and `MONITOR_PORT` default to `127.0.0.1` and `8091`.

```yaml
services:
   silo-monitor:
      image: ghcr.io/jasjeetsuri/silo-monitor:latest
      pull_policy: always
      restart: unless-stopped
      environment:
         SILO_URL: ${SILO_URL:?Set SILO_URL}
         SILO_API_KEY: ${SILO_API_KEY:?Set SILO_API_KEY}
         PLEX_URL: ${PLEX_URL:-}
         PLEX_TOKEN: ${PLEX_TOKEN:-}
         SONARR_URL: ${SONARR_URL:-}
         SONARR_API_KEY: ${SONARR_API_KEY:-}
         RADARR_URL: ${RADARR_URL:-}
         RADARR_API_KEY: ${RADARR_API_KEY:-}
         ARR_REPLACEMENTS_ENABLED: ${ARR_REPLACEMENTS_ENABLED:-false}
         REPLACEMENT_DATA_DIR: /data/replacements
         PUSH_CONTACT: ${PUSH_CONTACT:-}
         PUSH_DATA_DIR: /data/push
         ACTIVITY_DATA_DIR: /data/activity
         HOST_PROC_DIR: /host/proc
         HOST_NETWORK_STATS_DIR: /host/network
         LISTEN_ADDR: :8080
         GOMAXPROCS: "1"
         GOMEMLIMIT: 32MiB
      volumes:
         - notification-data:/data
         - /proc:/host/proc:ro
         - /sys/class/net/${HOST_NETWORK_INTERFACE:?Set HOST_NETWORK_INTERFACE}/statistics:/host/network:ro
      ports:
         - "${MONITOR_BIND:-127.0.0.1}:${MONITOR_PORT:-8091}:8080"
      read_only: true
      tmpfs:
         - /tmp:size=1m,mode=1777
      cap_drop:
         - ALL
      security_opt:
         - no-new-privileges:true
      pids_limit: 32
      mem_limit: 48m
      cpus: 0.10

volumes:
   notification-data:
```

Deploy the stack. For subsequent configuration changes, edit this same stack and its environment variables, then use **Update the stack** to apply them. All host-monitoring settings are included in this definition; no additional Compose files are needed for Portainer.

### Preserve existing data

The named volume at `/data` stores playback history (`activity/history.json`), push keys, subscriptions, rules, and alert state (`push/notifications.json`), and the optional replacement journal (`replacements/operations.json`). Keep this mount writable even though the container root filesystem is read-only. Do not use `docker compose down -v` or delete the volume during updates. Back it up securely: it contains private viewing history, media filenames, and push credentials.

When moving or renaming an existing stack, reuse its actual volume name instead of creating an empty one. Find it with `docker volume ls`. For example, to retain an existing volume named `silo-performance_notification-data`, replace the top-level volume definition above with:

```yaml
volumes:
   notification-data:
      external: true
      name: silo-performance_notification-data
```

Keep the service mount as `notification-data:/data`. The external volume must already exist; substitute your own existing name. For a new installation, use the original managed-volume definition. History remains enabled when `PUSH_CONTACT` is blank. Preserve `PUSH_CONTACT`, `PUSH_DATA_DIR`, and `ACTIVITY_DATA_DIR` when consolidating Compose overrides into a single stack.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `SILO_URL` | required | Silo base URL reachable by the monitor container |
| `SILO_API_KEY` | required | Silo admin API key |
| `PLEX_URL` | unset | Optional Plex Media Server base URL, usually `http://plex:32400` |
| `PLEX_TOKEN` | unset | Plex API token (`X-Plex-Token`), required when `PLEX_URL` is set |
| `SONARR_URL` / `SONARR_API_KEY` | unset | Optional Sonarr base URL and API key; configure together |
| `RADARR_URL` / `RADARR_API_KEY` | unset | Optional Radarr base URL and API key; configure together |
| `ARR_REPLACEMENTS_ENABLED` | `false` | Explicit opt-in to destructive Library actions; requires administrator-only proxy access |
| `REPLACEMENT_DATA_DIR` | `/data/replacements` in Docker/Compose | Private writable persistent journal; required when replacements are enabled |
| `LISTEN_ADDR` | `:8080` | Address used by the Go server |
| `MONITOR_BIND` | `127.0.0.1` | Host address published by Compose |
| `MONITOR_PORT` | `8091` | Host port published by Compose |
| `HOST_NETWORK_INTERFACE` | `eth0` | Host NIC used by the optional bandwidth override |
| `HOST_NETWORK_STATS_DIR` | unset | Directory containing host NIC `rx_bytes` and `tx_bytes` counters |
| `HOST_PROC_DIR` | unset | Read-only host `/proc` mount for the top CPU process list |
| `PUSH_CONTACT` | unset | Enables Web Push when set to a real `mailto:` address or HTTPS contact URL |
| `PUSH_DATA_DIR` | `/data/push` in Compose | Writable persistent directory for signing keys, subscriptions, rules, and alert state; required when push is enabled |
| `ACTIVITY_DATA_DIR` | `/data/activity` in Docker/Compose; unset for standalone binaries | Writable persistent playback-history directory; independent of push configuration |

The server samples resources every 5 seconds into bounded in-memory charts and Silo/configured Plex sessions every 10 seconds into playback history, persisted when `ACTIVITY_DATA_DIR` is set, even without notification subscriptions or an open browser. The browser polls resources every 5 seconds and sessions, nodes, and activity every 15 seconds while visible. Resource responses are cached for 4 seconds and session responses for 10 seconds. Browser polling pauses when Mobile Safari backgrounds the tab; server history collection continues.

The image is published for AMD64 and ARM64 at `ghcr.io/jasjeetsuri/silo-monitor:latest`.

Both installation methods above mount the selected host interface's byte counters and host `/proc` read-only. Network graphs report total host ingress and egress, and the process list reports the three highest CPU consumers as a percentage of total host CPU capacity.

## Push notifications

**Settings > Notifications** groups controls under **CPU Alerts**, **Disk Alerts**, **Playback Alerts**, and **Service Alerts**. Enable/Disable notifications and Send test remain at the top. Sliders show their current values and are disabled when their alert is off; titles and usernames are an opt-in setting under Playback Alerts.

**Service Alerts > Service outages and recovery** is off by default. Silo and configured Plex session API failures (including authentication failures or invalid responses) trigger one alert after at least 60 seconds of consecutive failed checks. The first successful check after an alerted outage queues recovery confirmation. Enabled nodes are checked through Silo's node API every 10 seconds; only explicit unhealthy results with a health-check timestamp no older than 45 seconds count. Missing/stale checks or node API failures are unknown, not proof of individual node failures. Disabled/removed nodes are excluded without recovery notifications. Node names appear in service alerts.

Outage notification state survives restarts, but sustained timers restart after downtime or monitoring gaps over 35 seconds. Turning the service rule off/on re-arms it. Silo and Plex checks run independently; an unavailable Silo API does not generate a cascade of node alerts. These are checks from Siloscope's network location, not an external uptime guarantee. Siloscope cannot notify while it is itself down, and losing outbound connectivity can prevent delivery.

Disk alerts are opt-in under **Settings > Notifications > Disk Alerts > High disk usage**. The threshold slider defaults to 90% (range 75-100%) and applies to each disk shown by Silo. An alert is sent on the first fresh reading at or above the threshold, including when enabled while a disk is already full enough. Each disk alerts once until usage falls at least five percentage points below the threshold, then can alert again. This state survives restarts; changing the disk rule or threshold re-arms it. Missing, unavailable, or stale disk readings do not trigger or re-arm alerts. Disk checks reuse the existing background resource samples and work with the app closed, without extra polling.

Once notifications are enabled, preferences autosave on this device: toggles save immediately, and sliders save on release or keyboard adjustment. CPU sliders allow 70-100% usage, 5-15 minutes sustained, and a 10-60 minute cooldown. Values are displayed while dragging. Back navigation waits for an in-progress save; failed saves keep the editor open, restore the last confirmed values, and show an error. Existing API rules remain supported; saved values outside the slider ranges are clamped in the editor and applied on the next save.

Push is optional and disabled until `PUSH_CONTACT` is set. Set it to your real contact address, for example `mailto:admin@your-domain.example`, and recreate the container with the updated Compose definition. A named volume at `/data` preserves the automatically generated VAPID signing keys and device subscriptions. With a bind mount instead, give UID 65532 write access to the directory. Do not expose this directory through the web server or commit its contents. Back it up securely; losing the keys requires devices to re-enable notifications.

If **Send test** does not arrive, check the container logs. The UI's **Test notification queued** message confirms only local queueing; `push test notification accepted by service (HTTP 201)` confirms provider acceptance, not display on the device. HTTP 403 means the provider rejected the request: check the contact URI and use the latest image. Older versions incorrectly doubled the `mailto:` prefix for email contacts; updating fixes this without replacing keys or subscriptions. Keep exactly one `mailto:` prefix in `PUSH_CONTACT`. For an accepted message that does not appear, check notification permission, Focus modes, and the installed Home Screen app on iOS. Wait at least one minute between test requests. Do not delete the data volume to troubleshoot delivery.

On iOS 16.4 or later, add the HTTPS site to the Home Screen, open that installed app, then choose **Settings > Notifications > Enable notifications** and grant permission. On supported desktop browsers, the same setting works over HTTPS (localhost also works for development). Use **Send test** to check delivery. Apple, Google/FCM, and Mozilla push endpoints are supported; outbound HTTPS access to those services is required. An authenticated reverse proxy is still required. Subscriptions must only be created by trusted administrators; same-origin checks are not authentication. Push can continue after a browser login expires until the device subscription is disabled or removed.

Preferences are per device. CPU alerts default to 85% sustained for five minutes, a 30-minute repeat cooldown, and a recovery notification once CPU falls at least five percentage points below the threshold. CPU uses existing cached samples without another host scan. Missing or stale samples reset the sustained timer. Playback/transcode alerts are off by default and reuse the always-on history collector's ten-second polls and browser-request caches. They continue with the app closed, without additional session polls.

The first successful session poll after startup, a polling gap over 30 seconds, or a preference change establishes a baseline without sending alerts for existing streams. Subsequent new sessions trigger playback alerts immediately. Transcode alerts make one decision on the first fresh poll at least 60 seconds after a session was first observed (normally about 60-70 seconds). This is not a continuous-transcoding timer: a session playing directly at that check will not trigger a later transcode alert. Audio-only transcoding counts. Sessions that end before the check, monitoring gaps, restarts, and rule changes cancel pending checks. Playing badges and History methods remain immediate. A full notification queue permits bounded retries of the already-decided transcode alert until 90 seconds after first observation, without reclassifying it. Events in one poll are grouped per source. Titles and usernames are hidden by default and can be enabled per device. CPU cooldown and subscription state survive restarts; the sustained CPU timer restarts because CPU activity during downtime is unknown.

Delivery is best-effort: Focus modes, connectivity, browser restrictions, or push-service errors may delay or prevent alerts. Sessions shorter than the polling interval can be missed. A bounded in-memory queue and one delivery worker limit resource usage; queued notifications are not retried or replayed after a restart. A full queue drops playback events; sustained service conditions can queue again at a later check. Expired subscriptions are removed after HTTP 404/410 responses. There is a limit of 32 devices, and test notifications are limited to one per device per minute. The service worker handles push/click events only and does not cache pages, credentials, or API responses. No new process scans are added; service alerts add background node-API polling when enabled.

## Activity and freshness

The **history icon at the top right of Playing** opens a separate **History** screen. Its compact list shows poster artwork when available, the film or series title, episode details, media player (Silo/Plex), and the user with a relative time. Only one item can be expanded at a time to show playback method, observed duration, and start/stop timestamps. The filter icon selects All, Silo, or Plex before paging and remembers the selection on this device. The back arrow returns to Playing and restores its scroll position; the Playing tab stays selected while viewing history. Settings subpages use the same borderless back-arrow style. Tapping History navigation does not leave a focus outline; keyboard navigation retains visible focus indicators on controls.

History retains up to 200 session records last seen within 30 days, across Silo and Plex, with 20 records shown at a time. Records are removed when older than 30 days or displaced by the 200-entry cap, whichever comes first. Playback method is the latest observed value (Direct play, Remux, Transcoding, or Unknown). Sessions already present at startup are labelled **First seen**, not assigned an invented start time. Active records are discarded after more than 35 seconds without an observation; failed requests never create stop or monitoring-gap entries. Completed records are unaffected by polling interruptions. Duration is elapsed observed time, including pauses, not watch time or media position. Short sessions between polls can be missed.

Activity is saved after each ten-second collection cycle when `ACTIVITY_DATA_DIR` is set. Docker/Compose defaults the directory to `/data/activity`, with records in `/data/activity/history.json`; mount a persistent volume at `/data` to retain history across container replacements. Both Compose examples above provide this mount, and history storage works independently of push notifications. For a standalone binary, set `ACTIVITY_DATA_DIR` to a private writable directory; leaving it unset uses memory only and logs a warning.

History files are replaced atomically with mode `0600` inside a directory created with mode `0700`. Startup rejects invalid or unwritable storage instead of silently clearing it. Retention is 30 days and 200 records. Startup removes legacy monitoring-gap entries and unfinished sessions from storage, retaining completed records; currently playing sessions get a fresh observed baseline. The latest unfinished collection cycle can be lost on an abrupt shutdown. Titles, usernames, and artwork references are retained on disk independently of the push-details preference. Protect the storage directory and `/api/activity` with the same care as the rest of the administrator dashboard. History already lost before persistence was enabled cannot be recovered.

System, Silo, Plex, and Nodes show warnings only when data is stale, unavailable, or still loading; healthy data has no update label. System age uses Silo's actual sample timestamp; session/node-list ages measure the last successful response, which can include up to 10 seconds of server caching. Resource data older than 15 seconds, session/node responses older than 35 seconds, failed requests, and offline connectivity are flagged stale. Browser requests time out after 10 seconds. Node health checks older than 45 seconds are shown as Unknown rather than Healthy.

Disk usage displays a usage bar with a large **free GiB** readout on the right, plus percentage and used/total capacity. Free space is derived from Silo's process-usable capacity minus used space, not nameplate capacity. Invalid metrics are omitted and per-disk stale readings are labelled. The current Silo resource API does not expose rclone cache size, so that optional metric is not displayed; Siloscope does not scan or modify the cache directory.

## Plex playback

To include Plex in the existing Playing screen, set both `PLEX_URL` and `PLEX_TOKEN` in `.env`, then recreate the monitor with your usual Compose files. The URL must be reachable from the monitor container; `localhost` refers to that container, not the Plex host. Use the Plex server URL, not `app.plex.tv` or the `/web` page. Prefer HTTPS when connecting over an untrusted network.

`PLEX_TOKEN` is your Plex API token, sent as the `X-Plex-Token` header. See [Plex's token instructions](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/) to obtain a server-owner token. Keep it in server-side configuration, never in the URL or browser settings.

The monitor reads `/status/sessions` every 10 seconds for activity history, with a shared 10-second server cache. The browser refreshes every 15 seconds while visible. Plex movies, episodes, and music appear beside Silo streams, labeled by source, with users, clients, playback method, progress, and paused state. Posters are proxied through the monitor so the token stays on the backend. Plex does not provide a reliable session start time here, so its cards show playback state instead.

The Playing badge counts sessions from both sources. Plex sessions do not affect Silo node routing or job counts. If either playback source fails, the other remains visible with an outage notice; unavailable sessions are removed until the source recovers. Leave both variables blank to disable Plex. Protect the monitor behind your existing authenticated proxy because it exposes playback activity and posters.

## Library replacements

Configure one or both URL/key pairs (`SONARR_URL`/`SONARR_API_KEY`, `RADARR_URL`/`RADARR_API_KEY`) to search existing managed titles in **Library**. URLs are service base URLs, optionally including their configured base path, not `/api/v3` URLs. Keys are sent only from the backend in `X-Api-Key` headers; redirects are refused. Movies search Radarr's existing catalogue; TV searches Sonarr series and then their episodes. No new titles are added.

On a shared Docker network, typical addresses are `http://sonarr:8989` and `http://radarr:7878`; use the actual service names and container ports in your installation. `localhost` refers to Siloscope's own container. Set these variables in `.env` or your stack manager's environment, then redeploy/recreate the same service; restarting alone does not apply changed environment variables. Search works with `ARR_REPLACEMENTS_ENABLED=false`.

TV also accepts `the simpsons s05e19` for the exact episode or `the simpsons s05` for that season, ordered by episode number. Matching is case-insensitive. An exact series title takes priority; otherwise a unique partial title resolves directly. Ambiguous titles show matching series first and retain the season/episode filter when you choose one. Only the selected series' episodes are fetched.

Replacements remain disabled until `ARR_REPLACEMENTS_ENABLED=true` and a writable persistent `REPLACEMENT_DATA_DIR` are configured. **First verify that all dashboard and API routes are accessible only to authenticated administrators through your reverse proxy.** Siloscope has no built-in authentication; same-origin request checks are CSRF protection, not authorization. Anyone with dashboard access can use its configured Arr credentials. Keep keys out of browser settings and logs.

**Replace** previews the exact filename, quality, and size. **Delete and search** rechecks the file and deletes only that movie file or single-episode file, then submits `MoviesSearch(movieIds)` or `EpisodeSearch(episodeIds)`. Shared multi-episode files are rejected. Movie, series, and episode records, profiles, and monitoring settings are not changed. Replacement is allowed during playback and may interrupt it; Siloscope does not stop the session first.

Deletion and search are not atomic. There will be downtime, and automatic search may find nothing or download the same release. No better/different release, download, import, or automatic rollback is guaranteed. Restore options depend on your Arr recycle-bin/backup configuration. Changes in Arr between final verification and deletion cannot be locked by Siloscope; avoid concurrent file-management actions on the same item.

Recent replacements distinguish search acceptance from command completion; neither confirms a downloaded/imported replacement. A known rejected search offers a **Retry search** action that never deletes again. If a deletion or search response is uncertain, inspect the item and queue in Arr directly; Siloscope will not automatically retry it. Restarted incomplete operations become uncertain and are not replayed. An unresolved operation blocks another replacement of that item, so resolve it directly in Arr. Do not delete the journal to bypass this protection.

The private atomic journal retains at most 100 operations; only failed-before-search or finished-search entries are evicted for space. Unresolved entries are not silently discarded. Storage failures disable further mutations. Catalogue responses are streamed one record at a time, limited to 64 MiB / 100,000 items, and cached as compact records for one minute; the browser debounces title search and displays 50 results at a time. Catalogue reads have a 30-second deadline to accommodate large libraries; preview and operation-status reads retain an eight-second deadline. Other Arr responses remain limited to 4 MiB. Operation polling runs only while Library is visible, at five-second intervals for up to two minutes, with manual refresh afterward. There is no background catalogue scan or download poller.

`REPLACEMENT_DATA_DIR` stores operation state in `operations.json`, not media files or Arr API keys. It must remain on persistent storage so incomplete operations stay blocked after a restart.

The integration uses the official Sonarr/Radarr v3 API contracts and was tested with fake servers, including exact-file deletion, targeted commands, shared-file rejection, unknown outcomes, and restart recovery. Read-only catalogue searches and exact TV episode/season searches were also verified against live services. Real replacement operations and reverse-proxy access controls have not been live-validated. No real media files were deleted during development.

## Terminate playback

In **Playing**, press the cross icon (**Select sessions to terminate**) to reveal selection circles. Select one or more Silo or Plex sessions, then press **Terminate (N)**. The request is sent immediately, with **no confirmation dialog**. Press the cross again or Escape to leave selection mode without stopping anything.

Each selected session receives its own termination request. Cards show pending, requested, or failed status independently; failed selections remain available for retry. Accepted requests are not proof that playback has ended: the card remains until the session disappears from a subsequent refresh. If a request times out or cannot be confirmed, check playback before retrying. Sessions without a usable termination identifier cannot be selected.

Silo termination uses its admin session-termination API; Plex uses its session-termination endpoint and the configured server-owner token. Server support and sufficient permissions are required. Credentials stay on the backend. Anyone with access to Siloscope can terminate playback, so restrict dashboard access to administrators through an authenticated reverse proxy.

## Transcoder settings

Settings > Transcoder edits Silo's server-wide transcoding configuration: transcoding and 4K permissions, hardware acceleration, hardware/software HDR tone mapping, throttling and buffers, and playback execution/egress routing. Chapter settings, GPU device selection, and FFmpeg/transcode directory paths are excluded.

Controls are grouped under **Transcoding**, **Hardware**, **Tone Mapping**, **Buffering**, and **Routing**, with the same headings and dividers used across Settings.

| Control | Available values |
| --- | --- |
| Transcoding | Enabled or disabled |
| Allow 4K transcoding | Enabled or disabled |
| Hardware acceleration | Auto, Intel Quick Sync (QSV), VA-API, NVIDIA NVENC, VideoToolbox (macOS), or Software |
| Hardware HDR tone mapping | Enabled or disabled |
| Software HDR tone mapping | Enabled or disabled |
| Throttle transcoding | Enabled or disabled |
| Buffer ahead | Seconds, 0 or greater |
| Back buffer | Seconds, either 0 or at least 120 |
| Direct play, remux, and video transcode egress | Prefer proxy, Proxy only, Prefer API server, or API server only; configured separately for each playback method |
| Remux and video transcode execution | Prefer worker, Worker only, Prefer API server, or API server only; configured separately for each playback method |

Execution controls where processing runs; egress controls where playback is delivered from. Hardware modes depend on the capabilities of the Silo host or execution node. Silo validates changes and reports unsupported values.

Toggles and dropdowns save automatically on change. Numeric fields save after a short typing pause or when committed, provided the value is valid. Rejected changes restore the previous value; unconfirmed saves require reloading settings before further edits. Display-preference resets never change Silo configuration. Restart notices come from Silo: saving is immediate, but settings marked as requiring a restart do not take effect until Silo is restarted. Saving never automatically restarts it. Missing fields are disabled rather than assigned guessed defaults. The Silo version must support the effective-settings, restart-keys, and batch settings APIs.

This makes the dashboard an administrative interface, not just a read-only wallboard. Anyone with access can change these server settings using its configured admin key. Keep the service bound to localhost or a trusted private network and require administrator authentication at your reverse proxy before exposing it. Same-origin checks prevent cross-site browser writes; they are not a substitute for authentication. Credentials remain on the backend, and only the listed settings can be read or written through this endpoint.

The top of every Settings view shows a banner when Silo reports a pending restart, including changes made outside Siloscope. Status refreshes every 15 seconds while Settings is visible and clears once Silo reports no pending restart. Known warnings remain visible during status outages. Individual controls that require a restart are labelled independently of the pending banner. Siloscope does not restart the server automatically.

The banner's **Restart now** button requests a graceful Silo restart after confirmation. Active playback may be interrupted. The button stays disabled while waiting for a new server start time; failed or unconfirmed requests show status feedback rather than silently retrying. Access to the dashboard therefore also grants the ability to restart Silo through its configured admin credentials.

## Display preferences

Playback cards include video resolution (4K, 1080p, 720p, or 480p), audio codec labels such as DTS, DTS-HD MA, DD, DD+, and TrueHD, and Silo's confirmed SW/HW tone-mapping mode. Source and output are shown together when they differ. Missing details are omitted; tone mapping is never inferred from hardware video acceleration. Plex audio profiles come from the selected audio stream when available.

Active playback also shows source-file bitrate in decimal Mbps when reported (50,000 kbps becomes 50 Mbps), not measured bandwidth or transcode output bitrate. Silo uses `source_bitrate_kbps`; Plex uses the selected original Media bitrate. Missing or invalid values are omitted. This detail is not stored in History and can be hidden in Playing > Media Details.

**Settings > Navigation** reorders all five bottom tabs with up/down controls. The default order is System, Playing, Library, Nodes, Settings. Order and the History filter are saved per device; resetting Navigation restores the default order without changing service settings.

The Settings tab has separate System, Playing, and Nodes menus, organized into labelled groups:

| Page | Groups |
| --- | --- |
| System | Layout, Graph Settings, Page Indicators, Disk Usage, CPU, Bandwidth, Memory |
| Playing | Page Indicators, Session Appearance, Media Details, Playback Details |
| Nodes | Page Indicators, Node Identity, Health & Activity, Resources |

Toggle whole panels or cards and individual details, including the CPU process list, session metadata, node statistics, and tab badges. Dependent controls are indented and disabled when their parent is hidden, without losing their saved selections. System's Layout group controls panel order; Graph Settings controls height and time window. Hidden content continues refreshing.

Display preferences save immediately in this browser on this device, not on the server. Reset tab restores one view; Reset all restores every display preference. Neither reset changes notification rules or Silo's transcoder settings. When browser storage is unavailable, changes apply for the current page only.

In **Settings > System > Graph Settings**, Time window selects 2, 3, 4, or 5 minutes for the CPU, memory, and network graphs. The default is 5 minutes. The full five-minute history is retained when selecting a shorter window, and graphs with fewer samples show the available history until the selected duration is filled. Height ranges from 100 to 220 pixels, with a default of 140 pixels.

Each main tab remembers its scroll position while switching views. Content scrolls independently of the bottom navigation, including in the iPhone Home Screen app.

## Build locally

```sh
docker build -t silo-monitor:local .
```
