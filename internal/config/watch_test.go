package config

import (
	"slices"
	"testing"
)

func TestRunnerWatchSelection(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		cats      []string
		tags      []string
		supported bool
		wantCats  []string
		wantTags  []string
	}{
		{"derived categories", "", []string{"tv"}, []string{"watch"}, true, []string{"tv"}, nil},
		{"derived tags", "", []string{}, []string{"watch"}, true, nil, []string{"watch"}},
		{"tags mode", WatchModeTags, []string{"tv"}, []string{"watch"}, true, nil, []string{"watch"}},
		{"tags mode unsupported", WatchModeTags, []string{"tv"}, []string{"watch"}, false, []string{"tv"}, nil},
		{"categories mode empty list", WatchModeCategories, []string{}, []string{"watch"}, true, []string{}, nil},
		{"invalid mode", "bogus", []string{"tv"}, []string{"w"}, true, []string{"tv"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Runner{WatchMode: c.mode, AllowedCategories: c.cats, WatchTags: c.tags}
			gotCats, gotTags := r.WatchSelection(c.supported)
			if !slices.Equal(gotCats, c.wantCats) || !slices.Equal(gotTags, c.wantTags) {
				t.Fatalf("WatchSelection = (%v, %v), want (%v, %v)", gotCats, gotTags, c.wantCats, c.wantTags)
			}
		})
	}
}

func TestClientSupportsTags(t *testing.T) {
	if ClientSupportsTags("Deluge ") {
		t.Error("deluge must not support tags")
	}
	if ClientSupportsTags(" DownloadStation ") {
		t.Error("downloadstation must not support tags")
	}
	if !ClientSupportsTags("qbit") {
		t.Error("qbit must support tags")
	}
}
