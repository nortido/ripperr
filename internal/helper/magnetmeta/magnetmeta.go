package magnetmeta

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"trackerr/internal/helper/torrentmeta"
)

// mkdirTemp это os.MkdirTemp за переменной пакета: тесту на guard-проверку
// hasAnnounceURL нужен способ доказать, что временный каталог (а с ним и
// клиент torrent) не создаётся вовсе, а не просто убирается через defer к
// моменту возврата Fetch - тем же приёмом, что magnetFetch у клиентов
// загрузок
var mkdirTemp = os.MkdirTemp

func Fetch(ctx context.Context, magnet string) (data []byte, err error) {
	// пустую строку отсекаем сразу, чтобы не поднимать клиент ради заведомо мусорного входа
	if strings.TrimSpace(magnet) == "" {
		return nil, fmt.Errorf("magnetmeta: пустая магнет-ссылка")
	}

	// DHT выключен (см. cc.NoDHT ниже), значит единственный способ найти рой -
	// адреса анонсеров прямо в магните (параметр tr). Без них AddMagnet всё
	// равно поднимет клиент и провисит весь таймаут в ожидании метаданных,
	// которые взять неоткуда - это чистая трата слота часового лимита трекера
	// на каждом прогоне. Проверяем до создания временного каталога и клиента.
	if !hasAnnounceURL(magnet) {
		return nil, fmt.Errorf("magnetmeta: в магните нет адреса анонсера (tr), а DHT выключен")
	}

	dir, err := mkdirTemp("", "trackerr-magnetmeta-*")
	if err != nil {
		return nil, fmt.Errorf("magnetmeta: не удалось создать временный каталог: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cc := torrent.NewDefaultClientConfig()
	cc.DataDir = dir
	cc.Seed = false
	cc.NoUpload = true
	// DHT-серверы (dht.NewServer, отдельно на IPv4 и IPv6) не закрываются
	// cl.Close() и держат горутину с живым UDP-сокетом до конца процесса на
	// каждый вызов Fetch, а демон вызывает Fetch на каждом магнитном сабмите
	// неделями подряд - упор в лимит дескрипторов неизбежен. Отключаем DHT
	// вовсе: приватные раздачи помечены private и по протоколу трекера не
	// анонсируются через DHT, а у публичных нужный адрес уже есть в самом
	// магните (параметр tr) и работает обмен пирами (PEX); если рой всё же не
	// нашёлся, submit уходит в запасной путь со скачиванием .torrent с трекера
	cc.NoDHT = true
	// дефолт библиотеки фиксирует порт 42069: параллельные вызовы Fetch (runner
	// держит по горутине на трекер, до нескольких submit одновременно) дерутся
	// за один и тот же порт и часть из них не поднимается вовсе. 0 отдаёт выбор
	// свободного порта ядру
	cc.ListenPort = 0
	// UPnP-проброс порта (по умолчанию включён) на каждый Fetch ищет роутер по
	// SSDP и шлёт ему SOAP-запрос через net/http; это независимые от cl.Close()
	// горутины, которые могут провисеть секундами и не гарантированно
	// остановлены к моменту возврата Fetch. Нам входящая доступность не нужна:
	// мы только читаем метаданные, а не раздаём
	cc.NoDefaultPortForwarding = true
	// webtorrent (веб-транспорт через pion/webrtc, пиры с tr=wss://...) включён
	// в библиотеке по умолчанию и реально в графе сборки: ICE-gathering, STUN и
	// mDNS-слушатель (pion/mdns) поднимаются на каждый вызов. Метаданные
	// добываются по обычному BitTorrent, веб-транспорт нам не нужен
	cc.DisableWebtorrent = true
	// дефолт библиотеки - 50 установленных соединений и 25 полуоткрытых НА
	// КАЖДЫЙ клиент, а клиент поднимается на каждый вызов Fetch, причём
	// параллельно (runner держит горутину на трекер). Нам нужны только
	// метаданные, десятка пиров с запасом хватает с головой; иначе пик равен
	// числу трекеров, умноженному на 75 сокетов, и упирается в лимит
	// дескрипторов контейнера
	cc.EstablishedConnsPerTorrent = 20
	cc.HalfOpenConnsPerTorrent = 10

	lg := log.NewLogger()
	lg.SetHandlers() // без хендлеров - тихо, библиотека не должна засорять наши логи
	cc.Logger = lg

	cl, err := torrent.NewClient(cc)
	if err != nil {
		return nil, fmt.Errorf("magnetmeta: не удалось создать клиент torrent: %w", err)
	}
	defer cl.Close()

	t, err := cl.AddMagnet(magnet)
	if err != nil {
		return nil, fmt.Errorf("magnetmeta: не удалось добавить магнит: %w", err)
	}
	defer t.Drop()

	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return nil, fmt.Errorf("magnetmeta: ожидание метаданных по магниту прервано: %w", ctx.Err())
	}

	mi := t.Metainfo()
	// библиотека при сборке metainfo из магнита заполняет только AnnounceList,
	// поле Announce остаётся пустым; qBittorrent и Transmission такое едят, но
	// файл получается нестандартным. Announce лежит вне словаря info, поэтому
	// правка не трогает инфохеш
	fillAnnounce(&mi)
	var buf bytes.Buffer
	if err = mi.Write(&buf); err != nil {
		return nil, fmt.Errorf("magnetmeta: не удалось закодировать метаданные: %w", err)
	}
	built := buf.Bytes()

	if err = verifyInfoHash(built, magnet); err != nil {
		return nil, err
	}

	return built, nil
}

func hasAnnounceURL(magnet string) bool {
	// net/url.Parse практически никогда не ошибается (даже на "not a magnet" и
	// произвольных схемах), поэтому отдельная ветка на err была недостижима на
	// практике; ошибка разбора трактуется так же, как отсутствие tr - дальше
	// AddMagnet всё равно даст точную причину отказа для мусорного входа
	u, err := url.Parse(magnet)
	if err != nil {
		return false
	}
	for _, tr := range u.Query()["tr"] {
		if strings.TrimSpace(tr) != "" {
			return true
		}
	}
	return false
}

func fillAnnounce(mi *metainfo.MetaInfo) {
	if mi.Announce != "" {
		return
	}
	for _, tier := range mi.AnnounceList {
		for _, tr := range tier {
			if tr != "" {
				mi.Announce = tr
				return
			}
		}
	}
}

func verifyInfoHash(built []byte, magnet string) error {
	wantHash, ok := torrentmeta.BtihFromMagnet(magnet)
	if !ok {
		// AddMagnet тот же URI уже распарсил успешно, так что это подозрительно само по себе
		return fmt.Errorf("magnetmeta: не удалось извлечь инфохеш из магнит-ссылки")
	}
	gotHash, err := torrentmeta.InfoHash(built)
	if err != nil {
		return fmt.Errorf("magnetmeta: не удалось вычислить инфохеш собранного файла: %w", err)
	}
	if !strings.EqualFold(wantHash, gotHash) {
		return fmt.Errorf("magnetmeta: инфохеш собранного .torrent (%s) не совпадает с магнитом (%s)", gotHash, wantHash)
	}
	return nil
}
