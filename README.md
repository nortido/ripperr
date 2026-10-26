<div align="center">

# Ripperr

Keeps the torrents you already have in sync with the forum trackers they came from.

</div>

---

## What is Ripperr?

When a release on a forum tracker is re-uploaded (new episodes, a better rip, fixed files), the topic
page changes, but your torrent client keeps the old version. Ripperr checks those topics a few times a
day and replaces the old torrent with the new one. Save path, category, tags and file selection stay
the same.

There is nothing to add by hand. Forum trackers write the topic link into the torrent comment, so
Ripperr takes the watch list straight from your client: the torrents in the categories or with the
tags you choose.

It works with qBittorrent, Transmission and Deluge. Updates and errors can go to Telegram, email,
Pushover, a webhook or browser push notifications.

## Features

- Replace in a safe order, with recovery after a crash.
- Old files are removed only if you allow it, and only for the categories you pick.
- Fresh releases are checked often, old ones rarely. A topic that keeps failing goes to the ignore list.
- Tracker mirrors: Ripperr picks a working domain and switches to another one when it fails.
- FlareSolverr for sites behind Cloudflare, HTTP or SOCKS5 proxy for downloads.
- Topics with a magnet link only: Ripperr gets the `.torrent` from peers, found through the trackers in the magnet link.
- Dry run: a full pass that changes nothing.
- Web UI in English and Russian, works on phones.
- One container, one SQLite file.

## Supported trackers

| Tracker | Access |
|---|---|
| RuTracker | account |
| Kinozal | account |
| Rutor | public |
| TeamHD | cookies |
| NNM-Club | cookies |
| Toloka | account |
| Tapochek | account |
| Riper.AM | account |
| BookTracker | account |
| AniDub | account |
| AnimeLayer | account |
| Selezen | account |
| Rustorka | account |
| AniLiberty | public |
| NewStudio | public |

## Quick start

You need a running qBittorrent, Transmission or Deluge that Ripperr can reach over the network.

```bash
docker build -t ripperr .
```

```bash
docker run -d --name ripperr \
  -p 6410:6410 \
  -v ./config:/config \
  ripperr
```

Open `http://<host>:6410` and create the account. This works only from your local network, so do it
before you open the port to the internet. Then:

1. Integrations: connect the torrent client and the notification channels.
2. Trackers: add your trackers and enter the login or cookies.
3. Settings: choose categories or tags to watch and set the schedule.

Run a dry pass from the dashboard first to see what Ripperr would change.

Browser push needs HTTPS. On iPhone and iPad it works only from a Home Screen app (iOS 16.4 or newer).

## Configuration

Almost everything is set in the web UI. Values you save there win over environment variables.

| Variable | Default | Meaning |
|---|---|---|
| `CONFIG_DIR` | `/config` | folder for the database, logs and the secrets key |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `DB_SECRETS_KEY` | empty | key for the secrets in the database; empty means the file `secrets.key` |
| `WEB_TRUSTED_PROXY` | `false` | trust `X-Forwarded-For`; turn on only if every request comes through your reverse proxy |

## Backups

Logins, passwords and tokens are stored encrypted. The key is the file `secrets.key` in the config
folder, or `DB_SECRETS_KEY` if you set it. Back up the key together with `ripperr.db`: without the key
the secrets cannot be recovered.

To go back to a build without encryption, stop the container and run:

```bash
docker run --rm -v ./config:/config ripperr /app/main decrypt-secrets
```

## Monitoring

`/healthz` and `/readyz` work without login. `/metrics` returns Prometheus metrics after you set a token
in Integrations (advanced mode).

## Building from source

Go 1.25 or newer.

```bash
go build -o ripperr ./cmd
go test ./...
```

## License

GNU Affero General Public License v3.0.

Ripperr does not host or index any content. It works only with torrents you already have, on trackers
where you have access. You are responsible for what you download.
