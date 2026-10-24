package leads

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/contacts"
	"leadline/internal/icp"
	"leadline/internal/scoring"
	"leadline/pkg/apperr"
	"leadline/pkg/httpx"
)

type Handler struct {
	svc      *Service
	contacts *contacts.Service
	scoring  *scoring.Service
	audit    *audit.Log
	mw       *auth.Middleware
}

func NewHandler(svc *Service, c *contacts.Service, s *scoring.Service, a *audit.Log, mw *auth.Middleware) *Handler {
	return &Handler{svc: svc, contacts: c, scoring: s, audit: a, mw: mw}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/leads", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/leads", h.mw.Org(auth.RoleMember, h.create))
	mux.HandleFunc("GET /api/v1/leads/export", h.mw.Org(auth.RoleMember, h.export))
	mux.HandleFunc("GET /api/v1/leads/facets", h.mw.Org(auth.RoleViewer, h.facets))
	mux.HandleFunc("POST /api/v1/leads/bulk", h.mw.Org(auth.RoleMember, h.bulk))
	mux.HandleFunc("GET /api/v1/leads/{id}", h.mw.Org(auth.RoleViewer, h.get))
	mux.HandleFunc("PUT /api/v1/leads/{id}", h.mw.Org(auth.RoleMember, h.update))
	mux.HandleFunc("DELETE /api/v1/leads/{id}", h.mw.Org(auth.RoleMember, h.delete))
	mux.HandleFunc("GET /api/v1/leads/{id}/events", h.mw.Org(auth.RoleViewer, h.events))
}

func leadID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.NotFound("lead")
	}
	return id, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page := httpx.PageFrom(r)
	out, total, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()), FilterFromQuery(r), page.Limit, page.Offset)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, page.Meta(total))
}

// LeadDetail is the lead page payload.
type LeadDetail struct {
	*Lead
	Contacts []contacts.Contact `json:"contacts"`
	Score    *scoring.Result    `json:"score"`
	Fit      *icp.FitResult     `json:"icp_fit_detail"`
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := leadID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	p := auth.MustPrincipal(r.Context())
	l, err := h.svc.Get(r.Context(), p, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	cs, err := h.contacts.ListForLead(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	sc, err := h.scoring.Explain(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	fit, err := h.scoring.ExplainFit(r.Context(), p.OrgID, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, LeadDetail{Lead: l, Contacts: cs, Score: sc, Fit: fit})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	l, err := h.svc.Create(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, l)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := leadID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	l, err := h.svc.Update(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, l)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := leadID(r)
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

func (h *Handler) bulk(w http.ResponseWriter, r *http.Request) {
	var in BulkInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	n, err := h.svc.Bulk(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, map[string]int{"affected": n})
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	id, err := leadID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Events(r.Context(), auth.MustPrincipal(r.Context()), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) facets(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Facets(r.Context(), auth.MustPrincipal(r.Context()), r.URL.Query().Get("field"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

var exportHeader = []string{
	"id", "company_name", "website", "industry", "country", "state", "city", "address", "phone", "business_email",
	"contact_first_name", "contact_last_name", "contact_email", "email_status", "employee_count", "founded_year",
	"lead_score", "status", "tags", "source", "created_at",
}

// export streams matching leads as CSV.
func (h *Handler) export(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	f := FilterFromQuery(r)
	if ids := httpx.QueryList(r, "ids"); len(ids) > 0 {
		for _, s := range ids {
			if id, err := uuid.Parse(s); err == nil {
				f.IDs = append(f.IDs, id)
			}
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="leads-%s.csv"`, time.Now().Format("20060102-1504")))
	cw := csv.NewWriter(w)
	_ = cw.Write(exportHeader)
	n := 0
	err := h.svc.Each(r.Context(), p, f, func(l *Lead) error {
		var first, last, email, status string
		if c := l.PrimaryContact; c != nil {
			first, last, status = c.FirstName, c.LastName, c.EmailStatus
			if c.Email != nil {
				email = *c.Email
			}
		}
		n++
		return cw.Write(sanitizeRow([]string{
			l.ID.String(), l.CompanyName, l.Website, l.Industry, l.Country, l.State, l.City, l.Address, l.Phone,
			deref(l.BusinessEmail), first, last, email, status, intStr(l.EmployeeCount), intStr(l.FoundedYear),
			strconv.Itoa(l.LeadScore), l.Status, strings.Join(l.Tags, ";"), l.Source, l.CreatedAt.Format(time.RFC3339),
		}))
	})
	cw.Flush()
	if err != nil {
		// Headers are already sent; the truncated file is the best we can do.
		return
	}
	h.audit.Record(r.Context(), audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "lead.exported", EntityType: "lead", Metadata: map[string]any{"count": n}})
}

// sanitizeRow neutralizes spreadsheet formula injection (=, +, -, @ prefixes).
func sanitizeRow(row []string) []string {
	for i, v := range row {
		if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
			row[i] = "'" + v
		}
	}
	return row
}

func intStr(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}
