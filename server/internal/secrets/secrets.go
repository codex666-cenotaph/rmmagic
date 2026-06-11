// Package secrets provides application-level envelope encryption for
// small secrets at rest (TOTP seeds, webhook signing keys, SMTP
// credentials). AES-256-GCM with a random nonce per value; the master
// key comes from the environment (KMS later) and never touches logs.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

type Box struct {
	aead cipher.AEAD
}

// NewBox builds a Box from a 64-char hex master key (32 bytes).
func NewBox(masterKeyHex string) (*Box, error) {
	key, err := hex.DecodeString(masterKeyHex)
	if err != nil || len(key) != 32 {
		return nil, errors.New("master key must be 64 hex chars (32 bytes)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext; the additional data binds the ciphertext to
// its context (e.g. the owning user ID) so values can't be swapped
// between rows.
func (b *Box) Seal(plaintext, additional []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, additional), nil
}

func (b *Box) Open(ciphertext, additional []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(ciphertext) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return b.aead.Open(nil, ciphertext[:ns], ciphertext[ns:], additional)
}
