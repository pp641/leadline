// Package httpx contains the response envelope, request decoding and the
// cross-cutting HTTP middleware (request IDs, logging, metrics, recovery,
// security headers).
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"leadline/pkg/apperr"
	"leadline/pkg/logger"
)

// Envelope is the response shape for every JSON endpoint.
type Envelope struct {
	Data  any        `json:"data"`
	Error *ErrorBody `json:"error"`
	Meta  any        `json:"meta"`
}

type ErrorBody struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
}

func JSON(w http.ResponseWriter, status int, data any, meta any) {
	if meta == nil {
		meta = map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(Envelope{Data: data, Meta: meta}) //nolint:errcheck
}

func OK(w http.ResponseWriter, data any)      { JSON(w, http.StatusOK, data, nil) }
func Created(w http.ResponseWriter, data any) { JSON(w, http.StatusCreated, data, nil) }
func NoContent(w http.ResponseWriter)         { w.WriteHeader(http.StatusNoContent) }

// Error maps err to a status code and writes the error envelope. Unexpected
// errors are logged and returned as a generic 500 without internal detail.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	body := &ErrorBody{Code: "internal_error", Message: "an unexpected error occurred"}

	if e, ok := apperr.As(err); ok && e.Kind != apperr.KindInternal {
		body = &ErrorBody{Code: e.Code, Message: e.Message, Fields: e.Fields}
		switch e.Kind {
		case apperr.KindInvalid:
			status = http.StatusUnprocessableEntity
		case apperr.KindNotFound:
			status = http.StatusNotFound
		case apperr.KindConflict:
			status = http.StatusConflict
		case apperr.KindUnauthorized:
			status = http.StatusUnauthorized
		case apperr.KindForbidden:
			status = http.StatusForbidden
		case apperr.KindRateLimited:
			status = http.StatusTooManyRequests
		case apperr.KindUnavailable:
			status = http.StatusServiceUnavailable
			logger.From(r.Context()).Warn("dependency unavailable", "error", err)
		}
	} else {
		logger.From(r.Context()).Error("request failed", "error", err)
	}
	body.RequestID = RequestID(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(Envelope{Error: body, Meta: map[string]any{}}) //nolint:errcheck
}

const maxBody = 1 << 20

// Decode reads a JSON body into v, limiting its size.
func Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return apperr.Invalid("request body is required", nil)
		}
		return apperr.Invalid("invalid JSON body: "+err.Error(), nil)
	}
	return nil
}

// Page holds offset pagination parameters.
type Page struct {
	Limit  int
	Offset int
}

// PageFrom reads ?page= and ?per_page= (default 50, max 200).
func PageFrom(r *http.Request) Page {
	per := QueryInt(r, "per_page", 50)
	if per < 1 {
		per = 50
	}
	if per > 200 {
		per = 200
	}
	page := QueryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	return Page{Limit: per, Offset: (page - 1) * per}
}

func (p Page) Meta(total int64) map[string]any {
	return map[string]any{
		"total": total, "page": p.Offset/p.Limit + 1, "per_page": p.Limit,
	}
}

func QueryInt(r *http.Request, key string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil {
		return v
	}
	return def
}

// QueryList splits a comma-separated query parameter, also accepting repeats.
func QueryList(r *http.Request, key string) []string {
	var out []string
	for _, v := range r.URL.Query()[key] {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// TrustProxyHeaders enables X-Forwarded-For. Set it only when the API is
// reachable exclusively through a reverse proxy that overwrites the header.
var TrustProxyHeaders bool

// ClientIP returns the caller's IP address.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); TrustProxyHeaders && xff != "" {
		ip, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(ip)
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
