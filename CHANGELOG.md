# Changelog

All notable changes to Ripperr. Versions follow [Semantic Versioning](https://semver.org).

## [2.7.2] - 2026-10-29

### Changed
- Lighter load on a shared FlareSolverr: Ripperr sends at most one request to it at a time for all trackers, so Prowlarr and other apps are not starved.
- The FlareSolverr browser session is recreated every 6 hours to free memory. No new password login is needed.

## [2.7.1] - 2026-10-29

### Fixed
- Forced checks survive a restart: releases marked for a forced check are checked right after start instead of waiting for the next scheduled pass.

## [2.7.0] - 2026-10-28

### Added
- Free disk space check before a replace. If the new version does not fit, the old torrent stays, the new `no_space` notice is sent (on by default) and the replace is tried again on the next check. Unknown free space never blocks a replace.
- Synology Download Station client (reduced mode): no tags, a replace downloads the new version in full and keeps old files, no redownload, no delete with files.

### Fixed
- The "Last check" column header lines up with the times below it.

## [2.6.1] - 2026-10-28

### Changed
- Shorter and up-to-date code comments.
- README with a dashboard screenshot, badges, a changelog and a comparison with other tools.

## [2.6.0] - 2026-10-28

### Added
- Mute button for a release in Telegram notifications.
- Pagination in the browser with a "to first page" button.
- Category colors kept in settings.
- Delete a release together with its files.
- Multi-arch Docker image (amd64 and arm64): `make build-multi`, `make push-multi`.

### Changed
- New Telegram message format: bold title, release name, tracker and category, link buttons.
- The Telegram mute button is hidden while another poller or a webhook holds the same bot (409), and comes back after the next successful poll.
- Clearing force removes a release from the forced queue at once.
- "Stop" replaces "restart" for a running check.
- Shorter running status in the header.

### Removed
- Unused magnet replace path in the torrent manager.

### Fixed
- Clearing force for all releases during a tracker rate-limit pause no longer keeps the status at "Checking" until the pause ends.
- Duplicate `Urgency` header in browser push requests.

## [2.5.0] - 2026-10-26

### Added
- Add a release by topic URL, delete releases.
- Polling for new releases and a "new" category.
- Watch torrents by categories or by tags.
- Onboarding checklist and a short tour.
- Notification center with event history.
- Browser push notifications.
- Password change, login lockout, sign-in with cookies.
- Schedule on a slot grid and a forecast of the next release time.
- Log file with a tail view, `decrypt-secrets` command.

### Changed
- Safer torrent replace with rename chains and late torrents.
- Pause on tracker 5xx errors.

### Security
- CSRF and SSRF protection.

## [2.4.0] - 2026-10-19

### Added
- Mirror rotation, pause on failures, check backoff.
- Mirror sync from a source with confirmation of new mirrors.
- Several instances of one tracker and custom trackers.
- Trackers page with drag and drop and a name filter.
- App icons, manifest and the Inter font.

### Security
- Secrets in the database are encrypted, masked in the API, session-only login.

## [2.3.0] - 2026-10-07

### Added
- Deluge client.
- Email and Pushover notifications.
- Prometheus metrics.
- Trackers: AniDub, AniLiberty, AnimeLayer, BookTracker, NewStudio, Riper.AM, Rustorka, Selezen, Tapochek, Toloka.

### Changed
- Replace adds the new torrent stopped and keeps the file selection.
- Project renamed to Ripperr, license AGPL-3.0.

## [2.2.0] - 2026-08-28

### Added
- Transmission client with its own magnet metadata fetcher.
- HTTP and SOCKS5 proxy for trackers, Telegram and mirrors.
- Release poster in Telegram.
- Highlight of the release being checked and the queue.
- Tracker registry and a versioned database schema.

### Changed
- Notification is sent when the torrent is really replaced.
- Replace is skipped when the info hash did not change.

## [2.1.0] - 2026-08-16

### Added
- Notifications with retry.
- Auto-ignore for broken topics, filters by tracker and category.
- Dry run and a stop flag.
- Forced re-download of a torrent, health check.
- Login for the web UI.
- Blackhole mode for safe replace.

### Changed
- Storage moved from PostgreSQL to SQLite.

## [2.0.0] - 2026-08-11

### Added
- Web UI.
- Force check, schedule, tracker mirrors.

## [1.4.1] - 2026-08-10

### Changed
- Large internal refactoring.

## [1.4.0] - 2026-08-08

### Added
- `.torrent` built from a magnet link for RuTracker, TLS client.

## [1.3.0] - 2026-06-11

### Added
- Tracker sessions.

## [1.2.0] - 2025-06-24

### Added
- Settings from a `.env` file.

### Fixed
- Kinozal domain and date parsing.

## [1.1.0] - 2024-07-04

### Added
- Tracker settings in environment variables.
- Download link parsing for NNM-Club.
- Ratio for updated torrents.
- Smaller Docker image.

## [1.0.0] - 2024-06-29

### Added
- Full update cycle: check the topic date, download the new `.torrent`, replace the old one in qBittorrent.
- Trackers: RuTracker, Kinozal, Rutor, TeamHD, NNM-Club.
- FlareSolverr, circuit breakers, per-tracker limits.
- Logger and Makefile.
