package imports

import (
	"net/http"

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
	mux.HandleFunc("GET /api/v1/imports", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/imports", h.mw.Org(auth.RoleMember, h.upload))
	mux.HandleFunc("GET /api/v1/imports/{id}", h.mw.Org(auth.RoleViewer, h.get))
	mux.HandleFunc("PUT /api/v1/imports/{id}/mapping", h.mw.Org(auth.RoleMember, h.mapping))
	mux.HandleFunc("POST /api/v1/imports/{id}/commit", h.mw.Org(auth.RoleMember, h.commit))
}

func importID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.NotFound("import")
	}
	return id, nil
}

var meta = map[string]any{"fields": Fields, "consent_bases": ConsentBases}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxFileSize+1<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, r, apperr.Invalid("multipart field 'file' is required (max 20 MB)", nil))
		return
	}
	defer file.Close()
	imp, err := h.svc.Upload(r.Context(), auth.MustPrincipal(r.Context()), header.Filename, file)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, imp, meta)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := importID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	imp, err := h.svc.Get(r.Context(), auth.MustPrincipal(r.Context()).OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, imp, meta)
}

func (h *Handler) mapping(w http.ResponseWriter, r *http.Request) {
	id, err := importID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in MappingInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	imp, err := h.svc.UpdateMapping(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, imp, meta)
}

func (h *Handler) commit(w http.ResponseWriter, r *http.Request) {
	id, err := importID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	imp, err := h.svc.Commit(r.Context(), auth.MustPrincipal(r.Context()), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, imp, meta)
}
