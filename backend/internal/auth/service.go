package auth

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"leadline/internal/audit"
	"leadline/internal/users"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/secure"
	"leadline/pkg/validate"
)

const bcryptCost = 12

// dummyHash is compared against when a login email does not exist so that
// response timing does not reveal which emails have accounts.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalizer-password"), bcryptCost)

type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ExpiresAt time.Time
	RevokedAt *time.Time
}

type SessionRepository struct{ db database.DBTX }

func NewSessionRepository(db database.DBTX) *SessionRepository { return &SessionRepository{db: db} }

func (r *SessionRepository) Create(ctx context.Context, userID uuid.UUID, tokenHash []byte, ua, ip string, exp time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `
		INSERT INTO sessions (user_id, refresh_token_hash, user_agent, ip, expires_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, userID, tokenHash, truncate(ua, 255), ip, exp).Scan(&id)
	return id, err
}

func (r *SessionRepository) GetByHash(ctx context.Context, hash []byte) (*Session, error) {
	var s Session
	err := r.db.QueryRow(ctx, `SELECT id, user_id, expires_at, revoked_at FROM sessions WHERE refresh_token_hash = $1`, hash).
		Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.RevokedAt)
	if database.IsNoRows(err) {
		return nil, apperr.Unauthorized("invalid session")
	}
	return &s, err
}

// Revoke marks a session revoked. It returns false if it was already revoked,
// which lets rotation detect concurrent reuse of the same refresh token.
func (r *SessionRepository) Revoke(ctx context.Context, id uuid.UUID, replacedBy *uuid.UUID) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE sessions SET revoked_at = now(), replaced_by = $2 WHERE id = $1 AND revoked_at IS NULL`, id, replacedBy)
	return tag.RowsAffected() == 1, err
}

func (r *SessionRepository) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

type Service struct {
	users      *users.Repository
	sessions   *SessionRepository
	tokens     *TokenIssuer
	audit      *audit.Log
	refreshTTL time.Duration
	// OnRegistered runs after a new account is created (e.g. to send the
	// email verification code). Errors are the hook's to handle.
	OnRegistered func(ctx context.Context, userID uuid.UUID)
}

func NewService(u *users.Repository, s *SessionRepository, t *TokenIssuer, a *audit.Log, refreshTTL time.Duration) *Service {
	return &Service{users: u, sessions: s, tokens: t, audit: a, refreshTTL: refreshTTL}
}

type RegisterInput struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	FullName    string `json:"full_name"`
	AcceptTerms bool   `json:"accept_terms"`
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type ClientInfo struct{ UserAgent, IP string }

// AuthResult is returned on register/login/refresh. RefreshToken is set as an
// httpOnly cookie by the handler and never serialized in the JSON body.
type AuthResult struct {
	User         *users.User `json:"user"`
	AccessToken  string      `json:"access_token"`
	ExpiresAt    time.Time   `json:"expires_at"`
	RefreshToken string      `json:"-"`
	RefreshExp   time.Time   `json:"-"`
	sessionID    uuid.UUID
}

func (s *Service) Register(ctx context.Context, in RegisterInput, ci ClientInfo) (*AuthResult, error) {
	in.Email = validate.NormalizeEmail(in.Email)
	in.FullName = strings.TrimSpace(in.FullName)
	v := validate.Errors{}
	v.Check(validate.Email(in.Email), "email", "must be a valid email address")
	v.Check(len(in.Password) >= 10, "password", "must be at least 10 characters")
	v.Check(len(in.Password) <= 72, "password", "must be at most 72 characters")
	v.Required("full_name", in.FullName)
	v.MaxLen("full_name", in.FullName, 120)
	v.Check(in.AcceptTerms, "accept_terms", "you must accept the terms of service and privacy policy")
	if err := v.Err(); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
	if err != nil {
		return nil, err
	}
	u, err := s.users.Create(ctx, in.Email, in.FullName, string(hash))
	if err != nil {
		return nil, err
	}
	if err := s.users.AcceptTerms(ctx, u.ID); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{UserID: u.ID, Action: "user.registered", EntityType: "user", EntityID: u.ID.String()})
	if s.OnRegistered != nil {
		s.OnRegistered(ctx, u.ID)
	}
	return s.startSession(ctx, u, ci)
}

func (s *Service) Login(ctx context.Context, in LoginInput, ci ClientInfo) (*AuthResult, error) {
	email := validate.NormalizeEmail(in.Email)
	u, err := s.users.GetByEmail(ctx, email)
	if err != nil && !apperr.IsNotFound(err) {
		return nil, err
	}
	if u == nil {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(in.Password)) //nolint:errcheck
		return nil, apperr.Unauthorized("invalid email or password")
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Password)) != nil {
		s.audit.Record(ctx, audit.Entry{UserID: u.ID, Action: "user.login_failed", EntityType: "user", EntityID: u.ID.String()})
		return nil, apperr.Unauthorized("invalid email or password")
	}
	if err := s.users.TouchLogin(ctx, u.ID); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{UserID: u.ID, Action: "user.login", EntityType: "user", EntityID: u.ID.String()})
	return s.startSession(ctx, u, ci)
}

// Refresh rotates the refresh token. Presenting an already-rotated token is
// treated as theft: every session for that user is revoked.
func (s *Service) Refresh(ctx context.Context, refreshToken string, ci ClientInfo) (*AuthResult, error) {
	if refreshToken == "" {
		return nil, apperr.Unauthorized("missing refresh token")
	}
	sess, err := s.sessions.GetByHash(ctx, secure.HashToken(refreshToken))
	if err != nil {
		return nil, err
	}
	if sess.RevokedAt != nil {
		_ = s.sessions.RevokeAllForUser(ctx, sess.UserID)
		s.audit.Record(ctx, audit.Entry{UserID: sess.UserID, Action: "session.reuse_detected", EntityType: "session", EntityID: sess.ID.String()})
		return nil, apperr.Unauthorized("session revoked")
	}
	if time.Now().After(sess.ExpiresAt) {
		return nil, apperr.Unauthorized("session expired")
	}
	u, err := s.users.GetByID(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	res, err := s.startSession(ctx, u, ci)
	if err != nil {
		return nil, err
	}
	newID := res.sessionID
	ok, err := s.sessions.Revoke(ctx, sess.ID, &newID)
	if err != nil {
		return nil, err
	}
	if !ok { // lost a race with another refresh using the same token
		_, _ = s.sessions.Revoke(ctx, newID, nil)
		return nil, apperr.Unauthorized("session revoked")
	}
	return res, nil
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	sess, err := s.sessions.GetByHash(ctx, secure.HashToken(refreshToken))
	if err != nil {
		return nil // already gone; logout is idempotent
	}
	_, err = s.sessions.Revoke(ctx, sess.ID, nil)
	return err
}

func (s *Service) Me(ctx context.Context, userID uuid.UUID) (*users.User, error) {
	return s.users.GetByID(ctx, userID)
}

func (s *Service) startSession(ctx context.Context, u *users.User, ci ClientInfo) (*AuthResult, error) {
	refresh, err := secure.RandomToken(32)
	if err != nil {
		return nil, err
	}
	exp := time.Now().Add(s.refreshTTL)
	sid, err := s.sessions.Create(ctx, u.ID, secure.HashToken(refresh), ci.UserAgent, ci.IP, exp)
	if err != nil {
		return nil, err
	}
	access, accessExp, err := s.tokens.Issue(u.ID, sid)
	if err != nil {
		return nil, err
	}
	return &AuthResult{User: u, AccessToken: access, ExpiresAt: accessExp, RefreshToken: refresh, RefreshExp: exp, sessionID: sid}, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
