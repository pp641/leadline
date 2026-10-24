package email

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"sort"
	"strings"
	"time"
)

// BuildMIME renders msg as an RFC 5322 multipart/alternative message.
// Header values are sanitized so user content can never inject headers.
func BuildMIME(msg EmailMessage, now time.Time) ([]byte, error) {
	var b bytes.Buffer
	boundary := randomBoundary()

	from := (&mail.Address{Name: clean(msg.FromName), Address: clean(msg.FromEmail)}).String()
	to := (&mail.Address{Name: clean(msg.ToName), Address: clean(msg.ToEmail)}).String()

	h := []struct{ k, v string }{
		{"From", from},
		{"To", to},
		{"Subject", mime.QEncoding.Encode("utf-8", clean(msg.Subject))},
		{"Date", now.Format(time.RFC1123Z)},
		{"Message-ID", "<" + clean(msg.MessageID) + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", `multipart/alternative; boundary="` + boundary + `"`},
	}
	if msg.ReplyTo != "" {
		h = append(h, struct{ k, v string }{"Reply-To", clean(msg.ReplyTo)})
	}
	if msg.InReplyTo != "" {
		ref := "<" + clean(msg.InReplyTo) + ">"
		h = append(h, struct{ k, v string }{"In-Reply-To", ref}, struct{ k, v string }{"References", ref})
	}
	if msg.UnsubscribeURL != "" {
		lu := "<" + clean(msg.UnsubscribeURL) + ">"
		if msg.UnsubscribeTo != "" {
			lu = "<mailto:" + clean(msg.UnsubscribeTo) + "?subject=unsubscribe>, " + lu
		}
		h = append(h,
			struct{ k, v string }{"List-Unsubscribe", lu},
			struct{ k, v string }{"List-Unsubscribe-Post", "List-Unsubscribe=One-Click"},
		)
	}
	keys := make([]string, 0, len(msg.Headers))
	for k := range msg.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h = append(h, struct{ k, v string }{clean(k), clean(msg.Headers[k])})
	}
	for _, kv := range h {
		fmt.Fprintf(&b, "%s: %s\r\n", kv.k, kv.v)
	}
	b.WriteString("\r\n")

	for _, part := range []struct{ ctype, body string }{
		{"text/plain; charset=utf-8", msg.TextBody},
		{"text/html; charset=utf-8", msg.HTMLBody},
	} {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.ctype)
		qp := quotedprintable.NewWriter(&b)
		if _, err := qp.Write([]byte(strings.ReplaceAll(part.body, "\n", "\r\n"))); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes(), nil
}

// clean strips CR/LF to prevent header injection.
func clean(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(strings.TrimSpace(s))
}

func randomBoundary() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return "ll-" + hex.EncodeToString(buf[:])
}
