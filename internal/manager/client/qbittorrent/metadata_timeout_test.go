package qbittorrent

import (
	"testing"
	"time"

	"ripperr/internal/manager/blackhole"
)

func TestBlackholeAddTimeoutOutlivesMetadataFetch(t *testing.T) {
	// раньше эту связку держал только комментарий. AddTimeout должен вмещать обе добычи подрят
	// (свою и через qbit) и минуту запаса, иначе на мертвом рое фолбэк без комментария не успеет
	want := magnetFetchTimeout + metadataFetchTimeout + time.Minute
	if blackhole.AddTimeout != want {
		t.Fatalf("blackhole.AddTimeout = %v, want magnetFetchTimeout (%v) + metadataFetchTimeout (%v) + 1m = %v: связка таймаутов рассинхронизирована, магнит с комментарием при мёртвом рое не успеет упасть в фолбэк без комментария до отмены контекста", blackhole.AddTimeout, magnetFetchTimeout, metadataFetchTimeout, want)
	}
}
