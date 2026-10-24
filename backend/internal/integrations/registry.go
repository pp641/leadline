package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/email"
	"leadline/internal/vault"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
)

// KindHubSpot is kept as a named constant for the CRM sync and tests.
const KindHubSpot = "hubspot"

type Integration struct {
	Kind      string         `json:"kind"`
	Config    map[string]any `json:"config"`
	Status    string         `json:"status"`
	LastError *string        `json:"last_error"`
	HasSecret bool           `json:"has_secret"`
	// SecretsSet lists the secret fields that are stored (values are never returned).
	SecretsSet []string  `json:"secrets_set"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Registry stores integration credentials and builds provider clients.
type Registry struct {
	pool  *pgxpool.Pool
	vault *vault.Vault
	audit *audit.Log
	// Overridable base URLs (tests).
	BaseURLs map[string]string
	// Testers check credentials for kinds implemented outside this package
	// (lead sources register theirs at startup).
	Testers map[string]func(ctx context.Context, creds Credentials) error
}

func NewRegistry(pool *pgxpool.Pool, v *vault.Vault, a *audit.Log) *Registry {
	return &Registry{pool: pool, vault: v, audit: a, BaseURLs: map[string]string{}, Testers: map[string]func(context.Context, Credentials) error{}}
}

// Credentials are an integration's field values: decrypted secrets plus
// non-secret settings, keyed by catalog field key.
type Credentials map[string]string

func (c Credentials) Get(k string) string { return strings.TrimSpace(c[k]) }

func (c Credentials) Bool(k string) bool { return c[k] == "true" }

// openSecrets decodes a sealed secret: a JSON object of secret fields, or
// (integrations saved before multi-field credentials) a bare token.
func (r *Registry) openSecrets(kind string, sealed []byte) (map[string]string, error) {
	out := map[string]string{}
	if len(sealed) == 0 {
		return out, nil
	}
	plain, err := r.vault.Open(sealed)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal([]byte(plain), &out) == nil && len(out) > 0 {
		return out, nil
	}
	if p, ok := Lookup(kind); ok {
		for _, f := range p.Fields {
			if f.Type == "secret" {
				return map[string]string{f.Key: plain}, nil
			}
		}
	}
	return map[string]string{"token": plain}, nil
}

func (r *Registry) List(ctx context.Context, orgID uuid.UUID) ([]Integration, error) {
	rows, err := r.pool.Query(ctx, `SELECT kind, config, status, last_error, secret, updated_at FROM integrations WHERE organization_id=$1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Integration{}
	for rows.Next() {
		var i Integration
		var raw, sealed []byte
		if err := rows.Scan(&i.Kind, &raw, &i.Status, &i.LastError, &sealed, &i.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &i.Config)
		i.HasSecret = len(sealed) > 0
		i.SecretsSet = []string{}
		if secrets, err := r.openSecrets(i.Kind, sealed); err == nil {
			for k := range secrets {
				i.SecretsSet = append(i.SecretsSet, k)
			}
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Credentials returns the field values of an active integration.
func (r *Registry) Credentials(ctx context.Context, orgID uuid.UUID, kind string) (Credentials, error) {
	var sealed, raw []byte
	var status string
	err := r.pool.QueryRow(ctx, `SELECT secret, config, status FROM integrations WHERE organization_id=$1 AND kind=$2`, orgID, kind).Scan(&sealed, &raw, &status)
	name := kind
	if p, ok := Lookup(kind); ok {
		name = p.Name
	}
	if database.IsNoRows(err) || status == "disabled" {
		return nil, apperr.Conflict("integration_not_configured", name+" is not connected — add it under Integrations")
	}
	if err != nil {
		return nil, err
	}
	secrets, err := r.openSecrets(kind, sealed)
	if err != nil {
		return nil, err
	}
	creds := Credentials{}
	cfg := map[string]any{}
	_ = json.Unmarshal(raw, &cfg)
	for k, v := range cfg {
		switch x := v.(type) {
		case string:
			creds[k] = x
		case bool:
			creds[k] = strconv.FormatBool(x)
		case float64:
			creds[k] = strconv.FormatFloat(x, 'f', -1, 64)
		}
	}
	for k, v := range secrets {
		creds[k] = v
	}
	return creds, nil
}

// ConnectedWith lists the organization's active integrations with a capability.
func (r *Registry) ConnectedWith(ctx context.Context, orgID uuid.UUID, cap string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT kind FROM integrations WHERE organization_id=$1 AND status <> 'disabled' ORDER BY updated_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if HasCapability(k, cap) {
			out = append(out, k)
		}
	}
	return out, rows.Err()
}

// Configured reports whether an active integration of kind exists.
func (r *Registry) Configured(ctx context.Context, orgID uuid.UUID, kind string) bool {
	var ok bool
	_ = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integrations WHERE organization_id=$1 AND kind=$2 AND status <> 'disabled' AND secret IS NOT NULL)`, orgID, kind).Scan(&ok)
	return ok
}

// Option reads a boolean config option.
func (r *Registry) Option(ctx context.Context, orgID uuid.UUID, kind, name string) bool {
	var raw []byte
	if err := r.pool.QueryRow(ctx, `SELECT config FROM integrations WHERE organization_id=$1 AND kind=$2 AND status <> 'disabled'`, orgID, kind).Scan(&raw); err != nil {
		return false
	}
	cfg := map[string]any{}
	_ = json.Unmarshal(raw, &cfg)
	v, _ := cfg[name].(bool)
	return v
}

func (r *Registry) HubSpot(ctx context.Context, orgID uuid.UUID) (*HubSpot, error) {
	c, err := r.Credentials(ctx, orgID, KindHubSpot)
	if err != nil {
		return nil, err
	}
	return &HubSpot{Token: c.Get("token"), BaseURL: r.BaseURLs[KindHubSpot]}, nil
}

type SaveInput struct {
	// Values are the catalog field values. Secret fields left empty keep
	// their stored value.
	Values map[string]any `json:"values"`
	// Secret is the legacy single-secret form (e.g. a HubSpot token).
	Secret string `json:"secret"`
	// Config is the legacy settings form.
	Config map[string]any `json:"config"`
	Status string         `json:"status"` // active | disabled
}

func valueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func (r *Registry) Save(ctx context.Context, p auth.Principal, kind string, in SaveInput) error {
	prov, ok := Lookup(kind)
	if !ok {
		return apperr.NotFound("integration")
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Status != "active" && in.Status != "disabled" {
		return apperr.Invalid("validation failed", map[string]string{"status": "must be active or disabled"})
	}
	values := map[string]any{}
	for k, v := range in.Config {
		values[k] = v
	}
	for k, v := range in.Values {
		values[k] = v
	}
	if s := strings.TrimSpace(in.Secret); s != "" {
		for _, f := range prov.Fields {
			if f.Type == "secret" {
				values[f.Key] = s
				break
			}
		}
	}

	// Existing secrets are kept unless replaced.
	var existing []byte
	_ = r.pool.QueryRow(ctx, `SELECT secret FROM integrations WHERE organization_id=$1 AND kind=$2`, p.OrgID, kind).Scan(&existing)
	secrets, err := r.openSecrets(kind, existing)
	if err != nil {
		secrets = map[string]string{}
	}
	cfg := map[string]any{}
	errs := map[string]string{}
	for _, f := range prov.Fields {
		v := valueString(values[f.Key])
		switch f.Type {
		case "secret":
			if v != "" {
				secrets[f.Key] = v
			}
			if f.Required && secrets[f.Key] == "" {
				errs[f.Key] = "is required"
			}
		case "toggle":
			if _, set := values[f.Key]; !set && f.Default != "" {
				v = f.Default
			}
			cfg[f.Key] = v == "true"
		default:
			if v == "" {
				v = f.Default
			}
			if f.Required && v == "" {
				errs[f.Key] = "is required"
			}
			if f.Type == "url" && v != "" && !strings.HasPrefix(v, "https://") {
				errs[f.Key] = "must start with https://"
			}
			if len(f.Options) > 0 && v != "" && !slices.ContainsFunc(f.Options, func(o Option) bool { return o.Value == v }) {
				errs[f.Key] = "is not a valid choice"
			}
			cfg[f.Key] = v
		}
	}
	if len(errs) > 0 {
		return apperr.Invalid("validation failed", errs)
	}
	rawCfg, _ := json.Marshal(cfg)
	var sealed []byte
	if len(secrets) > 0 {
		b, _ := json.Marshal(secrets)
		if sealed, err = r.vault.Seal(string(b)); err != nil {
			return err
		}
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO integrations (organization_id, kind, config, secret, status) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (organization_id, kind) DO UPDATE SET config=EXCLUDED.config, status=EXCLUDED.status,
			secret=COALESCE(EXCLUDED.secret, integrations.secret), last_error=NULL, updated_at=now()`,
		p.OrgID, kind, rawCfg, sealed, in.Status)
	if err == nil {
		r.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "integration.saved", EntityType: "integration", EntityID: kind})
	}
	return err
}

func (r *Registry) Delete(ctx context.Context, p auth.Principal, kind string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM integrations WHERE organization_id=$1 AND kind=$2`, p.OrgID, kind)
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("integration")
	}
	if err == nil {
		r.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "integration.deleted", EntityType: "integration", EntityID: kind})
	}
	return err
}

// Test makes a cheap authenticated call to confirm the credentials work.
func (r *Registry) Test(ctx context.Context, orgID uuid.UUID, kind string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, ok := Lookup(kind); !ok {
		return apperr.NotFound("integration")
	}
	creds, err := r.Credentials(ctx, orgID, kind)
	if err != nil {
		return err
	}
	switch {
	case kind == KindHubSpot:
		err = (&HubSpot{Token: creds.Get("token"), BaseURL: r.BaseURLs[KindHubSpot]}).Ping(ctx)
	case HasCapability(kind, CapCRM):
		var c CRM
		if c, err = r.crmFor(kind, creds); err == nil {
			err = c.Ping(ctx)
		}
	case HasCapability(kind, CapEmail):
		var cfg email.SMTPConfig
		if cfg, err = SMTPFor(kind, creds); err == nil {
			err = email.NewSMTP(cfg).Check(ctx)
		}
	case r.Testers[kind] != nil:
		err = r.Testers[kind](ctx, creds)
	default:
		return apperr.Invalid("this integration has nothing to test", nil)
	}
	status, msg := "active", (*string)(nil)
	if err != nil {
		s := err.Error()
		status, msg = "error", &s
	}
	_, _ = r.pool.Exec(ctx, `UPDATE integrations SET status=CASE WHEN status='disabled' THEN status ELSE $3 END, last_error=$4, updated_at=now()
		WHERE organization_id=$1 AND kind=$2`, orgID, kind, status, msg)
	return err
}

// ───────────────────────── HTTP ─────────────────────────

type Handler struct {
	reg *Registry
	mw  *auth.Middleware
	llm LLMProvider
}

func NewHandler(reg *Registry, mw *auth.Middleware, llm LLMProvider) *Handler {
	return &Handler{reg: reg, mw: mw, llm: llm}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/integrations", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("PUT /api/v1/integrations/{kind}", h.mw.Org(auth.RoleAdmin, h.save))
	mux.HandleFunc("DELETE /api/v1/integrations/{kind}", h.mw.Org(auth.RoleAdmin, h.delete))
	mux.HandleFunc("POST /api/v1/integrations/{kind}/test", h.mw.Org(auth.RoleAdmin, h.test))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.reg.List(r.Context(), auth.MustPrincipal(r.Context()).OrgID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out, map[string]any{
		"catalog": Catalog, "categories": Categories, "ai_enabled": Enabled(h.llm), "encryption_enabled": h.reg.vault.Enabled(),
	})
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request) {
	var in SaveInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.reg.Save(r.Context(), auth.MustPrincipal(r.Context()), r.PathValue("kind"), in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.reg.Delete(r.Context(), auth.MustPrincipal(r.Context()), r.PathValue("kind")); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	if err := h.reg.Test(r.Context(), auth.MustPrincipal(r.Context()).OrgID, r.PathValue("kind")); err != nil {
		httpx.Error(w, r, apperr.Invalid("connection test failed: "+err.Error(), nil))
		return
	}
	httpx.OK(w, map[string]string{"status": "ok"})
}
