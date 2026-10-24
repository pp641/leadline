package httpx

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/google/uuid"

	"leadline/pkg/logger"
	"leadline/pkg/metrics"
)

type Middleware func(http.Handler) http.Handler

// Chain applies middleware so the first listed runs outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type requestIDKey struct{}
type clientIPKey struct{}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// IP returns the client IP recorded for this request, if any.
func IP(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	return ip
}

// WithRequestID assigns a request ID (honouring a sane inbound X-Request-ID)
// and attaches it to the request logger.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		ctx = context.WithValue(ctx, clientIPKey{}, ClientIP(r))
		ctx = logger.With(ctx, "request_id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.From(r.Context()).Error("panic", "panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
				Error(w, r, fmt.Errorf("panic: %v", rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// LogAndMeasure writes one structured access log line and records metrics.
// Auth middleware adds organization_id/user_id to the request logger, so they
// appear here via the AccessLogAttrs hook.
func LogAndMeasure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		attrs := &AccessAttrs{}
		ctx := context.WithValue(r.Context(), accessAttrsKey{}, attrs)
		next.ServeHTTP(rec, r.WithContext(ctx))

		route := attrs.Route
		if route == "" {
			route = "unmatched"
		}
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		dur := time.Since(start)
		metrics.APIRequests.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
		metrics.APILatency.WithLabelValues(route, r.Method).Observe(dur.Seconds())

		if r.URL.Path == "/health" || r.URL.Path == "/ready" || r.URL.Path == "/metrics" {
			return
		}
		logger.From(r.Context()).Info("http request",
			"method", r.Method, "path", r.URL.Path, "route", route,
			"status", rec.status, "duration_ms", dur.Milliseconds(), "bytes", rec.bytes,
			"organization_id", attrs.OrganizationID, "user_id", attrs.UserID,
		)
	})
}

// AccessAttrs lets inner middleware (auth) report identity to the access log.
type AccessAttrs struct {
	OrganizationID string
	UserID         string
	Route          string
}

// CaptureRoute must wrap the ServeMux directly: the mux records the matched
// pattern on the request it receives, which outer middleware never sees.
func CaptureRoute(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		if a, ok := r.Context().Value(accessAttrsKey{}).(*AccessAttrs); ok {
			a.Route = r.Pattern
		}
	})
}

type accessAttrsKey struct{}

func SetAccessIdentity(ctx context.Context, userID, orgID string) {
	if a, ok := ctx.Value(accessAttrsKey{}).(*AccessAttrs); ok {
		if userID != "" {
			a.UserID = userID
		}
		if orgID != "" {
			a.OrganizationID = orgID
		}
	}
}
