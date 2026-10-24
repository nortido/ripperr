package torrentmeta

import (
	"runtime"
	"strings"
	"testing"
)

const allocLimit = 16 << 20

func hugeDeclaredString() []byte {
	// info читается сырым без выделения по длине, поэтому длинная строка идет в comment верхнего уровня
	return []byte("d7:comment134217000:" + strings.Repeat("x", 20) + "e")
}

func allocDelta(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestInfoHashBoundsDeclaredStringLength(t *testing.T) {
	var err error
	delta := allocDelta(func() { _, err = InfoHash(hugeDeclaredString()) })
	if err == nil {
		t.Fatal("want error for a string longer than the input")
	}
	if delta >= allocLimit {
		t.Errorf("allocated %d bytes, want < %d", delta, allocLimit)
	}
}

func TestCommentBoundsDeclaredStringLength(t *testing.T) {
	var err error
	delta := allocDelta(func() { _, err = Comment(hugeDeclaredString()) })
	if err == nil {
		t.Fatal("want error for a string longer than the input")
	}
	if delta >= allocLimit {
		t.Errorf("allocated %d bytes, want < %d", delta, allocLimit)
	}
}

func TestSetCommentBoundsDeclaredStringLength(t *testing.T) {
	var err error
	delta := allocDelta(func() { _, err = SetComment(hugeDeclaredString(), "c") })
	if err == nil {
		t.Fatal("want error for a string longer than the input")
	}
	if delta >= allocLimit {
		t.Errorf("allocated %d bytes, want < %d", delta, allocLimit)
	}
}

func TestHasV2(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
		fail bool
	}{
		{"v2", "d4:infod12:meta versioni2e4:name1:xee", true, false},
		{"hybrid", "d4:infod12:meta versioni2e6:lengthi1e4:name1:x6:pieces20:" + strings.Repeat("a", 20) + "ee", true, false},
		{"v1", "d4:infod6:lengthi1e4:name1:x6:pieces20:" + strings.Repeat("a", 20) + "ee", false, false},
		{"garbage", "not bencode", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := HasV2([]byte(c.data))
			if (err != nil) != c.fail {
				t.Fatalf("err = %v, want fail=%v", err, c.fail)
			}
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
