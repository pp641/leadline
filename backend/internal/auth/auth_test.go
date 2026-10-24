package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestTokenRoundTrip(t *testing.T) {
	ti := NewTokenIssuer([]byte("0123456789abcdef0123456789abcdef"), time.Minute)
	uid := uuid.New()
	tok, exp, err := ti.Issue(uid, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exp) > time.Minute || time.Until(exp) < 50*time.Second {
		t.Errorf("unexpected expiry %v", exp)
	}
	got, err := ti.Parse(tok)
	if err != nil || got != uid {
		t.Fatalf("Parse = %v, %v", got, err)
	}
}

func TestTokenRejections(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	ti := NewTokenIssuer(secret, -time.Minute) // already expired
	tok, _, _ := ti.Issue(uuid.New(), uuid.New())
	if _, err := ti.Parse(tok); err == nil {
		t.Error("expired token accepted")
	}

	other := NewTokenIssuer([]byte("another-secret-another-secret-xx"), time.Minute)
	tok, _, _ = other.Issue(uuid.New(), uuid.New())
	if _, err := NewTokenIssuer(secret, time.Minute).Parse(tok); err == nil {
		t.Error("token signed with another secret accepted")
	}

	// alg=none must be rejected.
	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{Subject: uuid.NewString(), Issuer: issuer,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))})
	s, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := NewTokenIssuer(secret, time.Minute).Parse(s); err == nil {
		t.Error("alg=none token accepted")
	}
}

func TestRoles(t *testing.T) {
	if !RoleOwner.AtLeast(RoleAdmin) || !RoleAdmin.AtLeast(RoleMember) || RoleViewer.AtLeast(RoleMember) {
		t.Error("role ordering wrong")
	}
	if Role("superuser").Valid() || Role("superuser").AtLeast(RoleViewer) {
		t.Error("unknown role must grant nothing")
	}
}
