# Ripperr

Keeps the torrents you already have in sync with the forum trackers they came from.

When a release on a tracker is re-uploaded (new episodes added to a season, a better rip, fixed files),
the topic page changes but your torrent client keeps seeding the old version. Ripperr watches those
topics for you and swaps the old torrent for the new one, without losing your save path, category,
tags or file selection.

- [How it works](#how-it-works)
- [Features](#features)
- [Supported trackers](#supported-trackers)
- [Mirror sync](#mirror-sync)
- [Getting started](#getting-started)
- [Configuration](#configuration)
- [Monitoring](#monitoring)
- [What Ripperr will not do to your downloads](#what-ripperr-will-not-do-to-your-downloads)
- [Building from source](#building-from-source)
- [License](#license)

## How it works

1. **Discovery.** Ripperr reads your torrent client (qBittorrent, Transmission or Deluge) and picks the torrents
   you watch: either by categories or by tags, one mode at a time (Deluge has no tags, so it watches by
   categories). Forum trackers write the topic
   URL into the torrent comment, so there is nothing to add by hand: your existing library is the
   watch list.
2. **Checks.** A few times a day (you choose the start hour and the number of passes) it opens each
   topic page and compares it with the torrent you hold: release date, download id or infohash,
   depending on the tracker. Fresh releases are checked often, old ones rarely.
3. **Replace.** When a topic has changed, Ripperr downloads the new `.torrent`, swaps it in for the
   old one in a fixed, recoverable order and carries over your previous file selection.
4. **Report.** Every check lands in the dashboard; updates and problems can go to Telegram, email,
   Pushover or a webhook.

## Features

- Zero setup per release: topics come from the torrent comments already in your client.
- qBittorrent (WebUI API), Transmission (RPC) and Deluge (Web UI JSON-RPC; Deluge has no tags and no forced re-download).
- Safe, ordered replace with crash recovery (see below), optional removal of old files per category.
- Keeps save path, category, tags and the per-file "download / skip" choices of the old torrent.
- Recheck schedule by release age; topics that keep failing are marked broken instead of being
  hammered forever (the pause between checks of a failing topic grows up to 24 hours), and you can put
  any release on an ignore list.
- Rechecks stay in the pass slot where they ran, so the check time does not creep through the day.
  After two or more updates at a similar time of day, a release is rechecked in the first slot after that time,
  and up to two next slots if the usual time varies.
- Tracker mirrors with automatic choice of a live domain and a switch to another mirror when the
  active one starts failing (you get a notification), FlareSolverr support for sites behind
  Cloudflare, optional download proxy (HTTP or SOCKS5).
- Mirror list upkeep: once a day Ripperr reads the Jackett definitions of your trackers, checks the
  hosts itself, adds new mirrors at the end of the list and suggests removing dead ones or changing
  the main domain (see [Mirror sync](#mirror-sync)).
- Magnet-only topics: the `.torrent` is fetched from the DHT and the topic link is written into it.
- Dry run: a full pass that touches nothing, to see what would be updated.
- Web UI in English and Russian, dark, light and OLED themes, works on phones.
- Notifications to Telegram (with the release poster), a generic webhook, email (SMTP), Pushover and browser push notifications (Web Push, one subscription per device, HTTPS only; iPhone and iPad only as a Home Screen app on iOS 16.4+),
  one set of events for all channels. Notification texts are English or Russian: one setting for all
  channels on the Integrations page (default English).
- Health probes and Prometheus metrics (see [Monitoring](#monitoring)).
- One account with a hashed password. Login is always required; the first account can be created only
  from a local address or an allowed host (see [Access](#access)).
- One container, one SQLite file, no external database.

## Supported trackers

| Tracker | Access | Enabled by default |
|---|---|---|
| RuTracker | account | yes |
| Kinozal | account | yes |
| Rutor | public | yes |
| TeamHD | cookies | no |
| NNM-Club | cookies | no |
| Toloka | account | no |
| Tapochek | account | no |
| Riper.AM | account | no |
| BookTracker | account | no |
| AniDub | account | no |
| AnimeLayer | account | no |
| Selezen | account | no |
| Rustorka | account | no |
| AniLiberty | public | no |
| NewStudio | public | no |

On the Trackers page you add the trackers you use (one per type) and remove the ones you do not; a
removed tracker loses its settings, login, password and cookies after a confirm, while the release
history and the torrents in the client stay. Every added tracker can be switched on, tested and tuned
(mirrors, credentials, cookies, hourly request limit). The main domain starts as the configured one
(the type default). You cannot type another one: add the address as a
mirror, then pick that mirror as the main domain from the list (the old main domain moves to the mirrors).

### Mirror sync

Once a day (the first run 2 minutes after start) Ripperr reads the public Jackett definitions of the
trackers you added and probes the hosts itself. The definitions come from the source chosen in
Integrations (card "Mirror source", shown in advanced mode): GitHub by default, jsDelivr, or your own HTTPS URL with `{path}`
in it. The download goes through the download proxy if you set one, unless "Direct connection" is on.
There is no fallback between sources: if the chosen one is blocked, pick another and press Test.

- New mirrors from Jackett are appended to the end of the list, at most 3 per tracker per run. The
  main domain and your old mirrors are always tried first.
  Jackett hosts with local names (`localhost`, `local`, `lan`, `internal`, `intranet`, `corp`, `home`,
  `private`, `localdomain`, `*.arpa`) are ignored.
- A mirror that does not answer for 3 runs in a row, or that Jackett lists as legacy, is suggested for
  removal. A change of the main domain (a redirect, a legacy or a dead main host) is always a
  suggestion that you apply and save yourself, and you get a notification (event "Tracker mirrors", the same event as a switch to another mirror).
- A removed host is kept as `retired`: torrents whose topic link uses it are still checked. It is
  dropped once no torrent in the client and no history record uses it.
  If you save a tracker dialog that still shows a mirror the job has retired meanwhile, it stays
  retired and the dialog tells you so.
- Setting **Confirm mirror changes** (Integrations, card "Mirror source", shown in advanced mode; off by
  default). Off: new mirrors are used at once and dead ones are removed automatically. On: an automatically
  added mirror is not used until you confirm it, and if only such a mirror answers, the tracker pauses and
  asks; dead mirrors are suggestions.

Off, the default, is a risk you accept. Your login and password go to every address of a tracker. If the
proxy breaks or every real host is blocked, a mirror taken from a third-party file can answer first,
receive the credentials and serve a swapped `.torrent`, and the replace then deletes the old torrent
and its files. Turn the setting on unless you trust the mirror source. Every automatic change is listed
in the tracker dialog.

## Getting started

Ripperr needs a running qBittorrent, Transmission or Deluge it can reach over the network.

```bash
docker build -t ripperr .
```

```bash
docker run -d --name ripperr \
  -p 6410:6410 \
  -v ./config:/config \
  ripperr
```

Open `http://<host>:6410`, create the account on the first visit, then:

1. **Integrations:** pick the torrent client (qBittorrent, Transmission or Deluge), enter its address and
   login, and press Test. Add the notification channels you want (Telegram, webhook, email, Pushover,
   Browser) and tick the events: one set is shared by all channels.
2. **Trackers:** add the trackers you use, enable them and enter credentials or cookies; the test
   button shows whether login works.
3. **Settings:** choose how to watch torrents, by categories or by tags (Deluge: categories only), and set
   the check schedule.

Run a dry pass from the dashboard first to see what Ripperr would do.

## Configuration

Environment variables cover startup and infrastructure only. Everything else (the torrent client,
schedule, categories and tags, deletion, mirrors, proxy, FlareSolverr, notifications, allowed hosts and
App URL) is set in the web UI and stored in the database.

| Variable | Default | Meaning |
|---|---|---|
| `CONFIG_DIR` | `/config` | where the database and the `logs/` folder live |
| `DB_PATH` | empty | path of the SQLite file; empty means `ripperr.db` in `CONFIG_DIR` |
| `DB_SECRETS_KEY` | empty | base64 of 32 bytes; encryption key for secrets in the database. If empty, the key is read from `secrets.key` next to the database file (created on first start). See [Secrets at rest](#secrets-at-rest) |
| `BLACKHOLE_DIR` | empty | folder for the safety copies of `.torrent` files; empty means `blackhole` in `CONFIG_DIR` |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `WEB_LISTEN` | `:6410` | listen address; outside Docker the port is a default that Settings → Access overrides |
| `WEB_ENABLED` | `true` | `false` runs the checks without the web server |
| `RIPPERR_IN_DOCKER` | `false` (`1` in the image) | set by the image: the port is fixed and the Settings → Access port field is hidden |
| `WEB_TRUSTED_PROXY` | `false` | trust `X-Forwarded-For` from a reverse proxy; set it only when every request comes through the proxy |
| `RUNNER_HTTP_TIMEOUT` | `30` | seconds to wait for a tracker page |
| `RUNNER_DOWNLOAD_TIMEOUT` | `240` | seconds to wait for a `.torrent` download |
| `RUNNER_TLS_PROFILE` | `chrome_131` | TLS fingerprint profile of the `.torrent` downloads |
| `RUNNER_CB_RETRIES` | `2` | failures in a row before the circuit breaker of a tracker opens |
| `RUNNER_CB_MINUTES_TO_RETRY` | `60` | minutes before an open circuit breaker lets the next request through |
| `MANAGER_MINUTES_TO_RECONNECT` | `5` | minutes between attempts to reach the torrent client after a failure |
| `MANAGER_PAUSED_MAX_RATIO` | `2` | share ratio limit given to the replacement of a paused, finished torrent (no UI setting) |

`WEB_LAN_BYPASS` no longer exists: any value is ignored. Remove it from your run line. Devices that used
the bypass sign in once.

### Logs

Every log line goes to stdout and to `/config/logs/ripperr.log`. The file rotates at 5 MB and Ripperr keeps `ripperr.log.1` to `ripperr.log.3` (up to 20 MB in total). With `LOG_LEVEL=debug` the debug lines are written too. Crash traces (panic, fatal error) also go to the file. After a restart the logs page shows the tail of the file.

### Access

- Until the account exists, every `/api` path except `/api/auth/state`, `/api/auth/setup`, `/api/auth/login` and
  `/api/auth/logout` answers `401`. The first account can be created only from a local address, `localhost`,
  a name without a dot, `.local`, `.lan`, `.home.arpa`, `.internal` or an allowed host; other `Host` values get `403`.
  A request whose `Host` is a bare IP without a port is refused too: that is what an HTTPS reverse proxy such as
  KeenDNS sends. Create the account on the LAN at `ip:6410` before you expose the port.
- The session lasts 30 days and is extended once a day while you use it. Sign out ends the sessions on
  all devices.
- Change the password in Settings, card Password: the current password and the new one twice (at least 8
  characters, as at setup). The change signs out every other device; this browser stays signed in. A wrong
  current password counts as a failed login attempt for the limit below.
- The session cookie is `Secure` over HTTPS, also behind a reverse proxy that ends TLS (the browser's
  `Origin` or `Referer` shows the scheme). Over HTTPS the cookies are named `__Host-ripperr_session` and
  `__Host-ripperr_device`: the browser keeps them only for the exact host you opened, so a neighbour
  subdomain cannot set them. Over plain `http` the names stay `ripperr_session` and `ripperr_device`. A browser
  that still has the old `ripperr_*` cookies is moved to the new names on its next request over HTTPS, and
  stays signed in.
- Every API call that changes data (not `GET`, `HEAD`, `OPTIONS`) needs the header `X-Ripperr: 1`; a request
  that the browser marks `Sec-Fetch-Site: same-site` or `cross-site` is refused with `403`. The web UI sends
  the header itself. A script or `curl` must add it, for example `-H 'X-Ripperr: 1'`.
- 5 failed login attempts lock login for 24 hours. There is no unlock button; the counters live in memory, so
  a container restart clears them. A browser that has signed in gets the device cookie `ripperr_device`
  (`__Host-ripperr_device` over HTTPS, 400 days): it has its own counter, so a lock caused by someone else from the same address does not block
  it, and its own 5 failures lock only this browser. A browser that is already signed in gets the cookie with
  its next request; such browsers share one counter until their next sign-in gives each its own. Browsers
  without the cookie share the counter of their client address; behind Docker Desktop port publishing that is
  one address for all of them. Sign out keeps the device cookies; a password change revokes them, and other
  browsers count by address until they sign in again. When 1024 addresses are already counted, a new address
  waits. At most 2 password checks run at once; more get `429`. Set `WEB_TRUSTED_PROXY=true` only when every
  request comes through the proxy, otherwise a client can fake the address.
- Browser notifications (Web Push) need a page opened over HTTPS, for example the HTTPS domain of your router's reverse proxy (KeenDNS). On the plain `http://` LAN address the browser does not offer push. Open Integrations → Browser on each device and press "Enable on this device": every device subscribes itself, and the list shows the devices with a delete button. iPhone and iPad get push only from a Home Screen app (iOS 16.4 or newer): in Safari use Share → Add to Home Screen, open Ripperr from the icon, sign in there and enable the notifications. The App URL is not required for push; it is only used for links in the other channels. An expired device (the push service answers 404 or 410) is removed by itself.
- Pages cannot be put in a frame (`X-Frame-Options: DENY`).

### Web access settings

Settings → Access has three fields. They are stored in the database; the port replaces the port of `WEB_LISTEN`.

- Allowed hosts: the domain names you reach Ripperr by through a reverse proxy (no scheme, no port).
- App URL: the external address with scheme (for example `https://ripperr.example.com`). Notifications link to it; empty means no links.
- Port: a change applies at once. The new port is opened, the answer is sent and the old port is closed.
  A port that is taken gives an error and nothing changes. Outside Docker only.
- In Docker the port is always `6410`. Choose the host port with `-p <host>:6410`; a stored port is ignored.
- Outside Docker, if the saved port cannot be opened at start, Ripperr logs a warning and listens on the
  `WEB_LISTEN` port.
### Secrets at rest

Tracker logins, passwords and cookies, and everything you save in the UI (client passwords, notification tokens, the account hash), are stored encrypted in the database (AES-256-GCM). The history, the check log and the ignore list stay plain.

- The key is `DB_SECRETS_KEY` if set, otherwise the file `secrets.key` next to the database file: `/config/secrets.key` by default, or the directory of `DB_PATH` if you set it. The file is created on the first start.
- Back up the key separately from the database. A database copy without the key is useless, and a key stored next to the database in the same volume protects only copies of the database file alone. Use `DB_SECRETS_KEY` to keep the key elsewhere.
- Move a file key to the variable: put the content of `secrets.key` into `DB_SECRETS_KEY`, then remove the file. A new random key in the variable is refused for an encrypted database.
- If the key is missing or wrong, Ripperr refuses to start and changes nothing in the database. It never creates a new key for an encrypted database.
- `GET` answers of the web API never contain stored secrets: the UI shows `********` instead. The eye button on a secret field loads the stored value on click (`POST /api/integrations/secret`, `POST /api/trackers/{key}/secret`) and shows it. These calls work only for a logged-in client, and the answer is not cached. Cookies and URL fields have no eye and are not revealed. Saving the field unchanged keeps the stored value. Clear the field to remove a secret. The value `********` itself cannot be used as a secret.
- Downgrade to a build without encryption: stop the container, then run `docker run --rm -v ./config:/config ripperr /app/main decrypt-secrets` (add `-e DB_SECRETS_KEY=...` if you use the variable).
- Rotate the key: run `decrypt-secrets` with the old key, delete `secrets.key` (or change `DB_SECRETS_KEY`) and start. A new key is created and the database is encrypted again.
- Lost key: the secrets cannot be recovered. With the container stopped, run `sqlite3 ripperr.db "DELETE FROM settings; UPDATE trackers SET credentials='', cookies=''; PRAGMA user_version=1;"` in the config directory, then start. Ripperr makes a new key and you enter the settings again; the history is kept.
- The database file is `ripperr.db` in the config directory. On the first start of this version the key check row is re-sealed with a new value (log line `secrets: key check re-sealed, older builds can no longer open this database`). Older builds then refuse the database with `secrets: key from <source> does not match this database`. To go back, restore the backup taken before the upgrade, or run `decrypt-secrets` with the new build first.
- FlareSolverr is not part of Ripperr and logs each request body at INFO, including the login form (login and password) of trackers that log in through it. To keep it out of the FlareSolverr log, start FlareSolverr with `LOG_LEVEL=warning` (see [Environment variables](https://github.com/FlareSolverr/FlareSolverr#environment-variables); the default is `info`).

## Monitoring

- `GET /healthz` always answers `200` with `{"status":"ok"}` and does not touch the database.
- `GET /readyz` answers `200` when the database responds and `503` when it does not. The Docker image
  uses it for its health check.
- `GET /metrics` returns Prometheus metrics. It answers `404` until you set a token in the UI
  (Integrations → Prometheus metrics, a card shown in advanced mode). After that send it as `Authorization: Bearer <token>`; a missing
  or wrong token gets `401`.

The probes need no login. The metrics are `ripperr_checks_total{tracker,result}` (checks by tracker and
result), `ripperr_notifications_total{channel,result}` (sent and failed notifications by channel) and
`ripperr_tracker_circuit_open{tracker}` (1 while the circuit breaker of an enabled tracker is open).
The counters start from zero after a restart.

## What Ripperr will not do to your downloads

Replacing a torrent that is being seeded is the risky part, so the order is fixed:

- Before anything changes, the new `.torrent` and the details of the replace are written to disk.
  If the process or the client dies halfway, the next start finds this record and finishes the job,
  so you never end up with neither version.
- If the old files stay, the new torrent is added **stopped** first, the old torrent is removed after
  that, and the new one starts last.
- If the old files are to be removed, the old torrent goes first and Ripperr waits until the client
  confirms it is gone before adding the new one. Otherwise deleting the old files could take files
  the new version shares with it.
- Old files are removed only when deletion is enabled and the torrent's category is on the list
  you allowed.
- The file selection of the old torrent is read before any change; if that fails, the update is
  skipped and retried on the next pass.
- When the topic still points to the same infohash, nothing is touched even if the page date moved.

## Building from source

Go 1.25 or newer.

```bash
go build -o ripperr ./cmd
```

```bash
go test ./...
```

The UI is plain HTML, CSS and ES modules in `internal/web/static`, embedded into the binary; there is
no frontend build step. A deeper description of the internals (in Russian) is in
[ARCHITECTURE.md](ARCHITECTURE.md).

## License

GNU Affero General Public License v3.0. If you run a modified version as a network service, you
must offer its source to the users of that service.

Ripperr does not host or index any content. It only works with torrents you already have, on
trackers where you have access, and you are responsible for what you download.
