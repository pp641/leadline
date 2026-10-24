-- Phase 2 & 3: discovery, enrichment, AI personalization, inbound replies,
-- CRM, multi-mailbox, integrations, API keys, webhooks, ICPs, agency mode.

-- ───────────────────────── Agency / multi-client ─────────────────────────
ALTER TABLE organizations
    ADD COLUMN parent_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
    ADD COLUMN is_agency boolean NOT NULL DEFAULT false;
CREATE INDEX organizations_parent_idx ON organizations(parent_id) WHERE parent_id IS NOT NULL;

-- ───────────────────────── Leads: CRM + enrichment fields ─────────────────
ALTER TABLE leads
    ADD COLUMN owner_id uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN website_quality text NOT NULL DEFAULT '' CHECK (website_quality IN ('','none','poor','ok','good')),
    ADD COLUMN tech_stack text[] NOT NULL DEFAULT '{}',
    ADD COLUMN icp_fit int,
    ADD COLUMN enriched_at timestamptz,
    ADD COLUMN discovery_run_id uuid;
CREATE INDEX leads_org_owner_idx ON leads(organization_id, owner_id);

CREATE TABLE lead_notes (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id          uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    author_id        uuid REFERENCES users(id) ON DELETE SET NULL,
    body             text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX lead_notes_lead_idx ON lead_notes(lead_id, created_at DESC);

CREATE TABLE tasks (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id          uuid REFERENCES leads(id) ON DELETE CASCADE,
    assignee_id      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    title            text NOT NULL,
    notes            text NOT NULL DEFAULT '',
    due_at           timestamptz,
    completed_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tasks_org_open_idx ON tasks(organization_id, assignee_id, due_at) WHERE completed_at IS NULL;
CREATE INDEX tasks_lead_idx ON tasks(lead_id);

-- ───────────────────────── Integrations (encrypted credentials) ───────────
CREATE TABLE integrations (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('apollo','google_places','hunter','hubspot')),
    config           jsonb NOT NULL DEFAULT '{}',   -- non-secret settings
    secret           bytea,                          -- AES-GCM sealed API key / token
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','error')),
    last_error       text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, kind)
);

-- ───────────────────────── Mailboxes: own credentials, inbox sync ─────────
ALTER TABLE mailboxes
    ADD COLUMN smtp_host            text NOT NULL DEFAULT '',
    ADD COLUMN smtp_port            int  NOT NULL DEFAULT 587,
    ADD COLUMN smtp_username        text NOT NULL DEFAULT '',
    ADD COLUMN smtp_password_enc    bytea,
    ADD COLUMN smtp_tls             text NOT NULL DEFAULT 'starttls' CHECK (smtp_tls IN ('none','starttls','tls')),
    ADD COLUMN imap_host            text NOT NULL DEFAULT '',
    ADD COLUMN imap_port            int  NOT NULL DEFAULT 993,
    ADD COLUMN imap_username        text NOT NULL DEFAULT '',
    ADD COLUMN imap_password_enc    bytea,
    ADD COLUMN imap_tls             boolean NOT NULL DEFAULT true,
    ADD COLUMN inbox_sync_enabled   boolean NOT NULL DEFAULT false,
    ADD COLUMN imap_uidvalidity     bigint NOT NULL DEFAULT 0,
    ADD COLUMN imap_last_uid        bigint NOT NULL DEFAULT 0,
    ADD COLUMN last_synced_at       timestamptz,
    ADD COLUMN sync_error           text;

-- Campaigns can rotate across several mailboxes; each lead sticks to one so
-- follow-ups thread correctly.
CREATE TABLE campaign_mailboxes (
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id      uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    mailbox_id       uuid NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    PRIMARY KEY (campaign_id, mailbox_id)
);
INSERT INTO campaign_mailboxes (organization_id, campaign_id, mailbox_id)
    SELECT organization_id, id, mailbox_id FROM campaigns WHERE mailbox_id IS NOT NULL;
ALTER TABLE campaign_leads ADD COLUMN mailbox_id uuid REFERENCES mailboxes(id) ON DELETE SET NULL;

-- ───────────────────────── AI personalization ─────────────────────────────
ALTER TABLE campaigns
    ADD COLUMN ai_personalization                boolean NOT NULL DEFAULT false,
    ADD COLUMN personalization_brief             text NOT NULL DEFAULT '',
    ADD COLUMN require_personalization_approval  boolean NOT NULL DEFAULT true;

CREATE TABLE lead_personalizations (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id      uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    lead_id          uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    content          text NOT NULL DEFAULT '',
    status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','generated','approved','rejected','failed')),
    model            text NOT NULL DEFAULT '',
    error            text,
    reviewed_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, lead_id)
);
CREATE INDEX lead_personalizations_status_idx ON lead_personalizations(campaign_id, status);

-- ───────────────────────── Inbound replies ────────────────────────────────
CREATE TABLE inbound_emails (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    mailbox_id        uuid REFERENCES mailboxes(id) ON DELETE SET NULL,
    campaign_id       uuid REFERENCES campaigns(id) ON DELETE SET NULL,
    lead_id           uuid REFERENCES leads(id) ON DELETE CASCADE,
    campaign_lead_id  uuid REFERENCES campaign_leads(id) ON DELETE SET NULL,
    email_message_id  uuid REFERENCES email_messages(id) ON DELETE SET NULL,
    message_id        text NOT NULL,
    in_reply_to       text NOT NULL DEFAULT '',
    from_email        text NOT NULL,
    from_name         text NOT NULL DEFAULT '',
    subject           text NOT NULL DEFAULT '',
    body_text         text NOT NULL DEFAULT '',
    received_at       timestamptz NOT NULL DEFAULT now(),
    kind              text NOT NULL DEFAULT 'reply' CHECK (kind IN ('reply','auto_reply','bounce')),
    classification    text,
    confidence        real,
    summary           text NOT NULL DEFAULT '',
    classified_by     text NOT NULL DEFAULT '' CHECK (classified_by IN ('','ai','rules','manual')),
    status            text NOT NULL DEFAULT 'new' CHECK (status IN ('new','handled','archived')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, message_id)
);
CREATE INDEX inbound_emails_org_idx ON inbound_emails(organization_id, received_at DESC);
CREATE INDEX inbound_emails_lead_idx ON inbound_emails(lead_id);

-- ───────────────────────── ICPs & discovery ───────────────────────────────
CREATE TABLE icps (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             text NOT NULL,
    criteria         jsonb NOT NULL DEFAULT '{}',
    is_default       boolean NOT NULL DEFAULT false,
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX icps_one_default_idx ON icps(organization_id) WHERE is_default;

CREATE TABLE discovery_runs (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    icp_id           uuid REFERENCES icps(id) ON DELETE SET NULL,
    provider         text NOT NULL,
    query            jsonb NOT NULL DEFAULT '{}',
    status           text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','completed','failed')),
    results_count    int NOT NULL DEFAULT 0,
    imported_count   int NOT NULL DEFAULT 0,
    error            text,
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    completed_at     timestamptz
);
CREATE INDEX discovery_runs_org_idx ON discovery_runs(organization_id, created_at DESC);

CREATE TABLE discovery_results (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    run_id            uuid NOT NULL REFERENCES discovery_runs(id) ON DELETE CASCADE,
    data              jsonb NOT NULL,
    duplicate_lead_id uuid REFERENCES leads(id) ON DELETE SET NULL,
    imported_lead_id  uuid REFERENCES leads(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX discovery_results_run_idx ON discovery_results(run_id);

-- ───────────────────────── API keys ───────────────────────────────────────
CREATE TABLE api_keys (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             text NOT NULL,
    prefix           text NOT NULL,
    key_hash         bytea NOT NULL UNIQUE,
    role             text NOT NULL CHECK (role IN ('admin','member','viewer')),
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    last_used_at     timestamptz,
    revoked_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX api_keys_org_idx ON api_keys(organization_id);

-- ───────────────────────── Outbound webhooks (transactional outbox) ───────
CREATE TABLE webhook_endpoints (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    url              text NOT NULL,
    description      text NOT NULL DEFAULT '',
    events           text[] NOT NULL,
    secret_enc       bytea NOT NULL,
    active           boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE webhook_deliveries (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    endpoint_id      uuid NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    event_type       text NOT NULL,
    payload          jsonb NOT NULL,
    status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','succeeded','failed')),
    attempts         int NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    response_status  int,
    last_error       text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    delivered_at     timestamptz
);
CREATE INDEX webhook_deliveries_due_idx ON webhook_deliveries(next_attempt_at) WHERE status = 'pending';
CREATE INDEX webhook_deliveries_endpoint_idx ON webhook_deliveries(endpoint_id, created_at DESC);

-- ───────────────────────── CRM sync links ─────────────────────────────────
CREATE TABLE crm_links (
    organization_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id              uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    provider             text NOT NULL,
    external_company_id  text,
    external_contact_id  text,
    synced_at            timestamptz NOT NULL DEFAULT now(),
    last_error           text,
    PRIMARY KEY (lead_id, provider)
);

-- Lead sources may now come from discovery providers.
ALTER TABLE lead_sources ADD COLUMN discovery_run_id uuid REFERENCES discovery_runs(id) ON DELETE SET NULL;
