package workflows

import (
	"net/http"

	"github.com/google/uuid"

	"leadline/internal/auth"
	"leadline/pkg/apperr"
	"leadline/pkg/httpx"
)

type Handler struct {
	svc    *Service
	engine *Engine
	mw     *auth.Middleware
}

func NewHandler(svc *Service, e *Engine, mw *auth.Middleware) *Handler {
	return &Handler{svc: svc, engine: e, mw: mw}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/workflows/meta", h.mw.Org(auth.RoleViewer, h.meta))
	mux.HandleFunc("GET /api/v1/workflows/templates", h.mw.Org(auth.RoleViewer, h.templates))
	mux.HandleFunc("GET /api/v1/workflows", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/workflows", h.mw.Org(auth.RoleAdmin, h.create))
	mux.HandleFunc("GET /api/v1/workflows/{id}", h.mw.Org(auth.RoleViewer, h.get))
	mux.HandleFunc("PUT /api/v1/workflows/{id}", h.mw.Org(auth.RoleAdmin, h.update))
	mux.HandleFunc("DELETE /api/v1/workflows/{id}", h.mw.Org(auth.RoleAdmin, h.delete))
	mux.HandleFunc("GET /api/v1/workflows/{id}/runs", h.mw.Org(auth.RoleViewer, h.runs))
	mux.HandleFunc("POST /api/v1/workflows/{id}/test", h.mw.Org(auth.RoleMember, h.test))
}

func pathID(r *http.Request) (uuid.UUID, error) {
	v, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.NotFound("workflow")
	}
	return v, nil
}

func (h *Handler) meta(w http.ResponseWriter, r *http.Request) { httpx.OK(w, MetaInfo()) }

func (h *Handler) templates(w http.ResponseWriter, r *http.Request) { httpx.OK(w, Templates) }

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()).OrgID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Create(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, out)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Get(r.Context(), auth.MustPrincipal(r.Context()).OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Update(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.Delete(r.Context(), auth.MustPrincipal(r.Context()), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) runs(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Runs(r.Context(), auth.MustPrincipal(r.Context()).OrgID, id, httpx.QueryInt(r, "limit", 50))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	id, err := pathID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in struct {
		LeadID         *uuid.UUID `json:"lead_id"`
		DealID         *uuid.UUID `json:"deal_id"`
		Classification string     `json:"classification"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.LeadID == nil && in.DealID == nil {
		httpx.Error(w, r, apperr.Invalid("validation failed", map[string]string{"lead_id": "pick a lead or a deal to test against"}))
		return
	}
	wf, err := h.svc.Get(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.engine.Test(r.Context(), p.OrgID, wf, in.LeadID, in.DealID, in.Classification)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}
