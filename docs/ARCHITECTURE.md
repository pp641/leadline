# Architecture notes

## Principles
- **PostgreSQL is the source of truth.** Redis/Valkey only carries job envelopes, locks and rate-limit counters. Losing Redis loses no business data, and the scheduler's recovery sweep re-enqueues any lost send jobs.
- **Provider-agnostic.** Business logic depends only on these interfaces: `email.EmailProvider`, `email.WebhookParser`, `verification.EmailVerifier`, `catalog.Source`, `integrations.LLMProvider`, `replies.Classifier`, `replies.MailboxReader` and `storage.ObjectStore`. Concrete providers: SMTP and log (email); the company-scraper PostgreSQL database, read-only (companies); Claude (LLM); HubSpot (CRM). Each organization's credentials live in `integrations`, encrypted by `vault`.
- **Untrusted input is fenced.** User-supplied hosts go through `pkg/safehttp` (SSRF guard at dial time). Text sent to the LLM is delimited as data, outputs are schema-constrained (structured outputs) and length-checked, and opt-outs never depend on the model.
- **Handler → Service → Repository.** Handlers decode, call one service method and encode. Services validate, and they own transactions and side effects. Errors are typed (`pkg/apperr`), and only `httpx.Error` maps them to status codes.
- **Tenant isolation by construction.** `auth.Middleware.Org` resolves the caller's membership from `X-Organization-ID` and puts the org in an `auth.Principal`. Every service takes `Principal.OrgID`, and every repository query filters on `organization_id`. `leads.Filter.Where` always emits the tenant condition as `$1`. `tests/TestTenantIsolationAndRoles` exercises this.

## Data model (Milestone 1)

```
users ─< organization_members >─ organizations ─< mailboxes
                                      │
           ┌──────────────────────────┼──────────────────────────────┐
         leads ─< lead_contacts     imports                     campaigns ─< campaign_steps
           │  ─< lead_sources (provenance, lawful basis)            │
           │  ─< lead_events (timeline)  ─< lead_tags               ├─< campaign_leads (per-lead sequence state)
           │  ─  lead_scores (current score + reasons + version)    └─< email_messages (rendered, idempotent)
           │  ─< lead_enrichment (Phase 2)                                 └─< email_events
         suppression_list (org-wide do-not-contact)     audit_logs     sessions
```

`campaign_leads.status`: `pending → active ⇄ sending → completed`, or terminal `replied | bounced | unsubscribed | suppressed | failed | stopped`.
`email_messages.status`: `queued → sending → sent | failed`, plus `cancelled` (pause/suppression), which releases the idempotency key.

Phase 2–3 tables (migration 0002):
- `icps`: profiles.
- `lead_enrichment`: one row per lead and provider (`company_db` for companies imported from company-scraper; `website`/`hunter` rows predate migration 0003).
- `leads.catalog_company_id` (migration 0003): the company-scraper company a lead came from. Migration 0003 dropped `discovery_runs` and `discovery_results`.
- `lead_personalizations`: AI openers with a review status, one per campaign and lead.
- `inbound_emails`: matched replies, auto-replies and bounces, with their classification.
- `lead_notes` and `tasks`: CRM.
- `campaign_mailboxes`: the mailbox pool; `campaign_leads.mailbox_id` pins a lead to one mailbox so follow-ups thread.
- `integrations`: encrypted provider credentials.
- `api_keys`: hashed keys.
- `webhook_endpoints` and `webhook_deliveries`: the outbox.
- `crm_links`: HubSpot IDs.
- `organizations.parent_id` and `is_agency`: agency mode.

## Jobs

| Queue | Job | Idempotency |
|---|---|---|
| default | `import.process` | Already-imported rows are detected as duplicates on retry |
| default | `verification.contacts` | Re-verifying is harmless |
| default | `scoring.leads`, `scoring.rescore_org` | Pure recomputation |
| email | `email.send` | Atomic `queued → sending` claim |
| default | `ai.personalize` | Only `pending` rows are written |
| default | `inbox.sync` | Per-mailbox Redis lock; UID cursor advances only past processed mail; `(org, message_id)` unique |
| default | `crm.sync` | Upsert by stored HubSpot IDs / contact email |

Worker loops besides the queue:
- campaign scheduler, every 30s;
- webhook dispatcher, every 3s, using `SKIP LOCKED` row leases;
- inbox poller, every 2 min;
- HubSpot auto-sync, every 5 min.

Retries use exponential backoff (15s × 2ⁿ ± 20%, capped at 1h) and then go to a per-queue dead-letter list (`q:<queue>:dead`). Handlers get a 2-minute timeout. On SIGTERM, workers stop fetching and finish their in-flight jobs.

## Scaling
- API and worker are stateless, so run as many as needed (`WORKER_REPLICAS`). Only one scheduler tick runs at a time (Redis lock), so limit accounting stays exact. The tick does little work: render and insert about `perTick` rows per running campaign.
- Indexes cover tenant-scoped listing, dedupe lookups, due campaign leads (`campaign_leads_due_idx`) and rolling-24h limit counts.
- For much larger orgs: keyset pagination for exports, materialized daily analytics, and partitioning `email_events` by month.
