import { api } from "@/lib/api";
import type { Campaign, CampaignAnalytics, CampaignLead, LeadFilter, Mailbox, PageMeta, Preview, Step } from "@/types/api";

export interface CampaignInput {
  name: string;
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
}

/** Who to add to a campaign: any mix of picked leads, a filter, an ICP and custom contacts. */
export interface AudienceBody {
  lead_ids?: string[];
  filter?: LeadFilter;
  icp_id?: string;
  min_fit?: number;
  contacts?: { email: string; first_name?: string; last_name?: string; company?: string }[];
  dry_run?: boolean;
}

export interface AudienceResult {
  requested: number;
  added: number;
  skipped: number;
  already_in_campaign: number;
  no_email: number;
  suppressed: number;
  created: number;
  invalid_emails: string[];
  dry_run: boolean;
}

export const campaignsService = {
  list: () => api.get<Campaign[]>("/campaigns", { per_page: 200 }),
  get: (id: string) => api.get<Campaign>(`/campaigns/${id}`),
  create: (body: Partial<CampaignInput>) => api.post<Campaign>("/campaigns", body),
  update: (id: string, body: CampaignInput) => api.put<Campaign>(`/campaigns/${id}`, body),
  remove: (id: string) => api.del(`/campaigns/${id}`),
  saveSteps: (id: string, steps: Step[]) => api.put<Step[]>(`/campaigns/${id}/steps`, { steps }),
  preview: (id: string, body: { step_number: number; subject_template?: string; body_template?: string }) =>
    api.post<Preview>(`/campaigns/${id}/preview`, body),
  leads: async (id: string, page: number, status: string, q: string) => {
    const res = await api.page<CampaignLead[]>(`/campaigns/${id}/leads`, { page, per_page: 50, status, q });
    return { leads: res.data, meta: res.meta as unknown as PageMeta };
  },
  addLeads: (id: string, body: AudienceBody) => api.post<AudienceResult>(`/campaigns/${id}/leads`, body),
  removeLeads: (id: string, lead_ids: string[]) => api.post<{ removed: number }>(`/campaigns/${id}/leads/remove`, { lead_ids }),
  action: (id: string, action: "launch" | "pause" | "resume" | "cancel") => api.post<Campaign>(`/campaigns/${id}/${action}`),
  analytics: (id: string) => api.get<CampaignAnalytics>(`/analytics/campaigns/${id}`, { days: 30 }),
  recordReply: (campaign_lead_id: string, classification: string, note: string) =>
    api.post("/email-events", { campaign_lead_id, event_type: "replied", classification, note }),
  mailboxes: () => api.get<Mailbox[]>("/mailboxes"),
};

export const TEMPLATE_VARIABLES = [
  "first_name", "last_name", "full_name", "job_title", "company_name", "website", "industry",
  "city", "state", "country", "sender_name", "sender_company", "personalized_line",
];

export const REPLY_CLASSIFICATIONS = [
  "positive", "interested", "question", "not_now", "not_interested", "unsubscribe", "wrong_person", "out_of_office", "spam", "unknown",
];

export const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
