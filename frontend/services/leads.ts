import { api } from "@/lib/api";
import type { Facet, Lead, LeadDetail, LeadEvent, LeadFilter, PageMeta } from "@/types/api";

/** UI filter state: single-select controls mapped onto the API's list filters. */
export interface LeadFilterState {
  q: string;
  industry: string;
  city: string;
  status: string;
  emailStatus: string;
  website: "" | "yes" | "no";
  minScore: string;
  sort: string;
  websiteQuality: string;
  owner: "" | "none" | string; // "" any, "none" unassigned, otherwise a user id
}

export const emptyFilters: LeadFilterState = { q: "", industry: "", city: "", status: "", emailStatus: "", website: "", minScore: "", sort: "", websiteQuality: "", owner: "" };

/** Query-string form, for GET /leads and the CSV export. */
export function toQueryParams(f: LeadFilterState) {
  return {
    q: f.q, industry: f.industry, city: f.city, status: f.status, email_status: f.emailStatus,
    has_website: f.website === "" ? undefined : f.website === "yes", min_score: f.minScore, sort: f.sort,
    website_quality: f.websiteQuality, owner: f.owner === "none" ? "none" : undefined, owner_id: f.owner && f.owner !== "none" ? f.owner : undefined,
  };
}

/** JSON body form, for bulk actions and "add all matching to campaign". */
export function toFilterBody(f: LeadFilterState): LeadFilter {
  return {
    q: f.q || undefined,
    industries: f.industry ? [f.industry] : undefined,
    cities: f.city ? [f.city] : undefined,
    statuses: f.status ? [f.status] : undefined,
    email_statuses: f.emailStatus ? [f.emailStatus] : undefined,
    has_website: f.website === "" ? undefined : f.website === "yes",
    min_score: f.minScore ? Number(f.minScore) : undefined,
    website_quality: f.websiteQuality ? [f.websiteQuality] : undefined,
    unassigned: f.owner === "none" || undefined,
    owner_id: f.owner && f.owner !== "none" ? f.owner : undefined,
  };
}

export const leadsService = {
  list: async (f: LeadFilterState, page: number, perPage = 50) => {
    const res = await api.page<Lead[]>("/leads", { ...toQueryParams(f), page, per_page: perPage });
    return { leads: res.data, meta: res.meta as unknown as PageMeta };
  },
  get: (id: string) => api.get<LeadDetail>(`/leads/${id}`),
  events: (id: string) => api.get<LeadEvent[]>(`/leads/${id}/events`),
  facets: (field: string) => api.get<Facet[]>("/leads/facets", { field }),
  create: (body: unknown) => api.post<Lead>("/leads", body),
  update: (id: string, body: unknown) => api.put<Lead>(`/leads/${id}`, body),
  remove: (id: string) => api.del(`/leads/${id}`),
  bulk: (body: { action: string; ids?: string[]; filter?: LeadFilter; tags?: string[]; status?: string }) =>
    api.post<{ affected: number }>("/leads/bulk", body),
  export: (f: LeadFilterState, ids: string[]) =>
    api.download("/leads/export", { ...toQueryParams(f), ids }, `leads-${new Date().toISOString().slice(0, 10)}.csv`),
};
