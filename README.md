<div align="center">

# 📽️ Ripperr 📽️

![License](https://img.shields.io/badge/license-AGPL--3.0-blue)
![Release](https://img.shields.io/badge/release-v2.8.0-2f9e44)

![Backend](https://img.shields.io/badge/backend-Go%201.25-00ADD8?logo=go&logoColor=white)
![Frontend](https://img.shields.io/badge/frontend-vanilla%20JS-F7DF1E?logo=javascript&logoColor=black)
![Database](https://img.shields.io/badge/database-SQLite-003B57?logo=sqlite&logoColor=white)
![Docker](https://img.shields.io/badge/docker-amd64%20%7C%20arm64-2496ED?logo=docker&logoColor=white)

![Trackers](https://img.shields.io/badge/trackers-15-4c6ef5)
![Clients](https://img.shields.io/badge/clients-4-4c6ef5)
![Notifiers](https://img.shields.io/badge/notifiers-5-4c6ef5)
![UI](https://img.shields.io/badge/UI-EN%20%7C%20RU-2f9e44)

[Русский](README.ru.md) · [Changelog](CHANGELOG.md)

Torrents from forum trackers update themselves to the latest version.

When a release on a forum tracker gets new episodes, another audio track or a new game version,
Ripperr downloads the new torrent and replaces the old one in your client.

<img src="docs/dashboard.png" alt="Ripperr dashboard" width="900">

</div>

---

## Why Ripperr

Forum trackers update a release inside the same topic. The topic asks you to download the torrent
file again and recheck the task in your client. The client keeps seeding the old torrent, and you
find out when the season has already ended. Ripperr downloads the new `.torrent` and runs the
recheck for you.

## Next to Sonarr, Radarr and topic watchers

Sonarr, Radarr and similar tools find releases and send them to your client. They do not follow a
forum topic that keeps adding episodes to the same torrent: Sonarr sees a download it already
imported and skips it. Ripperr watches every torrent in the categories or tags you choose, whether
Sonarr, Radarr, Prowlarr or you added it.

Monitorrent and TorrentMonitor also update releases, but you give them a list of topic links and
keep that list by hand. Ripperr starts from your torrent client:

- Trackers put the topic link in the torrent comment. Ripperr reads it from torrents in the
  categories or tags you pick, so a torrent you add to the client is watched right away.
- The new version keeps the save path, category, tags and file selection. Ripperr checks the new
  torrent before it removes the old one, and if it crashes halfway, it finishes the job on the next
  start. Old files are deleted only where you allow it. If the new version does not fit on the
  disk, the old torrent stays, you get a notice, and Ripperr tries again on the next check.
- Cloudflare is handled through FlareSolverr, dead domains through mirror switching, magnet-only
  topics through peers.
- It runs as one container with one SQLite file, and the web UI works on a phone.

## Supported trackers

RuTracker, Kinozal, Rutor, NNM-Club, TeamHD, Toloka, Tapochek, Riper.AM, BookTracker, AniDub,
AnimeLayer, AniLiberty, Selezen, Rustorka, NewStudio.

Torrent clients: qBittorrent, Transmission, Deluge, Synology Download Station (no tags; a replace
downloads the new version in full and keeps old files).

## Other features

- Notifications to Telegram, email, Pushover, a webhook or browser push.
- Checks follow the age of a release: fresh ones often, old ones rarely. Broken topics go to ignore.
- Dry run: a full pass that shows what would change and changes nothing.
- Force a check of one release or all of them from the dashboard.
- Logins and tokens are stored encrypted. Prometheus metrics, `/healthz` and `/readyz`.
- English and Russian UI.

## Run

The image on [Docker Hub](https://hub.docker.com/r/nortido/ripperr) is built for amd64 and arm64.

```bash
docker run -d --name ripperr -p 6410:6410 -v ./config:/config nortido/ripperr
```

Open `http://<host>:6410` and create the account. This works only from the local network, so do it
before you open the port. Then connect the torrent client, add trackers, pick categories or tags
and start with a dry run.

Back up `secrets.key` together with `ripperr.db` from the config folder. Without the key Ripperr
cannot read the saved passwords.

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
