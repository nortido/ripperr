package magnetmeta

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/anacrolix/log"
	"github.com/anacrolix/torrent"

	"trackerr/internal/helper/torrentmeta"
)

func Fetch(ctx context.Context, magnet string) (data []byte, err error) {
	// пустую строку отсекаем сразу, чтобы не поднимать клиент ради заведомо мусорного входа
	if strings.TrimSpace(magnet) == "" {
		return nil, fmt.Errorf("magnetmeta: пустая магнет-ссылка")
	}

	dir, err := os.MkdirTemp("", "trackerr-magnetmeta-*")
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
