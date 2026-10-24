// Package users stores user accounts. Users are global; tenant data hangs off
// organizations, which users join through memberships.
package users

import (
	"context"
	"time"

	"github.com/google/uuid"

	"leadline/pkg/apperr"
	"leadline/pkg/database"
)

type User struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	FullName     string     `json:"full_name"`
	PasswordHash string     `json:"-"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at"`
	// EmailVerified is true once the user proved they own the address.
	EmailVerified bool `json:"email_verified"`
}

type Repository struct{ db database.DBTX }

func NewRepository(db database.DBTX) *Repository { return &Repository{db: db} }

const cols = `id, email::text, full_name, password_hash, last_login_at, created_at, email_verified_at IS NOT NULL`

func scan(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.PasswordHash, &u.LastLoginAt, &u.CreatedAt, &u.EmailVerified); err != nil {
		if database.IsNoRows(err) {
			return nil, apperr.NotFound("user")
		}
		return nil, err
	}
	return &u, nil
}

func (r *Repository) Create(ctx context.Context, email, fullName, passwordHash string) (*User, error) {
	u, err := scan(r.db.QueryRow(ctx, `
		INSERT INTO users (email, full_name, password_hash) VALUES ($1, $2, $3)
		RETURNING `+cols, email, fullName, passwordHash))
	if database.IsUniqueViolation(err) {
		return nil, apperr.Conflict("email_taken", "an account with this email already exists")
	}
	return u, err
}

func (r *Repository) GetByEmail(ctx context.Context, email string) (*User, error) {
	return scan(r.db.QueryRow(ctx, `SELECT `+cols+` FROM users WHERE email = $1`, email))
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	return scan(r.db.QueryRow(ctx, `SELECT `+cols+` FROM users WHERE id = $1`, id))
}

func (r *Repository) TouchLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
	return err
}

// AcceptTerms records when the user accepted the terms of service.
func (r *Repository) AcceptTerms(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET terms_accepted_at = now() WHERE id = $1`, id)
	return err
}

// MarkEmailVerified records that the user owns their email address.
func (r *Repository) MarkEmailVerified(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE id = $1`, id)
	return err
}
