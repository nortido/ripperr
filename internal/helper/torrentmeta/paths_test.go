package torrentmeta

import (
	"bytes"
	"slices"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func buildInfoTorrent(t *testing.T, info metainfo.Info) []byte {
	t.Helper()
	info.PieceLength = 16384
	info.Pieces = make([]byte, 20)
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes()
}

func TestFilePaths(t *testing.T) {
	single := buildMinimalTorrent(t, "")
	multi := buildInfoTorrent(t, metainfo.Info{
		Name: "Show",
		Files: []metainfo.FileInfo{
			{Length: 1, Path: []string{"a.mkv"}},
			{Length: 1, Path: []string{".pad", "0"}, ExtendedFileAttrs: metainfo.ExtendedFileAttrs{Attr: "p"}},
			{Length: 1, Path: []string{"sub", "x.mkv"}, PathUtf8: []string{"sub", "b.mkv"}},
		},
	})
	cases := []struct {
		name string
		data []byte
		want []string
	}{
		{"single", single, []string{"test.txt"}},
		{"multi", multi, []string{"Show/a.mkv", "Show/sub/b.mkv"}},
	}
	for _, tc := range cases {
		got, err := FilePaths(tc.data)
		if err != nil {
			t.Fatalf("%s: FilePaths: %v", tc.name, err)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: FilePaths = %q, want %q", tc.name, got, tc.want)
		}
	}
	if _, err := FilePaths([]byte("garbage")); err == nil {
		t.Error("want error for garbage bytes")
	}
}
