package runner

import (
	"bytes"
	"strings"
	"testing"
)

func TestPageSampleStripsTagsAndCollapsesWhitespace(t *testing.T) {
	html := "<html><head><script>var x = 1;\nalert('hi');</script><style>.a{color:red}\n</style></head>" +
		"<body>\n\n<p>Hello   world</p>\n<div>Second\nline</div>\n\n</body></html>"
	got := pageSample(bytes.NewBufferString(html), true)
	want := "Hello world Second line"
	if got != want {
		t.Fatalf("pageSample() = %q, want %q", got, want)
	}
}

func TestPageSampleTruncatesByRunes(t *testing.T) {
	text := strings.Repeat("ж", pageSampleMaxRunes+100)
	got := pageSample(bytes.NewBufferString(text), true)
	if n := len([]rune(got)); n != pageSampleMaxRunes {
		t.Fatalf("rune length = %d, want %d", n, pageSampleMaxRunes)
	}
	if got != strings.Repeat("ж", pageSampleMaxRunes) {
		t.Fatal("truncation did not preserve leading runes")
	}
}
