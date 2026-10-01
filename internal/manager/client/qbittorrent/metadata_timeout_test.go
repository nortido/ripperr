package qbittorrent

import (
	"testing"
	"time"

	"ripperr/internal/manager/blackhole"
)

func TestBlackholeAddTimeoutOutlivesMetadataFetch(t *testing.T) {
	// связка двух пакетов раньше держалась только на дисциплине поддержки
	// комментария: blackhole.AddTimeout обязан покрывать оба последовательных
	// потолка внутри одного вызова AddFromMagnet, свою добычу
	// (magnetFetchTimeout) и, при её неудаче, ожидание qbit
	// (metadataFetchTimeout), плюс минуту запаса на дорогу. Иначе на мёртвом
	// для обеих добыч рое запасной путь «без комментария» упирается в уже
	// истёкший внешний контекст раньше своего собственного таймаута и не
	// срабатывает вовсе
	want := magnetFetchTimeout + metadataFetchTimeout + time.Minute
	if blackhole.AddTimeout != want {
		t.Fatalf("blackhole.AddTimeout = %v, want magnetFetchTimeout (%v) + metadataFetchTimeout (%v) + 1m = %v: связка таймаутов рассинхронизирована, магнит с комментарием при мёртвом рое не успеет упасть в фолбэк без комментария до отмены контекста", blackhole.AddTimeout, magnetFetchTimeout, metadataFetchTimeout, want)
	}
}
