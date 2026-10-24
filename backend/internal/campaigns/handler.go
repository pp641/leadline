package campaigns

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"leadline/internal/auth"
	"leadline/internal/email"
	"leadline/pkg/apperr"
	"leadline/pkg/httpx"
)

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/campaigns", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/campaigns", h.mw.Org(auth.RoleMember, h.create))
	mux.HandleFunc("GET /api/v1/campaigns/{id}", h.mw.Org(auth.RoleViewer, h.get))
	mux.HandleFunc("PUT /api/v1/campaigns/{id}", h.mw.Org(auth.RoleMember, h.update))
	mux.HandleFunc("DELETE /api/v1/campaigns/{id}", h.mw.Org(auth.RoleMember, h.delete))
	mux.HandleFunc("GET /api/v1/campaigns/{id}/steps", h.mw.Org(auth.RoleViewer, h.steps))
	mux.HandleFunc("PUT /api/v1/campaigns/{id}/steps", h.mw.Org(auth.RoleMember, h.replaceSteps))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/preview", h.mw.Org(auth.RoleViewer, h.preview))
	mux.HandleFunc("GET /api/v1/campaigns/{id}/leads", h.mw.Org(auth.RoleViewer, h.leads))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/leads", h.mw.Org(auth.RoleMember, h.addLeads))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/leads/remove", h.mw.Org(auth.RoleMember, h.removeLeads))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/launch", h.mw.Org(auth.RoleMember, h.lifecycle(h.svc.Launch)))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/pause", h.mw.Org(auth.RoleMember, h.lifecycle(h.svc.Pause)))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/resume", h.mw.Org(auth.RoleMember, h.lifecycle(h.svc.Resume)))
	mux.HandleFunc("POST /api/v1/campaigns/{id}/cancel", h.mw.Org(auth.RoleMember, h.lifecycle(h.svc.Cancel)))
}

func campaignID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.NotFound("campaign")
	}
	return id, nil
}

var templateMeta = map[string]any{"variables": email.Variables, "sendable_email_statuses": SendableEmailStatuses}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page := httpx.PageFrom(r)
	out, total, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()), r.URL.Query().Get("status"), page.Limit, page.Offset)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, page.Meta(total))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := campaignID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.svc.Get(r.Context(), auth.MustPrincipal(r.Context()), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, c, templateMeta)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.svc.Create(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, c)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := campaignID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.svc.Update(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, c)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := campaignID(r)
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

func (h *Handler) steps(w http.ResponseWriter, r *http.Request) {
	id, err := campaignID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	c, err := h.svc.Get(r.Context(), auth.MustPrincipal(r.Context()), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, c.Steps, templateMeta)
}

func (h *Handler) replaceSteps(w http.ResponseWriter, r *http.Request) {
	id, err := campaignID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in struct {
		Steps []StepInput `json:"steps"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	steps, err := h.svc.ReplaceSteps(r.Context(), auth.MustPrincipal(r.Context()), id, in.Steps)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, steps)
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	id, err := campaignID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in PreviewInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Preview(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) leads(w http.ResponseWriter, r *http.Request) {
	id, err := campaignID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	page := httpx.PageFrom(r)
	q := r.URL.Query()
	out, total, err := h.svc.Leads(r.Context(), auth.MustPrincipal(r.Context()), id, q.Get("status"), q.Get("q"), page.Limit, page.Offset)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, page.Meta(total))
}

func (h *Handler) addLeads(w http.ResponseWriter, r *http.Request) {
	id, err := campaignID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in AudienceInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	res, err := h.svc.AddLeads(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, res)
}

func (h *Handler) removeLeads(w http.ResponseWriter, r *http.Request) {
	id, err := campaignID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in struct {
		LeadIDs []uuid.UUID `json:"lead_ids"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	n, err := h.svc.RemoveLeads(r.Context(), auth.MustPrincipal(r.Context()), id, in.LeadIDs)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, map[string]int{"removed": n})
}

type lifecycleFn func(ctx context.Context, p auth.Principal, id uuid.UUID) (*Campaign, error)

// lifecycle wraps launch/pause/resume/cancel, which share one shape.
func (h *Handler) lifecycle(fn lifecycleFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := campaignID(r)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		c, err := fn(r.Context(), auth.MustPrincipal(r.Context()), id)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		httpx.OK(w, c)
	}
}
