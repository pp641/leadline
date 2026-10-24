// Package onboarding covers everything between "new account" and "ready to
// work": email verification codes, the resumable setup wizard and its
// getting-started checklist, and email invitations to join an organization.
package onboarding

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/audit"
	"leadline/internal/auth"
	"leadline/internal/email"
	"leadline/internal/users"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/secure"
	"leadline/pkg/validate"
)

const (
	codeTTL        = 15 * time.Minute
	codeAttempts   = 5
	resendCooldown = 60 * time.Second
	inviteTTL      = 7 * 24 * time.Hour
	maxInvites     = 20
)

type Config struct {
	AppURL, FromEmail, FromName string
}

type Service struct {
	pool   *pgxpool.Pool
	users  *users.Repository
	mailer email.EmailProvider
	cfg    Config
	audit  *audit.Log
	log    *slog.Logger
}

func NewService(pool *pgxpool.Pool, u *users.Repository, mailer email.EmailProvider, cfg Config, a *audit.Log, log *slog.Logger) *Service {
	return &Service{pool: pool, users: u, mailer: mailer, cfg: cfg, audit: a, log: log}
}

// ───────────── mail ─────────────

func (s *Service) send(ctx context.Context, to, subject, text, htmlBody string) error {
	domain := "leadline.local"
	if at := strings.LastIndex(s.cfg.FromEmail, "@"); at >= 0 {
		domain = s.cfg.FromEmail[at+1:]
	}
	_, err := s.mailer.Send(ctx, email.EmailMessage{
		MessageID: uuid.NewString() + "@" + domain, FromEmail: s.cfg.FromEmail, FromName: s.cfg.FromName,
		ToEmail: to, Subject: subject, TextBody: text, HTMLBody: htmlBody,
	})
	return err
}

func layout(title, body string) string {
	return `<!doctype html><html><body style="margin:0;background:#f5f5f4;font-family:system-ui,-apple-system,Segoe UI,sans-serif;color:#0b0b0b">
<table width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:32px 16px">
<table width="480" cellpadding="0" cellspacing="0" style="max-width:480px;background:#ffffff;border:1px solid #e5e5e5;border-radius:12px">
<tr><td style="padding:28px 32px 8px;font-weight:600;font-size:16px">Leadline</td></tr>
<tr><td style="padding:8px 32px 32px;font-size:15px;line-height:1.55">
<h1 style="font-size:20px;margin:0 0 12px">` + html.EscapeString(title) + `</h1>` + body + `
</td></tr></table>
<p style="font-size:12px;color:#737373;margin-top:16px">You received this because of activity on your Leadline account.</p>
</td></tr></table></body></html>`
}

// Notify sends a short system notification (used by workflow "notify"
// actions). path is an app path such as /crm?deal=… linked from the email.
func (s *Service) Notify(ctx context.Context, to, subject, message, path string) error {
	link := strings.TrimRight(s.cfg.AppURL, "/") + path
	body := `<p>` + html.EscapeString(message) + `</p><p><a href="` + html.EscapeString(link) +
		`" style="display:inline-block;background:#0b0b0b;color:#fff;padding:10px 16px;border-radius:8px;text-decoration:none">Open in Leadline</a></p>`
	return s.send(ctx, to, subject, message+"\n\n"+link, layout(subject, body))
}

// ───────────── email verification ─────────────

func codeHash(userID uuid.UUID, code string) string {
	h := sha256.Sum256([]byte(userID.String() + ":" + code))
	return hex.EncodeToString(h[:])
}

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// SendVerification emails a fresh 6-digit code to an unverified user.
func (s *Service) SendVerification(ctx context.Context, userID uuid.UUID) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.EmailVerified {
		return apperr.Conflict("already_verified", "your email address is already verified")
	}
	var sentAt time.Time
	err = s.pool.QueryRow(ctx, `SELECT sent_at FROM email_verifications WHERE user_id=$1`, userID).Scan(&sentAt)
	if err == nil && time.Since(sentAt) < resendCooldown {
		wait := int((resendCooldown - time.Since(sentAt)).Seconds()) + 1
		return apperr.Conflict("resend_too_soon", fmt.Sprintf("please wait %d seconds before requesting another code", wait))
	}
	if err != nil && !database.IsNoRows(err) {
		return err
	}
	code, err := newCode()
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO email_verifications (user_id, code_hash, attempts, expires_at, sent_at) VALUES ($1,$2,0,$3,now())
		ON CONFLICT (user_id) DO UPDATE SET code_hash=EXCLUDED.code_hash, attempts=0, expires_at=EXCLUDED.expires_at, sent_at=now()`,
		userID, codeHash(userID, code), time.Now().Add(codeTTL)); err != nil {
		return err
	}
	text := fmt.Sprintf("Hi %s,\n\nYour Leadline verification code is %s. It expires in 15 minutes.\n\nIf you didn't create a Leadline account, ignore this email.\n", u.FullName, code)
	body := `<p>Hi ` + html.EscapeString(u.FullName) + `,</p><p>Enter this code to verify your email address:</p>
<p style="font-size:32px;font-weight:700;letter-spacing:8px;margin:20px 0;font-family:ui-monospace,Menlo,monospace">` + code + `</p>
<p style="color:#525252">It expires in 15 minutes. If you didn't create a Leadline account, you can ignore this email.</p>`
	if err := s.send(ctx, u.Email, "Your Leadline verification code: "+code, text, layout("Verify your email", body)); err != nil {
		return apperr.Unavailable("we couldn't send the verification email; try again shortly", err)
	}
	s.audit.Record(ctx, audit.Entry{UserID: userID, Action: "user.verification_sent", EntityType: "user", EntityID: userID.String()})
	return nil
}

// SendVerificationAsync is the auth OnRegistered hook: a failed send must not
// fail the sign-up (the user can resend from the app).
func (s *Service) SendVerificationAsync(ctx context.Context, userID uuid.UUID) {
	go func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.SendVerification(c, userID); err != nil {
			s.log.Warn("verification email not sent", "user_id", userID, "error", err)
		}
	}()
}

func (s *Service) VerifyEmail(ctx context.Context, userID uuid.UUID, code string) error {
	code = strings.TrimSpace(code)
	var stored string
	var attempts int
	var expires time.Time
	err := s.pool.QueryRow(ctx, `SELECT code_hash, attempts, expires_at FROM email_verifications WHERE user_id=$1`, userID).Scan(&stored, &attempts, &expires)
	if database.IsNoRows(err) {
		return apperr.Invalid("request a new code first", map[string]string{"code": "no active code"})
	}
	if err != nil {
		return err
	}
	if time.Now().After(expires) {
		return apperr.Invalid("this code has expired — request a new one", map[string]string{"code": "expired"})
	}
	if attempts >= codeAttempts {
		return apperr.Invalid("too many attempts — request a new code", map[string]string{"code": "too many attempts"})
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(codeHash(userID, code))) != 1 {
		_, _ = s.pool.Exec(ctx, `UPDATE email_verifications SET attempts = attempts + 1 WHERE user_id=$1`, userID)
		left := codeAttempts - attempts - 1
		return apperr.Invalid(fmt.Sprintf("that code isn't right (%d attempts left)", left), map[string]string{"code": "incorrect"})
	}
	if err := s.users.MarkEmailVerified(ctx, userID); err != nil {
		return err
	}
	_, _ = s.pool.Exec(ctx, `DELETE FROM email_verifications WHERE user_id=$1`, userID)
	s.audit.Record(ctx, audit.Entry{UserID: userID, Action: "user.email_verified", EntityType: "user", EntityID: userID.String()})
	return nil
}

// ───────────── setup wizard + checklist ─────────────

// ChecklistItem is one getting-started task, computed from real data.
type ChecklistItem struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Hint  string `json:"hint"`
	Href  string `json:"href"`
	Done  bool   `json:"done"`
}

type Status struct {
	State         map[string]any  `json:"state"`
	Checklist     []ChecklistItem `json:"checklist"`
	Completed     int             `json:"completed"`
	EmailVerified bool            `json:"email_verified"`
	Organization  struct {
		Name            string `json:"name"`
		LegalName       string `json:"legal_name"`
		PhysicalAddress string `json:"physical_address"`
	} `json:"organization"`
}

func (s *Service) Status(ctx context.Context, p auth.Principal) (*Status, error) {
	out := &Status{}
	var raw []byte
	var verified, icp, mailbox, leads, team, campaign, sources bool
	err := s.pool.QueryRow(ctx, `
		SELECT o.onboarding, o.name, o.legal_name, o.physical_address,
		  (SELECT email_verified_at IS NOT NULL FROM users WHERE id=$2),
		  EXISTS (SELECT 1 FROM icps WHERE organization_id=$1),
		  EXISTS (SELECT 1 FROM mailboxes WHERE organization_id=$1),
		  EXISTS (SELECT 1 FROM leads WHERE organization_id=$1),
		  (SELECT count(*) FROM organization_members WHERE organization_id=$1) > 1
		    OR EXISTS (SELECT 1 FROM invitations WHERE organization_id=$1 AND revoked_at IS NULL),
		  EXISTS (SELECT 1 FROM campaigns WHERE organization_id=$1 AND status <> 'draft'),
		  EXISTS (SELECT 1 FROM integrations WHERE organization_id=$1 AND status <> 'disabled')
		FROM organizations o WHERE o.id=$1`, p.OrgID, p.UserID).Scan(&raw, &out.Organization.Name, &out.Organization.LegalName,
		&out.Organization.PhysicalAddress, &verified, &icp, &mailbox, &leads, &team, &campaign, &sources)
	if err != nil {
		return nil, err
	}
	out.State = map[string]any{}
	_ = json.Unmarshal(raw, &out.State)
	out.EmailVerified = verified
	out.Checklist = []ChecklistItem{
		{"verify_email", "Verify your email", "Confirm the code we emailed you", "/onboarding", verified},
		{"company", "Complete your company profile", "Name and the postal address shown in every email footer", "/settings?tab=organization", strings.TrimSpace(out.Organization.PhysicalAddress) != ""},
		{"icp", "Define your ideal customer", "Countries, cities, industries and signals", "/icp", icp},
		{"sources", "Connect a data source", "Apollo, Google Maps, Hunter and more", "/integrations", sources},
		{"leads", "Add your first leads", "Import a CSV or search a connected source", "/leads", leads},
		{"mailbox", "Connect a sending mailbox", "The address campaigns are sent from", "/settings?tab=mailboxes", mailbox},
		{"team", "Invite your team", "Teammates join with the role you choose", "/settings?tab=members", team},
		{"campaign", "Launch your first campaign", "A compliant, paced email sequence", "/campaigns", campaign},
	}
	for _, c := range out.Checklist {
		if c.Done {
			out.Completed++
		}
	}
	return out, nil
}

// SaveState stores the wizard's progress and answers (merged into existing state).
func (s *Service) SaveState(ctx context.Context, p auth.Principal, patch map[string]any) (*Status, error) {
	b, err := json.Marshal(patch)
	if err != nil || len(b) > 32<<10 {
		return nil, apperr.Invalid("validation failed", map[string]string{"state": "invalid or too large"})
	}
	if _, err := s.pool.Exec(ctx, `UPDATE organizations SET onboarding = onboarding || $2::jsonb, updated_at=now() WHERE id=$1`, p.OrgID, b); err != nil {
		return nil, err
	}
	if done, _ := patch["completed"].(bool); done {
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "organization.onboarding_completed", EntityType: "organization", EntityID: p.OrgID.String()})
	}
	return s.Status(ctx, p)
}

// ───────────── invitations ─────────────

type Invitation struct {
	ID         uuid.UUID  `json:"id"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	InvitedBy  *string    `json:"invited_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at"`
	Status     string     `json:"status"` // pending | accepted | expired
}

func tokenHash(token string) string { return hex.EncodeToString(secure.HashToken(token)) }

func (s *Service) Invitations(ctx context.Context, p auth.Principal) ([]Invitation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.email::text, i.role, u.full_name, i.created_at, i.expires_at, i.accepted_at
		FROM invitations i LEFT JOIN users u ON u.id = i.invited_by
		WHERE i.organization_id=$1 AND i.revoked_at IS NULL ORDER BY i.created_at DESC LIMIT 100`, p.OrgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invitation{}
	for rows.Next() {
		var i Invitation
		if err := rows.Scan(&i.ID, &i.Email, &i.Role, &i.InvitedBy, &i.CreatedAt, &i.ExpiresAt, &i.AcceptedAt); err != nil {
			return nil, err
		}
		switch {
		case i.AcceptedAt != nil:
			i.Status = "accepted"
		case time.Now().After(i.ExpiresAt):
			i.Status = "expired"
		default:
			i.Status = "pending"
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

type InviteResult struct {
	Invited        []string `json:"invited"`
	AlreadyMembers []string `json:"already_members"`
	Invalid        []string `json:"invalid"`
}

func (s *Service) Invite(ctx context.Context, p auth.Principal, emails []string, role string) (*InviteResult, error) {
	if role == "" {
		role = "member"
	}
	if role != "admin" && role != "member" && role != "viewer" {
		return nil, apperr.Invalid("validation failed", map[string]string{"role": "must be admin, member or viewer"})
	}
	if len(emails) == 0 || len(emails) > maxInvites {
		return nil, apperr.Invalid("validation failed", map[string]string{"emails": fmt.Sprintf("invite between 1 and %d people at a time", maxInvites)})
	}
	inviter, err := s.users.GetByID(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if !inviter.EmailVerified {
		return nil, apperr.Forbidden("verify your email address before inviting teammates")
	}
	var orgName string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM organizations WHERE id=$1`, p.OrgID).Scan(&orgName); err != nil {
		return nil, err
	}
	res := &InviteResult{Invited: []string{}, AlreadyMembers: []string{}, Invalid: []string{}}
	for _, raw := range emails {
		addr := validate.NormalizeEmail(raw)
		if !validate.Email(addr) {
			res.Invalid = append(res.Invalid, raw)
			continue
		}
		var member bool
		_ = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organization_members m JOIN users u ON u.id=m.user_id
			WHERE m.organization_id=$1 AND u.email=$2)`, p.OrgID, addr).Scan(&member)
		if member {
			res.AlreadyMembers = append(res.AlreadyMembers, addr)
			continue
		}
		token, err := secure.RandomToken(32)
		if err != nil {
			return nil, err
		}
		// Re-inviting replaces the pending invitation (new link, new expiry).
		err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE invitations SET revoked_at=now() WHERE organization_id=$1 AND email=$2
				AND accepted_at IS NULL AND revoked_at IS NULL`, p.OrgID, addr); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO invitations (organization_id, email, role, token_hash, invited_by, expires_at)
				VALUES ($1,$2,$3,$4,$5,$6)`, p.OrgID, addr, role, tokenHash(token), p.UserID, time.Now().Add(inviteTTL))
			return err
		})
		if err != nil {
			return nil, err
		}
		link := s.cfg.AppURL + "/invite?token=" + token
		text := fmt.Sprintf("%s invited you to join %s on Leadline as %s.\n\nAccept the invitation: %s\n\nThe link expires in 7 days.\n", inviter.FullName, orgName, role, link)
		body := `<p><b>` + html.EscapeString(inviter.FullName) + `</b> invited you to join <b>` + html.EscapeString(orgName) + `</b> on Leadline as <b>` + role + `</b>.</p>
<p style="margin:24px 0"><a href="` + html.EscapeString(link) + `" style="background:#0b0b0b;color:#ffffff;text-decoration:none;padding:12px 20px;border-radius:8px;font-weight:600;display:inline-block">Accept invitation</a></p>
<p style="color:#525252;font-size:13px">The link expires in 7 days. If you weren't expecting this, you can ignore it.</p>`
		if err := s.send(ctx, addr, inviter.FullName+" invited you to "+orgName+" on Leadline", text, layout("You're invited to "+orgName, body)); err != nil {
			s.log.Warn("invitation email not sent", "email", addr, "error", err)
		}
		res.Invited = append(res.Invited, addr)
	}
	if len(res.Invited) > 0 {
		s.audit.Record(ctx, audit.Entry{OrgID: p.OrgID, UserID: p.UserID, Action: "invitation.sent", EntityType: "organization",
			EntityID: p.OrgID.String(), Metadata: map[string]any{"emails": res.Invited, "role": role}})
	}
	return res, nil
}

func (s *Service) Revoke(ctx context.Context, p auth.Principal, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE invitations SET revoked_at=now() WHERE organization_id=$1 AND id=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, p.OrgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return apperr.NotFound("invitation")
	}
	return err
}

// InviteInfo is what the invite page shows before the visitor signs in.
type InviteInfo struct {
	Organization  string    `json:"organization"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	InvitedBy     string    `json:"invited_by"`
	ExpiresAt     time.Time `json:"expires_at"`
	AccountExists bool      `json:"account_exists"`
}

func (s *Service) lookup(ctx context.Context, db database.DBTX, token string) (uuid.UUID, uuid.UUID, *InviteInfo, error) {
	var id, org uuid.UUID
	info := &InviteInfo{}
	var accepted, revoked *time.Time
	err := db.QueryRow(ctx, `
		SELECT i.id, i.organization_id, o.name, i.email::text, i.role, COALESCE(u.full_name, ''), i.expires_at, i.accepted_at, i.revoked_at,
		       EXISTS (SELECT 1 FROM users x WHERE x.email = i.email)
		FROM invitations i JOIN organizations o ON o.id = i.organization_id LEFT JOIN users u ON u.id = i.invited_by
		WHERE i.token_hash=$1`, tokenHash(token)).Scan(&id, &org, &info.Organization, &info.Email, &info.Role, &info.InvitedBy,
		&info.ExpiresAt, &accepted, &revoked, &info.AccountExists)
	if database.IsNoRows(err) {
		return id, org, nil, apperr.NotFound("invitation")
	}
	if err != nil {
		return id, org, nil, err
	}
	switch {
	case revoked != nil:
		return id, org, nil, apperr.Conflict("invitation_revoked", "this invitation was withdrawn — ask for a new one")
	case accepted != nil:
		return id, org, nil, apperr.Conflict("invitation_used", "this invitation has already been accepted — sign in instead")
	case time.Now().After(info.ExpiresAt):
		return id, org, nil, apperr.Conflict("invitation_expired", "this invitation has expired — ask for a new one")
	}
	return id, org, info, nil
}

func (s *Service) Lookup(ctx context.Context, token string) (*InviteInfo, error) {
	_, _, info, err := s.lookup(ctx, s.pool, token)
	return info, err
}

// Accept adds the signed-in user to the inviting organization. The invitation
// was sent to the user's address, so accepting it also verifies that address.
func (s *Service) Accept(ctx context.Context, userID uuid.UUID, token string) (uuid.UUID, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return uuid.Nil, err
	}
	var orgID uuid.UUID
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		id, org, info, err := s.lookup(ctx, tx, token)
		if err != nil {
			return err
		}
		if !strings.EqualFold(info.Email, u.Email) {
			return apperr.Forbidden("this invitation was sent to " + info.Email + " — sign in with that address")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1,$2,$3)
			ON CONFLICT (organization_id, user_id) DO NOTHING`, org, userID, info.Role); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invitations SET accepted_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		orgID = org
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	_ = s.users.MarkEmailVerified(ctx, userID)
	s.audit.Record(ctx, audit.Entry{OrgID: orgID, UserID: userID, Action: "invitation.accepted", EntityType: "organization", EntityID: orgID.String()})
	return orgID, nil
}

// ───────────── HTTP ─────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/verify-email/send", h.mw.User(h.sendCode))
	mux.HandleFunc("POST /api/v1/auth/verify-email", h.mw.User(h.verify))
	mux.HandleFunc("GET /api/v1/onboarding", h.mw.Org(auth.RoleViewer, h.status))
	mux.HandleFunc("PUT /api/v1/onboarding", h.mw.Org(auth.RoleAdmin, h.save))
	mux.HandleFunc("GET /api/v1/invitations", h.mw.Org(auth.RoleAdmin, h.list))
	mux.HandleFunc("POST /api/v1/invitations", h.mw.Org(auth.RoleAdmin, h.invite))
	mux.HandleFunc("DELETE /api/v1/invitations/{id}", h.mw.Org(auth.RoleAdmin, h.revoke))
	mux.HandleFunc("GET /api/v1/invitations/lookup", h.lookupHandler)
	mux.HandleFunc("POST /api/v1/invitations/accept", h.mw.User(h.accept))
}

func (h *Handler) sendCode(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.SendVerification(r.Context(), auth.MustPrincipal(r.Context()).UserID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, map[string]string{"status": "sent"})
}

func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.VerifyEmail(r.Context(), auth.MustPrincipal(r.Context()).UserID, in.Code); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, map[string]bool{"verified": true})
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Status(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.SaveState(r.Context(), auth.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Invitations(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) invite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Emails []string `json:"emails"`
		Role   string   `json:"role"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := h.svc.Invite(r.Context(), auth.MustPrincipal(r.Context()), in.Emails, in.Role)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("invitation"))
		return
	}
	if err := h.svc.Revoke(r.Context(), auth.MustPrincipal(r.Context()), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) lookupHandler(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if len(token) < 20 {
		httpx.Error(w, r, apperr.NotFound("invitation"))
		return
	}
	out, err := h.svc.Lookup(r.Context(), token)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) accept(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Error(w, r, err)
		return
	}
	org, err := h.svc.Accept(r.Context(), auth.MustPrincipal(r.Context()).UserID, in.Token)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, map[string]string{"organization_id": org.String()})
}

// SetMailer replaces the account-email transport (tests capture messages).
func (s *Service) SetMailer(m email.EmailProvider) { s.mailer = m }
