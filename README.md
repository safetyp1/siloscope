# Siloscope

A mobile-friendly dashboard for monitoring Silo, viewing Silo and Plex playback, and managing media replacements with Sonarr and Radarr.

## What you can do

| When you need to... | Siloscope provides... |
| --- | --- |
| Spot server pressure before it affects playback | CPU, memory, bandwidth, disk usage, free space, and top-process views |
| See who is watching and troubleshoot a stream | Combined Silo/Plex playback, progress, client details, quality, bitrate, and playback method |
| Review recent viewing activity | Playback history with Silo/Plex filters, users, durations, and timestamps |
| Check playback infrastructure | Node health, resource usage, routed streams, and jobs |
| Find a problematic movie or episode | Sonarr/Radarr title search, exact episode search, and season browsing |
| Replace an unwanted media file | File preview, release blocklisting, replacement search, and operation status |
| Know when something needs attention | Push alerts for CPU, disk usage, playback, transcoding, outages, and recovery |
| Adjust playback behavior | Silo transcoder settings, hardware acceleration, HDR tone mapping, and restart controls |
| Tailor the dashboard to each device | Reorder tabs, choose visible details, and adjust charts |

## Compatible with

| Silo | Plex |
| --- | --- |
| <a href="https://github.com/Silo-Server/silo-server"><img src="docs/images/silo-logo.png" alt="Silo logo" width="64" height="64"></a> | <a href="https://www.plex.tv/"><img src="docs/images/plex-logo.svg" alt="Plex logo" width="64" height="64"></a> |
| Resource and node monitoring, playback, and transcoder settings | Optional playback monitoring and session termination |

Logos identify compatible services; no affiliation or endorsement is implied. See [logo sources](docs/images/LOGOS.md).

Sonarr and Radarr are optional integrations for searching and replacing existing library items.

## Requirements

- Silo with the **v2 administrator API** and an unscoped API key owned by an enabled administrator. Older v1-only servers are not supported.
- Docker on AMD64 or ARM64. Host bandwidth and process monitoring require a Linux Docker host.
- Optional: a Plex server-owner token, Sonarr/Radarr API keys, and HTTPS for push notifications.

**Restrict access to trusted administrators through an authenticated reverse proxy.** Siloscope has no built-in login. Dashboard access can allow stopping playback, changing settings, restarting Silo, and deleting media files when replacements are enabled.

## Screenshots

Some screenshots show an earlier interface.

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

1. Copy [.env.example](.env.example) to `.env`.
2. Set `SILO_URL` to the Silo base address reachable from the container, without an `/api/v2` suffix.
3. Set `SILO_API_KEY` to an unscoped API key owned by an enabled Silo admin.
4. On the Linux Docker host, run `ip route show default`. Set `HOST_NETWORK_INTERFACE` in `.env` to the interface shown after `dev`, such as `eth0` or `enp0s6`.
5. Add optional integrations from the [configuration table](#configuration).
6. For push notifications, set `PUSH_CONTACT` to your real `mailto:` address or HTTPS contact URL. This is a sender contact, not a recipient.
7. Start the service with host bandwidth and process monitoring:

   ```sh
   docker compose -f compose.yaml -f compose.host-network.yaml -f compose.host-processes.yaml up -d
   ```

Use this same command after changing `.env`; a container restart alone does not apply environment changes. For image updates, pull the published image first, then recreate with the same files:

```sh
docker compose -f compose.yaml -f compose.host-network.yaml -f compose.host-processes.yaml pull
docker compose -f compose.yaml -f compose.host-network.yaml -f compose.host-processes.yaml up -d
```

Open `http://127.0.0.1:8091`. Keep `.env` and API keys out of source control. Service URLs must be reachable from the container; `localhost` refers to the container itself.

## Run with Portainer

Set `SILO_URL`, `SILO_API_KEY`, and `HOST_NETWORK_INTERFACE` in your stack's environment variables. Add optional integrations as needed.

<details>
<summary>Complete Portainer stack</summary>

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

</details>

Deploy the stack. Apply later configuration changes with **Update the stack**.

### Preserve existing data

Keep `/data` writable and persistent to retain history, notifications, and replacement records. Back it up securely. **Do not delete the volume or run `docker compose down -v` during updates.**

When moving or renaming a stack, reuse its existing volume. For example:

```yaml
volumes:
   notification-data:
      external: true
      name: silo-performance_notification-data
```

Replace the example name with your actual volume name and keep the service mount as `notification-data:/data`.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `SILO_URL` | required | Silo base URL reachable by the monitor container |
| `SILO_API_KEY` | required | Silo admin API key |
| `PLEX_URL` | unset | Optional Plex Media Server base URL, usually `http://plex:32400` |
| `PLEX_TOKEN` | unset | Plex server-owner token; required with `PLEX_URL` ([find your token](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/)) |
| `SONARR_URL` / `SONARR_API_KEY` | unset | TV library integration; configure together, usually `http://sonarr:8989` |
| `RADARR_URL` / `RADARR_API_KEY` | unset | Movie library integration; configure together, usually `http://radarr:7878` |
| `ARR_REPLACEMENTS_ENABLED` | `false` | Explicit opt-in to destructive Library actions; requires administrator-only proxy access |
| `REPLACEMENT_DATA_DIR` | `/data/replacements` in Docker/Compose | Persistent replacement records; required when replacements are enabled |
| `LISTEN_ADDR` | `:8080` | Address used by the Go server |
| `MONITOR_BIND` | `127.0.0.1` | Host address published by Compose |
| `MONITOR_PORT` | `8091` | Host port published by Compose |
| `HOST_NETWORK_INTERFACE` | `eth0` | Host NIC used by the optional bandwidth override |
| `HOST_NETWORK_STATS_DIR` | unset | Host network statistics path; supplied by the network override |
| `HOST_PROC_DIR` | unset | Host process statistics path; supplied by the process override |
| `PUSH_CONTACT` | unset | Enables Web Push when set to a real `mailto:` address or HTTPS contact URL |
| `PUSH_DATA_DIR` | `/data/push` in Compose | Persistent notification data; required when push is enabled |
| `ACTIVITY_DATA_DIR` | `/data/activity` in Docker/Compose | Persistent playback history; set explicitly for standalone binaries |

The image is published for AMD64 and ARM64 at `ghcr.io/jasjeetsuri/silo-monitor:latest`.

## Push notifications

Choose the alerts you want under **Settings > Notifications**:

| Alert | Scenario |
| --- | --- |
| CPU | Sustained high usage and recovery, with configurable thresholds and cooldown |
| Disk | Usage exceeds your selected threshold |
| Playback | A new Silo or Plex session starts |
| Transcoding | A new session is transcoding at its one-time check around one minute after discovery; includes audio-only transcoding |
| Service | Silo, Plex, or an enabled node becomes unavailable or recovers |

Set `PUSH_CONTACT`, open Siloscope over HTTPS, and enable notifications on each device. On iOS 16.4 or later, first add Siloscope to the Home Screen and open it there. Titles and usernames are optional notification details.

Alerts can arrive with the dashboard closed. Delivery depends on connectivity and device permissions; Siloscope cannot alert while it is down.

## Monitoring and playback

- **System:** resource charts, free disk space, top CPU processes, and the Silo API server build.
- **Playing:** Silo and Plex sessions together, including users, clients, progress, resolution, audio details, source-file bitrate, and direct-play/transcode status.
- **History:** recent activity with All/Silo/Plex filters and expandable session details. Retains up to 200 records within 30 days; durations represent observed sessions, not exact watch time.
- **Nodes:** playback-node health, resource usage, routed streams, and jobs. Stale or unavailable data is clearly marked.

## Find and replace media

**Library** searches titles already managed by Sonarr or Radarr; it does not add new titles. Search is available without enabling replacements.

| Search | Result |
| --- | --- |
| Movie or series title | Matching library items |
| `the simpsons s05e19` | The exact episode |
| `the simpsons s05` | Episodes from season five |

If multiple series match, choose one to continue.

Enable `ARR_REPLACEMENTS_ENABLED=true` to use **Replace**. Review the file and release, then confirm **Delete and search** to delete the file, blocklist its release, and search for a replacement. Recent operations show progress and offer **Retry search** when available.

An individually stored episode from a season pack can be replaced: only that episode's file is deleted, the whole pack release is blocklisted, and a replacement search is requested for that episode.

**Replacement can interrupt playback and does not guarantee a new or better file.** Files containing multiple episodes and files without identifiable download history must be handled directly in Arr. If an operation is uncertain, check Arr before retrying; do not clear replacement records to bypass the warning.

## Terminate playback

Select one or more Silo or Plex sessions in **Playing**, then choose **Terminate**. This acts immediately without a confirmation dialog.

## Settings

- **Transcoder:** control transcoding and 4K permissions, hardware acceleration, HDR tone mapping, buffering, throttling, and playback routing.
- **Restart:** see pending restart requirements and restart Silo after confirmation. Restarting can interrupt playback.
- **Display:** choose visible panels and playback details, reorder all five tabs, and adjust chart height and time range. Preferences are saved per device.
