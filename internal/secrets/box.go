package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"strings"
)

const (
	keySize        = 32
	envelopePrefix = "enc:v1:"
)

var (
	ErrNotSealed = errors.New("secrets: value is not sealed")
	ErrOpen      = errors.New("secrets: cannot open sealed value")
)

type Box struct {
	aead cipher.AEAD
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plaintext []byte, aad string) string {
	return envelopePrefix + base64.StdEncoding.EncodeToString(b.aead.Seal(nil, nil, plaintext, []byte(aad)))
}

func (b *Box) Open(envelope, aad string) ([]byte, error) {
	payload, ok := strings.CutPrefix(envelope, envelopePrefix)
	if !ok {
		return nil, ErrNotSealed
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, ErrOpen
	}
	plaintext, err := b.aead.Open(nil, nil, raw, []byte(aad))
	if err != nil {
		return nil, ErrOpen
	}
	return plaintext, nil
}

func IsSealed(s string) bool {
	return strings.HasPrefix(s, envelopePrefix)
}
