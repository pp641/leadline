// Package vault seals third-party credentials (API keys, mailbox passwords,
// webhook secrets) with AES-256-GCM before they reach the database.
package vault

import (
	"errors"

	"leadline/pkg/apperr"
	"leadline/pkg/secure"
)

var ErrNoKey = apperr.Conflict("encryption_key_missing", "ENCRYPTION_KEY is not configured on the server, so credentials cannot be stored")

type Vault struct{ box *secure.Box }

// New returns a vault; with a nil/empty key every operation fails with ErrNoKey.
func New(key []byte) (*Vault, error) {
	if len(key) == 0 {
		return &Vault{}, nil
	}
	b, err := secure.NewBox(key)
	if err != nil {
		return nil, err
	}
	return &Vault{box: b}, nil
}

func (v *Vault) Enabled() bool { return v != nil && v.box != nil }

func (v *Vault) Seal(plain string) ([]byte, error) {
	if !v.Enabled() {
		return nil, ErrNoKey
	}
	return v.box.Seal([]byte(plain))
}

func (v *Vault) Open(sealed []byte) (string, error) {
	if !v.Enabled() {
		return "", ErrNoKey
	}
	if len(sealed) == 0 {
		return "", errors.New("no secret stored")
	}
	b, err := v.box.Open(sealed)
	return string(b), err
}
