-- Company discovery, website enrichment and verification moved to the separate
-- company-scraper project. Leadline now searches that database (read-only) and
-- imports companies as leads, so its own discovery runs and the Apollo,
-- Google Places and Hunter integrations are removed. Leads already imported keep
-- their source, source_url and lead_sources provenance.

ALTER TABLE lead_sources DROP COLUMN discovery_run_id;
ALTER TABLE leads DROP COLUMN discovery_run_id;
DROP TABLE discovery_results;
DROP TABLE discovery_runs;

DELETE FROM integrations WHERE kind IN ('apollo', 'google_places', 'hunter');
ALTER TABLE integrations DROP CONSTRAINT integrations_kind_check;
ALTER TABLE integrations ADD CONSTRAINT integrations_kind_check CHECK (kind IN ('hubspot'));

-- The company-scraper company a lead was imported from (exact dedupe on re-import).
ALTER TABLE leads ADD COLUMN catalog_company_id uuid;
CREATE INDEX leads_org_catalog_company_idx ON leads(organization_id, catalog_company_id) WHERE catalog_company_id IS NOT NULL;
