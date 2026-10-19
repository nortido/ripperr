package mirrorsync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const yamlFixture = `id: sample
name: Sample
replaces:
  - old-sample
  - older-sample
language: ru-RU
links:
  - https://A.example/
  - "https://b.example/"
  - 'http://c.example/' # IPv6 only
  - ftp://skip.example/
  - not-a-url
  - https://a.example/dup
legacylinks:
  - https://old.example/
  - http://a.example/
  - https://OLD.example/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
  links:
    - https://nested.example/
settings:
  - name: username
    type: text
`

const csFixture = `public class Sample : IndexerBase
{
    public override string SiteLink { get; protected set; } = "https://X.example/";
    public override string[] AlternativeSiteLinks => new[]
    {
        "https://y.example/",
        "https://x.example/"
    };
    public override string[] LegacySiteLinks => new[]
    {
        "https://z.example/" // old
    };
    public override string Other => new[] { "https://other.example/" };
}
`

func TestParseYAML(t *testing.T) {
	got, err := parseYAML([]byte(yamlFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := source{Links: []string{"a.example", "b.example", "c.example"}, Legacy: []string{"old.example", "a.example"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestParseYAMLCRLF(t *testing.T) {
	got, err := parseYAML([]byte(strings.ReplaceAll(yamlFixture, "\n", "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Links, []string{"a.example", "b.example", "c.example"}) {
		t.Fatalf("links %v", got.Links)
	}
}

func TestParseYAMLNoLinks(t *testing.T) {
	for _, in := range []string{"", "id: x\nlegacylinks:\n  - https://old.example/\n", "links:\n  - ftp://x.example/\n"} {
		if _, err := parseYAML([]byte(in)); err != errNoLinks {
			t.Fatalf("%q: err %v", in, err)
		}
	}
}

func TestParseCS(t *testing.T) {
	got, err := parseCS([]byte(csFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := source{Links: []string{"x.example", "y.example"}, Legacy: []string{"z.example"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestParseCSSiteLinkOnly(t *testing.T) {
	in := `public override string SiteLink { get; protected set; } = "https://only.example/";
public override string[] LegacySiteLinks => Array.Empty<string>();
public int N { get; set; } = 1;
`
	got, err := parseCS([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, source{Links: []string{"only.example"}}) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseCSNoSiteLink(t *testing.T) {
	in := `public override string[] AlternativeSiteLinks => new[] { "https://y.example/" };`
	if _, err := parseCS([]byte(in)); err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Definitions/ok.yml":
			_, _ = w.Write([]byte(yamlFixture))
		case "/Indexers/Definitions/Ok.cs":
			_, _ = w.Write([]byte(csFixture))
		case "/Definitions/limited.yml":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/Definitions/huge.yml":
			_, _ = w.Write([]byte("links:\n  - https://a.example/\n" + strings.Repeat("# pad\n", 200000)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	base := srv.URL + "/"

	got, err := fetchSource(context.Background(), srv.Client(), base+"Definitions/ok.yml", "Definitions/ok.yml")
	if err != nil || !reflect.DeepEqual(got.Links, []string{"a.example", "b.example", "c.example"}) {
		t.Fatalf("yml: %+v %v", got, err)
	}
	got, err = fetchSource(context.Background(), srv.Client(), base+"Indexers/Definitions/Ok.cs", "Indexers/Definitions/Ok.cs")
	if err != nil || !reflect.DeepEqual(got.Links, []string{"x.example", "y.example"}) {
		t.Fatalf("cs: %+v %v", got, err)
	}
	for _, p := range []string{"Definitions/limited.yml", "Definitions/huge.yml", "Definitions/missing.yml"} {
		if _, err := fetchSource(context.Background(), srv.Client(), base+p, p); err == nil {
			t.Fatalf("%s: expected error", p)
		}
	}
}

func TestFetchSourceCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(yamlFixture))
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchSource(ctx, srv.Client(), srv.URL+"/Definitions/ok.yml", "Definitions/ok.yml"); err == nil {
		t.Fatal("expected error")
	}
}

func TestJackettSources(t *testing.T) {
	keys := []string{"anidub", "aniliberty", "animelayer", "booktracker", "kinozal", "newstudio", "nnm", "riperam", "rustorka", "rutor", "rutracker", "selezen", "tapochek", "teamhd", "toloka"}
	if len(jackettSources) != len(keys) {
		t.Fatalf("got %d entries", len(jackettSources))
	}
	for _, k := range keys {
		p, ok := jackettSources[k]
		if !ok {
			t.Fatalf("missing %s", k)
		}
		if !strings.HasSuffix(p, ".yml") && !strings.HasSuffix(p, ".cs") {
			t.Fatalf("%s: bad path %s", k, p)
		}
	}
}
