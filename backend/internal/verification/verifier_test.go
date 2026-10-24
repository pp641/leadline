package verification

import (
	"context"
	"testing"
)

func TestBasicWithoutDNS(t *testing.T) {
	v := NewBasic(false)
	cases := map[string]Status{
		"not-an-email":           StatusInvalid,
		"a@b":                    StatusInvalid,
		"someone@mailinator.com": StatusDisposable,
		"info@acme.com":          StatusRoleBased,
		"Priya@Acme.com":         StatusUnknown, // DNS disabled: cannot claim valid
	}
	for in, want := range cases {
		res, err := v.Verify(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != want {
			t.Errorf("%s: got %s (%s), want %s", in, res.Status, res.Reason, want)
		}
	}
}

func TestTestDomainsOnlyWhenEnabled(t *testing.T) {
	v := NewBasic(false)
	if res, _ := v.Verify(context.Background(), "jo@shop.example"); res.Status == StatusValid {
		t.Fatal("test domains must not be valid by default")
	}
	v.AllowTestDomains = true
	if res, _ := v.Verify(context.Background(), "jo@shop.example"); res.Status != StatusValid {
		t.Fatalf("got %s", res.Status)
	}
	if res, _ := v.Verify(context.Background(), "sales@shop.test"); res.Status != StatusRoleBased {
		t.Fatalf("got %s", res.Status)
	}
}
