package organization

import (
	"net/http"

	"github.com/google/uuid"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/pkg/apperr"
	"leadline/pkg/httpx"
)

type Handler struct {
	svc   *Service
	mw    *auth.Middleware
	audit *audit.Log
}

func NewHandler(svc *Service, mw *auth.Middleware, a *audit.Log) *Handler {
	return &Handler{svc: svc, mw: mw, audit: a}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/organizations", h.mw.User(h.list))
	mux.HandleFunc("POST /api/v1/organizations", h.mw.User(h.create))
	// "current" = the organization named in X-Organization-ID.
	mux.HandleFunc("GET /api/v1/organizations/current", h.mw.Org(auth.RoleViewer, h.get))
	mux.HandleFunc("PATCH /api/v1/organizations/current", h.mw.Org(auth.RoleAdmin, h.update))
	mux.HandleFunc("DELETE /api/v1/organizations/current", h.mw.Org(auth.RoleOwner, h.delete))
	mux.HandleFunc("GET /api/v1/organizations/current/members", h.mw.Org(auth.RoleViewer, h.members))
	mux.HandleFunc("POST /api/v1/organizations/current/members", h.mw.Org(auth.RoleAdmin, h.addMember))
	mux.HandleFunc("PATCH /api/v1/organizations/current/members/{userID}", h.mw.Org(auth.RoleAdmin, h.updateMember))
	mux.HandleFunc("DELETE /api/v1/organizations/current/members/{userID}", h.mw.Org(auth.RoleAdmin, h.removeMember))
	mux.HandleFunc("GET /api/v1/organizations/current/audit-logs", h.mw.Org(auth.RoleAdmin, h.auditLogs))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.svc.ListForUser(r.Context(), auth.MustPrincipal(r.Context()).UserID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, orgs)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	org, err := h.svc.Create(r.Context(), auth.MustPrincipal(r.Context()).UserID, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, org)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	org, err := h.svc.Get(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, org)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in UpdateInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	org, err := h.svc.Update(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, org)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), auth.MustPrincipal(r.Context())); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) members(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.Members(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, m)
}

func (h *Handler) addMember(w http.ResponseWriter, r *http.Request) {
	var in AddMemberInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.AddMember(r.Context(), auth.MustPrincipal(r.Context()), in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) updateMember(w http.ResponseWriter, r *http.Request) {
	uid, err := uuid.Parse(r.PathValue("userID"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("member"))
		return
	}
	var in struct {
		Role auth.Role `json:"role"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.UpdateMemberRole(r.Context(), auth.MustPrincipal(r.Context()), uid, in.Role); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	uid, err := uuid.Parse(r.PathValue("userID"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("member"))
		return
	}
	if err := h.svc.RemoveMember(r.Context(), auth.MustPrincipal(r.Context()), uid); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) auditLogs(w http.ResponseWriter, r *http.Request) {
	page := httpx.PageFrom(r)
	recs, total, err := h.audit.List(r.Context(), auth.MustPrincipal(r.Context()).OrgID, page.Limit, page.Offset)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, recs, page.Meta(total))
}
