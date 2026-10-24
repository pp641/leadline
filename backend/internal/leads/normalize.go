package leads

import (
	"net/url"
	"regexp"
	"strings"
)

// NormalizeWebsite returns a canonical URL ("https://example.com/path") and
// its bare domain ("example.com"). Empty or unparseable input yields "", "".
func NormalizeWebsite(raw string) (website, domain string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", ""
	}
	host := strings.ToLower(u.Hostname())
	if !strings.Contains(host, ".") {
		return "", ""
	}
	u.Host = host
	if p := u.Port(); p != "" {
		u.Host = host + ":" + p
	}
	u.Fragment = ""
	website = strings.TrimRight(u.String(), "/")
	return website, strings.TrimPrefix(host, "www.")
}

var spaceRe = regexp.MustCompile(`\s+`)

// NameKey is the dedupe key for company name + city.
func NameKey(company, city string) string {
	n := strings.ToLower(spaceRe.ReplaceAllString(strings.TrimSpace(company), " "))
	c := strings.ToLower(spaceRe.ReplaceAllString(strings.TrimSpace(city), " "))
	if n == "" {
		return ""
	}
	return n + "|" + c
}

// Clean trims and collapses whitespace in free-text fields.
func Clean(s string) string {
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

// NormalizeTags lowercases, trims and de-duplicates tags.
func NormalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range tags {
		t = strings.ToLower(Clean(t))
		if t == "" || len(t) > 50 || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
