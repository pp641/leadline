-- Milestone 1 schema. PostgreSQL is the source of truth for all business entities.
-- Every tenant-owned table carries organization_id and every query filters on it.

CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ───────────────────────── Identity & tenancy ─────────────────────────

CREATE TABLE users (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email          citext NOT NULL UNIQUE,
    password_hash  text NOT NULL,
    full_name      text NOT NULL DEFAULT '',
    last_login_at  timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash  bytea NOT NULL UNIQUE,
    user_agent          text NOT NULL DEFAULT '',
    ip                  text NOT NULL DEFAULT '',
    expires_at          timestamptz NOT NULL,
    revoked_at          timestamptz,
    replaced_by         uuid,
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_idx ON sessions(user_id);

CREATE TABLE organizations (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  text NOT NULL,
    slug                  text NOT NULL UNIQUE,
    -- Shown in every outbound email footer (CAN-SPAM / GDPR sender identification).
    legal_name            text NOT NULL DEFAULT '',
    physical_address      text NOT NULL DEFAULT '',
    daily_send_limit      int  NOT NULL DEFAULT 500 CHECK (daily_send_limit >= 0),
    scoring_config        jsonb,
    data_retention_days   int CHECK (data_retention_days IS NULL OR data_retention_days > 0),
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE organization_members (
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role             text NOT NULL CHECK (role IN ('owner','admin','member','viewer')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);
CREATE INDEX organization_members_user_idx ON organization_members(user_id);

CREATE TABLE mailboxes (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             text NOT NULL,
    from_email       citext NOT NULL,
    from_name        text NOT NULL,
    reply_to         citext,
    daily_limit      int NOT NULL DEFAULT 50 CHECK (daily_limit >= 0),
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, from_email)
);

-- ───────────────────────── Leads ─────────────────────────

CREATE TABLE imports (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    filename         text NOT NULL,
    storage_key      text NOT NULL,
    status           text NOT NULL DEFAULT 'uploaded'
                     CHECK (status IN ('uploaded','previewed','queued','processing','completed','failed')),
    mapping          jsonb NOT NULL DEFAULT '{}',
    headers          jsonb NOT NULL DEFAULT '[]',
    source           text NOT NULL DEFAULT 'csv_import',
    consent_basis    text NOT NULL DEFAULT 'legitimate_interest',
    total_rows       int NOT NULL DEFAULT 0,
    valid_rows       int NOT NULL DEFAULT 0,
    invalid_rows     int NOT NULL DEFAULT 0,
    duplicate_rows   int NOT NULL DEFAULT 0,
    imported_rows    int NOT NULL DEFAULT 0,
    preview          jsonb NOT NULL DEFAULT '[]',
    error            text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    completed_at     timestamptz
);
CREATE INDEX imports_org_idx ON imports(organization_id, created_at DESC);

CREATE TABLE leads (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    company_name     text NOT NULL,
    website          text NOT NULL DEFAULT '',
    domain           text NOT NULL DEFAULT '',  -- normalized from website, used for dedupe
    industry         text NOT NULL DEFAULT '',
    description      text NOT NULL DEFAULT '',
    country          text NOT NULL DEFAULT '',
    state            text NOT NULL DEFAULT '',
    city             text NOT NULL DEFAULT '',
    address          text NOT NULL DEFAULT '',
    phone            text NOT NULL DEFAULT '',
    business_email   citext,
    linkedin_url     text NOT NULL DEFAULT '',
    social_urls      jsonb NOT NULL DEFAULT '[]',
    employee_count   int,
    founded_year     int,
    source           text NOT NULL DEFAULT 'manual',
    source_url       text NOT NULL DEFAULT '',
    import_id        uuid REFERENCES imports(id) ON DELETE SET NULL,
    lead_score       int NOT NULL DEFAULT 0,
    status           text NOT NULL DEFAULT 'new' CHECK (status IN
                     ('new','contacted','replied','interested','qualified','meeting','won','lost','unsubscribed')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX leads_org_created_idx  ON leads(organization_id, created_at DESC, id);
CREATE INDEX leads_org_score_idx    ON leads(organization_id, lead_score DESC);
CREATE INDEX leads_org_domain_idx   ON leads(organization_id, domain) WHERE domain <> '';
CREATE INDEX leads_org_name_city    ON leads(organization_id, lower(company_name), lower(city));
CREATE INDEX leads_org_industry_idx ON leads(organization_id, lower(industry));
CREATE INDEX leads_org_city_idx     ON leads(organization_id, lower(city));
CREATE INDEX leads_org_status_idx   ON leads(organization_id, status);
CREATE INDEX leads_name_trgm_idx    ON leads USING gin (company_name gin_trgm_ops);

CREATE TABLE lead_contacts (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id       uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id               uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    first_name            text NOT NULL DEFAULT '',
    last_name             text NOT NULL DEFAULT '',
    job_title             text NOT NULL DEFAULT '',
    email                 citext,
    email_status          text NOT NULL DEFAULT 'unverified' CHECK (email_status IN
                          ('unverified','valid','invalid','risky','unknown','disposable','role_based')),
    email_verified_at     timestamptz,
    verification_details  jsonb NOT NULL DEFAULT '{}',
    linkedin_url          text NOT NULL DEFAULT '',
    source                text NOT NULL DEFAULT 'manual',
    confidence            real NOT NULL DEFAULT 1.0,
    is_primary            boolean NOT NULL DEFAULT false,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX lead_contacts_lead_idx  ON lead_contacts(lead_id);
CREATE INDEX lead_contacts_email_idx ON lead_contacts(organization_id, email);
CREATE INDEX lead_contacts_unverified_idx ON lead_contacts(organization_id) WHERE email_status = 'unverified' AND email IS NOT NULL;

-- Phase 2: populated by enrichment providers.
CREATE TABLE lead_enrichment (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id          uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    provider         text NOT NULL,
    data             jsonb NOT NULL DEFAULT '{}',
    enriched_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (lead_id, provider)
);

CREATE TABLE lead_scores (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id          uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    score            int NOT NULL,
    reasons          jsonb NOT NULL DEFAULT '[]',
    score_version    int NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (lead_id)  -- current score; history lives in lead_events
);

-- Where each lead came from and on what basis it may be contacted.
CREATE TABLE lead_sources (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id          uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    source           text NOT NULL,
    source_url       text NOT NULL DEFAULT '',
    import_id        uuid REFERENCES imports(id) ON DELETE SET NULL,
    consent_basis    text NOT NULL DEFAULT 'legitimate_interest',
    metadata         jsonb NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX lead_sources_lead_idx ON lead_sources(lead_id);

CREATE TABLE lead_events (
    id               bigserial PRIMARY KEY,
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id          uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    event_type       text NOT NULL,
    actor_user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    data             jsonb NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX lead_events_lead_idx ON lead_events(lead_id, created_at DESC);

CREATE TABLE lead_tags (
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id          uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    tag              text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (lead_id, tag)
);
CREATE INDEX lead_tags_org_tag_idx ON lead_tags(organization_id, tag);

-- ───────────────────────── Compliance ─────────────────────────

CREATE TABLE suppression_list (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email            citext NOT NULL,
    reason           text NOT NULL CHECK (reason IN ('unsubscribed','hard_bounce','complaint','manual','erased')),
    source           text NOT NULL DEFAULT '',
    campaign_id      uuid,
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, email)
);

CREATE TABLE audit_logs (
    id               bigserial PRIMARY KEY,
    organization_id  uuid REFERENCES organizations(id) ON DELETE CASCADE,
    user_id          uuid REFERENCES users(id) ON DELETE SET NULL,
    action           text NOT NULL,
    entity_type      text NOT NULL DEFAULT '',
    entity_id        text NOT NULL DEFAULT '',
    metadata         jsonb NOT NULL DEFAULT '{}',
    ip               text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_org_idx ON audit_logs(organization_id, created_at DESC);

-- ───────────────────────── Campaigns ─────────────────────────

CREATE TABLE campaigns (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name                    text NOT NULL,
    status                  text NOT NULL DEFAULT 'draft' CHECK (status IN
                            ('draft','scheduled','running','paused','completed','cancelled')),
    mailbox_id              uuid REFERENCES mailboxes(id) ON DELETE SET NULL,
    daily_limit             int NOT NULL DEFAULT 50 CHECK (daily_limit >= 0),
    start_at                timestamptz,
    timezone                text NOT NULL DEFAULT 'UTC',
    send_window_start       time NOT NULL DEFAULT '09:00',
    send_window_end         time NOT NULL DEFAULT '17:00',
    send_days               int[] NOT NULL DEFAULT '{1,2,3,4,5}',  -- ISO weekday, 1 = Monday
    allowed_email_statuses  text[] NOT NULL DEFAULT '{valid}',
    max_bounce_rate         numeric(5,4) NOT NULL DEFAULT 0.05,
    stop_on_reply           boolean NOT NULL DEFAULT true,
    paused_reason           text,
    created_by              uuid REFERENCES users(id) ON DELETE SET NULL,
    launched_at             timestamptz,
    completed_at            timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX campaigns_org_idx ON campaigns(organization_id, created_at DESC);
CREATE INDEX campaigns_active_idx ON campaigns(status) WHERE status IN ('scheduled','running');

CREATE TABLE campaign_steps (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id       uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    step_number       int NOT NULL CHECK (step_number >= 1),
    delay_days        int NOT NULL DEFAULT 0 CHECK (delay_days >= 0),
    subject_template  text NOT NULL DEFAULT '',  -- empty on follow-ups = reply in thread
    body_template     text NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, step_number)
);

CREATE TABLE campaign_leads (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id       uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    lead_id           uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    contact_id        uuid REFERENCES lead_contacts(id) ON DELETE SET NULL,
    email             citext NOT NULL,
    status            text NOT NULL DEFAULT 'pending' CHECK (status IN
                      ('pending','active','sending','completed','replied','bounced',
                       'unsubscribed','suppressed','failed','stopped')),
    next_step_number  int NOT NULL DEFAULT 1,
    next_send_at      timestamptz,
    last_sent_at      timestamptz,
    stop_reason       text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, lead_id)
);
CREATE INDEX campaign_leads_due_idx   ON campaign_leads(campaign_id, next_send_at) WHERE status = 'active';
CREATE INDEX campaign_leads_email_idx ON campaign_leads(organization_id, email);
CREATE INDEX campaign_leads_lead_idx  ON campaign_leads(lead_id);

-- One row per outbound email. Content is rendered and stored before sending.
CREATE TABLE email_messages (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id          uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    campaign_lead_id     uuid NOT NULL REFERENCES campaign_leads(id) ON DELETE CASCADE,
    lead_id              uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    contact_id           uuid REFERENCES lead_contacts(id) ON DELETE SET NULL,
    step_id              uuid NOT NULL REFERENCES campaign_steps(id) ON DELETE CASCADE,
    step_number          int NOT NULL,
    mailbox_id           uuid REFERENCES mailboxes(id) ON DELETE SET NULL,
    to_email             citext NOT NULL,
    from_email           text NOT NULL,
    from_name            text NOT NULL,
    reply_to             text NOT NULL DEFAULT '',
    subject              text NOT NULL,
    body_text            text NOT NULL,
    body_html            text NOT NULL,
    message_id           text NOT NULL UNIQUE,      -- RFC 5322 Message-ID we generate
    in_reply_to          text NOT NULL DEFAULT '',
    provider             text NOT NULL DEFAULT '',
    provider_message_id  text,
    status               text NOT NULL DEFAULT 'queued'
                         CHECK (status IN ('queued','sending','sent','failed','cancelled')),
    attempts             int NOT NULL DEFAULT 0,
    next_attempt_at      timestamptz NOT NULL DEFAULT now(),
    last_error           text,
    sent_at              timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
-- Idempotency: a campaign step is sent to a lead at most once. Cancelled rows
-- (e.g. campaign paused before send) release the key so the step can be retried.
CREATE UNIQUE INDEX email_messages_idem_idx ON email_messages(campaign_id, lead_id, step_id) WHERE status <> 'cancelled';
CREATE INDEX email_messages_org_created_idx      ON email_messages(organization_id, created_at);
CREATE INDEX email_messages_mailbox_created_idx  ON email_messages(mailbox_id, created_at);
CREATE INDEX email_messages_campaign_created_idx ON email_messages(campaign_id, created_at);
CREATE INDEX email_messages_status_idx           ON email_messages(status, updated_at) WHERE status IN ('queued','sending');
CREATE INDEX email_messages_provider_id_idx      ON email_messages(provider_message_id) WHERE provider_message_id IS NOT NULL;

CREATE TABLE email_events (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id        uuid REFERENCES campaigns(id) ON DELETE CASCADE,
    lead_id            uuid REFERENCES leads(id) ON DELETE CASCADE,
    email_message_id   uuid REFERENCES email_messages(id) ON DELETE CASCADE,
    message_id         text NOT NULL DEFAULT '',
    event_type         text NOT NULL CHECK (event_type IN
                       ('queued','sent','delivered','bounced','complained','unsubscribed','replied','failed')),
    provider_event_id  text,
    metadata           jsonb NOT NULL DEFAULT '{}',
    occurred_at        timestamptz NOT NULL DEFAULT now(),
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX email_events_provider_idx ON email_events(organization_id, provider_event_id) WHERE provider_event_id IS NOT NULL;
CREATE INDEX email_events_campaign_idx ON email_events(campaign_id, event_type);
CREATE INDEX email_events_org_idx      ON email_events(organization_id, occurred_at);
CREATE INDEX email_events_lead_idx     ON email_events(lead_id, occurred_at DESC);
