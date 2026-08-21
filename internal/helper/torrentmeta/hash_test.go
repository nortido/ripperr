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

func TestSameHash_MatchByData(t *testing.T) {
	data := buildMinimalTorrent(t, "comment")
	hash, err := InfoHash(data)
	if err != nil {
		t.Fatalf("InfoHash: %v", err)
	}

	same, known := SameHash(data, "", hash)
	if !known || !same {
		t.Errorf("got same=%v known=%v, want same=true known=true", same, known)
	}
}

func TestSameHash_MatchByMagnet(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef01234567"
	magnet := "magnet:?xt=urn:btih:" + hash

	same, known := SameHash(nil, magnet, hash)
	if !known || !same {
		t.Errorf("got same=%v known=%v, want same=true known=true", same, known)
	}
}

func TestSameHash_Mismatch(t *testing.T) {
	data := buildMinimalTorrent(t, "comment")

	same, known := SameHash(data, "", "ffffffffffffffffffffffffffffffffffffff")
	if !known || same {
		t.Errorf("got same=%v known=%v, want same=false known=true", same, known)
	}
}

func TestSameHash_EmptyExisting(t *testing.T) {
	data := buildMinimalTorrent(t, "comment")

	same, known := SameHash(data, "", "")
	if known || same {
		t.Errorf("got same=%v known=%v, want same=false known=false", same, known)
	}
}

func TestSameHash_NoData(t *testing.T) {
	same, known := SameHash(nil, "", "0123456789abcdef0123456789abcdef01234567")
	if known || same {
		t.Errorf("got same=%v known=%v, want same=false known=false", same, known)
	}
}

func TestSameHash_BrokenData(t *testing.T) {
	same, known := SameHash([]byte("not a torrent"), "", "0123456789abcdef0123456789abcdef01234567")
	if known || same {
		t.Errorf("got same=%v known=%v, want same=false known=false", same, known)
	}
}
