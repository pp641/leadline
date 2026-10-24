/* eslint-disable @typescript-eslint/no-explicit-any */
import { fixtures, recomputeAll, type MockDB, type Row } from "../db";
import { insertLead } from "./leads";
import {
  audit, byOrg, conflict, created, id, invalid, noContent, notFound, ok, paginate, route, stamp, type Ctx,
} from "../server";

const fx = fixtures as any;
const lc = (s: unknown) => String(s ?? "").toLowerCase().trim();

// ───────────── ICPs ─────────────

route("GET", "/icps", "viewer", (c) => ok(byOrg(c.db.icps, c).sort((a, b) => Number(b.is_default) - Number(a.is_default) || a.created_at.localeCompare(b.created_at))));
route("GET", "/icps/:id", "viewer", (c) => { const i = byOrg(c.db.icps, c).find((x) => x.id === c.params.id); if (!i) throw notFound("ICP"); return ok(i); });
function saveICP(c: Ctx, existing?: Row) {
  const name = String(c.body.name ?? "").trim();
  if (!name) throw invalid("validation failed", { name: "is required" });
  const first = !byOrg(c.db.icps, c).length;
  if (c.body.is_default || first) byOrg(c.db.icps, c).forEach((i) => (i.is_default = false));
  const icp = existing ?? { id: id(), organization_id: c.orgId, created_at: stamp() };
  Object.assign(icp, { name, criteria: c.body.criteria ?? {}, is_default: !!c.body.is_default || first || (existing?.is_default ?? false), updated_at: stamp() });
  if (!existing) c.db.icps.push(icp);
  recomputeAll(c.db);
  return icp;
}
route("POST", "/icps", "member", (c) => created(saveICP(c)));
route("PUT", "/icps/:id", "member", (c) => { const i = byOrg(c.db.icps, c).find((x) => x.id === c.params.id); if (!i) throw notFound("ICP"); return ok(saveICP(c, i)); });
route("DELETE", "/icps/:id", "member", (c) => { c.db.icps = c.db.icps.filter((x) => x.id !== c.params.id); recomputeAll(c.db); return noContent(); });

// ───────────── company database (company-scraper) ─────────────

function facets(db: MockDB, orgId: string | null) {
  const leads = db.leads.filter((l) => l.organization_id === orgId);
  const rows = [...db.catalog.map((x) => ({ country: x.country, state: x.state, city: x.city, industry: x.industry, tech: x.tech_stack ?? [] })),
    ...leads.map((l) => ({ country: l.country, state: l.state, city: l.city, industry: l.industry, tech: l.tech_stack ?? [] }))];
  const codeOf = (name: string) => fx.countries.find((c: Row) => lc(c.name) === lc(name) || lc(c.code) === lc(name))?.code ?? "";
  const countries = new Map<string, number>(), states = new Map<string, Row>(), cities = new Map<string, Row>(), inds = new Map<string, number>(), tech = new Map<string, number>();
  for (const r of rows) {
    const code = codeOf(r.country);
    if (code) countries.set(code, (countries.get(code) ?? 0) + 1);
    if (r.state) { const k = code + "|" + lc(r.state); states.set(k, { country: code, state: "", name: r.state, count: (states.get(k)?.count ?? 0) + 1 }); }
    if (r.city) { const k = code + "|" + lc(r.state) + "|" + lc(r.city); cities.set(k, { country: code, state: r.state, name: r.city, count: (cities.get(k)?.count ?? 0) + 1 }); }
    if (r.industry) inds.set(lc(r.industry), (inds.get(lc(r.industry)) ?? 0) + 1);
    for (const t of r.tech) tech.set(t, (tech.get(t) ?? 0) + 1);
  }
  const opts = (m: Map<string, number>) => [...m].map(([value, count]) => ({ value, count })).sort((a, b) => b.count - a.count);
  return {
    countries: fx.countries.map((c: Row) => ({ ...c, count: countries.get(c.code) ?? 0 })).sort((a: Row, b: Row) => (b.count - a.count) || a.name.localeCompare(b.name)),
    states: [...states.values()].sort((a, b) => b.count - a.count), cities: [...cities.values()].sort((a, b) => b.count - a.count),
    industries: opts(inds), tech_stack: opts(tech), company_db: true,
  };
}
route("GET", "/catalog/status", "viewer", () => ok({ configured: true, reachable: true }));
route("GET", "/catalog/facets", "viewer", (c) => ok(facets(c.db, c.orgId)));

function catalogMatch(x: Row, q: Row) {
  const any = (vals: string[] | undefined, v: string) => !vals?.length || vals.map(lc).includes(lc(v));
  if (q.industries?.length && !q.industries.some((i: string) => lc(x.industry).includes(lc(i).replace(/s$/, "")))) return false;
  const countryOk = !q.countries?.length || q.countries.some((cn: string) => lc(cn) === lc(x.country) || (lc(cn) === "in" && lc(x.country) === "india"));
  if (!countryOk || !any(q.states, x.state) || !any(q.cities, x.city)) return false;
  const site = !!x.website;
  switch (q.website) {
    case "missing": if (site) return false; break;
    case "present": if (!site) return false; break;
    case "good": case "poor": if (x.website_quality !== q.website) return false; break;
    case "missing_or_poor": if (site && !["none", "poor"].includes(x.website_quality)) return false; break;
  }
  if (q.verified_only && x.verification_status !== "verified") return false;
  if (q.require_email && !x.email) return false;
  return true;
}
const dupOf = (c: Ctx, x: Row) => c.db.leads.find((l) => l.organization_id === c.orgId && (l.catalog_company_id === x.id || (x.domain && l.domain === x.domain) ||
  (lc(l.company_name) === lc(x.name) && lc(l.city) === lc(x.city))))?.id ?? null;
route("POST", "/catalog/search", "viewer", (c) => {
  let q: Row;
  if (c.body.icp_id) { const icp = byOrg(c.db.icps, c).find((i) => i.id === c.body.icp_id); if (!icp) throw notFound("ICP"); q = { ...icp.criteria }; }
  else if (c.body.query) q = c.body.query;
  else throw invalid("validation failed", { icp_id: "choose an ICP or provide a query" });
  q.verified_only = q.verified_only || c.body.verified_only;
  const limit = Math.min(Number(c.body.limit) || 100, 500);
  const rank = (x: Row) => (x.verification_status === "verified" ? 0 : 1) + (x.email ? 0 : 0.5);
  return ok(c.db.catalog.filter((x) => catalogMatch(x, q)).sort((a, b) => rank(a) - rank(b)).slice(0, limit).map((x) => ({ company: x, duplicate_lead_id: dupOf(c, x) })));
});
route("POST", "/catalog/import", "member", (c) => {
  const ids: string[] = c.body.company_ids ?? [];
  const res = { imported: 0, duplicates: 0, lead_ids: [] as string[] };
  for (const x of c.db.catalog.filter((cc) => ids.includes(cc.id))) {
    if (dupOf(c, x)) { res.duplicates++; continue; }
    const l = insertLead(c, { company_name: x.name, website: x.website, industry: x.industry, country: x.country, state: x.state, city: x.city, address: x.address,
      phone: x.phone, business_email: x.email, source: x.source, source_url: x.source_url, tags: c.body.tags ?? [], tech_stack: x.tech_stack ?? [],
      website_quality: ["none", "poor", "ok", "good"].includes(x.website_quality) ? x.website_quality : "", employee_count: x.employee_count, founded_year: x.founded_year },
      { catalog_company_id: x.id, enriched_at: x.verified_at });
    res.lead_ids.push(l.id);
  }
  res.imported = res.lead_ids.length;
  return ok(res);
});

// ───────────── integrations ─────────────

const provider = (kind: string) => fx.catalog.find((p: Row) => p.kind === kind);
const integrationView = (i: Row) => ({ kind: i.kind, config: i.config, status: i.status, last_error: i.last_error, has_secret: i.has_secret, secrets_set: i.secrets_set, updated_at: i.updated_at });

route("GET", "/integrations", "viewer", (c) => ok(byOrg(c.db.integrations, c).map(integrationView),
  { catalog: fx.catalog, categories: fx.categories, ai_enabled: false, encryption_enabled: true }));
route("PUT", "/integrations/:kind", "admin", (c) => {
  const p = provider(c.params.kind);
  if (!p) throw notFound("integration");
  const existing = byOrg(c.db.integrations, c).find((i) => i.kind === p.kind);
  const values: Row = { ...(c.body.config ?? {}), ...(c.body.values ?? {}) };
  const secrets = new Set<string>(existing?.secrets_set ?? []);
  const config: Row = {};
  const errs: Record<string, string> = {};
  for (const f of p.fields as Row[]) {
    const v = values[f.key];
    if (f.type === "secret") { if (String(v ?? "").trim()) secrets.add(f.key); if (f.required && !secrets.has(f.key)) errs[f.key] = "is required"; }
    else if (f.type === "toggle") config[f.key] = v === undefined ? f.default === "true" : v === true || v === "true";
    else { const s = String(v ?? f.default ?? "").trim(); if (f.required && !s) errs[f.key] = "is required"; if (f.type === "url" && s && !s.startsWith("https://")) errs[f.key] = "must start with https://"; config[f.key] = s; }
  }
  if (Object.keys(errs).length) throw invalid("validation failed", errs);
  const row = existing ?? { organization_id: c.orgId, kind: p.kind };
  Object.assign(row, { config, status: c.body.status === "disabled" ? "disabled" : "active", last_error: null, has_secret: secrets.size > 0, secrets_set: [...secrets], updated_at: stamp() });
  if (!existing) c.db.integrations.push(row);
  audit(c, "integration.saved", "integration", p.kind);
  return noContent();
});
route("DELETE", "/integrations/:kind", "admin", (c) => {
  c.db.integrations = c.db.integrations.filter((i) => !(i.organization_id === c.orgId && i.kind === c.params.kind));
  return noContent();
});
route("POST", "/integrations/:kind/test", "admin", (c) => {
  const i = byOrg(c.db.integrations, c).find((x) => x.kind === c.params.kind);
  if (!i) throw conflict("integration_not_configured", `${provider(c.params.kind)?.name ?? c.params.kind} is not connected — add it under Integrations`);
  i.status = i.status === "disabled" ? "disabled" : "active"; i.last_error = null; i.updated_at = stamp();
  return ok({ status: "ok" });
});

// ───────────── lead sources (demo results) ─────────────

const DEMO_CITIES = [["Noida", "Uttar Pradesh"], ["Gurugram", "Haryana"], ["New Delhi", "Delhi"], ["Pune", "Maharashtra"], ["Bengaluru", "Karnataka"]];
function demoCandidates(kind: string, params: Row): Row[] {
  const n = Math.min(Number(params.limit) || 20, 25);
  const what = lc(params.query || params.keywords || params.industry || params.category || "business") || "business";
  const where = String(params.location || params.city || (params.locations ?? "") || "").split(",")[0].trim();
  const domains = String(params.domains ?? "").split(/[\s,]+/).map((d) => d.replace(/^https?:\/\/(www\.)?/, "").split("/")[0]).filter((d) => d.includes("."));
  const base = domains.length ? domains : Array.from({ length: n }, (_, i) => `${what.split(" ")[0]}-${["prime", "urban", "metro", "nova", "zenith", "bright", "apex", "true", "next", "blue"][i % 10]}${i}.in`);
  return base.slice(0, Math.max(domains.length, n)).map((domain, i) => {
    const [city, state] = where ? [where, ""] : DEMO_CITIES[i % DEMO_CITIES.length];
    const name = domains.length ? domain.split(".")[0].replace(/[-_]/g, " ").replace(/\b\w/g, (m) => m.toUpperCase())
      : `${["Prime", "Urban", "Metro", "Nova", "Zenith", "Bright", "Apex", "True", "Next", "Blue"][i % 10]} ${what.replace(/\b\w/g, (m) => m.toUpperCase())} ${i + 1}`;
    const people = kind === "google_places" || kind === "crunchbase" ? [] : [
      { first_name: ["Riya", "Kabir", "Anika", "Vivaan"][i % 4], last_name: ["Joshi", "Bhatia", "Rao", "Sethi"][i % 4], title: ["Founder", "CEO", "Marketing Head", "Owner"][i % 4],
        email: `${["riya", "kabir", "anika", "vivaan"][i % 4]}@${domain}`, email_status: i % 5 === 4 ? "risky" : "valid", phone: "", linkedin_url: "", confidence: 0.92 },
      ...(i % 2 ? [{ first_name: "", last_name: "", title: "", email: `info@${domain}`, email_status: "valid", phone: "", linkedin_url: "", confidence: 0.8 }] : []),
    ];
    return {
      external_id: `${kind}-${i}`, company_name: name, website: kind === "google_places" && i % 3 === 2 ? "" : `https://${domain}`, domain: kind === "google_places" && i % 3 === 2 ? "" : domain,
      industry: what, description: `${name} — demo result from ${provider(kind)?.name ?? kind}.`, country: "India", state, city,
      address: `${10 + i}, Main Road, ${city}`, phone: `+91 98${String(10000000 + i * 7919).slice(0, 8)}`, email: "", linkedin_url: "", social_urls: [],
      employee_count: kind === "google_places" ? null : [8, 15, 30, 60, 120][i % 5], founded_year: [2012, 2016, 2019, 2021, 2023][i % 5], tech_stack: [],
      source_url: kind === "google_places" ? `https://maps.google.com/?cid=${1000 + i}` : "", contacts: people,
      extra: kind === "google_places" ? { rating: (3.8 + (i % 12) / 10).toFixed(1) } : kind === "crunchbase" ? { last_funding_type: ["seed", "series a", "series b"][i % 3] } : {},
    };
  });
}
route("GET", "/sources", "viewer", (c) => {
  const connected = new Set(byOrg(c.db.integrations, c).filter((i) => i.status !== "disabled").map((i) => i.kind));
  return ok(fx.sources.map((s: Row) => ({ ...s, connected: connected.has(s.kind) })));
});
route("POST", "/sources/:kind/search", "member", (c) => {
  const s = fx.sources.find((x: Row) => x.kind === c.params.kind);
  if (!s) throw notFound("lead source");
  if (!byOrg(c.db.integrations, c).some((i) => i.kind === s.kind && i.status !== "disabled")) throw conflict("integration_not_configured", `${s.name} is not connected — add it under Integrations`);
  const params = c.body.params ?? {};
  for (const f of (s.search_fields ?? []) as Row[]) if (f.required && !String(params[f.key] ?? "").trim()) throw invalid(`${s.name}: ${f.label.toLowerCase()} is required`);
  return ok(demoCandidates(s.kind, params).map((cand) => ({
    ...cand, duplicate_lead_id: c.db.leads.find((l) => l.organization_id === c.orgId && ((cand.domain && l.domain === cand.domain) || (lc(l.company_name) === lc(cand.company_name) && lc(l.city) === lc(cand.city))))?.id ?? null,
  })));
});
route("POST", "/sources/:kind/import", "member", (c) => {
  const res = { imported: 0, duplicates: 0, skipped: 0, lead_ids: [] as string[] };
  for (const cand of (c.body.candidates ?? []) as Row[]) {
    if (c.db.leads.some((l) => l.organization_id === c.orgId && ((cand.domain && l.domain === cand.domain) || (lc(l.company_name) === lc(cand.company_name) && lc(l.city) === lc(cand.city))))) { res.duplicates++; continue; }
    const primary = (cand.contacts ?? []).find((p: Row) => p.first_name) ?? (cand.contacts ?? [])[0];
    const l = insertLead(c, { ...cand, business_email: cand.email || (cand.contacts ?? []).find((p: Row) => !p.first_name)?.email || null, source: c.params.kind,
      tags: [...(c.body.tags ?? []), c.params.kind], contact: primary ? { email: primary.email, first_name: primary.first_name, last_name: primary.last_name, job_title: primary.title } : undefined });
    res.lead_ids.push(l.id);
  }
  res.imported = res.lead_ids.length;
  return ok(res);
});

// ───────────── suppression, scoring ─────────────

route("GET", "/suppressions", "viewer", (c) => {
  const q = lc(c.query.get("q"));
  return paginate(byOrg(c.db.suppressions, c).filter((s) => !q || s.email.includes(q)).sort((a, b) => b.created_at.localeCompare(a.created_at)), c.query);
});
route("POST", "/suppressions", "member", (c) => {
  let added = 0;
  for (const e of (c.body.emails ?? []) as string[]) {
    const email = lc(e);
    if (!email.includes("@") || byOrg(c.db.suppressions, c).some((s) => s.email === email)) continue;
    c.db.suppressions.push({ id: id(), organization_id: c.orgId, email, reason: "manual", source: "manual", created_at: stamp() });
    added++;
  }
  return ok({ added });
});
route("DELETE", "/suppressions/:id", "admin", (c) => { c.db.suppressions = c.db.suppressions.filter((s) => s.id !== c.params.id); return noContent(); });

route("GET", "/scoring", "viewer", (c) => ok(c.db.scoring[c.orgId!] ?? fx.scoring.config, fx.scoring.meta));
route("PUT", "/scoring", "admin", (c) => {
  const cur = c.db.scoring[c.orgId!] ?? fx.scoring.config;
  c.db.scoring[c.orgId!] = { version: (cur.version ?? 1) + 1, rules: c.body.rules ?? [] };
  return ok(c.db.scoring[c.orgId!]);
});
route("GET", "/scoring/suggestions", "viewer", () => ok([
  { rule: { field: "industry", operator: "in", values: ["restaurant"], points: 10, reason: "Restaurants reply more often", enabled: true },
    segment: "industry = restaurant", leads: 64, positive_rate: 0.19, baseline_rate: 0.11, lift: 1.7 },
  { rule: { field: "website_quality", operator: "eq", values: ["poor"], points: 10, reason: "Outdated websites convert better", enabled: true },
    segment: "website quality = poor", leads: 41, positive_rate: 0.22, baseline_rate: 0.11, lift: 2.0 },
]));

// ───────────── mailboxes, API keys, webhooks ─────────────

function mailboxView(c: Ctx, m: Row) {
  const today = new Date().toISOString().slice(0, 10);
  const sent = c.db.daily.find((d) => d.organization_id === c.orgId && d.date === today)?.sent ?? 0;
  const pool = byOrg(c.db.mailboxes, c).length || 1;
  return { ...m, sent_last_24h: Math.round(sent / pool) };
}
route("GET", "/mailboxes", "viewer", (c) => ok(byOrg(c.db.mailboxes, c).map((m) => mailboxView(c, m))));
function saveMailbox(c: Ctx, m?: Row) {
  const b = c.body;
  if (!String(b.from_email ?? "").includes("@")) throw invalid("validation failed", { from_email: "must be a valid email address" });
  const row = m ?? { id: id(), organization_id: c.orgId, created_at: stamp(), last_synced_at: null, sync_error: null };
  Object.assign(row, {
    name: b.name || b.from_email, from_email: b.from_email, from_name: b.from_name ?? "", reply_to: b.reply_to ?? null, daily_limit: Number(b.daily_limit) || 50,
    status: b.status || "active", smtp_host: b.smtp_host ?? "", smtp_port: b.smtp_port || 587, smtp_username: b.smtp_username ?? "", smtp_tls: b.smtp_tls || "starttls",
    has_smtp_password: !!(b.smtp_password || m?.has_smtp_password), imap_host: b.imap_host ?? "", imap_port: b.imap_port || 993, imap_username: b.imap_username ?? "",
    imap_tls: b.imap_tls ?? true, has_imap_password: !!(b.imap_password || m?.has_imap_password), inbox_sync_enabled: !!b.inbox_sync_enabled,
  });
  if (!m) c.db.mailboxes.push(row);
  return mailboxView(c, row);
}
route("POST", "/mailboxes", "admin", (c) => created(saveMailbox(c)));
route("PUT", "/mailboxes/:id", "admin", (c) => { const m = byOrg(c.db.mailboxes, c).find((x) => x.id === c.params.id); if (!m) throw notFound("mailbox"); return ok(saveMailbox(c, m)); });
route("DELETE", "/mailboxes/:id", "admin", (c) => { c.db.mailboxes = c.db.mailboxes.filter((m) => m.id !== c.params.id); return noContent(); });
route("POST", "/mailboxes/:id/test", "admin", () => ok({ smtp: "ok", imap: "ok" }));
route("POST", "/mailboxes/:id/sync", "member", (c) => { const m = byOrg(c.db.mailboxes, c).find((x) => x.id === c.params.id); if (m) m.last_synced_at = stamp(); return { data: { queued: true }, status: 202 }; });

route("GET", "/api-keys", "admin", (c) => ok(byOrg(c.db.apiKeys, c).map(({ organization_id: _o, ...k }) => { void _o; return k; })));
route("POST", "/api-keys", "admin", (c) => {
  if (!String(c.body.name ?? "").trim()) throw invalid("validation failed", { name: "is required" });
  const prefix = "ll_" + id().slice(0, 6);
  const k = { id: id(), organization_id: c.orgId, name: c.body.name, prefix, role: c.body.role || "member", last_used_at: null, revoked_at: null, created_at: stamp() };
  c.db.apiKeys.push(k);
  return created({ ...k, token: `${prefix}_${id().replace(/-/g, "")}` });
});
route("DELETE", "/api-keys/:id", "admin", (c) => { const k = byOrg(c.db.apiKeys, c).find((x) => x.id === c.params.id); if (k) k.revoked_at = stamp(); return noContent(); });

const EVENT_TYPES = ["lead.created", "lead.status_changed", "email.sent", "email.bounced", "email.replied", "email.unsubscribed", "reply.received", "campaign.completed", "campaign.paused"];
route("GET", "/webhook-endpoints", "admin", (c) => ok(byOrg(c.db.webhooks, c), { event_types: EVENT_TYPES }));
route("POST", "/webhook-endpoints", "admin", (c) => {
  if (!String(c.body.url ?? "").startsWith("https://")) throw invalid("validation failed", { url: "must be an https:// URL" });
  const w = { id: id(), organization_id: c.orgId, url: c.body.url, description: "", events: c.body.events ?? [], active: true };
  c.db.webhooks.push(w);
  return created({ ...w, secret: "whsec_" + id().replace(/-/g, "") });
});
route("PUT", "/webhook-endpoints/:id", "admin", (c) => { const w = byOrg(c.db.webhooks, c).find((x) => x.id === c.params.id); if (!w) throw notFound("endpoint"); Object.assign(w, c.body); return ok(w); });
route("DELETE", "/webhook-endpoints/:id", "admin", (c) => { c.db.webhooks = c.db.webhooks.filter((w) => w.id !== c.params.id); return noContent(); });
route("POST", "/webhook-endpoints/:id/test", "admin", (c) => {
  c.db.deliveries.unshift({ id: id(), endpoint_id: c.params.id, event_type: "test", status: "succeeded", attempts: 1, response_status: 200, last_error: null, created_at: stamp() });
  return { data: { queued: true }, status: 202 };
});
route("GET", "/webhook-endpoints/:id/deliveries", "admin", (c) => ok(c.db.deliveries.filter((d) => d.endpoint_id === c.params.id).slice(0, 50)));

// ───────────── dashboards ─────────────

route("GET", "/dashboards", "viewer", (c) => ok(byOrg(c.db.dashboards, c).sort((a, b) => b.updated_at.localeCompare(a.updated_at))));
route("POST", "/dashboards", "member", (c) => {
  if (!String(c.body.name ?? "").trim()) throw invalid("validation failed", { name: "is required" });
  const d = { id: id(), organization_id: c.orgId, name: c.body.name.trim(), widgets: c.body.widgets ?? [], steps: c.body.steps ?? [], created_by: c.userId, created_at: stamp(), updated_at: stamp() };
  c.db.dashboards.push(d);
  return created(d);
});
route("GET", "/dashboards/:id", "viewer", (c) => { const d = byOrg(c.db.dashboards, c).find((x) => x.id === c.params.id); if (!d) throw notFound("dashboard"); return ok(d); });
route("PUT", "/dashboards/:id", "member", (c) => {
  const d = byOrg(c.db.dashboards, c).find((x) => x.id === c.params.id);
  if (!d) throw notFound("dashboard");
  Object.assign(d, { name: String(c.body.name ?? d.name).trim(), widgets: c.body.widgets ?? [], steps: c.body.steps ?? [], updated_at: stamp() });
  return ok(d);
});
route("DELETE", "/dashboards/:id", "member", (c) => { c.db.dashboards = c.db.dashboards.filter((d) => d.id !== c.params.id); return noContent(); });

export {};
