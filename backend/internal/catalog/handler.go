package catalog

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"leadline/internal/auth"
	"leadline/pkg/apperr"
	"leadline/pkg/httpx"
)

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/catalog/status", h.mw.Org(auth.RoleViewer, h.status))
	mux.HandleFunc("GET /api/v1/catalog/facets", h.mw.Org(auth.RoleViewer, h.facets))
	mux.HandleFunc("POST /api/v1/catalog/search", h.mw.Org(auth.RoleViewer, h.search))
	mux.HandleFunc("POST /api/v1/catalog/import", h.mw.Org(auth.RoleMember, h.importCompanies))
	mux.HandleFunc("GET /api/v1/leads/{id}/enrichment", h.mw.Org(auth.RoleViewer, h.report))
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	httpx.OK(w, h.svc.Status(ctx))
}

func (h *Handler) facets(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Facets(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	var in SearchInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Search(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) importCompanies(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CompanyIDs []uuid.UUID `json:"company_ids"`
		Tags       []string    `json:"tags"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Import(r.Context(), auth.MustPrincipal(r.Context()), in.CompanyIDs, in.Tags)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("lead"))
		return
	}
	out, err := h.svc.Report(r.Context(), auth.MustPrincipal(r.Context()).OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}
