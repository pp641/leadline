package email

import (
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
)

// Variables available in subject and body templates.
var Variables = []string{
	"first_name", "last_name", "full_name", "job_title", "company_name", "website", "industry",
	"city", "state", "country", "sender_name", "sender_company", "personalized_line",
}

// placeholder matches {{ name }} or {{ name | fallback }}.
var placeholder = regexp.MustCompile(`\{\{\s*([a-zA-Z_]+)\s*(?:\|\s*([^}]*?)\s*)?\}\}`)

// ValidateTemplate reports unknown variables. It deliberately supports only
// simple substitution — no logic — so user templates cannot execute anything.
func ValidateTemplate(tpl string) error {
	var unknown []string
	for _, m := range placeholder.FindAllStringSubmatch(tpl, -1) {
		if !isVariable(m[1]) {
			unknown = append(unknown, m[1])
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown variable(s): %s", strings.Join(unknown, ", "))
	}
	return nil
}

func isVariable(name string) bool {
	for _, v := range Variables {
		if v == name {
			return true
		}
	}
	return false
}

// Render substitutes variables. A missing value uses the fallback, if any;
// it returns the names of variables that rendered empty so callers can warn.
func Render(tpl string, vars map[string]string) (string, []string) {
	var empty []string
	out := placeholder.ReplaceAllStringFunc(tpl, func(m string) string {
		sub := placeholder.FindStringSubmatch(m)
		v := strings.TrimSpace(vars[sub[1]])
		if v == "" {
			v = sub[2]
		}
		if v == "" {
			empty = append(empty, sub[1])
		}
		return v
	})
	return out, empty
}

var urlRe = regexp.MustCompile(`https?://[^\s<>"]+`)

// TextToHTML converts a plain-text body to minimal, safe HTML: content is
// escaped, URLs are linked and blank lines become paragraphs.
func TextToHTML(text string) string {
	var b strings.Builder
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		if strings.TrimSpace(para) == "" {
			continue
		}
		esc := html.EscapeString(para)
		esc = urlRe.ReplaceAllStringFunc(esc, func(u string) string {
			return `<a href="` + u + `">` + u + `</a>`
		})
		b.WriteString("<p>" + strings.ReplaceAll(esc, "\n", "<br>") + "</p>\n")
	}
	return b.String()
}

// Footer is the sender identification and unsubscribe notice appended to every
// campaign email (required by CAN-SPAM, GDPR/PECR and most ESP policies).
type Footer struct {
	SenderCompany   string
	PhysicalAddress string
	UnsubscribeURL  string
}

func (f Footer) Text() string {
	var b strings.Builder
	b.WriteString("\n\n--\n")
	if id := strings.TrimSpace(strings.Join(nonEmpty(f.SenderCompany, f.PhysicalAddress), ", ")); id != "" {
		b.WriteString(id + "\n")
	}
	b.WriteString("Don't want to hear from us? Unsubscribe: " + f.UnsubscribeURL + "\n")
	return b.String()
}

func (f Footer) HTML() string {
	id := html.EscapeString(strings.Join(nonEmpty(f.SenderCompany, f.PhysicalAddress), ", "))
	return `<hr style="border:none;border-top:1px solid #ddd;margin:24px 0 8px">` +
		`<p style="font-size:12px;color:#777">` + id + `<br>` +
		`Don't want to hear from us? <a href="` + html.EscapeString(f.UnsubscribeURL) + `">Unsubscribe</a></p>`
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// WrapHTML wraps body HTML in a minimal document.
func WrapHTML(body string) string {
	return `<!doctype html><html><body style="font-family:-apple-system,Segoe UI,Arial,sans-serif;font-size:14px;line-height:1.5;color:#222">` +
		body + `</body></html>`
}
