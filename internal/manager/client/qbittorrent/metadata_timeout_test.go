package qbittorrent

import (
	"testing"
	"time"

	"trackerr/internal/manager/blackhole"
)

// TestBlackholeAddTimeoutOutlivesMetadataFetch страхует связку двух пакетов,
// которая раньше держалась только на дисциплине поддержки комментария:
// blackhole.AddTimeout обязан оставаться длиннее metadataFetchTimeout на те же
// зафиксированные в комментариях в blackhole.go 1 минуту запаса, иначе у
// магнита с заданным комментарием контекст добавления убьют посреди ожидания
// метаданных раньше, чем сработает штатный фолбэк «без комментария».
func TestBlackholeAddTimeoutOutlivesMetadataFetch(t *testing.T) {
	want := metadataFetchTimeout + time.Minute
	if blackhole.AddTimeout != want {
		t.Fatalf("blackhole.AddTimeout = %v, want metadataFetchTimeout (%v) + 1m = %v: связка таймаутов рассинхронизирована, магнит с комментарием при медленном рое не успеет упасть в фолбэк без комментария до отмены контекста", blackhole.AddTimeout, metadataFetchTimeout, want)
	}
}
