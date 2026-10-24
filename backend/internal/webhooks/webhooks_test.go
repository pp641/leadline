package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func TestSign(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	body := []byte(`{"type":"lead.created"}`)
	got := Sign("whsec_x", ts, body)
	m := hmac.New(sha256.New, []byte("whsec_x"))
	fmt.Fprintf(m, "%d.%s", ts.Unix(), body)
	want := fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(m.Sum(nil)))
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
