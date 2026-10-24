// Package mailboxes manages sender identities. Each mailbox has its own daily
// limit; the transport itself is the configured EmailProvider.
package mailboxes

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/vault"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/validate"
)

type Mailbox struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	FromEmail  string    `json:"from_email"`
	FromName   string    `json:"from_name"`
	ReplyTo    *string   `json:"reply_to"`
	DailyLimit int       `json:"daily_limit"`
	Status     string    `json:"status"`
	SentLast24 int       `json:"sent_last_24h"`
	CreatedAt  time.Time `json:"created_at"`
	// Own transport (optional; otherwise the server's EMAIL_PROVIDER is used).
	SMTPHost        string     `json:"smtp_host"`
	SMTPPort        int        `json:"smtp_port"`
	SMTPUsername    string     `json:"smtp_username"`
	SMTPTLS         string     `json:"smtp_tls"`
	HasSMTPPassword bool       `json:"has_smtp_password"`
	IMAPHost        string     `json:"imap_host"`
	IMAPPort        int        `json:"imap_port"`
	IMAPUsername    string     `json:"imap_username"`
	IMAPTLS         bool       `json:"imap_tls"`
	HasIMAPPassword bool       `json:"has_imap_password"`
	InboxSync       bool       `json:"inbox_sync_enabled"`
	LastSyncedAt    *time.Time `json:"last_synced_at"`
	SyncError       *string    `json:"sync_error"`
}

type Input struct {
	Name       string  `json:"name"`
	FromEmail  string  `json:"from_email"`
	FromName   string  `json:"from_name"`
	ReplyTo    *string `json:"reply_to"`
	DailyLimit int     `json:"daily_limit"`
	Status     string  `json:"status"`

	SMTPHost     string `json:"smtp_host"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPUsername string `json:"smtp_username"`
	SMTPPassword string `json:"smtp_password"` // write-only; empty keeps the stored one
	SMTPTLS      string `json:"smtp_tls"`
	IMAPHost     string `json:"imap_host"`
	IMAPPort     int    `json:"imap_port"`
	IMAPUsername string `json:"imap_username"`
	IMAPPassword string `json:"imap_password"` // write-only
	IMAPTLS      *bool  `json:"imap_tls"`
	InboxSync    bool   `json:"inbox_sync_enabled"`
}

func (in *Input) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	in.FromName = strings.TrimSpace(in.FromName)
	in.FromEmail = validate.NormalizeEmail(in.FromEmail)
	if in.Status == "" {
		in.Status = "active"
	}
	v := validate.Errors{}
	v.Required("name", in.Name)
	v.Required("from_name", in.FromName)
	v.Check(validate.Email(in.FromEmail), "from_email", "must be a valid email address")
	if in.ReplyTo != nil && *in.ReplyTo != "" {
		r := validate.NormalizeEmail(*in.ReplyTo)
		in.ReplyTo = &r
		v.Check(validate.Email(r), "reply_to", "must be a valid email address")
	} else {
		in.ReplyTo = nil
	}
	v.Check(in.DailyLimit >= 0 && in.DailyLimit <= 10000, "daily_limit", "must be between 0 and 10000")
	in.SMTPHost, in.IMAPHost = strings.TrimSpace(in.SMTPHost), strings.TrimSpace(in.IMAPHost)
	if in.SMTPPort == 0 {
		in.SMTPPort = 587
	}
	if in.IMAPPort == 0 {
		in.IMAPPort = 993
	}
	if in.SMTPTLS == "" {
		in.SMTPTLS = "starttls"
	}
	if in.IMAPTLS == nil {
		t := true
		in.IMAPTLS = &t
	}
	v.Check(validate.OneOf(in.SMTPTLS, "none", "starttls", "tls"), "smtp_tls", "must be none, starttls or tls")
	v.Check(in.SMTPPort > 0 && in.SMTPPort < 65536 && in.IMAPPort > 0 && in.IMAPPort < 65536, "smtp_port", "invalid port")
	v.Check(!in.InboxSync || in.IMAPHost != "", "imap_host", "required for inbox sync")
	v.MaxLen("smtp_host", in.SMTPHost, 255)
	v.MaxLen("imap_host", in.IMAPHost, 255)
	v.Check(validate.OneOf(in.Status, "active", "disabled"), "status", "must be active or disabled")
	return v.Err()
}

type Repository struct{ db database.DBTX }

func NewRepository(db database.DBTX) *Repository { return &Repository{db: db} }

const cols = `m.id, m.name, m.from_email::text, m.from_name, m.reply_to::text, m.daily_limit, m.status, m.created_at,
	(SELECT count(*) FROM email_messages e WHERE e.mailbox_id = m.id AND e.created_at > now() - interval '24 hours' AND e.status <> 'cancelled'),
	m.smtp_host, m.smtp_port, m.smtp_username, m.smtp_tls, m.smtp_password_enc IS NOT NULL,
	m.imap_host, m.imap_port, m.imap_username, m.imap_tls, m.imap_password_enc IS NOT NULL,
	m.inbox_sync_enabled, m.last_synced_at, m.sync_error`

func scan(row pgx.Row) (*Mailbox, error) {
	var m Mailbox
	err := row.Scan(&m.ID, &m.Name, &m.FromEmail, &m.FromName, &m.ReplyTo, &m.DailyLimit, &m.Status, &m.CreatedAt, &m.SentLast24,
		&m.SMTPHost, &m.SMTPPort, &m.SMTPUsername, &m.SMTPTLS, &m.HasSMTPPassword,
		&m.IMAPHost, &m.IMAPPort, &m.IMAPUsername, &m.IMAPTLS, &m.HasIMAPPassword,
		&m.InboxSync, &m.LastSyncedAt, &m.SyncError)
	if database.IsNoRows(err) {
		return nil, apperr.NotFound("mailbox")
	}
	return &m, err
}

func (r *Repository) Get(ctx context.Context, orgID, id uuid.UUID) (*Mailbox, error) {
	return scan(r.db.QueryRow(ctx, `SELECT `+cols+` FROM mailboxes m WHERE m.organization_id=$1 AND m.id=$2`, orgID, id))
}

func (r *Repository) List(ctx context.Context, orgID uuid.UUID) ([]Mailbox, error) {
	rows, err := r.db.Query(ctx, `SELECT `+cols+` FROM mailboxes m WHERE m.organization_id=$1 ORDER BY m.created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mailbox{}
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

type Service struct {
	repo  *Repository
	db    database.DBTX
	audit *audit.Log
	vault *vault.Vault
}

func NewService(db database.DBTX, a *audit.Log, v *vault.Vault) *Service {
	return &Service{repo: NewRepository(db), db: db, audit: a, vault: v}
}

// seal encrypts a new password; empty input means "keep the stored one" (nil).
func (s *Service) seal(pw string) ([]byte, error) {
	if pw == "" {
		return nil, nil
	}
	return s.vault.Seal(pw)
}

func (s *Service) Get(ctx context.Context, p auth.Principal, id uuid.UUID) (*Mailbox, error) {
	return s.repo.Get(ctx, p.OrgID, id)
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]Mailbox, error) {
	return s.repo.List(ctx, p.OrgID)
}

func (s *Service) Create(ctx context.Context, p auth.Principal, in Input) (*Mailbox, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	smtpPW, err := s.seal(in.SMTPPassword)
	if err != nil {
		return nil, err
	}
	imapPW, err := s.seal(in.IMAPPassword)
	if err != nil {
		return nil, err
	}
	var id uuid.UUID
	err = s.db.QueryRow(ctx, `
		INSERT INTO mailboxes (organization_id, name, from_email, from_name, reply_to, daily_limit, status,
			smtp_host, smtp_port, smtp_username, smtp_password_enc, smtp_tls,
			imap_host, imap_port, imap_username, imap_password_enc, imap_tls, inbox_sync_enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) RETURNING id`,
		p.OrgID, in.Name, in.FromEmail, in.FromName, in.ReplyTo, in.DailyLimit, in.Status,
		in.SMTPHost, in.SMTPPort, in.SMTPUsername, smtpPW, in.SMTPTLS,
		in.IMAPHost, in.IMAPPort, in.IMAPUsername, imapPW, *in.IMAPTLS, in.InboxSync).Scan(&id)
	if database.IsUniqueViolation(err) {
		return nil, apperr.Conflict("mailbox_exists", "a mailbox with this from address already exists")
	}
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "mailbox.created", EntityType: "mailbox", EntityID: id.String()})
	return s.repo.Get(ctx, p.OrgID, id)
}

func (s *Service) Update(ctx context.Context, p auth.Principal, id uuid.UUID, in Input) (*Mailbox, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	smtpPW, err := s.seal(in.SMTPPassword)
	if err != nil {
		return nil, err
	}
	imapPW, err := s.seal(in.IMAPPassword)
	if err != nil {
		return nil, err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE mailboxes SET name=$3, from_email=$4, from_name=$5, reply_to=$6, daily_limit=$7, status=$8,
			smtp_host=$9, smtp_port=$10, smtp_username=$11, smtp_password_enc=COALESCE($12, smtp_password_enc), smtp_tls=$13,
			imap_host=$14, imap_port=$15, imap_username=$16, imap_password_enc=COALESCE($17, imap_password_enc), imap_tls=$18,
			inbox_sync_enabled=$19, sync_error=NULL, updated_at=now()
		WHERE organization_id=$1 AND id=$2`,
		p.OrgID, id, in.Name, in.FromEmail, in.FromName, in.ReplyTo, in.DailyLimit, in.Status,
		in.SMTPHost, in.SMTPPort, in.SMTPUsername, smtpPW, in.SMTPTLS,
		in.IMAPHost, in.IMAPPort, in.IMAPUsername, imapPW, *in.IMAPTLS, in.InboxSync)
	if database.IsUniqueViolation(err) {
		return nil, apperr.Conflict("mailbox_exists", "a mailbox with this from address already exists")
	}
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, apperr.NotFound("mailbox")
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "mailbox.updated", EntityType: "mailbox", EntityID: id.String()})
	return s.repo.Get(ctx, p.OrgID, id)
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	var active int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM campaigns WHERE organization_id=$1 AND mailbox_id=$2 AND status IN ('scheduled','running')`, p.OrgID, id).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return apperr.Conflict("mailbox_in_use", "mailbox is used by an active campaign; pause or reassign it first")
	}
	tag, err := s.db.Exec(ctx, `DELETE FROM mailboxes WHERE organization_id=$1 AND id=$2`, p.OrgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("mailbox")
	}
	s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "mailbox.deleted", EntityType: "mailbox", EntityID: id.String()})
	return nil
}

// ───────────────────────── HTTP ─────────────────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/mailboxes", h.mw.Org(auth.RoleViewer, h.list))
	mux.HandleFunc("POST /api/v1/mailboxes", h.mw.Org(auth.RoleAdmin, h.create))
	mux.HandleFunc("PUT /api/v1/mailboxes/{id}", h.mw.Org(auth.RoleAdmin, h.update))
	mux.HandleFunc("DELETE /api/v1/mailboxes/{id}", h.mw.Org(auth.RoleAdmin, h.delete))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context(), auth.MustPrincipal(r.Context()))
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
	m, err := h.svc.Create(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.Created(w, m)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("mailbox"))
		return
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	m, err := h.svc.Update(r.Context(), auth.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, m)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("mailbox"))
		return
	}
	if err := h.svc.Delete(r.Context(), auth.MustPrincipal(r.Context()), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}
