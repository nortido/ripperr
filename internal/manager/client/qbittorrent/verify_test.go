package qbittorrent

import "testing"

func TestIncomingHashSameAs_NilBufferWithMagnet(t *testing.T) {
	// manager.SubmitMagnet зовет IncomingHashSameAs с buf == nil на каждом магните.
	// Без проверки на nil это паника на горячем пути
	c := &Client{}
	hash := "0123456789abcdef0123456789abcdef01234567"
	magnet := "magnet:?xt=urn:btih:" + hash

	same, known := c.IncomingHashSameAs(nil, magnet, hash)
	if !same || !known {
		t.Errorf("got same=%v known=%v, want same=true known=true", same, known)
	}
}

func TestIncomingHashSameAs_NilBufferNoMagnet(t *testing.T) {
	c := &Client{}
	hash := "0123456789abcdef0123456789abcdef01234567"

	same, known := c.IncomingHashSameAs(nil, "", hash)
	if same || known {
		t.Errorf("got same=%v known=%v, want same=false known=false", same, known)
	}
}
