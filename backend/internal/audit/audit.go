// Package audit records security- and compliance-relevant actions.
package audit

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"leadline/pkg/database"
	"leadline/pkg/httpx"
	"leadline/pkg/logger"
)

type Entry struct {
	OrgID      uuid.UUID // zero for account-level actions
	UserID     uuid.UUID // zero for system actions
	Action     string    // e.g. "campaign.launched"
	EntityType string
	EntityID   string
	Metadata   map[string]any
}

type Log struct{ db database.DBTX }

func New(db database.DBTX) *Log { return &Log{db: db} }

// Record writes an audit entry. Failures are logged, never returned: an audit
// write must not break the user's action.
func (l *Log) Record(ctx context.Context, e Entry) {
	meta, _ := json.Marshal(e.Metadata)
	if e.Metadata == nil {
		meta = []byte("{}")
	}
	_, err := l.db.Exec(ctx, `
		INSERT INTO audit_logs (organization_id, user_id, action, entity_type, entity_id, metadata, ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		nullUUID(e.OrgID), nullUUID(e.UserID), e.Action, e.EntityType, e.EntityID, meta, httpx.IP(ctx))
	if err != nil {
		logger.From(ctx).Error("audit write failed", "action", e.Action, "error", err)
	}
}

type Record struct {
	ID         int64           `json:"id"`
	UserID     *uuid.UUID      `json:"user_id"`
	UserEmail  *string         `json:"user_email"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Metadata   json.RawMessage `json:"metadata"`
	IP         string          `json:"ip"`
	CreatedAt  string          `json:"created_at"`
}

func (l *Log) List(ctx context.Context, orgID uuid.UUID, limit, offset int) ([]Record, int64, error) {
	var total int64
	if err := l.db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE organization_id=$1`, orgID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := l.db.Query(ctx, `
		SELECT a.id, a.user_id, u.email::text, a.action, a.entity_type, a.entity_id, a.metadata, a.ip,
		       to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM audit_logs a LEFT JOIN users u ON u.id = a.user_id
		WHERE a.organization_id = $1
		ORDER BY a.created_at DESC LIMIT $2 OFFSET $3`, orgID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.UserID, &r.UserEmail, &r.Action, &r.EntityType, &r.EntityID, &r.Metadata, &r.IP, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
