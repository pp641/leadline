// Package inbox processes inbound email: replies and bounces arriving in
// connected mailboxes (IMAP) or forwarded by a provider webhook. Each message
// is parsed, matched to the outbound email it answers, classified, and turned
// into a reply/bounce event that stops or continues the sequence.
package inbox

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

type Parsed struct {
	MessageID   string
	InReplyTo   string
	References  []string
	FromEmail   string
	FromName    string
	Subject     string
	Date        time.Time
	Text        string // reply text with quoted history removed
	FullText    string // whole decoded text (used for bounce parsing)
	AutoReply   bool
	Bounce      bool
	BounceHard  bool
	EmbeddedIDs []string // Message-IDs mentioned in a bounce report
}

var (
	angleIDRe     = regexp.MustCompile(`<([^<>\s]+@[^<>\s]+)>`)
	dsnStatusRe   = regexp.MustCompile(`(?im)^\s*Status:\s*([245])\.\d{1,3}\.\d{1,3}`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	quoteHeaderRe = regexp.MustCompile(`(?im)^(On .{5,200}wrote:|-{2,}\s*Original Message\s*-{2,}|From:\s.+\nSent:\s.+|_{10,})\s*$`)
)

// Parse reads an RFC 5322 message.
func Parse(raw []byte) (*Parsed, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	h := msg.Header
	p := &Parsed{
		MessageID: trimID(h.Get("Message-Id")),
		InReplyTo: trimID(h.Get("In-Reply-To")),
		Subject:   decodeHeader(h.Get("Subject")),
	}
	for _, m := range angleIDRe.FindAllStringSubmatch(h.Get("References"), -1) {
		p.References = append(p.References, m[1])
	}
	if from, err := mail.ParseAddress(decodeHeader(h.Get("From"))); err == nil {
		p.FromEmail, p.FromName = strings.ToLower(from.Address), from.Name
	}
	if d, err := h.Date(); err == nil {
		p.Date = d
	} else {
		p.Date = time.Now()
	}

	text, contentType := extractText(h.Get("Content-Type"), h.Get("Content-Transfer-Encoding"), msg.Body)
	p.FullText = text
	p.Text = stripQuoted(text)

	auto := strings.ToLower(h.Get("Auto-Submitted"))
	subj := strings.ToLower(p.Subject)
	p.AutoReply = (auto != "" && auto != "no") || h.Get("X-Autoreply") != "" || h.Get("X-Autorespond") != "" ||
		strings.EqualFold(h.Get("Precedence"), "auto_reply") ||
		strings.HasPrefix(subj, "out of office") || strings.HasPrefix(subj, "automatic reply") || strings.HasPrefix(subj, "auto:")

	local := strings.SplitN(p.FromEmail, "@", 2)[0]
	p.Bounce = strings.Contains(contentType, "multipart/report") || local == "mailer-daemon" || local == "postmaster" ||
		strings.HasPrefix(subj, "undeliverable") || strings.HasPrefix(subj, "delivery status notification") ||
		strings.Contains(subj, "mail delivery failed") || strings.Contains(subj, "returned mail")
	if p.Bounce {
		p.AutoReply = false
		p.BounceHard = true
		if m := dsnStatusRe.FindStringSubmatch(text); m != nil && m[1] == "4" {
			p.BounceHard = false
		}
		for _, m := range angleIDRe.FindAllStringSubmatch(text, -1) {
			p.EmbeddedIDs = append(p.EmbeddedIDs, m[1])
		}
	}
	if p.MessageID == "" {
		// Some bounce generators omit it; derive a stable synthetic ID.
		p.MessageID = "synthetic-" + base64.RawURLEncoding.EncodeToString([]byte(p.FromEmail+"|"+p.Subject+"|"+p.Date.UTC().Format(time.RFC3339)))
	}
	return p, nil
}

func trimID(s string) string {
	s = strings.TrimSpace(s)
	if m := angleIDRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return strings.Trim(s, "<>")
}

var wordDecoder = &mime.WordDecoder{}

func decodeHeader(s string) string {
	if d, err := wordDecoder.DecodeHeader(s); err == nil {
		return d
	}
	return s
}

// extractText returns the best text representation and the top-level media type.
func extractText(ctype, cte string, body io.Reader) (string, string) {
	media, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		media = "text/plain"
	}
	if strings.HasPrefix(media, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		var plain, html, other strings.Builder
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			t, sub := extractText(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part)
			switch {
			case strings.HasPrefix(sub, "text/plain") && plain.Len() == 0:
				plain.WriteString(t)
			case strings.HasPrefix(sub, "text/html") && html.Len() == 0:
				html.WriteString(t)
			case strings.HasPrefix(sub, "multipart/"), strings.HasPrefix(sub, "message/"),
				strings.HasPrefix(sub, "text/") && sub != "text/plain" && sub != "text/html":
				// Includes delivery-status and text/rfc822-headers parts of bounces.
				other.WriteString(t + "\n")
			}
		}
		switch {
		case plain.Len() > 0:
			return plain.String() + "\n" + other.String(), media
		case html.Len() > 0:
			return html.String() + "\n" + other.String(), media
		}
		return other.String(), media
	}
	data, _ := io.ReadAll(io.LimitReader(decodeCTE(cte, body), 2<<20))
	text := string(data)
	if media == "text/html" {
		text = htmlToText(text)
	}
	if strings.HasPrefix(media, "text/") || strings.HasPrefix(media, "message/") {
		return text, media
	}
	return "", media
}

func decodeCTE(cte string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, &stripNewlines{r: r})
	}
	return r
}

type stripNewlines struct{ r io.Reader }

func (s *stripNewlines) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	j := 0
	for i := 0; i < n; i++ {
		if p[i] != '\r' && p[i] != '\n' {
			p[j] = p[i]
			j++
		}
	}
	return j, err
}

func htmlToText(s string) string {
	s = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>`).ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	return strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
}

// stripQuoted keeps only the new part of a reply.
func stripQuoted(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if loc := quoteHeaderRe.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			continue
		}
		out = append(out, line)
	}
	s = strings.TrimSpace(strings.Join(out, "\n"))
	if len(s) > 5000 {
		s = s[:5000]
	}
	return s
}
