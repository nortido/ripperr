package torrentmeta

import (
	"bytes"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

func TestInfoHash(t *testing.T) {
	data := buildMinimalTorrent(t, "comment")
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("load metainfo: %v", err)
	}
	want := mi.HashInfoBytes().HexString()

	got, err := InfoHash(data)
	if err != nil {
		t.Fatalf("InfoHash: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
