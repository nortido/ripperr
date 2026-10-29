<div align="center">

# 📽️ Ripperr 📽️

![License](https://img.shields.io/badge/license-AGPL--3.0-blue)
![Release](https://img.shields.io/badge/release-v2.7.3-2f9e44)

![Backend](https://img.shields.io/badge/backend-Go%201.25-00ADD8?logo=go&logoColor=white)
![Frontend](https://img.shields.io/badge/frontend-vanilla%20JS-F7DF1E?logo=javascript&logoColor=black)
![Database](https://img.shields.io/badge/database-SQLite-003B57?logo=sqlite&logoColor=white)
![Docker](https://img.shields.io/badge/docker-amd64%20%7C%20arm64-2496ED?logo=docker&logoColor=white)

![Trackers](https://img.shields.io/badge/trackers-15-4c6ef5)
![Clients](https://img.shields.io/badge/clients-4-4c6ef5)
![Notifiers](https://img.shields.io/badge/notifiers-5-4c6ef5)
![UI](https://img.shields.io/badge/UI-EN%20%7C%20RU-2f9e44)

[Русский](README.ru.md) · [Changelog](CHANGELOG.md)

Your torrents from forum trackers stay fresh by themselves.

New episodes, extra audio tracks, a new game version: when the release on a forum tracker is
updated, Ripperr replaces it in your torrent client. You do nothing.

<img src="docs/dashboard.png" alt="Ripperr dashboard" width="900">

</div>

---

## Why Ripperr

Forum trackers update releases in place. The topic says "torrent file updated, download it again
and recheck the task in your client", but your client keeps seeding the old one, and you only
notice when the season is over. Ripperr downloads the new `.torrent` and does the recheck for you.

## Keep your stack, add Ripperr

Ripperr does not replace Sonarr, Radarr or whatever else fills your client. They find and grab
releases. They do not follow a forum topic that gets new episodes in the same torrent: Sonarr
sees an already imported download and moves on. Ripperr closes that gap. Whatever lands in the
watched categories or tags, from Sonarr, Radarr, Prowlarr or your own hands, stays up to date.

Topic watchers like Monitorrent or TorrentMonitor do the update job too, but they start with a
list of topic links that you fill in and keep up to date by hand. Ripperr starts with your
torrent client instead:

- **No list to keep.** Trackers write the topic link into the torrent comment. Ripperr reads it
  from the torrents in the categories or tags you pick. Add a torrent to the client and it is
  watched.
- **Replace without surprises.** Save path, category, tags and your file selection carry over to
  the new version. The new torrent is checked before the old one goes away, and a crash in the
  middle is recovered on the next start. Old files are deleted only where you allow it. If the new
  version does not fit on the disk, the old torrent stays and you get a notice. Ripperr tries again
  on the next check.
- **Works where trackers fight back.** Cloudflare through FlareSolverr, dead domains through
  mirror switching, magnet-only topics through peers.
- **Easy to run.** One container, one SQLite file, a web UI that works on a phone.

## Supported trackers

RuTracker, Kinozal, Rutor, NNM-Club, TeamHD, Toloka, Tapochek, Riper.AM, BookTracker, AniDub,
AnimeLayer, AniLiberty, Selezen, Rustorka, NewStudio.

Torrent clients: qBittorrent, Transmission, Deluge, Synology Download Station (no tags; a replace
downloads the new version in full and keeps old files).

## Also inside

- Notifications to Telegram, email, Pushover, a webhook or browser push.
- Smart schedule: fresh releases are checked often, old ones rarely, broken topics go to ignore.
- Dry run: a full pass that shows what would change and changes nothing.
- Force a check of any release, or all of them, from the dashboard.
- Logins and tokens are stored encrypted. Prometheus metrics, `/healthz` and `/readyz`.
- English and Russian UI.

## Run

The image on [Docker Hub](https://hub.docker.com/r/nortido/ripperr) is built for amd64 and arm64.

```bash
docker run -d --name ripperr -p 6410:6410 -v ./config:/config nortido/ripperr
```

Open `http://<host>:6410` and create the account (only from the local network, so do it before you
open the port). Connect the torrent client, add trackers, pick categories or tags, then start with
a dry run.

Backup `secrets.key` together with `ripperr.db` from the config folder: without the key the saved
passwords cannot be read.

## Build

Go 1.25 or newer.

```bash
go build -o ripperr ./cmd
go test ./...
```

`docker build -t ripperr .` builds the image for your platform, `make build-multi` builds it for amd64 and arm64.

## License

GNU Affero General Public License v3.0.

Ripperr does not host or index any content. It works only with torrents you already have, on
trackers where you have access. You are responsible for what you download.
