/* eslint-disable @typescript-eslint/no-explicit-any */
import { fixtures, type MockDB, type Row } from "../db";
import { campaignSummary } from "./campaigns";
import { byOrg, invalid, notFound, ok, route, type Ctx } from "../server";

const fx = fixtures as any;
const ratio = (a: number, b: number) => (b > 0 ? a / b : 0);
const lc = (s: unknown) => String(s ?? "").toLowerCase().trim();
const POSITIVE = ["positive", "interested"];

function periodDays(c: Ctx) { return Math.min(Math.max(Number(c.query.get("days")) || 30, 1), 365); }
function daily(c: Ctx, days: number) {
  const from = new Date(Date.now() - (days - 1) * 864e5).toISOString().slice(0, 10);
  return byOrg(c.db.daily, c).filter((d) => d.date >= from).sort((a, b) => a.date.localeCompare(b.date)).map(({ date, sent, bounced, replied }) => ({ date, sent, bounced, replied }));
}
function metrics(sent: number, bounced: number, contacted: number, replied: number, positive: number, unsub: number) {
  return { sent, delivered: 0, bounced, complaints: 0, unsubscribed: unsub, failed: 0, contacted_leads: contacted, replied_leads: replied, positive_replies: positive,
    delivery_rate: ratio(sent - bounced, sent), bounce_rate: ratio(bounced, sent), reply_rate: ratio(replied, contacted), positive_reply_rate: ratio(positive, contacted),
    unsubscribe_rate: ratio(unsub, contacted) };
}

route("GET", "/analytics/overview", "viewer", (c) => {
  const days = periodDays(c);
  const series = daily(c, days);
  const leads = byOrg(c.db.leads, c);
  const sent = series.reduce((n, d) => n + d.sent, 0), bounced = series.reduce((n, d) => n + d.bounced, 0);
  const contacted = leads.filter((l) => l.status !== "new").length;
  const msgs = byOrg(c.db.inbox, c).filter((m) => m.kind === "reply");
  const replied = new Set(msgs.map((m) => m.lead_id)).size, positive = new Set(msgs.filter((m) => POSITIVE.includes(m.classification)).map((m) => m.lead_id)).size;
  const won = leads.filter((l) => l.status === "won").length, meetings = leads.filter((l) => ["meeting", "won"].includes(l.status)).length;
  return ok({
    total_leads: leads.length, verified_leads: leads.filter((l) => c.db.contacts.some((x) => x.lead_id === l.id && x.email_status === "valid")).length,
    active_campaigns: byOrg(c.db.campaigns, c).filter((x) => x.status === "running").length, interested_leads: leads.filter((l) => l.status === "interested").length,
    meetings, won, conversion_rate: ratio(meetings, contacted), period: metrics(sent, bounced, contacted, replied, positive, leads.filter((l) => l.status === "unsubscribed").length),
    daily: series, days,
  });
});

function campaignRow(db: MockDB, x: Row) {
  const cl = db.campaignLeads.filter((k) => k.campaign_id === x.id);
  const s = campaignSummary(db, x.id);
  const msgs = db.inbox.filter((m) => m.campaign_id === x.id && m.kind === "reply");
  const positive = new Set(msgs.filter((m) => POSITIVE.includes(m.classification)).map((m) => m.lead_id)).size;
  const contacted = cl.filter((k) => (k.sent_steps ?? 0) > 0).length;
  return { id: x.id, name: x.name, status: x.status, leads: cl.length, ...metrics(s.sent, s.bounced, contacted, s.replied, positive, 0) };
}
route("GET", "/analytics/campaigns", "viewer", (c) => ok(byOrg(c.db.campaigns, c).filter((x) => x.status !== "draft").map((x) => campaignRow(c.db, x))));
route("GET", "/analytics/campaigns/:id", "viewer", (c) => {
  const x = byOrg(c.db.campaigns, c).find((k) => k.id === c.params.id);
  if (!x) throw notFound("campaign");
  const cl = c.db.campaignLeads.filter((k) => k.campaign_id === x.id);
  const funnel = ["queued", "active", "paused", "completed", "replied", "bounced", "unsubscribed"].map((status) => ({ status, count: cl.filter((k) => k.status === status).length })).filter((f) => f.count);
  const nSteps = c.db.steps.filter((s) => s.campaign_id === x.id).length || 1;
  const stepsStats = Array.from({ length: nSteps }, (_, i) => ({ step_number: i + 1, sent: cl.filter((k) => (k.sent_steps ?? 0) > i).length, bounced: 0,
    replied: cl.filter((k) => k.status === "replied" && (k.sent_steps ?? 1) === i + 1).length }));
  const share = cl.length / Math.max(1, byOrg(c.db.campaignLeads, c).length);
  return ok({ ...campaignRow(c.db, x), funnel, steps: stepsStats,
    daily: daily(c, 30).map((d) => ({ ...d, sent: Math.round(d.sent * share), replied: Math.round(d.replied * share), bounced: Math.round(d.bounced * share) })) });
});

route("GET", "/analytics/advanced", "viewer", (c) => {
  const msgs = byOrg(c.db.inbox, c);
  const cls = new Map<string, number>();
  msgs.forEach((m) => m.classification && cls.set(m.classification, (cls.get(m.classification) ?? 0) + 1));
  const cl = byOrg(c.db.campaignLeads, c);
  const mailboxes = byOrg(c.db.mailboxes, c).map((m) => {
    const mine = cl.filter((k) => k.mailbox_id === m.id);
    const sent = mine.reduce((n, k) => n + (k.sent_steps ?? 0), 0), replied = mine.filter((k) => k.status === "replied").length;
    return { id: m.id, from_email: m.from_email, sent, bounced: 0, replied, bounce_rate: 0, reply_rate: ratio(replied, mine.length) };
  });
  const segment = (field: string) => {
    const groups = new Map<string, { contacted: number; replied: number; positive: number }>();
    for (const l of byOrg(c.db.leads, c).filter((x) => x.status !== "new")) {
      const g = groups.get(l[field] || "(unknown)") ?? { contacted: 0, replied: 0, positive: 0 };
      g.contacted++;
      if (["replied", "interested", "qualified", "meeting", "won", "lost"].includes(l.status)) g.replied++;
      if (["interested", "qualified", "meeting", "won"].includes(l.status)) g.positive++;
      groups.set(l[field] || "(unknown)", g);
    }
    return [...groups].map(([value, g]) => ({ value, ...g, reply_rate: ratio(g.replied, g.contacted), positive_rate: ratio(g.positive, g.contacted) }))
      .sort((a, b) => b.contacted - a.contacted).slice(0, 8);
  };
  return ok({
    classifications: [...cls].map(([status, count]) => ({ status, count })).sort((a, b) => b.count - a.count), mailboxes,
    segments: { city: segment("city"), industry: segment("industry"), source: segment("source"), website_quality: segment("website_quality") },
    median_hours_to_reply: msgs.length ? 18.5 : null, replies_by_step: [1, 2, 3].map((n) => ({ step_number: n, sent: cl.filter((k) => (k.sent_steps ?? 0) >= n).length, bounced: 0,
      replied: cl.filter((k) => k.status === "replied" && (k.sent_steps ?? 1) === n).length })), days: periodDays(c),
  });
});

// ───────────── lead insights (port of internal/analytics/leads.go) ─────────────

const countryCode = (s: string) => fx.countries.find((x: Row) => lc(x.name) === lc(s) || lc(x.code) === lc(s))?.code ?? "";
const countryName = (s: string) => fx.countries.find((x: Row) => lc(x.code) === lc(countryCode(s)))?.name ?? s;
const STAGE: Record<string, string> = { new: "new", contacted: "in_outreach", replied: "engaged", interested: "engaged", qualified: "engaged", meeting: "won", won: "won" };
const stageOf = (s: string) => STAGE[s] ?? "closed";
const band = (n: number) => (n >= 90 ? "90–100" : `${Math.floor(n / 10) * 10}–${Math.floor(n / 10) * 10 + 9}`);
const SIZE = ["1–10", "11–50", "51–200", "201–1,000", "1,000+"];

function hasEmail(db: MockDB, l: Row) { return !!l.business_email || db.contacts.some((x) => x.lead_id === l.id && x.email); }
function dim(db: MockDB, l: Row, k: string): string | null {
  switch (k) {
    case "industry": return lc(l.industry) || null;
    case "city": case "state": return String(l[k] ?? "").trim() || null;
    case "country": return l.country ? countryName(l.country) : null;
    case "source": return l.source || null;
    case "status": return l.status;
    case "stage": return stageOf(l.status);
    case "website": return !l.website ? "no website" : ["", "none"].includes(l.website_quality) ? "not checked" : l.website_quality === "ok" ? "good" : l.website_quality;
    case "founded_year": return l.founded_year ? String(l.founded_year) : null;
    case "size": { const e = l.employee_count; return e == null ? null : e <= 10 ? SIZE[0] : e <= 50 ? SIZE[1] : e <= 200 ? SIZE[2] : e <= 1000 ? SIZE[3] : SIZE[4]; }
    case "score": return band(l.lead_score ?? 0);
    case "icp_fit": return l.icp_fit == null ? null : band(l.icp_fit);
  }
  return null;
}
function matches(db: MockDB, l: Row, f: Row): boolean {
  const any = (vals: string[] | undefined, v: unknown) => !vals?.length || vals.map(lc).includes(lc(v));
  if (!any(f.industries, l.industry) || !any(f.cities, l.city) || !any(f.states, l.state) || !any(f.sources, l.source) || !any(f.statuses, l.status)) return false;
  if (f.countries?.length && !f.countries.some((c: string) => countryCode(c) && countryCode(c) === countryCode(l.country))) return false;
  if (f.tags?.length && !f.tags.some((t: string) => (l.tags ?? []).map(lc).includes(lc(t)))) return false;
  if (f.website_quality?.length && !f.website_quality.map(lc).includes(dim(db, l, "website"))) return false;
  const rng = (v: number | null, lo?: number | null, hi?: number | null) => (lo == null || (v != null && v >= lo)) && (hi == null || (v != null && v <= hi));
  if (!rng(l.founded_year, f.founded_from, f.founded_to) || !rng(l.employee_count, f.employees_min, f.employees_max) || !rng(l.lead_score, f.score_min, f.score_max) || !rng(l.icp_fit, f.icp_fit_min)) return false;
  if (f.has_email != null && hasEmail(db, l) !== f.has_email) return false;
  if (f.has_phone != null && !!l.phone !== f.has_phone) return false;
  if (f.has_website != null && !!l.website !== f.has_website) return false;
  if (f.created_from && l.created_at < f.created_from) return false;
  if (f.created_to && l.created_at >= new Date(new Date(f.created_to).getTime() + 864e5).toISOString()) return false;
  return true;
}
function filtered(c: Ctx, filter: Row, steps: Row[] = []) {
  return byOrg(c.db.leads, c).filter((l) => matches(c.db, l, filter ?? {}) && steps.every((s) => matches(c.db, l, s)));
}
function buckets(map: Map<string, number>) { return [...map].map(([key, count]) => ({ key, count })); }
function fold(b: { key: string; count: number }[], n: number) {
  b.sort((a, x) => x.count - a.count || a.key.localeCompare(x.key));
  if (b.length <= n + 1) return b;
  return [...b.slice(0, n), { key: "Other", count: b.slice(n).reduce((s, x) => s + x.count, 0) }];
}
const ordered = (k: string, b: { key: string; count: number }[]) =>
  b.sort((a, x) => (k === "size" ? SIZE.indexOf(a.key) - SIZE.indexOf(x.key) : parseInt(a.key) - parseInt(x.key)));

route("POST", "/analytics/leads", "viewer", (c) => {
  const { filter = {}, steps = [], compare_by = "industry" } = c.body;
  const top = Math.min(Math.max(Number(c.body.top) || 10, 1), 25);
  if (steps.length > 12) throw invalid("validation failed", { steps: "at most 12 steps" });
  const leads = filtered(c, filter, steps);
  const db = c.db;
  const withEmail = leads.filter((l) => hasEmail(db, l)).length;
  const fits = leads.filter((l) => l.icp_fit != null);
  const totals = {
    leads: leads.length, with_email: withEmail, with_phone: leads.filter((l) => l.phone).length, with_website: leads.filter((l) => l.website).length,
    contacted: leads.filter((l) => l.status !== "new").length, engaged: leads.filter((l) => ["replied", "interested", "qualified", "meeting", "won"].includes(l.status)).length,
    won: leads.filter((l) => ["meeting", "won"].includes(l.status)).length, avg_score: leads.length ? leads.reduce((s, l) => s + l.lead_score, 0) / leads.length : 0,
    avg_icp_fit: fits.length ? fits.reduce((s, l) => s + l.icp_fit, 0) / fits.length : null,
  };
  const breakdowns: Record<string, { key: string; count: number }[]> = {};
  for (const k of ["industry", "city", "state", "country", "source", "website", "founded_year", "size", "score", "icp_fit"]) {
    const m = new Map<string, number>();
    for (const l of leads) { const v = dim(db, l, k); if (v) m.set(v, (m.get(v) ?? 0) + 1); }
    breakdowns[k] = ["founded_year", "size", "score", "icp_fit"].includes(k) ? ordered(k, buckets(m)) : fold(buckets(m), top);
  }
  const tagMap = new Map<string, number>();
  leads.forEach((l) => (l.tags ?? []).forEach((t: string) => tagMap.set(t, (tagMap.get(t) ?? 0) + 1)));
  breakdowns.tag = fold(buckets(tagMap), top);
  const stages = ["new", "in_outreach", "engaged", "won", "closed"].map((key) => ({ key, count: leads.filter((l) => stageOf(l.status) === key).length }));
  // Trend
  const times = leads.map((l) => l.created_at).sort();
  const span = times.length ? new Date(times[times.length - 1]).getTime() - new Date(times[0]).getTime() : 0;
  const unit = span > 400 * 864e5 ? "month" : span > 90 * 864e5 ? "week" : "day";
  const keyOf = (iso: string) => {
    const d = new Date(iso);
    if (unit === "month") return `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}-01`;
    if (unit === "week") { const w = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate() - ((d.getUTCDay() + 6) % 7))); return w.toISOString().slice(0, 10); }
    return iso.slice(0, 10);
  };
  const tm = new Map<string, number>();
  leads.forEach((l) => tm.set(keyOf(l.created_at), (tm.get(keyOf(l.created_at)) ?? 0) + 1));
  const trend: { date: string; count: number }[] = [];
  if (tm.size) {
    const keys = [...tm.keys()].sort();
    for (let d = new Date(keys[0] + "T00:00:00Z"); d.toISOString().slice(0, 10) <= keys[keys.length - 1] && trend.length < 1000;) {
      const k = d.toISOString().slice(0, 10);
      trend.push({ date: k, count: tm.get(k) ?? 0 });
      if (unit === "month") d.setUTCMonth(d.getUTCMonth() + 1); else d.setUTCDate(d.getUTCDate() + (unit === "week" ? 7 : 1));
    }
  }
  // Comparison
  const groups = new Map<string, Row>();
  for (const l of leads) {
    const keys = compare_by === "tag" ? (l.tags ?? []) : [dim(db, l, compare_by)].filter(Boolean);
    for (const k of keys as string[]) {
      const g = groups.get(k) ?? { key: k, leads: 0, with_email: 0, contacted: 0, engaged: 0, scoreSum: 0, fitSum: 0, fitN: 0, stages: {} as Record<string, number> };
      g.leads++; if (hasEmail(db, l)) g.with_email++; if (l.status !== "new") g.contacted++;
      if (["replied", "interested", "qualified", "meeting", "won"].includes(l.status)) g.engaged++;
      g.scoreSum += l.lead_score; if (l.icp_fit != null) { g.fitSum += l.icp_fit; g.fitN++; }
      const st = stageOf(l.status); g.stages[st] = (g.stages[st] ?? 0) + 1;
      groups.set(k, g);
    }
  }
  let comparison: Row[] = [...groups.values()].map(({ scoreSum, fitSum, fitN, ...g }) => ({ ...g, avg_score: scoreSum / Math.max(g.leads, 1), avg_icp_fit: fitN ? fitSum / fitN : null }));
  if (["founded_year", "size", "score", "icp_fit"].includes(compare_by)) {
    const order = ordered(compare_by, comparison.map((r) => ({ key: r.key, count: r.leads }))).map((b) => b.key);
    comparison = comparison.sort((a, b) => order.indexOf(a.key) - order.indexOf(b.key)).slice(-30);
  } else comparison = comparison.sort((a, b) => b.leads - a.leads || a.key.localeCompare(b.key)).slice(0, top);
  return ok({ totals, breakdowns, stages, trend, trend_unit: unit, compare_by, comparison, generated_at: new Date().toISOString() });
});

route("POST", "/analytics/leads/steps", "viewer", (c) => {
  const steps: Row[] = c.body.steps ?? [];
  if (steps.length > 12) throw invalid("validation failed", { steps: "at most 12 steps" });
  let current = byOrg(c.db.leads, c);
  const out = [{ step: 0, count: current.length }];
  steps.forEach((s, i) => { current = current.filter((l) => matches(c.db, l, s)); out.push({ step: i + 1, count: current.length }); });
  return ok(out);
});

export {};
