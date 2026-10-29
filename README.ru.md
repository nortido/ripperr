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

[English](README.md) · [Changelog](CHANGELOG.md)

Раздачи с форумных трекеров сами остаются свежими.

Новые серии, дополнительные звуковые дорожки, новая версия игры: когда раздачу на форумном трекере
обновили, Ripperr заменяет ее в вашем torrent-клиенте. Вам ничего делать не нужно.

<img src="docs/dashboard.png" alt="Панель Ripperr" width="900">

</div>

---

## Зачем Ripperr

Форумные трекеры обновляют раздачу в той же теме. В теме пишут «торрент-файл обновлен, скачайте его
заново и перепроверьте задачу в клиенте», а клиент продолжает раздавать старый, и это замечаешь,
только когда сезон уже закончился. Ripperr сам скачивает новый `.torrent` и делает перепроверку.

## Оставьте свой стек, добавьте Ripperr

Ripperr не заменяет Sonarr, Radarr и все остальное, что наполняет ваш клиент. Они ищут и забирают
раздачи. Но они не следят за темой форума, где новые серии добавляются в тот же торрент: Sonarr
видит уже импортированную загрузку и идет дальше. Ripperr закрывает этот пробел. Все, что попало в
отслеживаемые категории или теги из Sonarr, Radarr, Prowlarr или руками, остается актуальным.

Монитор тем вроде Monitorrent или TorrentMonitor тоже обновляет раздачи, но начинает со списка
ссылок на темы, который вы заполняете и поддерживаете вручную. Ripperr начинает с вашего
torrent-клиента:

- **Не нужно вести список.** Трекеры пишут ссылку на тему в комментарий торрента. Ripperr берет ее
  из торрентов в выбранных вами категориях или тегах. Добавили торрент в клиент, и он уже под
  наблюдением.
- **Замена без сюрпризов.** Путь сохранения, категория, теги и выбор файлов переходят в новую
  версию. Новый торрент проверяется до того, как уходит старый, а сбой посередине исправляется при
  следующем запуске. Старые файлы удаляются только там, где вы это разрешили. Если новая версия не
  помещается на диск, старый торрент остается, а вам приходит уведомление. Ripperr попробует снова
  при следующей проверке.
- **Работает там, где трекеры сопротивляются.** Cloudflare через FlareSolverr, мертвые домены через
  переключение зеркал, темы только с magnet-ссылкой через пиров.
- **Просто запустить.** Один контейнер, один файл SQLite, веб-интерфейс, которым удобно пользоваться
  с телефона.

## Поддерживаемые трекеры

RuTracker, Kinozal, Rutor, NNM-Club, TeamHD, Toloka, Tapochek, Riper.AM, BookTracker, AniDub,
AnimeLayer, AniLiberty, Selezen, Rustorka, NewStudio.

Torrent-клиенты: qBittorrent, Transmission, Deluge, Synology Download Station (без тегов; при замене
новая версия скачивается целиком, старые файлы остаются).

## Еще внутри

- Уведомления в Telegram, на email, в Pushover, в webhook или push в браузер.
- Умное расписание: свежие раздачи проверяются часто, старые редко, сломанные темы уходят в игнор.
- Пробный прогон: полный проход, который показывает, что изменится, и ничего не меняет.
- Принудительная проверка любой раздачи или всех сразу с панели.
- Логины и токены хранятся в зашифрованном виде. Метрики Prometheus, `/healthz` и `/readyz`.
- Интерфейс на английском и русском.

## Запуск

Образ на [Docker Hub](https://hub.docker.com/r/nortido/ripperr) собран для amd64 и arm64.

```bash
docker run -d --name ripperr -p 6410:6410 -v ./config:/config nortido/ripperr
```

Откройте `http://<host>:6410` и создайте учетную запись (только из локальной сети, поэтому сделайте
это до того, как откроете порт наружу). Подключите torrent-клиент, добавьте трекеры, выберите
категории или теги и начните с пробного прогона.

Сохраняйте `secrets.key` вместе с `ripperr.db` из папки конфигурации: без ключа сохраненные пароли
не прочитать.

## Сборка

Go 1.25 или новее.

```bash
go build -o ripperr ./cmd
go test ./...
```

`docker build -t ripperr .` собирает образ для вашей платформы, `make build-multi` собирает его для amd64 и arm64.

## Лицензия

GNU Affero General Public License v3.0.

Ripperr не размещает и не индексирует никакой контент. Он работает только с торрентами, которые у
вас уже есть, на трекерах, где у вас есть доступ. За то, что вы скачиваете, отвечаете вы.
