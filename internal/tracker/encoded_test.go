package tracker

import (
	"testing"

	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/tracker/client"
)

type typedClient struct {
	client.IClient
	pt proxyModel.ProxyType
}

func (c typedClient) GetProxyType() proxyModel.ProxyType { return c.pt }

type encodedClient struct {
	typedClient
	encoded bool
}

func (c encodedClient) EncodedBody() bool { return c.encoded }

func TestCachedTrackerEncodedBody(t *testing.T) {
	cases := []struct {
		name  string
		inner client.IClient
		want  bool
	}{
		{"inner says true", encodedClient{typedClient{pt: proxyModel.TypeTLS}, true}, true},
		{"inner says false", encodedClient{typedClient{pt: proxyModel.TypeFlare}, false}, false},
		{"no method, flare", typedClient{pt: proxyModel.TypeFlare}, true},
		{"no method, tls", typedClient{pt: proxyModel.TypeTLS}, false},
		{"no method, none", typedClient{pt: proxyModel.TypeNone}, false},
	}
	for _, c := range cases {
		ct := &CachedTracker{IClient: c.inner}
		if got := ct.EncodedBody(); got != c.want {
			t.Errorf("%s: EncodedBody = %v, want %v", c.name, got, c.want)
		}
	}
}
