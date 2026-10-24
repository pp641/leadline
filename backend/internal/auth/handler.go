package auth

import (
	"net/http"
	"time"

	"leadline/pkg/apperr"
	"leadline/pkg/httpx"
	"leadline/pkg/redisx"
	"leadline/pkg/validate"
)

const refreshCookie = "ll_refresh"

type Handler struct {
	svc          *Service
	mw           *Middleware
	ipLimiter    *redisx.Limiter
	emailLimiter *redisx.Limiter
	secureCookie bool
}

func NewHandler(svc *Service, mw *Middleware, ipLimiter, emailLimiter *redisx.Limiter, secureCookie bool) *Handler {
	return &Handler{svc: svc, mw: mw, ipLimiter: ipLimiter, emailLimiter: emailLimiter, secureCookie: secureCookie}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("GET /api/v1/auth/me", h.mw.User(h.me))
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if !h.ipLimiter.Allow(r.Context(), httpx.ClientIP(r)) {
		httpx.Error(w, r, apperr.RateLimited())
		return
	}
	var in RegisterInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	res, err := h.svc.Register(r.Context(), in, clientInfo(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	h.setRefreshCookie(w, res.RefreshToken, res.RefreshExp)
	httpx.Created(w, res)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var in LoginInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !h.ipLimiter.Allow(r.Context(), httpx.ClientIP(r)) ||
		!h.emailLimiter.Allow(r.Context(), validate.NormalizeEmail(in.Email)) {
		httpx.Error(w, r, apperr.RateLimited())
		return
	}
	res, err := h.svc.Login(r.Context(), in, clientInfo(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	h.setRefreshCookie(w, res.RefreshToken, res.RefreshExp)
	httpx.OK(w, res)
}

// refresh and logout authenticate with the httpOnly cookie, so they require a
// custom header: browsers will not attach it to cross-site requests without a
// CORS preflight, which this API never grants (CSRF protection). The cookie is
// also SameSite=Strict.
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Requested-With") == "" {
		httpx.Error(w, r, apperr.Forbidden("missing X-Requested-With header"))
		return
	}
	c, _ := r.Cookie(refreshCookie)
	token := ""
	if c != nil {
		token = c.Value
	}
	res, err := h.svc.Refresh(r.Context(), token, clientInfo(r))
	if err != nil {
		h.clearRefreshCookie(w)
		httpx.Error(w, r, err)
		return
	}
	h.setRefreshCookie(w, res.RefreshToken, res.RefreshExp)
	httpx.OK(w, res)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Requested-With") == "" {
		httpx.Error(w, r, apperr.Forbidden("missing X-Requested-With header"))
		return
	}
	if c, _ := r.Cookie(refreshCookie); c != nil {
		if err := h.svc.Logout(r.Context(), c.Value); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}
	h.clearRefreshCookie(w)
	httpx.NoContent(w)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	u, err := h.svc.Me(r.Context(), MustPrincipal(r.Context()).UserID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, u)
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: token, Path: "/api/v1/auth", Expires: exp,
		HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: "", Path: "/api/v1/auth", MaxAge: -1,
		HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteStrictMode,
	})
}

func clientInfo(r *http.Request) ClientInfo {
	return ClientInfo{UserAgent: r.UserAgent(), IP: httpx.ClientIP(r)}
}
