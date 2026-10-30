package tlscapture

import (
	"strings"
	"testing"

	"golang.org/x/crypto/cryptobyte"
)

type helloExt struct {
	id   uint16
	data []byte
}

func u16s(vals ...uint16) []byte {
	var b cryptobyte.Builder
	for _, v := range vals {
		b.AddUint16(v)
	}
	return b.BytesOrPanic()
}

func lenPrefixed16(body []byte) []byte {
	var b cryptobyte.Builder
	b.AddUint16LengthPrefixed(func(c *cryptobyte.Builder) { c.AddBytes(body) })
	return b.BytesOrPanic()
}

func sniData(host string) []byte {
	var b cryptobyte.Builder
	b.AddUint16LengthPrefixed(func(l *cryptobyte.Builder) {
		l.AddUint8(0)
		l.AddUint16LengthPrefixed(func(n *cryptobyte.Builder) { n.AddBytes([]byte(host)) })
	})
	return b.BytesOrPanic()
}

func alpnData(protos ...string) []byte {
	var b cryptobyte.Builder
	b.AddUint16LengthPrefixed(func(l *cryptobyte.Builder) {
		for _, p := range protos {
			l.AddUint8LengthPrefixed(func(n *cryptobyte.Builder) { n.AddBytes([]byte(p)) })
		}
	})
	return b.BytesOrPanic()
}

func versionsData(vers ...uint16) []byte {
	var b cryptobyte.Builder
	b.AddUint8LengthPrefixed(func(l *cryptobyte.Builder) {
		for _, v := range vers {
			l.AddUint16(v)
		}
	})
	return b.BytesOrPanic()
}

func buildHello(legacy uint16, ciphers []uint16, exts []helloExt) []byte {
	var hs cryptobyte.Builder
	hs.AddUint8(1)
	hs.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint16(legacy)
		b.AddBytes(make([]byte, 32))
		b.AddUint8LengthPrefixed(func(s *cryptobyte.Builder) { s.AddBytes(make([]byte, 32)) })
		b.AddUint16LengthPrefixed(func(c *cryptobyte.Builder) {
			for _, v := range ciphers {
				c.AddUint16(v)
			}
		})
		b.AddUint8LengthPrefixed(func(c *cryptobyte.Builder) { c.AddUint8(0) })
		b.AddUint16LengthPrefixed(func(e *cryptobyte.Builder) {
			for _, x := range exts {
				e.AddUint16(x.id)
				e.AddUint16LengthPrefixed(func(d *cryptobyte.Builder) { d.AddBytes(x.data) })
			}
		})
	})
	var rec cryptobyte.Builder
	rec.AddUint8(0x16)
	rec.AddUint16(0x0301)
	rec.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(hs.BytesOrPanic()) })
	return rec.BytesOrPanic()
}

func vectorHello(legacy uint16, ciphers []uint16, extIDs []uint16, sigalgs []uint16, alpn []string, versions []uint16) []byte {
	cs := append([]uint16{0x0a0a}, ciphers...)
	var exts []helloExt
	exts = append(exts, helloExt{id: 0x1a1a})
	for _, id := range extIDs {
		x := helloExt{id: id}
		switch id {
		case 0x0000:
			x.data = sniData("example.com")
		case 0x0010:
			if len(alpn) > 0 {
				x.data = alpnData(alpn...)
			}
		case 0x002b:
			x.data = versionsData(append([]uint16{0x2a2a}, versions...)...)
		case 0x000d:
			x.data = lenPrefixed16(u16s(sigalgs...))
		}
		exts = append(exts, x)
	}
	return buildHello(legacy, cs, exts)
}

var (
	v1Ciphers = []uint16{0x1301, 0x1302, 0x1303, 0xc02b, 0xc02f, 0xc02c, 0xc030, 0xcca9, 0xcca8, 0xc013, 0xc014, 0x009c, 0x009d, 0x002f, 0x0035}
	v1Exts    = []uint16{0x001b, 0x0000, 0x0033, 0x0010, 0x4469, 0x0017, 0x002d, 0x000d, 0x0005, 0x0023, 0x0012, 0x002b, 0xff01, 0x000b, 0x000a, 0x0015}
	v1Sig     = []uint16{0x0403, 0x0804, 0x0401, 0x0503, 0x0805, 0x0501, 0x0806, 0x0601}

	v2Ciphers = []uint16{0xc030, 0xc02c, 0xc028, 0xc024, 0xc014, 0xc00a, 0x009f, 0x006b, 0x0039, 0xcca9, 0xcca8, 0xccaa, 0xff85, 0x00c4, 0x0088, 0x0081, 0x009d, 0x003d, 0x0035, 0x00c0, 0x0084, 0xc02f, 0xc02b, 0xc027, 0xc023, 0xc013, 0xc009, 0x009e, 0x0067, 0x0033, 0x00be, 0x0045, 0x009c, 0x003c, 0x002f, 0x00ba, 0x0041, 0xc011, 0xc007, 0x0005, 0x0004, 0xc012, 0xc008, 0x0016, 0x000a, 0x00ff}
	v2Exts    = []uint16{0x0000, 0x000b, 0x000a, 0x000d, 0x0010}
	v2Sig     = []uint16{0x0601, 0x0603, 0xefef, 0x0501, 0x0503, 0x0401, 0x0403, 0xeeee, 0xeded, 0x0301, 0x0303, 0x0201, 0x0203}

	v3Ciphers = []uint16{0x1301, 0x1303, 0x1302, 0xc02b, 0xc02f, 0xcca9, 0xcca8, 0xc02c, 0xc030, 0xc00a, 0xc009, 0xc013, 0xc014, 0x009c, 0x009d, 0x002f, 0x0035}
	v3Exts    = []uint16{0x0000, 0x0017, 0xff01, 0x000a, 0x000b, 0x0023, 0x0010, 0x0005, 0x0022, 0x0033, 0x002b, 0x000d, 0x002d, 0x001c, 0x0015}
	v3Sig     = []uint16{0x0403, 0x0503, 0x0603, 0x0804, 0x0805, 0x0806, 0x0401, 0x0501, 0x0601, 0x0203, 0x0201}
)

func TestJA4Vectors(t *testing.T) {
	cases := []struct {
		name string
		rec  []byte
		want string
	}{
		{"spec example", vectorHello(0x0303, v1Ciphers, v1Exts, v1Sig, []string{"h2"}, []uint16{0x0304}), "t13d1516h2_8daaf6152771_e5627efa2ab1"},
		{"tls-alpn-h2 pcap", vectorHello(0x0303, v2Ciphers, v2Exts, v2Sig, []string{"h2"}, nil), "t12d4605h2_85626a9a5f7f_aaf95bb78ec9"},
		{"tls12 pcap", vectorHello(0x0303, v3Ciphers, v3Exts, v3Sig, []string{"h2"}, []uint16{0x0304}), "t13d1715h2_5b57614c22b0_3d5424432f57"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := JA4(tc.rec)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("JA4 = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestJA4NoALPNNoSigalgs(t *testing.T) {
	got, err := JA4(vectorHello(0x0303, v1Ciphers, v1Exts, nil, nil, []uint16{0x0304}))
	if err != nil {
		t.Fatal(err)
	}
	a, _, c := splitJA4(t, got)
	if a != "t13d151600" {
		t.Fatalf("part a = %s, want t13d151600", a)
	}
	if c != "6d807ffa2a79" {
		t.Fatalf("ext hash = %s, want 6d807ffa2a79", c)
	}
}

func TestJA4ALPNForms(t *testing.T) {
	ext := []uint16{0x0000, 0x0010}
	cases := []struct {
		name  string
		alpn  []string
		wantA string
	}{
		{"http/1.1", []string{"http/1.1"}, "t12d0002h1"},
		{"single char", []string{"x"}, "t12d0002xx"},
		{"non alphanumeric", []string{"\xab\xcd"}, "t12d0002ad"},
	}
	for _, tc := range cases {
		got, err := JA4(vectorHello(0x0303, nil, ext, nil, tc.alpn, nil))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if a, _, _ := splitJA4(t, got); a != tc.wantA {
			t.Errorf("%s: part a = %s, want %s", tc.name, a, tc.wantA)
		}
	}
}

func TestJA4Malformed(t *testing.T) {
	good := vectorHello(0x0303, v2Ciphers, v2Exts, v2Sig, []string{"h2"}, nil)
	bad := [][]byte{
		nil,
		{1, 2, 3},
		append([]byte{0x15}, good[1:]...),
		good[:len(good)-3],
		append(append([]byte(nil), good[:5]...), append([]byte{0x02}, good[6:]...)...),
	}
	for i, rec := range bad {
		if got, err := JA4(rec); err == nil {
			t.Errorf("case %d: want error, got %s", i, got)
		}
	}
}

func splitJA4(t *testing.T, s string) (a, b, c string) {
	t.Helper()
	parts := strings.Split(s, "_")
	if len(parts) != 3 {
		t.Fatalf("bad JA4 %q", s)
	}
	return parts[0], parts[1], parts[2]
}

func TestJA4NoALPNExtension(t *testing.T) {
	got, err := JA4(vectorHello(0x0303, v1Ciphers, []uint16{0x0000, 0x002b}, nil, nil, []uint16{0x0304}))
	if err != nil {
		t.Fatal(err)
	}
	if a, _, _ := splitJA4(t, got); a != "t13d150200" {
		t.Fatalf("part a = %s, want t13d150200", a)
	}
}
