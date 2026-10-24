// Package analytics computes organization- and campaign-level metrics
// directly from PostgreSQL. Rates use distinct messages/leads so duplicate
// provider events never inflate them.
//
// At MVP volumes (≈ millions of messages) these indexed queries are fast; a
// rollup worker writing daily aggregates can replace them later without
// changing the API.
package analytics

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadline/internal/auth"
	"leadline/internal/email"
	"leadline/pkg/apperr"
	"leadline/pkg/database"
	"leadline/pkg/httpx"
)

type Metrics struct {
	Sent              int64   `json:"sent"`
	Delivered         int64   `json:"delivered"` // explicit provider delivery events
	Bounced           int64   `json:"bounced"`
	Complaints        int64   `json:"complaints"`
	Unsubscribed      int64   `json:"unsubscribed"`
	Failed            int64   `json:"failed"`
	ContactedLeads    int64   `json:"contacted_leads"`
	RepliedLeads      int64   `json:"replied_leads"`
	PositiveReplies   int64   `json:"positive_replies"`
	DeliveryRate      float64 `json:"delivery_rate"` // (sent − bounced) / sent
	BounceRate        float64 `json:"bounce_rate"`
	ReplyRate         float64 `json:"reply_rate"` // per contacted lead
	PositiveReplyRate float64 `json:"positive_reply_rate"`
	UnsubscribeRate   float64 `json:"unsubscribe_rate"`
}

func (m *Metrics) rates() {
	m.DeliveryRate = ratio(m.Sent-m.Bounced, m.Sent)
	m.BounceRate = ratio(m.Bounced, m.Sent)
	m.ReplyRate = ratio(m.RepliedLeads, m.ContactedLeads)
	m.PositiveReplyRate = ratio(m.PositiveReplies, m.ContactedLeads)
	m.UnsubscribeRate = ratio(m.Unsubscribed, m.ContactedLeads)
}

func ratio(a, b int64) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b)
}

type Overview struct {
	TotalLeads      int64        `json:"total_leads"`
	VerifiedLeads   int64        `json:"verified_leads"`
	ActiveCampaigns int64        `json:"active_campaigns"`
	InterestedLeads int64        `json:"interested_leads"`
	Meetings        int64        `json:"meetings"`
	Won             int64        `json:"won"`
	ConversionRate  float64      `json:"conversion_rate"` // meetings / contacted leads
	Period          Metrics      `json:"period"`
	Daily           []DailyPoint `json:"daily"`
	Days            int          `json:"days"`
}

type DailyPoint struct {
	Date    string `json:"date"`
	Sent    int64  `json:"sent"`
	Bounced int64  `json:"bounced"`
	Replied int64  `json:"replied"`
}

type CampaignRow struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
	Leads  int64     `json:"leads"`
	Metrics
}

type CampaignDetail struct {
	CampaignRow
	Funnel []StatusCount `json:"funnel"`
	Steps  []StepMetrics `json:"steps"`
	Daily  []DailyPoint  `json:"daily"`
}

type StatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type StepMetrics struct {
	StepNumber int   `json:"step_number"`
	Sent       int64 `json:"sent"`
	Bounced    int64 `json:"bounced"`
	Replied    int64 `json:"replied"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// metricsSQL computes Metrics over messages matching a scope; $1 is always
// the organization, $2 the window start, $3 an optional campaign.
const metricsSQL = `
WITH msgs AS (
	SELECT id, lead_id, status FROM email_messages
	WHERE organization_id = $1 AND created_at >= $2 AND ($3::uuid IS NULL OR campaign_id = $3)
), ev AS (
	SELECT e.event_type, e.email_message_id, e.lead_id, e.metadata FROM email_events e
	WHERE e.organization_id = $1 AND e.email_message_id IN (SELECT id FROM msgs)
)
SELECT
	(SELECT count(*) FROM msgs WHERE status = 'sent'),
	(SELECT count(DISTINCT email_message_id) FROM ev WHERE event_type = 'delivered'),
	(SELECT count(DISTINCT email_message_id) FROM ev WHERE event_type = 'bounced'
		AND COALESCE(metadata->>'bounce_type','hard') NOT IN ('soft','transient')),
	(SELECT count(DISTINCT email_message_id) FROM ev WHERE event_type = 'complained'),
	(SELECT count(DISTINCT lead_id) FROM ev WHERE event_type = 'unsubscribed'),
	(SELECT count(*) FROM msgs WHERE status = 'failed'),
	(SELECT count(DISTINCT lead_id) FROM msgs WHERE status = 'sent'),
	(SELECT count(DISTINCT lead_id) FROM ev WHERE event_type = 'replied'),
	(SELECT count(DISTINCT lead_id) FROM ev WHERE event_type = 'replied' AND metadata->>'classification' = ANY($4))`

func (s *Service) metrics(ctx context.Context, orgID uuid.UUID, since time.Time, campaignID *uuid.UUID) (Metrics, error) {
	var m Metrics
	err := s.pool.QueryRow(ctx, metricsSQL, orgID, since, campaignID, email.PositiveClassifications).Scan(
		&m.Sent, &m.Delivered, &m.Bounced, &m.Complaints, &m.Unsubscribed, &m.Failed,
		&m.ContactedLeads, &m.RepliedLeads, &m.PositiveReplies)
	m.rates()
	return m, err
}

func (s *Service) daily(ctx context.Context, orgID uuid.UUID, days int, campaignID *uuid.UUID) ([]DailyPoint, error) {
	rows, err := s.pool.Query(ctx, `
		WITH d AS (SELECT generate_series((now() AT TIME ZONE 'UTC')::date - ($2::int - 1), (now() AT TIME ZONE 'UTC')::date, '1 day')::date AS day)
		SELECT to_char(d.day, 'YYYY-MM-DD'),
		  (SELECT count(*) FROM email_messages m WHERE m.organization_id=$1 AND m.status='sent'
		     AND ($3::uuid IS NULL OR m.campaign_id=$3) AND (m.sent_at AT TIME ZONE 'UTC')::date = d.day),
		  (SELECT count(*) FROM email_events e WHERE e.organization_id=$1 AND e.event_type='bounced'
		     AND ($3::uuid IS NULL OR e.campaign_id=$3) AND (e.occurred_at AT TIME ZONE 'UTC')::date = d.day),
		  (SELECT count(*) FROM email_events e WHERE e.organization_id=$1 AND e.event_type='replied'
		     AND ($3::uuid IS NULL OR e.campaign_id=$3) AND (e.occurred_at AT TIME ZONE 'UTC')::date = d.day)
		FROM d ORDER BY d.day`, orgID, days, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DailyPoint{}
	for rows.Next() {
		var p DailyPoint
		if err := rows.Scan(&p.Date, &p.Sent, &p.Bounced, &p.Replied); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) Overview(ctx context.Context, p auth.Principal, days int) (*Overview, error) {
	o := &Overview{Days: days}
	if err := s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM leads WHERE organization_id=$1),
		       (SELECT count(DISTINCT lead_id) FROM lead_contacts WHERE organization_id=$1 AND email_status='valid'),
		       (SELECT count(*) FROM campaigns WHERE organization_id=$1 AND status IN ('running','scheduled')),
		       (SELECT count(*) FROM leads WHERE organization_id=$1 AND status IN ('interested','qualified','meeting','won')),
		       (SELECT count(*) FROM leads WHERE organization_id=$1 AND status IN ('meeting','won')),
		       (SELECT count(*) FROM leads WHERE organization_id=$1 AND status = 'won')`, p.OrgID).
		Scan(&o.TotalLeads, &o.VerifiedLeads, &o.ActiveCampaigns, &o.InterestedLeads, &o.Meetings, &o.Won); err != nil {
		return nil, err
	}
	var err error
	since := time.Now().AddDate(0, 0, -days)
	if o.Period, err = s.metrics(ctx, p.OrgID, since, nil); err != nil {
		return nil, err
	}
	o.ConversionRate = ratio(o.Meetings, o.Period.ContactedLeads)
	if o.Daily, err = s.daily(ctx, p.OrgID, days, nil); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Service) Campaigns(ctx context.Context, p auth.Principal) ([]CampaignRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.status, (SELECT count(*) FROM campaign_leads cl WHERE cl.campaign_id = c.id)
		FROM campaigns c WHERE c.organization_id=$1 AND c.status <> 'draft' ORDER BY c.created_at DESC LIMIT 100`, p.OrgID)
	if err != nil {
		return nil, err
	}
	var out []CampaignRow
	for rows.Next() {
		var r CampaignRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Status, &r.Leads); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	epoch := time.Unix(0, 0)
	for i := range out {
		if out[i].Metrics, err = s.metrics(ctx, p.OrgID, epoch, &out[i].ID); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = []CampaignRow{}
	}
	return out, nil
}

func (s *Service) Campaign(ctx context.Context, p auth.Principal, id uuid.UUID, days int) (*CampaignDetail, error) {
	d := &CampaignDetail{}
	err := s.pool.QueryRow(ctx, `SELECT c.id, c.name, c.status, (SELECT count(*) FROM campaign_leads cl WHERE cl.campaign_id=c.id)
		FROM campaigns c WHERE c.organization_id=$1 AND c.id=$2`, p.OrgID, id).Scan(&d.ID, &d.Name, &d.Status, &d.Leads)
	if database.IsNoRows(err) {
		return nil, apperr.NotFound("campaign")
	}
	if err != nil {
		return nil, err
	}
	if d.Metrics, err = s.metrics(ctx, p.OrgID, time.Unix(0, 0), &id); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM campaign_leads WHERE organization_id=$1 AND campaign_id=$2 GROUP BY status ORDER BY 2 DESC`, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	d.Funnel = []StatusCount{}
	for rows.Next() {
		var sc StatusCount
		if err := rows.Scan(&sc.Status, &sc.Count); err != nil {
			rows.Close()
			return nil, err
		}
		d.Funnel = append(d.Funnel, sc)
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `
		SELECT st.step_number,
		  (SELECT count(*) FROM email_messages m WHERE m.step_id = st.id AND m.status='sent'),
		  (SELECT count(DISTINCT e.email_message_id) FROM email_events e JOIN email_messages m ON m.id = e.email_message_id
		     WHERE m.step_id = st.id AND e.event_type='bounced'),
		  (SELECT count(DISTINCT e.lead_id) FROM email_events e JOIN email_messages m ON m.id = e.email_message_id
		     WHERE m.step_id = st.id AND e.event_type='replied')
		FROM campaign_steps st WHERE st.organization_id=$1 AND st.campaign_id=$2 ORDER BY st.step_number`, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	d.Steps = []StepMetrics{}
	for rows.Next() {
		var sm StepMetrics
		if err := rows.Scan(&sm.StepNumber, &sm.Sent, &sm.Bounced, &sm.Replied); err != nil {
			rows.Close()
			return nil, err
		}
		d.Steps = append(d.Steps, sm)
	}
	rows.Close()

	if d.Daily, err = s.daily(ctx, p.OrgID, days, &id); err != nil {
		return nil, err
	}
	return d, nil
}

// ───────────────────────── HTTP ─────────────────────────

type Handler struct {
	svc *Service
	mw  *auth.Middleware
}

func NewHandler(svc *Service, mw *auth.Middleware) *Handler { return &Handler{svc: svc, mw: mw} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/analytics/overview", h.mw.Org(auth.RoleViewer, h.overview))
	mux.HandleFunc("GET /api/v1/analytics/campaigns", h.mw.Org(auth.RoleViewer, h.campaigns))
	mux.HandleFunc("GET /api/v1/analytics/campaigns/{id}", h.mw.Org(auth.RoleViewer, h.campaign))
	mux.HandleFunc("GET /api/v1/analytics/advanced", h.mw.Org(auth.RoleViewer, h.advanced))
	mux.HandleFunc("POST /api/v1/analytics/leads", h.mw.Org(auth.RoleViewer, h.leadInsights))
	mux.HandleFunc("POST /api/v1/analytics/leads/steps", h.mw.Org(auth.RoleViewer, h.stepCounts))
}

func days(r *http.Request) int {
	d := httpx.QueryInt(r, "days", 30)
	return max(1, min(d, 365))
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Overview(r.Context(), auth.MustPrincipal(r.Context()), days(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, o)
}

func (h *Handler) campaigns(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Campaigns(r.Context(), auth.MustPrincipal(r.Context()))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, out)
}

func (h *Handler) campaign(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, apperr.NotFound("campaign"))
		return
	}
	d, err := h.svc.Campaign(r.Context(), auth.MustPrincipal(r.Context()), id, days(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, d)
}

// ───────────────────────── Advanced analytics ─────────────────────────

type Segment struct {
	Value        string  `json:"value"`
	Contacted    int64   `json:"contacted"`
	Replied      int64   `json:"replied"`
	Positive     int64   `json:"positive"`
	ReplyRate    float64 `json:"reply_rate"`
	PositiveRate float64 `json:"positive_rate"`
}

type MailboxPerf struct {
	ID         uuid.UUID `json:"id"`
	FromEmail  string    `json:"from_email"`
	Sent       int64     `json:"sent"`
	Bounced    int64     `json:"bounced"`
	Replied    int64     `json:"replied"`
	BounceRate float64   `json:"bounce_rate"`
	ReplyRate  float64   `json:"reply_rate"`
}

type Advanced struct {
	Classifications    []StatusCount        `json:"classifications"`
	Mailboxes          []MailboxPerf        `json:"mailboxes"`
	Segments           map[string][]Segment `json:"segments"`
	MedianHoursToReply *float64             `json:"median_hours_to_reply"`
	RepliesByStep      []StepMetrics        `json:"replies_by_step"`
	Days               int                  `json:"days"`
}

// Advanced breaks performance down by reply classification, mailbox, lead
// segment (industry / city / source / website quality) and time to reply.
func (s *Service) Advanced(ctx context.Context, p auth.Principal, days int) (*Advanced, error) {
	since := time.Now().AddDate(0, 0, -days)
	out := &Advanced{Days: days, Segments: map[string][]Segment{}, Classifications: []StatusCount{}, Mailboxes: []MailboxPerf{}, RepliesByStep: []StepMetrics{}}

	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(NULLIF(metadata->>'classification',''), 'unclassified'), count(DISTINCT lead_id)
		FROM email_events WHERE organization_id=$1 AND event_type='replied' AND occurred_at >= $2
		GROUP BY 1 ORDER BY 2 DESC`, p.OrgID, since)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c StatusCount
		if err := rows.Scan(&c.Status, &c.Count); err != nil {
			rows.Close()
			return nil, err
		}
		out.Classifications = append(out.Classifications, c)
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `
		SELECT mb.id, mb.from_email::text,
		  count(*) FILTER (WHERE m.status='sent'),
		  count(DISTINCT m.id) FILTER (WHERE EXISTS (SELECT 1 FROM email_events e WHERE e.email_message_id=m.id AND e.event_type='bounced')),
		  count(DISTINCT m.lead_id) FILTER (WHERE EXISTS (SELECT 1 FROM email_events e WHERE e.email_message_id=m.id AND e.event_type='replied'))
		FROM mailboxes mb LEFT JOIN email_messages m ON m.mailbox_id=mb.id AND m.created_at >= $2
		WHERE mb.organization_id=$1 GROUP BY mb.id ORDER BY 3 DESC`, p.OrgID, since)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var m MailboxPerf
		if err := rows.Scan(&m.ID, &m.FromEmail, &m.Sent, &m.Bounced, &m.Replied); err != nil {
			rows.Close()
			return nil, err
		}
		m.BounceRate, m.ReplyRate = ratio(m.Bounced, m.Sent), ratio(m.Replied, m.Sent)
		out.Mailboxes = append(out.Mailboxes, m)
	}
	rows.Close()

	for name, expr := range map[string]string{"industry": "lower(l.industry)", "city": "l.city", "source": "l.source", "website_quality": "l.website_quality"} {
		rows, err := s.pool.Query(ctx, `
			WITH contacted AS (
				SELECT DISTINCT m.lead_id FROM email_messages m WHERE m.organization_id=$1 AND m.status='sent' AND m.sent_at >= $2
			), rep AS (
				SELECT e.lead_id, bool_or(e.metadata->>'classification' = ANY($3)) AS positive
				FROM email_events e WHERE e.organization_id=$1 AND e.event_type='replied' AND e.occurred_at >= $2 GROUP BY e.lead_id
			)
			SELECT COALESCE(NULLIF(`+expr+`,''),'(none)'), count(*), count(rep.lead_id), count(*) FILTER (WHERE rep.positive)
			FROM contacted c JOIN leads l ON l.id=c.lead_id LEFT JOIN rep ON rep.lead_id=c.lead_id
			GROUP BY 1 HAVING count(*) >= 3 ORDER BY 2 DESC LIMIT 15`, p.OrgID, since, email.PositiveClassifications)
		if err != nil {
			return nil, err
		}
		segs := []Segment{}
		for rows.Next() {
			var sg Segment
			if err := rows.Scan(&sg.Value, &sg.Contacted, &sg.Replied, &sg.Positive); err != nil {
				rows.Close()
				return nil, err
			}
			sg.ReplyRate, sg.PositiveRate = ratio(sg.Replied, sg.Contacted), ratio(sg.Positive, sg.Contacted)
			segs = append(segs, sg)
		}
		rows.Close()
		out.Segments[name] = segs
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM (e.occurred_at - m.sent_at)) / 3600)
		FROM email_events e JOIN email_messages m ON m.id=e.email_message_id
		WHERE e.organization_id=$1 AND e.event_type='replied' AND e.occurred_at >= $2 AND m.sent_at IS NOT NULL AND e.occurred_at > m.sent_at`,
		p.OrgID, since).Scan(&out.MedianHoursToReply); err != nil {
		return nil, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT m.step_number, count(DISTINCT m.id) FILTER (WHERE m.status='sent'),
		  count(DISTINCT m.id) FILTER (WHERE EXISTS (SELECT 1 FROM email_events e WHERE e.email_message_id=m.id AND e.event_type='bounced')),
		  count(DISTINCT m.lead_id) FILTER (WHERE EXISTS (SELECT 1 FROM email_events e WHERE e.email_message_id=m.id AND e.event_type='replied'))
		FROM email_messages m WHERE m.organization_id=$1 AND m.created_at >= $2 GROUP BY 1 ORDER BY 1`, p.OrgID, since)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sm StepMetrics
		if err := rows.Scan(&sm.StepNumber, &sm.Sent, &sm.Bounced, &sm.Replied); err != nil {
			rows.Close()
			return nil, err
		}
		out.RepliesByStep = append(out.RepliesByStep, sm)
	}
	rows.Close()
	return out, nil
}

func (h *Handler) advanced(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.Advanced(r.Context(), auth.MustPrincipal(r.Context()), days(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.OK(w, a)
}
