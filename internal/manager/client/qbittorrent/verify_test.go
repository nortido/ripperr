package qbittorrent

import "testing"

func TestIncomingHashSameAs_NilBufferWithMagnet(t *testing.T) {
	// manager.SubmitMagnet вызывает IncomingHashSameAs с buf == nil на каждом
	// магнитном сабмите: без guard на nil это паника на горячем пути,
	// buf.Len() и buf.Bytes() на nil-указателе
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
