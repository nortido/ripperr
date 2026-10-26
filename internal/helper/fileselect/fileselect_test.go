package fileselect

import (
	"reflect"
	"strings"
	"testing"

	"ripperr/internal/model"
)

func oldList(items ...string) []model.TorrentFile {
	// суффикс "!" у пути значит "не качать"
	out := make([]model.TorrentFile, 0, len(items))
	for _, it := range items {
		if p, ok := strings.CutSuffix(it, "!"); ok {
			out = append(out, model.TorrentFile{Path: p})
			continue
		}
		out = append(out, model.TorrentFile{Path: it, Wanted: true})
	}
	return out
}

func TestUnwanted(t *testing.T) {
	tests := []struct {
		name string
		old  []model.TorrentFile
		cur  []string
		want []int
	}{
		{"nil old", nil, []string{"R/a", "R/b"}, nil},
		{"old all wanted", oldList("R/a", "R/b"), []string{"R/a", "R/b", "R/c"}, nil},
		{"cur one file", oldList("R/a", "R/b!"), []string{"R/a"}, nil},
		{"root renamed", oldList("R1/a", "R1/b!"), []string{"R2/a", "R2/b"}, []int{1}},
		{"new root level file", oldList("R1/a", "R1/b!"), []string{"R2/a", "R2/b", "R2/c"}, []int{1, 2}},
		{
			"folder rule",
			oldList("R1/S1/e1", "R1/S1/e2", "R1/S2/e1!"),
			[]string{"R2/S1/e1", "R2/S1/e3", "R2/S2/e1", "R2/S2/e2"},
			[]int{2, 3},
		},
		{
			"nested new subfolder",
			oldList("R1/S1/e1", "R1/x!"),
			[]string{"R2/S1/Extras/b", "R2/S1/e1", "R2/x"},
			[]int{2},
		},
		{"new folder absent in old", oldList("R1/a", "R1/b!"), []string{"R2/a", "R2/New/c"}, []int{1}},
		{"nothing left wanted", oldList("R1/a", "R1/b!"), []string{"R2/b", "R2/c"}, nil},
		{"old all unwanted", oldList("R1/a!", "R1/b!"), []string{"R2/a", "R2/b"}, []int{0, 1}},
		{
			"old all unwanted new files",
			oldList("R1/a!", "R1/S/b!"),
			[]string{"R2/a", "R2/S/b", "R2/S/c", "R2/d"},
			[]int{0, 1, 2, 3},
		},
		{"old all unwanted single file", oldList("a!"), []string{"a"}, []int{0}},
		{"no common root", oldList("a", "Sub/b!"), []string{"a", "Sub/b", "Sub/c"}, []int{1, 2}},
		{"symmetric single folder", oldList("S1/e1", "S1/e2!"), []string{"S1/e1", "S1/e2", "S1/e3"}, []int{1, 2}},
		{"old only files ignored", oldList("R1/a", "R1/b!", "R1/z"), []string{"R2/a", "R2/b"}, []int{1}},
		{
			"no subfolder forward",
			oldList("S1/e1", "S1/e2!"),
			[]string{"S1/e1", "S1/e2", "S2/e1"},
			[]int{1, 2},
		},
		{
			"no subfolder reverse",
			oldList("S1/a", "S1/b!", "S2/c!"),
			[]string{"S1/a", "S1/b", "S1/d"},
			[]int{1, 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Unwanted(tc.old, tc.cur)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Unwanted = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestApplied(t *testing.T) {
	ours := oldList("R1/a", "R1/b!")
	tests := []struct {
		name  string
		old   []model.TorrentFile
		files []model.TorrentFile
		paths []string
		want  bool
	}{
		{"ours", ours, oldList("R2/a", "R2/b!"), []string{"R2/a", "R2/b"}, true},
		{"other", ours, oldList("R2/a!", "R2/b"), []string{"R2/a", "R2/b"}, false},
		{"all unwanted both sides", oldList("R1/a!", "R1/b!"), oldList("R2/a!", "R2/b!"), []string{"R2/a", "R2/b"}, true},
		{"subset", ours, oldList("R2/a!", "R2/b!"), []string{"R2/a", "R2/b"}, false},
		{
			"file paths override",
			oldList("Show/Episode 1.mkv", "Show/e2.mkv!"),
			oldList("Show/e1.mkv", "Show/e2.mkv!"),
			[]string{"Show/Episode 1.mkv", "Show/e2.mkv"},
			true,
		},
		{"nothing left wanted", ours, oldList("R2/b!", "R2/c!"), []string{"R2/b", "R2/c"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Applied(tc.old, tc.files, tc.paths); got != tc.want {
				t.Errorf("Applied = %v, want %v", got, tc.want)
			}
		})
	}
}
