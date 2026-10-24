# Leadline — Lead Intelligence & Compliant Outbound

A B2B platform for finding and qualifying leads, running gradual, compliant email sequences, handling replies, and working interested prospects through to a deal.

```
ICP → Company database (company-scraper: discovered, website-checked, verified) or CSV
    → Dedupe → Email verification → Lead scoring + ICP fit → AI personalization (reviewed)
    → Campaign (multi-mailbox) → Sending → Replies & bounces (IMAP) → AI classification → CRM → HubSpot
```

| Layer | Stack |
|---|---|
| Frontend | Next.js 16 (App Router), React 19, TypeScript, Tailwind v4, shadcn/ui, TanStack Query, React Hook Form + Zod, Recharts |
| Backend | Go 1.24, `net/http` (1.22+ routing), pgx, go-redis, Anthropic Go SDK, go-imap, slog, Prometheus |
| Data | PostgreSQL 17 (source of truth), Valkey/Redis (queues, locks, rate limits) |
| Infra | Docker Compose; Mailpit catches email locally |

All three phases of the build spec are implemented. The table below lists each feature and how it is verified.

---

## Quick start

Requires only Docker.

```bash
make up          # creates .env with random secrets, builds and starts everything
make seed        # demo user, org, mailbox, 200 leads, a default ICP, a 3-step draft campaign
```

| | URL |
|---|---|
| App | http://localhost:3000 (sign in as `demo@leadline.test` / `demo-password-123`) |
| API | http://localhost:8080 (also proxied at `/api` on the app origin) |
| Mailpit (every email sent locally) | http://localhost:8025 |
| Metrics | http://localhost:8080/metrics, worker on `:9090/metrics` |

If ports 3000, 8080 or 8025 are taken, set `FRONTEND_PORT`, `API_PORT` and `MAILPIT_PORT` in `.env` and keep `APP_URL` in sync.

Without `make`: run `cp .env.example .env`, fill in `JWT_SECRET`, `SESSION_SECRET` (≥ 32 chars) and `ENCRYPTION_KEY` (`openssl rand -base64 32`), then run `docker compose up -d --build`.

**To try it:** sign in, open Campaigns, open the seeded campaign and click **Launch**. Emails arrive in Mailpit at a pace set by the daily limit. Open the **ICP Builder** to see every lead's fit score. To test a large import, run `make sample-csv` (10,000 synthetic leads) and upload the file under Leads → Import.

### Enabling the optional features

| Feature | How to enable |
|---|---|
| AI openers and AI reply classification | `LLM_PROVIDER=anthropic`, `LLM_API_KEY=…` in `.env` (default model `claude-opus-5-5`; override with `LLM_MODEL`). Without a key, openers are off and replies are classified by rules. |
| Finding new companies | Run the separate **company-scraper** project (`../company-scraper`, `make up`). It collects companies (OpenStreetMap, or a list of websites), analyses their sites and verifies their email, phone and website. Set `SCRAPER_DATABASE_URL` to its read-only `leadline_reader` role; then ICP Builder → **Find new leads** searches that database and imports the companies you pick. Both stacks share the `company-data` Docker network, which `make up` creates. |
| CRM sync | Integrations → **HubSpot** with a private-app token (scopes: companies and contacts read/write). Turn on auto-sync to push interested leads. |
| Reply and bounce detection | Settings → Mailboxes → Edit → **Inbox sync (IMAP)**. Use an app password for Gmail or Microsoft 365. |
| Sending through a mailbox's own server | Settings → Mailboxes → Edit → **Own SMTP server** |
| Agency / multi-client mode | Settings → Agency (owner) |
| Programmatic access | Integrations → **API keys** and **Webhooks** |

Integration keys, mailbox passwords and webhook secrets are encrypted with `ENCRYPTION_KEY` (AES-256-GCM). If you change that key, the stored credentials become unreadable.

> Compose services read `.env` through `env_file`, so variables exported in your shell (for example another project's `SMTP_HOST`) cannot silently override them.

## Demo mode (no backend)

Set `MOCK_MODE=true` in `.env` and rebuild the frontend (`docker compose up -d --build frontend`), or run
`NEXT_PUBLIC_MOCK_MODE=true npm run dev` in `frontend/`. The whole UI then runs on an in-browser mock backend
(`frontend/lib/mock`) with realistic sample data — sign-up and email verification, onboarding, leads, CSV import,
ICP and company-database search, integrations and provider imports, campaigns, inbox, CRM, analytics and dashboards.
Nothing calls the Go API or the database; changes are kept in the browser's localStorage, and the floating
**Demo mode** badge has a **Reset** button. Sign in with `demo@leadline.test` / `demo-password-123`, or sign up —
verification codes and invitation links appear on screen instead of being emailed.

Set `MOCK_MODE=false` (the default) and rebuild to use the real API and PostgreSQL again.

## What's included

| Area | Highlights | Verified by |
|---|---|---|
| **Auth & tenancy** | Email/password, 15-min JWT plus rotating refresh cookie with reuse detection, roles owner/admin/member/viewer, every query scoped by organization | `TestAuthSessions`, `TestTenantIsolationAndRoles` |
| **Leads** | CRUD, filters, bulk actions, tags, timeline, CSV export; CSV import of up to 50k rows with mapping, validation, dedupe and preview (10k rows in about 6s end to end) | `TestMilestoneOneFlow`, `TestLargeImport` |
| **ICP engine** | Criteria for industry, location, size, opened-after year, website quality, tech stack, keywords, buying signals; produces an explainable 0–100 fit for every lead and doubles as a company-database search | `TestWebhooksCatalogCRMandICP`, `icp` unit tests |
| **Company database** | Read-only search of the company-scraper database by ICP (industry, location, keywords, website quality, verified only); duplicates flagged; import copies provenance, consent basis, website signals and the verification report, and drops emails the scraper found undeliverable. Discovery, crawling and company verification live in company-scraper. | `TestCatalogImport`, `catalog` unit tests |
| **Verification** | Contact emails: syntax, disposable domains, role addresses, MX lookup | `verification` unit tests |
| **Scoring** | Configurable versioned rules including `website_quality`, `tech_stack` and `icp_fit`; **suggestions learned from outcomes** | `scoring` unit tests |
| **Campaigns** | Sequences with threaded follow-ups, sending windows, campaign, mailbox and org daily limits, pacing, bounce auto-pause, **rotation across mailboxes** (each lead stays on one) | `TestMilestoneOneFlow`, `TestMailboxRotationAndPersonalization` |
| **AI personalization** | One fact-grounded opener per lead (Claude, structured output, prompt-injection-safe) as `{{personalized_line}}`; human review by default; generated ahead of send time | `TestMailboxRotationAndPersonalization` |
| **Sending** | Background workers, at-most-once per step, retries with backoff, dead-letter queue, per-mailbox SMTP credentials, provider auth failure pauses the campaign | `TestProviderFaultPausesCampaign` |
| **Replies** | IMAP sync (read-only) or inbound webhook; matching by thread headers or sender; auto-reply and bounce (DSN) detection; unrelated mail never stored | `TestInboundReplyPipeline`, `inbox` unit tests |
| **Reply classification** | 10 labels; opt-outs always decided by rules; AI for the rest; manual reclassification | same |
| **CRM** | Owners, notes, tasks, pipeline board, timeline; HubSpot company and contact sync (manual or automatic) | `TestWebhooksCatalogCRMandICP` |
| **Deals pipeline** | Configurable pipelines and stages (probability, "stuck after" limit), drag-and-drop board with value and weighted forecast, won/lost with loss reasons, per-deal timeline, `deal.*` webhooks | `TestDealsAndWorkflows` |
| **Workflows** | When → if → then automation: triggers on classified replies, deal events and stuck or inactive deals; conditions on lead and deal fields; actions create deals, tasks, notes and tags, assign owners round-robin, move deals, stop sequences, notify by email and sync to CRM; templates, dry-run test and run history | `TestDealsAndWorkflows`, `TestInboundReplyPipeline` |
| **Analytics** | KPIs, daily series, per campaign and per step; advanced breakdowns by reply class, mailbox, segment and time to reply | integration tests |
| **API & webhooks** | Org-scoped API keys (`ll_…`); signed outbound webhooks with a retrying outbox and a delivery log | `TestAPIKeys`, `TestWebhooksCatalogCRMandICP` |
| **Agency mode** | Client organizations under an agency; agency members inherit access; per-client rollups | `TestAgencyMode` |
| **Compliance** | Unsubscribe link and RFC 8058 one-click header, suppression list, bounce and complaint suppression, required postal address, consent basis, audit log, erasure, export | multiple |

## Repository layout

```
backend/
  cmd/server  cmd/worker  cmd/seed
  config/  migrations/ (0001 Milestone 1, 0002 Phase 2–3; applied on startup)
  internal/
    auth organization users mailboxes apikeys agency      identity, tenancy, RBAC, API keys, agencies
    leads contacts imports scoring icp                     lead data, ICP fit, scoring
    catalog                                                company database (company-scraper) search + import
    verification                                           EmailVerifier
    campaigns  personalization                             sequences, scheduler, delivery, AI openers
    email  inbox  replies  suppression                     transport, MIME, events, IMAP, classifiers
    crm (+ sync.go)  integrations                          notes/tasks/pipeline, HubSpot, LLM, registry
    webhooks  analytics  audit  jobs  workers  app  vault
  pkg/  apperr httpx database queue redisx storage secure safehttp logger metrics validate
  tests/  end-to-end tests (real Postgres + Valkey, fake external APIs)
frontend/
  app/(app)/  dashboard leads icp campaigns inbox crm analytics integrations settings
  components/ services/ lib/ hooks/ types/
docs/openapi.yaml  (all 115 endpoints)    docs/ARCHITECTURE.md
```

## How it works

**Sending.** One scheduler at a time (held by a Redis lock) does the following for each running campaign:
- checks the sending window;
- computes a budget from the campaign, org and mailbox-pool limits, paced across the day;
- pre-generates AI openers;
- waits for reviewed openers;
- picks each lead's mailbox;
- renders the email;
- inserts it under a unique `(campaign, lead, step)` key, so a step can never be sent twice;
- enqueues it.

Send workers claim messages atomically (`queued → sending`), check suppression and campaign state again, then send through the mailbox's own SMTP server or the default provider. If a worker crashes mid-send, the message is marked failed rather than resent.

**Replies.** Each mailbox's inbox is synced every 2 minutes, read-only. A message is stored only if it answers a campaign email, matched by `In-Reply-To`/`References`, by a Message-ID quoted in a bounce report, or by sender address. Then:
- **Bounces** suppress the address, unless they are soft (4.x.x).
- **Auto-replies** are logged and the sequence continues.
- **Replies** are classified: opt-outs by fixed rules, everything else by Claude when configured. Replies stop the sequence. Unsubscribe and spam replies also suppress the address. Positive replies move the lead to *interested*, which triggers HubSpot auto-sync when it is on.

**Workflows.** Classified replies and deal changes queue a `workflow.event` job after they commit; the worker also scans every 10 minutes for stuck and inactive deals. Each workflow runs at most once per event (a unique `(workflow, event)` key), so retries and rescans never repeat an action. A workflow never re-triggers itself, and cascades stop after 3 levels.

**Webhooks** use a transactional outbox: an event row is written in the same transaction as the change, so an event exists if and only if the change committed. A dispatcher signs each delivery (`X-Leadline-Signature: t=…,v1=hex(HMAC-SHA256(secret, "t.body"))`) and retries with backoff, up to 8 attempts.

## Security & compliance

- **Passwords and sessions:** bcrypt password hashing; JWT access tokens plus rotating refresh tokens with reuse detection; CSRF header required on cookie endpoints.
- **Access control:** every query is scoped to the caller's organization, and API keys are bound to one organization and role. Agency members get access to client organizations only through their agency membership.
- **Secrets:** integration keys, mailbox passwords and webhook secrets are stored encrypted (AES-256-GCM); keys and webhook secrets are shown only once.
- **SSRF protection:** every user-supplied host (webhook URLs, mailbox SMTP/IMAP) passes a dial-time guard that blocks private and metadata addresses.
- **Untrusted text sent to the AI:** lead data and reply text are wrapped as data, and the model is told to ignore instructions inside them. Openers may use only stored facts, and humans review them by default.
- **Email compliance:** a postal address and unsubscribe link in every email, RFC 8058 one-click unsubscribe, a suppression list that the sender cannot override for unsubscribes, complaints or erasures, a consent basis recorded on every imported or discovered lead, an audit log, data export and erasure.
- **What it doesn't do:** no scraping that bypasses site terms or anti-bot measures, no spoofing, no filter evasion.
- **Data sources:** companies come from company-scraper, which uses open data (OpenStreetMap, ODbL; attribution is shown with results) and polite, robots.txt-respecting fetches of each company's own homepage and contact page. Leadline itself crawls nothing. Review your local email-marketing and data-protection law (CAN-SPAM, GDPR/PECR, India's DPDP Act and others) before going live.

## API

REST API under `/api/v1` with a `{data, error, meta}` envelope. The full spec is [`docs/openapi.yaml`](docs/openapi.yaml); `make check-openapi` fails if any registered route is missing from it.

```bash
# With an API key (Integrations → API keys): no organization header needed
curl -s localhost:8080/api/v1/leads?min_icp_fit=70 -H "Authorization: Bearer ll_xxxxxx_…"
```

Inbound integrations:
- `POST /api/v1/webhooks/email/generic`: delivery, bounce, complaint and reply events from your email service provider.
- `POST /api/v1/webhooks/inbound/generic`: raw inbound emails.

Both are signed with `X-Webhook-Signature: hex(HMAC-SHA256(body, WEBHOOK_SECRET))`.

## Development

```bash
make test-unit          # Go unit tests (in Docker)
make test-integration   # 11 end-to-end tests against throwaway Postgres + Valkey
make lint               # OpenAPI route check + go vet + tsc + eslint
make logs               # follow API + worker logs
```

For frontend hot reload: `cd frontend && npm install && API_INTERNAL_URL=http://localhost:8080 npm run dev`.

## Known limits

- **External APIs are tested against fakes only.** HubSpot and Claude are covered by contract tests against fake servers, so check each with a real key (Integrations → **Test**) before relying on it.
- **Inbox sync covers IMAP only.** The Gmail and Microsoft Graph APIs are not supported, so use app passwords. Sync fetches the last 7 days on first connect.
- **Daily limits are rolling 24-hour windows.** Delivery rate is (sent − hard bounces) ÷ sent unless your provider sends delivery receipts.
- **Analytics are computed live.** A rollup worker can replace this at large scale without changing the API.
- **Team members must register before they can be added.** There are no email invitations yet.
# leadline
