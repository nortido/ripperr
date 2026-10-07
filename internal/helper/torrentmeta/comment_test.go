package torrentmeta

import (
	"bytes"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func buildMinimalTorrent(t *testing.T, comment string) []byte {
	t.Helper()
	info := metainfo.Info{
		PieceLength: 16384,
		Pieces:      make([]byte, 20),
		Name:        "test.txt",
		Length:      0,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{
		InfoBytes: infoBytes,
		Announce:  "http://tracker.example/announce",
		Comment:   comment,
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes()
}

func TestSetComment_RoundTrip(t *testing.T) {
	data := buildMinimalTorrent(t, "old comment")

	beforeHash, err := InfoHash(data)
	if err != nil {
		t.Fatalf("InfoHash before: %v", err)
	}

	updated, err := SetComment(data, "new comment")
	if err != nil {
		t.Fatalf("SetComment: %v", err)
	}

	// комментарий не часть info-словаря, поэтому его подмена не должна двигать
	// инфохеш; переезд на перекодировку словаря сломал бы это молча
	afterHash, err := InfoHash(updated)
	if err != nil {
		t.Fatalf("InfoHash after: %v", err)
	}
	if beforeHash != afterHash {
		t.Errorf("infohash changed after SetComment: before %q, after %q", beforeHash, afterHash)
	}

	mi, err := metainfo.Load(bytes.NewReader(updated))
	if err != nil {
		t.Fatalf("reload metainfo: %v", err)
	}
	if mi.Comment != "new comment" {
		t.Errorf("got comment %q, want %q", mi.Comment, "new comment")
	}
	if mi.Announce != "http://tracker.example/announce" {
		t.Errorf("announce not preserved: got %q", mi.Announce)
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if info.Name != "test.txt" {
		t.Errorf("info name not preserved: got %q", info.Name)
	}
}

func TestSetComment_InvalidData(t *testing.T) {
	if _, err := SetComment([]byte("not a torrent"), "comment"); err == nil {
		t.Fatal("expected error for invalid torrent data")
	}
}

func TestCommentRead(t *testing.T) {
	got, err := Comment(buildMinimalTorrent(t, "https://x/t1"))
	if err != nil || got != "https://x/t1" {
		t.Fatalf("Comment = %q, %v, want the stored comment", got, err)
	}
	got, err = Comment(buildMinimalTorrent(t, ""))
	if err != nil || got != "" {
		t.Fatalf("Comment of a comment-less file = %q, %v, want empty", got, err)
	}
	if _, err = Comment([]byte("not a torrent")); err == nil {
		t.Fatal("expected error for invalid torrent data")
	}
}
