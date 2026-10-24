package secure

import (
	"bytes"
	"testing"
)

func TestSignVerify(t *testing.T) {
	secret := []byte("s3cret")
	tok := Sign(secret, []byte(`{"e":"a@b.test"}`))
	got, err := Verify(secret, tok)
	if err != nil || string(got) != `{"e":"a@b.test"}` {
		t.Fatalf("Verify = %q, %v", got, err)
	}
	if _, err := Verify([]byte("other"), tok); err == nil {
		t.Error("wrong secret accepted")
	}
	tampered := "eyJlIjoiZXZpbEBiLnRlc3QifQ" + tok[len(tok)-44:]
	if _, err := Verify(secret, tampered); err == nil {
		t.Error("tampered payload accepted")
	}
	if _, err := Verify(secret, "garbage"); err == nil {
		t.Error("malformed token accepted")
	}
}

func TestBox(t *testing.T) {
	b, err := NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ct, err := b.Seal([]byte("smtp-password"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("smtp-password")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	pt, err := b.Open(ct)
	if err != nil || string(pt) != "smtp-password" {
		t.Fatalf("Open = %q, %v", pt, err)
	}
	ct[len(ct)-1] ^= 1
	if _, err := b.Open(ct); err == nil {
		t.Error("tampered ciphertext accepted")
	}
}
