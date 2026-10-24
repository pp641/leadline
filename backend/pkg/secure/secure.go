// Package secure holds small cryptographic helpers: random tokens, hashing,
// HMAC-signed tokens and AES-GCM encryption for stored credentials.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

// RandomToken returns a URL-safe random string with n bytes of entropy.
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken is used to store refresh tokens: lookups by hash, never plaintext.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Sign returns "payload.signature" where payload is base64url-encoded.
func Sign(secret []byte, payload []byte) string {
	p := base64.RawURLEncoding.EncodeToString(payload)
	return p + "." + mac(secret, p)
}

// Verify checks a token produced by Sign and returns the payload.
func Verify(secret []byte, token string) ([]byte, error) {
	p, sig, ok := strings.Cut(token, ".")
	if !ok {
		return nil, errors.New("malformed token")
	}
	if subtle.ConstantTimeCompare([]byte(sig), []byte(mac(secret, p))) != 1 {
		return nil, errors.New("invalid signature")
	}
	return base64.RawURLEncoding.DecodeString(p)
}

// HMACHex is used for webhook signature checks.
func HMACHex(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return encodeHex(m.Sum(nil))
}

func mac(secret []byte, s string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func encodeHex(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
}

// Box encrypts small secrets (mailbox / integration credentials) at rest.
type Box struct{ aead cipher.AEAD }

func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("encryption key must be 32 bytes")
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

func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (b *Box) Open(ciphertext []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(ciphertext) < n {
		return nil, errors.New("ciphertext too short")
	}
	return b.aead.Open(nil, ciphertext[:n], ciphertext[n:], nil)
}
