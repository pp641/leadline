-- Custom analytics dashboards: an ordered widget layout plus step-by-step
-- filters, shared by everyone in the organization.
CREATE TABLE dashboards (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             text NOT NULL,
    widgets          jsonb NOT NULL DEFAULT '[]',
    steps            jsonb NOT NULL DEFAULT '[]',
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX dashboards_org_idx ON dashboards(organization_id, updated_at DESC);
