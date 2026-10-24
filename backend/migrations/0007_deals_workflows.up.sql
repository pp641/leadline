-- Deals pipeline: configurable pipelines/stages, deals with value and close
-- date, and a per-deal timeline. Workflows: "when → if → then" automation
-- rules with a run history (one run per workflow per event).
CREATE TABLE pipelines (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             text NOT NULL,
    is_default       boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pipelines_org_idx ON pipelines(organization_id);

CREATE TABLE pipeline_stages (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    pipeline_id      uuid NOT NULL REFERENCES pipelines(id) ON DELETE CASCADE,
    name             text NOT NULL,
    position         int NOT NULL,
    probability      int NOT NULL DEFAULT 0 CHECK (probability BETWEEN 0 AND 100),
    kind             text NOT NULL DEFAULT 'open' CHECK (kind IN ('open','won','lost')),
    stale_days       int CHECK (stale_days IS NULL OR stale_days > 0)
);
CREATE INDEX pipeline_stages_pipeline_idx ON pipeline_stages(pipeline_id, position);

CREATE TABLE deals (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    pipeline_id       uuid NOT NULL REFERENCES pipelines(id) ON DELETE CASCADE,
    stage_id          uuid NOT NULL REFERENCES pipeline_stages(id),
    lead_id           uuid REFERENCES leads(id) ON DELETE SET NULL,
    name              text NOT NULL,
    value             numeric(14,2) NOT NULL DEFAULT 0 CHECK (value >= 0),
    currency          text NOT NULL DEFAULT 'INR',
    expected_close    date,
    owner_id          uuid REFERENCES users(id) ON DELETE SET NULL,
    status            text NOT NULL DEFAULT 'open' CHECK (status IN ('open','won','lost')),
    lost_reason       text NOT NULL DEFAULT '',
    next_step         text NOT NULL DEFAULT '',
    next_step_at      timestamptz,
    source            text NOT NULL DEFAULT 'manual',
    stage_entered_at  timestamptz NOT NULL DEFAULT now(),
    last_activity_at  timestamptz NOT NULL DEFAULT now(),
    closed_at         timestamptz,
    created_by        uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX deals_board_idx ON deals(organization_id, pipeline_id, stage_id, updated_at DESC);
CREATE INDEX deals_lead_idx ON deals(lead_id) WHERE lead_id IS NOT NULL;

CREATE TABLE deal_events (
    id               bigserial PRIMARY KEY,
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    deal_id          uuid NOT NULL REFERENCES deals(id) ON DELETE CASCADE,
    event_type       text NOT NULL,
    actor_id         uuid REFERENCES users(id) ON DELETE SET NULL,
    data             jsonb NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX deal_events_deal_idx ON deal_events(deal_id, created_at DESC);

CREATE TABLE workflows (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             text NOT NULL,
    description      text NOT NULL DEFAULT '',
    enabled          boolean NOT NULL DEFAULT true,
    trigger          text NOT NULL,
    trigger_config   jsonb NOT NULL DEFAULT '{}',
    conditions       jsonb NOT NULL DEFAULT '[]',
    actions          jsonb NOT NULL DEFAULT '[]',
    template         text NOT NULL DEFAULT '',
    run_count        int NOT NULL DEFAULT 0,
    last_run_at      timestamptz,
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workflows_trigger_idx ON workflows(organization_id, trigger) WHERE enabled;

CREATE TABLE workflow_runs (
    id               bigserial PRIMARY KEY,
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    workflow_id      uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    event_key        text NOT NULL,
    trigger          text NOT NULL,
    lead_id          uuid REFERENCES leads(id) ON DELETE SET NULL,
    deal_id          uuid REFERENCES deals(id) ON DELETE SET NULL,
    subject          text NOT NULL DEFAULT '',
    status           text NOT NULL CHECK (status IN ('succeeded','partial','failed','skipped')),
    results          jsonb NOT NULL DEFAULT '[]',
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workflow_id, event_key)
);
CREATE INDEX workflow_runs_wf_idx ON workflow_runs(workflow_id, created_at DESC);
