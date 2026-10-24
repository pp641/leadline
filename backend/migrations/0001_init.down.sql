-- Drops everything created by 0001_init.up.sql. Destroys all data.
DROP TABLE IF EXISTS email_events, email_messages, campaign_leads, campaign_steps, campaigns,
    audit_logs, suppression_list, lead_tags, lead_events, lead_sources, lead_scores, lead_enrichment,
    lead_contacts, leads, imports, mailboxes, organization_members, organizations, sessions, users CASCADE;
