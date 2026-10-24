-- Enterprise onboarding: verified email + terms acceptance on accounts,
-- one-time email verification codes, a resumable setup wizard per
-- organization, and email invitations to join an organization.
ALTER TABLE users
    ADD COLUMN email_verified_at timestamptz,
    ADD COLUMN terms_accepted_at timestamptz;
-- Accounts created before verification existed are trusted as-is.
UPDATE users SET email_verified_at = created_at WHERE email_verified_at IS NULL;

CREATE TABLE email_verifications (
    user_id     uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    code_hash   text NOT NULL,
    attempts    int NOT NULL DEFAULT 0,
    expires_at  timestamptz NOT NULL,
    sent_at     timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE organizations ADD COLUMN onboarding jsonb NOT NULL DEFAULT '{}';
-- Existing organizations are already set up.
UPDATE organizations SET onboarding = '{"completed": true}' WHERE onboarding = '{}';

CREATE TABLE invitations (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email            citext NOT NULL,
    role             text NOT NULL CHECK (role IN ('admin','member','viewer')),
    token_hash       text NOT NULL UNIQUE,
    invited_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL,
    accepted_at      timestamptz,
    revoked_at       timestamptz
);
CREATE UNIQUE INDEX invitations_pending_idx ON invitations(organization_id, email) WHERE accepted_at IS NULL AND revoked_at IS NULL;
