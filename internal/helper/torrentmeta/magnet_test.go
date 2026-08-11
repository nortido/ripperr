package torrentmeta

import "testing"

func TestBtihFromMagnet_Hex(t *testing.T) {
	const hex = "0123456789abcdef0123456789abcdef01234567"
	got, ok := BtihFromMagnet("magnet:?xt=urn:btih:" + hex + "&dn=test")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != hex {
		t.Errorf("got %q, want %q", got, hex)
	}
}

func TestBtihFromMagnet_Base32(t *testing.T) {
	const hex = "0123456789abcdef0123456789abcdef01234567"
	const base32 = "AERUKZ4JVPG66AJDIVTYTK6N54ASGRLH"
	got, ok := BtihFromMagnet("magnet:?xt=urn:btih:" + base32)
	if !ok {
		t.Fatal("expected ok")
	}
	if got != hex {
		t.Errorf("got %q, want %q", got, hex)
	}
}

func TestBtihFromMagnet_Invalid(t *testing.T) {
	if _, ok := BtihFromMagnet("magnet:?dn=test"); ok {
		t.Fatal("expected not ok without xt")
	}
}
