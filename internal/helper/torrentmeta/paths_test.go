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

func TestFileSizes(t *testing.T) {
	multi := buildInfoTorrent(t, metainfo.Info{
		Name: "R",
		Files: []metainfo.FileInfo{
			{Length: 10, Path: []string{"a"}},
			{Length: 5, Path: []string{".pad", "5"}, ExtendedFileAttrs: metainfo.ExtendedFileAttrs{Attr: "p"}},
			{Length: 20, Path: []string{"sub", "b"}},
		},
	})
	paths, sizes, err := FileSizes(multi)
	if err != nil {
		t.Fatalf("FileSizes multi: %v", err)
	}
	if want := []string{"R/a", "R/sub/b"}; !slices.Equal(paths, want) {
		t.Errorf("multi paths = %q, want %q", paths, want)
	}
	if want := []int64{10, 20}; !slices.Equal(sizes, want) {
		t.Errorf("multi sizes = %v, want %v", sizes, want)
	}

	single := buildInfoTorrent(t, metainfo.Info{Name: "one.bin", Length: 77})
	paths, sizes, err = FileSizes(single)
	if err != nil {
		t.Fatalf("FileSizes single: %v", err)
	}
	if want := []string{"one.bin"}; !slices.Equal(paths, want) {
		t.Errorf("single paths = %q, want %q", paths, want)
	}
	if want := []int64{77}; !slices.Equal(sizes, want) {
		t.Errorf("single sizes = %v, want %v", sizes, want)
	}

	if _, _, err := FileSizes([]byte("garbage")); err == nil {
		t.Error("want error for garbage bytes")
	}
}
