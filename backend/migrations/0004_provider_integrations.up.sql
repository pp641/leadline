-- Integrations are now a catalog of data providers, CRMs and email services
-- (see internal/integrations/catalog.go); the kind is validated in Go.
ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_kind_check;
