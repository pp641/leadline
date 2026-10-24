// Types mirroring the Go API's JSON. Keep in sync with docs/openapi.yaml.

export type Role = "owner" | "admin" | "member" | "viewer";

export interface User {
  id: string;
  email: string;
  full_name: string;
  last_login_at: string | null;
  created_at: string;
  email_verified: boolean;
}

export interface Organization {
  id: string;
  name: string;
  slug: string;
  legal_name: string;
  physical_address: string;
  daily_send_limit: number;
  data_retention_days: number | null;
  role?: Role;
  created_at: string;
  parent_id: string | null;
  is_agency: boolean;
}

export interface Member {
  user_id: string;
  email: string;
  full_name: string;
  role: Role;
  created_at: string;
}

export type EmailStatus = "unverified" | "valid" | "invalid" | "risky" | "unknown" | "disposable" | "role_based";

export const LEAD_STATUSES = [
  "new", "contacted", "replied", "interested", "qualified", "meeting", "won", "lost", "unsubscribed",
] as const;
export type LeadStatus = (typeof LEAD_STATUSES)[number];

export interface Lead {
  id: string;
  company_name: string;
  website: string;
  domain: string;
  industry: string;
  description: string;
  country: string;
  state: string;
  city: string;
  address: string;
  phone: string;
  business_email: string | null;
  linkedin_url: string;
  social_urls: string[];
  employee_count: number | null;
  founded_year: number | null;
  source: string;
  source_url: string;
  lead_score: number;
  status: LeadStatus;
  owner_id: string | null;
  owner_name: string | null;
  website_quality: "" | "none" | "poor" | "ok" | "good";
  tech_stack: string[];
  icp_fit: number | null;
  enriched_at: string | null;
  tags: string[];
  primary_contact: { id: string; first_name: string; last_name: string; email: string | null; email_status: EmailStatus } | null;
  latest_campaign: string | null;
  created_at: string;
  updated_at: string;
}

export interface Contact {
  id: string;
  lead_id: string;
  first_name: string;
  last_name: string;
  job_title: string;
  email: string | null;
  email_status: EmailStatus;
  email_verified_at: string | null;
  verification_details: { reason?: string; provider?: string };
  linkedin_url: string;
  source: string;
  confidence: number;
  is_primary: boolean;
  suppressed: boolean;
}

export interface LeadDetail extends Lead {
  contacts: Contact[];
  score: { score: number; reasons: string[]; score_version: number };
  icp_fit_detail: { fit: number; matched: string[]; missed: string[] } | null;
}

export interface LeadEvent {
  id: number;
  event_type: string;
  actor_name: string | null;
  data: Record<string, unknown>;
  created_at: string;
}

export interface Facet {
  value: string;
  count: number;
}

export interface LeadFilter {
  q?: string;
  sources?: string[];
  industries?: string[];
  cities?: string[];
  statuses?: string[];
  email_statuses?: string[];
  tags?: string[];
  min_score?: number;
  has_website?: boolean;
  has_email?: boolean;
  not_in_campaign_id?: string;
  sort?: string;
  website_quality?: string[];
  unassigned?: boolean;
  owner_id?: string;
}

export interface ImportRow {
  row: number;
  values: Record<string, string>;
  errors?: string[];
  duplicate?: string;
}

export interface Import {
  id: string;
  filename: string;
  status: "uploaded" | "previewed" | "queued" | "processing" | "completed" | "failed";
  mapping: Record<string, string>;
  headers: string[];
  source: string;
  consent_basis: string;
  total_rows: number;
  valid_rows: number;
  invalid_rows: number;
  duplicate_rows: number;
  imported_rows: number;
  preview: { rows: ImportRow[]; problems: ImportRow[] } | null;
  error: string | null;
  created_at: string;
}

export interface Mailbox {
  id: string;
  name: string;
  from_email: string;
  from_name: string;
  reply_to: string | null;
  daily_limit: number;
  status: "active" | "disabled";
  sent_last_24h: number;
  smtp_host: string;
  smtp_port: number;
  smtp_username: string;
  smtp_tls: "none" | "starttls" | "tls";
  has_smtp_password: boolean;
  imap_host: string;
  imap_port: number;
  imap_username: string;
  imap_tls: boolean;
  has_imap_password: boolean;
  inbox_sync_enabled: boolean;
  last_synced_at: string | null;
  sync_error: string | null;
}

export type CampaignStatus = "draft" | "scheduled" | "running" | "paused" | "completed" | "cancelled";

export interface Step {
  id?: string;
  step_number?: number;
  delay_days: number;
  subject_template: string;
  body_template: string;
}

export interface Campaign {
  id: string;
  name: string;
  status: CampaignStatus;
  mailbox_id: string | null;
  daily_limit: number;
  start_at: string | null;
  timezone: string;
  send_window_start: string;
  send_window_end: string;
  send_days: number[];
  allowed_email_statuses: string[];
  max_bounce_rate: number;
  stop_on_reply: boolean;
  mailbox_ids: string[];
  ai_personalization: boolean;
  personalization_brief: string;
  require_personalization_approval: boolean;
  paused_reason: string | null;
  launched_at: string | null;
  completed_at: string | null;
  created_at: string;
  summary?: { leads: number; active: number; sent: number; replied: number; bounced: number };
  steps?: Step[];
}

export interface CampaignLead {
  id: string;
  lead_id: string;
  company_name: string;
  contact_name: string;
  email: string;
  email_status: EmailStatus;
  status: string;
  next_step_number: number;
  next_send_at: string | null;
  last_sent_at: string | null;
  stop_reason: string | null;
}

export interface Preview {
  subject: string;
  text: string;
  html: string;
  empty_variables: string[];
  to: string;
  from: string;
  company_name: string;
}

export interface Metrics {
  sent: number;
  delivered: number;
  bounced: number;
  complaints: number;
  unsubscribed: number;
  failed: number;
  contacted_leads: number;
  replied_leads: number;
  positive_replies: number;
  delivery_rate: number;
  bounce_rate: number;
  reply_rate: number;
  positive_reply_rate: number;
  unsubscribe_rate: number;
}

export interface DailyPoint {
  date: string;
  sent: number;
  bounced: number;
  replied: number;
}

export interface Overview {
  total_leads: number;
  verified_leads: number;
  active_campaigns: number;
  interested_leads: number;
  meetings: number;
  won: number;
  conversion_rate: number;
  period: Metrics;
  daily: DailyPoint[];
  days: number;
}

export interface CampaignAnalyticsRow extends Metrics {
  id: string;
  name: string;
  status: CampaignStatus;
  leads: number;
}

export interface CampaignAnalytics extends CampaignAnalyticsRow {
  funnel: { status: string; count: number }[];
  steps: { step_number: number; sent: number; bounced: number; replied: number }[];
  daily: DailyPoint[];
}

export interface Suppression {
  id: string;
  email: string;
  reason: "unsubscribed" | "hard_bounce" | "complaint" | "manual" | "erased";
  source: string;
  created_at: string;
}

export interface ScoringRule {
  id?: string;
  field: string;
  operator: string;
  values?: string[];
  min?: number | null;
  max?: number | null;
  points: number;
  reason: string;
  enabled: boolean;
}

export interface ScoringConfig {
  version: number;
  rules: ScoringRule[];
}

export interface PageMeta {
  total: number;
  page: number;
  per_page: number;
}

// ───────────── Phase 2 / 3 ─────────────

export interface ICPCriteria {
  industries?: string[];
  countries?: string[];
  states?: string[];
  cities?: string[];
  employees_min?: number | null;
  employees_max?: number | null;
  founded_after_year?: number | null;
  website?: "" | "any" | "missing" | "missing_or_poor" | "poor" | "present" | "good";
  keywords?: string[];
  tech_stack?: string[];
  buying_signals?: string[];
  require_social?: boolean;
  require_email?: boolean;
}

export interface ICP {
  id: string;
  name: string;
  criteria: ICPCriteria;
  is_default: boolean;
  updated_at: string;
}

/** A company from the company database (company-scraper). */
export interface CatalogCompany {
  id: string;
  name: string;
  industry: string;
  description: string;
  website: string;
  domain: string;
  email: string | null;
  phone: string;
  city: string;
  state: string;
  country: string;
  employee_count: number | null;
  founded_year: number | null;
  source: string;
  source_url: string;
  attribution: string;
  website_status: string;
  website_quality: string;
  email_status: string;
  phone_valid: boolean | null;
  verification_status: "verified" | "partial" | "failed" | "unverified";
  address?: string;
  linkedin_url?: string;
  social_urls?: string[] | null;
  tech_stack?: string[] | null;
  verified_at?: string | null;
  verification?: { checks?: Record<string, { passed: boolean; status?: string; detail?: string }>; passed?: number; failed?: number; checked_at?: string } | null;
  website_report?: {
    title?: string; description?: string; quality?: string; https?: boolean; mobile_friendly?: boolean; load_ms?: number;
    issues?: string[]; contact_page?: string; copyright_year?: number; reachable?: boolean; blocked_by?: string;
  } | null;
}

export interface CatalogResult {
  company: CatalogCompany;
  duplicate_lead_id: string | null;
}

export interface CatalogOption {
  value: string;
  count: number;
}

export interface CatalogCountry {
  code: string;
  name: string;
  count: number;
}

/** A state (state is "") or a city, with its parents; country is an ISO code or "". */
export interface CatalogPlace {
  country: string;
  state: string;
  name: string;
  count: number;
}

export interface CatalogFacets {
  countries: CatalogCountry[];
  states: CatalogPlace[];
  cities: CatalogPlace[];
  industries: CatalogOption[];
  tech_stack: CatalogOption[];
  company_db: boolean;
}

export interface CatalogStatus {
  configured: boolean;
  reachable: boolean;
  error?: string;
}

export interface InboundMessage {
  id: string;
  lead_id: string | null;
  company_name: string | null;
  campaign_id: string | null;
  campaign_name: string | null;
  from_email: string;
  from_name: string;
  subject: string;
  body_text: string;
  received_at: string;
  kind: "reply" | "auto_reply" | "bounce";
  classification: string | null;
  confidence: number | null;
  summary: string;
  classified_by: string;
  status: "new" | "handled" | "archived";
}

export interface Note {
  id: string;
  author_name: string | null;
  body: string;
  created_at: string;
}

export interface Task {
  id: string;
  lead_id: string | null;
  company_name: string | null;
  assignee_id: string | null;
  assignee_name: string | null;
  title: string;
  notes: string;
  due_at: string | null;
  completed_at: string | null;
}

export interface PipelineColumn {
  status: string;
  count: number;
  leads: { id: string; company_name: string; city: string; lead_score: number; owner_name: string | null; open_tasks: number; updated_at: string }[];
}

export interface Integration {
  kind: string;
  config: Record<string, unknown>;
  status: "active" | "disabled" | "error";
  last_error: string | null;
  has_secret: boolean;
  secrets_set: string[];
  updated_at: string;
}

export interface ProviderField {
  key: string;
  label: string;
  type: "secret" | "text" | "url" | "email" | "number" | "select" | "toggle" | "textarea" | "list";
  required?: boolean;
  placeholder?: string;
  help?: string;
  default?: string;
  options?: { value: string; label: string }[];
}

/** An entry of the integration catalog (GET /integrations meta.catalog). */
export interface IntegrationProvider {
  kind: string;
  name: string;
  category: string;
  tagline: string;
  description: string;
  website: string;
  docs_url?: string;
  color: string;
  capabilities: ("import" | "crm_sync" | "email_send" | "inbound")[];
  fields: ProviderField[];
  search_fields?: ProviderField[];
  notes?: string[];
}

export interface LeadSource extends IntegrationProvider {
  connected: boolean;
}

export interface SourceContact {
  first_name: string;
  last_name: string;
  title: string;
  email: string;
  email_status: string;
  phone: string;
  linkedin_url: string;
  confidence: number;
}

/** A company found by a data provider, normalized to lead fields. */
export interface SourceCandidate {
  external_id: string;
  company_name: string;
  website: string;
  domain: string;
  industry: string;
  description: string;
  country: string;
  state: string;
  city: string;
  address: string;
  phone: string;
  email: string;
  linkedin_url: string;
  social_urls: string[] | null;
  employee_count: number | null;
  founded_year: number | null;
  tech_stack: string[] | null;
  source_url: string;
  contacts: SourceContact[] | null;
  extra?: Record<string, unknown>;
  duplicate_lead_id?: string | null;
}

export interface APIKey {
  id: string;
  name: string;
  prefix: string;
  role: Role;
  last_used_at: string | null;
  revoked_at: string | null;
  created_at: string;
  token?: string;
}

export interface WebhookEndpoint {
  id: string;
  url: string;
  description: string;
  events: string[];
  active: boolean;
  secret?: string;
}

export interface WebhookDelivery {
  id: string;
  event_type: string;
  status: "pending" | "succeeded" | "failed";
  attempts: number;
  response_status: number | null;
  last_error: string | null;
  created_at: string;
}

export interface Personalization {
  id: string;
  lead_id: string;
  company_name: string;
  contact_name: string;
  content: string;
  status: "pending" | "generated" | "approved" | "rejected" | "failed";
  error: string | null;
}

export interface AgencyClient {
  id: string;
  name: string;
  leads: number;
  active_campaigns: number;
  sent_30d: number;
  replies_30d: number;
  bounced_30d: number;
}

export interface ScoringSuggestion {
  rule: ScoringRule;
  segment: string;
  leads: number;
  positive_rate: number;
  baseline_rate: number;
  lift: number;
}

export interface AdvancedAnalytics {
  classifications: { status: string; count: number }[];
  mailboxes: { id: string; from_email: string; sent: number; bounced: number; replied: number; bounce_rate: number; reply_rate: number }[];
  segments: Record<string, { value: string; contacted: number; replied: number; positive: number; reply_rate: number; positive_rate: number }[]>;
  median_hours_to_reply: number | null;
  replies_by_step: { step_number: number; sent: number; bounced: number; replied: number }[];
}

// ───────────── Lead insights (analytics) ─────────────

export interface InsightsFilter {
  industries?: string[];
  cities?: string[];
  states?: string[];
  countries?: string[];
  sources?: string[];
  tags?: string[];
  statuses?: string[];
  website_quality?: string[];
  founded_from?: number | null;
  founded_to?: number | null;
  employees_min?: number | null;
  employees_max?: number | null;
  score_min?: number | null;
  score_max?: number | null;
  icp_fit_min?: number | null;
  has_email?: boolean | null;
  has_phone?: boolean | null;
  has_website?: boolean | null;
  created_from?: string;
  created_to?: string;
}

export interface InsightBucket { key: string; count: number }

export interface LeadCompareRow {
  key: string;
  leads: number;
  with_email: number;
  contacted: number;
  engaged: number;
  avg_score: number;
  avg_icp_fit: number | null;
  stages: Record<string, number>;
}

export interface LeadInsights {
  totals: {
    leads: number; with_email: number; with_phone: number; with_website: number;
    contacted: number; engaged: number; won: number; avg_score: number; avg_icp_fit: number | null;
  };
  breakdowns: Record<"industry" | "city" | "state" | "country" | "source" | "tag" | "website" | "founded_year" | "size" | "score" | "icp_fit", InsightBucket[]>;
  stages: InsightBucket[];
  trend: { date: string; count: number }[];
  trend_unit: "day" | "week" | "month";
  compare_by: string;
  comparison: LeadCompareRow[];
}

// ───────────── Onboarding ─────────────

export interface ChecklistItem { key: string; title: string; hint: string; href: string; done: boolean }

export interface OnboardingStatus {
  state: {
    completed?: boolean;
    step?: string;
    checklist_dismissed?: boolean;
    profile?: Record<string, string>;
    goals?: Record<string, string | string[]>;
  };
  checklist: ChecklistItem[];
  completed: number;
  email_verified: boolean;
  organization: { name: string; legal_name: string; physical_address: string };
}

export interface Invitation {
  id: string;
  email: string;
  role: "admin" | "member" | "viewer";
  invited_by: string | null;
  created_at: string;
  expires_at: string;
  accepted_at: string | null;
  status: "pending" | "accepted" | "expired";
}

export interface InviteInfo {
  organization: string;
  email: string;
  role: string;
  invited_by: string;
  expires_at: string;
  account_exists: boolean;
}

// ───────────── Deals pipeline ─────────────

export type StageKind = "open" | "won" | "lost";

export interface DealStage {
  id: string;
  name: string;
  position: number;
  probability: number;
  kind: StageKind;
  stale_days: number | null;
}

export interface DealPipeline {
  id: string;
  name: string;
  is_default: boolean;
  stages: DealStage[];
  created_at: string;
}

export interface Deal {
  id: string;
  pipeline_id: string;
  stage_id: string;
  stage_name: string;
  probability: number;
  lead_id: string | null;
  company_name: string;
  city: string;
  name: string;
  value: number;
  currency: string;
  expected_close: string | null;
  owner_id: string | null;
  owner_name: string | null;
  status: StageKind;
  lost_reason: string;
  next_step: string;
  next_step_at: string | null;
  source: string;
  stage_entered_at: string;
  last_activity_at: string;
  days_in_stage: number;
  stale: boolean;
  open_tasks: number;
  closed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface DealEvent {
  id: number;
  event_type: string;
  actor_name: string | null;
  data: Record<string, unknown>;
  created_at: string;
}

export interface DealBoard {
  pipeline: DealPipeline;
  columns: { stage: DealStage; deals: Deal[]; count: number; value: number; weighted: number }[];
  totals: { open_count: number; open_value: number; weighted_value: number; won_value_30d: number; won_count_30d: number; stale_count: number };
}

export interface DealInput {
  pipeline_id?: string;
  stage_id?: string;
  lead_id?: string;
  name?: string;
  value?: number;
  currency?: string;
  expected_close?: string;
  owner_id?: string;
  next_step?: string;
  next_step_at?: string | null;
}

// ───────────── Workflows ─────────────

export interface WorkflowCondition { field: string; op: string; value: unknown }
export interface WorkflowAction { type: string; params: Record<string, unknown> }

export interface Workflow {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  trigger: string;
  trigger_config: Record<string, unknown>;
  conditions: WorkflowCondition[];
  actions: WorkflowAction[];
  template: string;
  run_count: number;
  last_run_at: string | null;
  created_at: string;
  updated_at: string;
}

export type WorkflowInput = Pick<Workflow, "name" | "description" | "trigger" | "trigger_config" | "conditions" | "actions"> & { enabled?: boolean };

export interface WorkflowTemplate extends Omit<WorkflowInput, "enabled"> {
  key: string;
  category: string;
}

export interface WorkflowRun {
  id: number;
  trigger: string;
  lead_id: string | null;
  deal_id: string | null;
  subject: string;
  status: "succeeded" | "partial" | "failed" | "skipped";
  results: { type: string; status: "ok" | "skipped" | "failed"; message: string }[];
  created_at: string;
  company_name: string | null;
}

export interface WorkflowMeta {
  triggers: { trigger: string; label: string; description: string; group: string }[];
  fields: { field: string; label: string; type: "text" | "number" | "enum" | "tags" | "user" | "stage"; options?: { value: string; label: string }[] }[];
  ops: { value: string; label: string }[];
  actions: { type: string; label: string; description: string; needs_deal: boolean }[];
  variables: string[];
  lost_reasons: string[];
}

export interface WorkflowTestResult {
  trigger_matches: boolean;
  matched: boolean;
  subject: string;
  conditions: (WorkflowCondition & { actual: unknown; passed: boolean })[];
  actions: { type: string; status: string; message: string }[];
}
