package deals

import (
	"net/http"
	"strings"

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
	mux.HandleFunc("GET /api/v1/pipelines", h.mw.Org(auth.RoleViewer, h.pipelines))
	mux.HandleFunc("POST /api/v1/pipelines", h.mw.Org(auth.RoleAdmin, h.createPipeline))
	mux.HandleFunc("PUT /api/v1/pipelines/{id}", h.mw.Org(auth.RoleAdmin, h.updatePipeline))
	mux.HandleFunc("DELETE /api/v1/pipelines/{id}", h.mw.Org(auth.RoleAdmin, h.deletePipeline))
	mux.HandleFunc("GET /api/v1/deals/board", h.mw.Org(auth.RoleViewer, h.board))
	mux.HandleFunc("GET /api/v1/deals", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/deals", h.mw.Org(auth.RoleMember, h.create))
	mux.HandleFunc("GET /api/v1/deals/{id}", h.mw.Org(auth.RoleViewer, h.get))
	mux.HandleFunc("PUT /api/v1/deals/{id}", h.mw.Org(auth.RoleMember, h.update))
	mux.HandleFunc("DELETE /api/v1/deals/{id}", h.mw.Org(auth.RoleMember, h.delete))
	mux.HandleFunc("POST /api/v1/deals/{id}/move", h.mw.Org(auth.RoleMember, h.move))
	mux.HandleFunc("POST /api/v1/deals/{id}/notes", h.mw.Org(auth.RoleMember, h.addNote))
}

func pathID(r *http.Request, what string) (uuid.UUID, error) {
	v, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.NotFound(what)
	}
	return v, nil
}

func queryID(r *http.Request, key string, p auth.Principal) *uuid.UUID {
	switch v := r.URL.Query().Get(key); v {
	case "":
		return nil
	case "me":
		return &p.UserID
	default:
		if u, err := uuid.Parse(v); err == nil {
			return &u
		}
	}
	return nil
}

func (h *Handler) pipelines(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	out, err := h.svc.Pipelines(r.Context(), p.OrgID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) createPipeline(w http.ResponseWriter, r *http.Request) {
	var in PipelineInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.CreatePipeline(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, out)
}

func (h *Handler) updatePipeline(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "pipeline")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in PipelineInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.UpdatePipeline(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) deletePipeline(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "pipeline")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.DeletePipeline(r.Context(), auth.MustPrincipal(r.Context()), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) board(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	out, err := h.svc.Board(r.Context(), p.OrgID, queryID(r, "pipeline_id", p), queryID(r, "owner", p))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	f := ListFilter{
		PipelineID: queryID(r, "pipeline_id", p),
		LeadID:     queryID(r, "lead_id", p),
		OwnerID:    queryID(r, "owner", p),
		Status:     r.URL.Query().Get("status"),
		Q:          strings.TrimSpace(r.URL.Query().Get("q")),
		Limit:      httpx.QueryInt(r, "limit", 500),
	}
	out, err := h.svc.List(r.Context(), p.OrgID, f)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Create(r.Context(), p.OrgID, UserActor(p), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, out)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	id, err := pathID(r, "deal")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	d, err := h.svc.Get(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	events, err := h.svc.Events(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"deal": d, "events": events})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	id, err := pathID(r, "deal")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Update(r.Context(), p.OrgID, id, UserActor(p), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "deal")
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

func (h *Handler) move(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	id, err := pathID(r, "deal")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in struct {
		StageID    uuid.UUID `json:"stage_id"`
		LostReason string    `json:"lost_reason"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Move(r.Context(), p.OrgID, id, UserActor(p), in.StageID, in.LostReason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) addNote(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	id, err := pathID(r, "deal")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.AddNote(r.Context(), p.OrgID, id, UserActor(p), in.Body); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}
