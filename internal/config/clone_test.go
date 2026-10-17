package config

import (
	"slices"
	"testing"
)

func TestCloneCopiesMirrorLists(t *testing.T) {
	src := Tracker{"kinozal": &TrackerItem{
		Mirrors: []string{"kinozal.me"}, Retired: []string{"kinozal.tv"}, Unconfirmed: []string{"x.example"},
	}}
	c := src.Clone()
	c["kinozal"].Mirrors[0] = "a.example"
	c["kinozal"].Retired[0] = "b.example"
	c["kinozal"].Unconfirmed[0] = "c.example"
	it := src["kinozal"]
	if !slices.Equal(it.Mirrors, []string{"kinozal.me"}) || !slices.Equal(it.Retired, []string{"kinozal.tv"}) || !slices.Equal(it.Unconfirmed, []string{"x.example"}) {
		t.Fatalf("source changed through the clone: mirrors %v retired %v unconfirmed %v", it.Mirrors, it.Retired, it.Unconfirmed)
	}
}
