package securestore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

var ErrUnavailable = errors.New("secret storage unavailable")

type Cipher struct {
	aead cipher.AEAD
}

func NewFromEnvironment() (*Cipher, error) {
	raw := os.Getenv("V0.7_SETTINGS_ENCRYPTION_KEY")
	if raw == "" {
		return &Cipher{}, nil
	}
	key, err := decodeKey(raw)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize settings encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize settings encryption: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func decodeKey(raw string) ([]byte, error) {
	if len(raw) == 64 {
		if key, err := hex.DecodeString(raw); err == nil && len(key) == 32 {
			return key, nil
		}
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("V0.7_SETTINGS_ENCRYPTION_KEY must decode to exactly 32 bytes")
	}
	return key, nil
}

func (c *Cipher) Available() bool {
	return c != nil && c.aead != nil
}

func (c *Cipher) Encrypt(scope, owner, field, plaintext string) ([]byte, error) {
	if !c.Available() {
		return nil, ErrUnavailable
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	aad := []byte(scope + "\x00" + owner + "\x00" + field)
	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), aad)
	return append([]byte("v1"), append(nonce, sealed...)...), nil
}

func (c *Cipher) Decrypt(scope, owner, field string, ciphertext []byte) (string, error) {
	if !c.Available() {
		return "", ErrUnavailable
	}
	if len(ciphertext) < 2+c.aead.NonceSize() || string(ciphertext[:2]) != "v1" {
		return "", errors.New("invalid encrypted secret")
	}
	nonce := ciphertext[2 : 2+c.aead.NonceSize()]
	aad := []byte(scope + "\x00" + owner + "\x00" + field)
	plain, err := c.aead.Open(nil, nonce, ciphertext[2+c.aead.NonceSize():], aad)
	if err != nil {
		return "", errors.New("cannot decrypt stored secret")
	}
	return string(plain), nil
}
