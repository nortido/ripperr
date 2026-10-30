package runner

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"ripperr/internal/model"
	proxyModel "ripperr/internal/model/proxy"
	trackerModel "ripperr/internal/model/tracker"
)

type typedPageClient struct {
	mockPageClient
	pt proxyModel.ProxyType
}

func (c typedPageClient) GetProxyType() proxyModel.ProxyType { return c.pt }

type encodedPageClient struct {
	typedPageClient
	encoded bool
}

func (c encodedPageClient) EncodedBody() bool { return c.encoded }

func TestBodyEncoded(t *testing.T) {
	cases := []struct {
		name string
		tc   typedPageClient
		want bool
	}{
		{"none", typedPageClient{pt: proxyModel.TypeNone}, false},
		{"flare", typedPageClient{pt: proxyModel.TypeFlare}, true},
		{"tls", typedPageClient{pt: proxyModel.TypeTLS}, false},
	}
	for _, c := range cases {
		if got := bodyEncoded(c.tc); got != c.want {
			t.Errorf("%s: bodyEncoded = %v, want %v", c.name, got, c.want)
		}
	}
	yes, no := true, false
	if got := bodyEncoded(encodedPageClient{typedPageClient{pt: proxyModel.TypeTLS}, yes}); !got {
		t.Error("tls with EncodedBody true: want true")
	}
	if got := bodyEncoded(encodedPageClient{typedPageClient{pt: proxyModel.TypeFlare}, no}); got {
		t.Error("flare with EncodedBody false: want false")
	}
}

type flipPageClient struct {
	mockPageClient
	flag *atomic.Bool
	seen *bool
}

func (c flipPageClient) GetProxyType() proxyModel.ProxyType { return proxyModel.TypeTLS }
func (c flipPageClient) EncodedBody() bool                  { return c.flag.Load() }
func (c flipPageClient) PickTorrent(_ *bytes.Buffer, encoded bool, _ trackerModel.Held) trackerModel.Pick {
	*c.seen = encoded
	return trackerModel.Pick{Miss: "stop"}
}

type flipPageTracker struct {
	pageTracker
	flag *atomic.Bool
}

func (p *flipPageTracker) Response(context.Context, int, string, bool) (*bytes.Buffer, error) {
	p.flag.Store(true)
	return p.page, nil
}

func TestCheckPageReadsEncodedAfterFetch(t *testing.T) {
	var flag atomic.Bool
	var seen bool
	tc := flipPageClient{flag: &flag, seen: &seen}
	h := &handler{trackerService: &flipPageTracker{
		pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: bytes.NewBufferString(strings.Repeat("x", 60))},
		flag:        &flag,
	}}
	if _, err := h.checkPage(t.Context(), 1, tc, &model.Entry{URL: "https://x/1"}, nil); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("PickTorrent got encoded=false though the fetch switched the session to flare-only")
	}
}
