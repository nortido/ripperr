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

	"ripperr/internal/helper/torrentmeta"
)

var mkdirTemp = os.MkdirTemp // тест подменяет, чтобы видеть: без tr каталог и клиент вообще не создаются

func Fetch(ctx context.Context, magnet string) (data []byte, err error) {
	if strings.TrimSpace(magnet) == "" {
		return nil, fmt.Errorf("magnetmeta: пустая magnet-ссылка")
	}

	// DHT выключен, рой можно найти только по анонсерам из tr. Без них клиент провисит
	// весь таймаут впустую и съест слот часового лимита трекера
	if !hasAnnounceURL(magnet) {
		return nil, fmt.Errorf("magnetmeta: в magnet-ссылке нет адреса трекера (tr), а DHT выключен")
	}

	dir, err := mkdirTemp("", "ripperr-magnetmeta-*")
	if err != nil {
		return nil, fmt.Errorf("magnetmeta: не удалось создать временный каталог: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cc := torrent.NewDefaultClientConfig()
	cc.DataDir = dir
	cc.Seed = false
	cc.NoUpload = true
	// DHT-серверы не закрываются в cl.Close() и держат UDP-сокет до конца процесса, а Fetch
	// зовут на каждый магнет неделями. Приватные раздачи в DHT и так не анонсируются, у
	// публичных есть tr и PEX, а если рой не нашелся, submit качает .torrent с трекера
	cc.NoDHT = true
	// дефолтный порт 42069 один на всех: параллельные Fetch (по горутине на трекер)
	// за него дерутся и часть не стартует. С 0 свободный порт выберет ядро
	cc.ListenPort = 0
	// UPnP на каждом Fetch ищет роутер по SSDP, и эти горутины живут отдельно от cl.Close().
	// Входящие нам не нужны, мы только читаем метаданные
	cc.NoDefaultPortForwarding = true
	// webtorrent включен по умолчанию и на каждый вызов поднимает ICE, STUN и mDNS из pion.
	// Метаданные идут по обычному BitTorrent
	cc.DisableWebtorrent = true
	// по дефолту 50+25 сокетов на клиент, а клиентов в параллель столько же, сколько трекеров.
	// Для метаданных хватит десятка пиров, иначе упремся в лимит дескрипторов контейнера
	cc.EstablishedConnsPerTorrent = 20
	cc.HalfOpenConnsPerTorrent = 10

	lg := log.NewLogger()
	lg.SetHandlers() // без хендлеров библиотека молчит и не засоряет наши логи
	cc.Logger = lg

	cl, err := torrent.NewClient(cc)
	if err != nil {
		return nil, fmt.Errorf("magnetmeta: не удалось создать torrent-клиент: %w", err)
	}
	defer cl.Close()

	t, err := cl.AddMagnet(magnet)
	if err != nil {
		return nil, fmt.Errorf("magnetmeta: не удалось добавить magnet-ссылку: %w", err)
	}
	defer t.Drop()

	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return nil, fmt.Errorf("magnetmeta: ожидание метаданных magnet прервано: %w", ctx.Err())
	}

	mi := t.Metainfo()
	// из магнита библиотека заполняет только AnnounceList. qBittorrent и Transmission
	// это едят, но файл выходит нестандартный. Announce лежит вне info, инфохеш не меняется
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
	// url.Parse почти никогда не падает, даже на "not a magnet". Если упал, считаем что tr нет
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
		// AddMagnet этот URI уже разобрал, так что сюда попадать не должны
		return fmt.Errorf("magnetmeta: не удалось извлечь infohash из magnet-ссылки")
	}
	gotHash, err := torrentmeta.InfoHash(built)
	if err != nil {
		return fmt.Errorf("magnetmeta: не удалось вычислить infohash собранного файла: %w", err)
	}
	if !strings.EqualFold(wantHash, gotHash) {
		return fmt.Errorf("magnetmeta: infohash собранного .torrent (%s) не совпадает с magnet-ссылкой (%s)", gotHash, wantHash)
	}
	return nil
}
