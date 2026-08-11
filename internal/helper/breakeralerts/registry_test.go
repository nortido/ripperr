package breakeralerts

import (
	"testing"
)

func TestRegistryAddRemove(t *testing.T) {
	r := New()
	r.Open("kinozal", "timeout")
	list := r.List()
	if len(list) != 1 || list[0].Tracker != "kinozal" || list[0].LastError != "timeout" {
		t.Fatalf("list = %+v", list)
	}
	r.Open("kinozal", "still down")
	list = r.List()
	if list[0].LastError != "still down" {
		t.Fatalf("error not updated: %+v", list[0])
	}
	r.Close("kinozal")
	if len(r.List()) != 0 {
		t.Fatal("expected empty after close")
	}
}

func TestRegistryMultipleTrackers(t *testing.T) {
	r := New()
	r.Open("kinozal", "a")
	r.Open("rutracker", "b")
	if len(r.List()) != 2 {
		t.Fatalf("expected 2 incidents")
	}
	r.Close("kinozal")
	list := r.List()
	if len(list) != 1 || list[0].Tracker != "rutracker" {
		t.Fatalf("list = %+v", list)
	}
}
