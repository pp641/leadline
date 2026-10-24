package sources

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"leadline/internal/integrations"
)

// obj is a decoded JSON object with forgiving accessors: providers nest and
// name fields differently (and change them), so readers try several keys.
type obj map[string]any

// path walks dotted keys ("location.country").
func (o obj) path(key string) any {
	var cur any = map[string]any(o)
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

// s returns the first non-empty string (or number) among keys.
func (o obj) s(keys ...string) string {
	for _, k := range keys {
		switch v := o.path(k).(type) {
		case string:
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case map[string]any:
			// {value: …} wrappers (Crunchbase) and {text: …} (Google).
			for _, inner := range []string{"value", "text", "url", "name"} {
				if s, ok := v[inner].(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
	}
	return ""
}

var firstNumber = regexp.MustCompile(`\d[\d,]*`)

// n returns the first positive integer among keys; strings like "51-200" or
// "1,000+" yield their first number.
func (o obj) n(keys ...string) int {
	for _, k := range keys {
		switch v := o.path(k).(type) {
		case float64:
			if v > 0 {
				return int(v)
			}
		case string:
			if m := firstNumber.FindString(v); m != "" {
				if n, err := strconv.Atoi(strings.ReplaceAll(m, ",", "")); err == nil && n > 0 {
					return n
				}
			}
		case map[string]any:
			if n := obj(v).n("value", "min", "value_usd"); n > 0 {
				return n
			}
		}
	}
	return 0
}

func (o obj) b(key string) bool {
	v, _ := o.path(key).(bool)
	return v
}

func (o obj) o(keys ...string) obj {
	for _, k := range keys {
		if m, ok := o.path(k).(map[string]any); ok {
			return obj(m)
		}
	}
	return obj{}
}

func (o obj) arr(keys ...string) []obj {
	for _, k := range keys {
		if list, ok := o.path(k).([]any); ok {
			out := make([]obj, 0, len(list))
			for _, x := range list {
				if m, ok := x.(map[string]any); ok {
					out = append(out, obj(m))
				}
			}
			return out
		}
	}
	return nil
}

func (o obj) strs(keys ...string) []string {
	for _, k := range keys {
		if list, ok := o.path(k).([]any); ok {
			var out []string
			for _, x := range list {
				switch v := x.(type) {
				case string:
					out = append(out, v)
				case map[string]any:
					if s := obj(v).s("value", "name"); s != "" {
						out = append(out, s)
					}
				}
			}
			return out
		}
	}
	return nil
}

// call is a JSON request to a provider API.
func call(ctx context.Context, name, method, u string, headers map[string]string, body any, out any) (int, error) {
	return integrations.CallJSON(ctx, name, method, u, headers, body, nil, out)
}

func callForm(ctx context.Context, name, u string, form url.Values, out any) (int, error) {
	return integrations.CallJSON(ctx, name, http.MethodPost, u, nil, nil, form, out)
}

// yearOf extracts a 4-digit year ("2015-03-01" → 2015).
func yearOf(s string) int {
	if len(s) >= 4 {
		if y, err := strconv.Atoi(s[:4]); err == nil && y > 1800 && y < 2200 {
			return y
		}
	}
	return 0
}
