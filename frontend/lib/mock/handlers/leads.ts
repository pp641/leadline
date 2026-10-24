 
import { icpFit, recomputeLead, scoreLead, type MockDB, type Row } from "../db";
import {
  audit, byOrg, created, id, invalid, leadEvent, noContent, notFound, ok, paginate, route, stamp, type Ctx,
} from "../server";

// ───────────── shaping ─────────────

export function leadView(db: MockDB, l: Row) {
  const contacts = db.contacts.filter((c) => c.lead_id === l.id);
  const p = contacts.find((c) => c.is_primary) ?? contacts[0];
  const owner = l.owner_id ? db.users.find((u) => u.id === l.owner_id) : null;
  return {
    ...l, owner_name: owner?.full_name ?? null,
    primary_contact: p ? { id: p.id, first_name: p.first_name, last_name: p.last_name, email: p.email, email_status: p.email_status } : null,
  };
}

const lc = (s: unknown) => String(s ?? "").toLowerCase();
const list = (v: string | null) => (v ? v.split(",").map((x) => x.trim()).filter(Boolean) : []);

/** Shared lead filter: query params (list page) or a JSON filter body (bulk, campaigns). */
export function matchLead(db: MockDB, l: Row, f: Row): boolean {
  const q = lc(f.q);
  if (q) {
    const contacts = db.contacts.filter((c) => c.lead_id === l.id);
    const hay = [l.company_name, l.domain, l.city, l.industry, l.business_email, ...contacts.flatMap((c) => [c.email, c.first_name, c.last_name])].map(lc).join(" ");
    if (!hay.includes(q)) return false;
  }
  const inList = (vals: string[] | undefined, v: unknown) => !vals?.length || vals.map(lc).includes(lc(v));
  if (!inList(f.industries, l.industry) || !inList(f.cities, l.city) || !inList(f.statuses, l.status) || !inList(f.sources, l.source)) return false;
  if (f.website_quality?.length && !f.website_quality.map(lc).includes(lc(l.website_quality))) return false;
  if (f.tags?.length && !f.tags.some((t: string) => l.tags.map(lc).includes(lc(t)))) return false;
  if (f.email_statuses?.length) {
    const st = db.contacts.filter((c) => c.lead_id === l.id).map((c) => c.email_status);
    if (!f.email_statuses.some((s: string) => st.includes(s))) return false;
  }
  if (f.min_score != null && f.min_score !== "" && l.lead_score < Number(f.min_score)) return false;
  if (f.min_icp_fit != null && f.min_icp_fit !== "" && (l.icp_fit ?? -1) < Number(f.min_icp_fit)) return false;
  if (f.has_website === true && !l.website) return false;
  if (f.has_website === false && l.website) return false;
  if (f.has_email === true && !l.business_email && !db.contacts.some((c) => c.lead_id === l.id && c.email)) return false;
  if (f.unassigned && l.owner_id) return false;
  if (f.owner_id && l.owner_id !== f.owner_id) return false;
  if (f.not_in_campaign_id && db.campaignLeads.some((x) => x.campaign_id === f.not_in_campaign_id && x.lead_id === l.id)) return false;
  if (f.ids?.length && !f.ids.includes(l.id)) return false;
  return true;
}

function filterFromQuery(q: URLSearchParams): Row {
  const b = (k: string) => (q.get(k) === "true" ? true : q.get(k) === "false" ? false : undefined);
  return {
    q: q.get("q"), industries: list(q.get("industry")), cities: list(q.get("city")), statuses: list(q.get("status")), sources: list(q.get("source")),
    email_statuses: list(q.get("email_status")), website_quality: list(q.get("website_quality")), tags: list(q.get("tag")),
    min_score: q.get("min_score"), min_icp_fit: q.get("min_icp_fit"), has_website: b("has_website"),
    unassigned: q.get("owner") === "none", owner_id: q.get("owner_id"), ids: list(q.get("ids")),
  };
}

function sortLeads(rows: Row[], sort: string | null) {
  const by: Record<string, (a: Row, b: Row) => number> = {
    "-score": (a, b) => b.lead_score - a.lead_score, "-icp_fit": (a, b) => (b.icp_fit ?? -1) - (a.icp_fit ?? -1),
    company_name: (a, b) => a.company_name.localeCompare(b.company_name), created_at: (a, b) => a.created_at.localeCompare(b.created_at),
  };
  return [...rows].sort(by[sort ?? ""] ?? ((a, b) => b.created_at.localeCompare(a.created_at)));
}

export function scopedLeads(c: Ctx, f: Row) {
  return byOrg(c.db.leads, c).filter((l) => matchLead(c.db, l, f));
}

function getLead(c: Ctx, leadId = c.params.id) {
  const l = c.db.leads.find((x) => x.id === leadId && x.organization_id === c.orgId);
  if (!l) throw notFound("lead");
  return l;
}

function normalizeWebsite(w: string) {
  w = String(w ?? "").trim();
  if (!w) return { website: "", domain: "" };
  const withScheme = /^https?:\/\//.test(w) ? w : `https://${w}`;
  try { return { website: withScheme, domain: new URL(withScheme).hostname.replace(/^www\./, "") }; } catch { return { website: "", domain: "" }; }
}

export function insertLead(c: Ctx, input: Row, extra: Row = {}) {
  const name = String(input.company_name ?? "").trim();
  if (!name) throw invalid("validation failed", { company_name: "is required" });
  const { website, domain } = normalizeWebsite(input.website ?? (input.domain ? input.domain : ""));
  const l: Row = {
    id: id(), organization_id: c.orgId, company_name: name, website, domain, industry: lc(input.industry).trim(), description: input.description ?? "",
    country: input.country ?? "", state: input.state ?? "", city: input.city ?? "", address: input.address ?? "", phone: input.phone ?? "",
    business_email: input.business_email || input.email || null, linkedin_url: input.linkedin_url ?? "", social_urls: input.social_urls ?? [],
    employee_count: input.employee_count ?? null, founded_year: input.founded_year ?? null, source: input.source ?? "manual", source_url: input.source_url ?? "",
    import_id: null, lead_score: 0, status: input.status ?? "new", owner_id: null, website_quality: input.website_quality ?? "", tech_stack: input.tech_stack ?? [],
    icp_fit: null, enriched_at: null, tags: [...new Set((input.tags ?? []) as string[])], latest_campaign: null, created_at: stamp(), updated_at: stamp(), ...extra,
  };
  c.db.leads.push(l);
  const contact = input.contact;
  if (contact && (contact.email || contact.first_name || contact.last_name)) addContact(c, l.id, { ...contact, source: l.source });
  recomputeLead(c.db, l);
  leadEvent(c, l.id, extra.import_id ? "imported" : input.source && input.source !== "manual" ? "discovered" : "created", { source: l.source });
  return l;
}

function addContact(c: Ctx, leadId: string, input: Row) {
  const email = input.email ? lc(input.email).trim() : null;
  const first = !c.db.contacts.some((x) => x.lead_id === leadId);
  const status = !email ? "unverified" : /@(example|test)\./.test(email) ? "valid" : email.endsWith(".invalid") ? "invalid" : "valid";
  const ct = { id: id(), organization_id: c.orgId, lead_id: leadId, first_name: input.first_name ?? "", last_name: input.last_name ?? "", job_title: input.job_title ?? input.title ?? "",
    email, email_status: status, email_verified_at: email ? stamp() : null, verification_details: email ? { provider: "basic" } : {}, linkedin_url: input.linkedin_url ?? "",
    source: input.source ?? "manual", confidence: input.confidence ?? 1, is_primary: first || !!input.is_primary, suppressed: false, created_at: stamp() };
  if (ct.is_primary) c.db.contacts.forEach((x) => { if (x.lead_id === leadId) x.is_primary = false; });
  c.db.contacts.push(ct);
  return ct;
}

// ───────────── leads ─────────────

route("GET", "/leads", "viewer", (c) => paginate(sortLeads(scopedLeads(c, filterFromQuery(c.query)), c.query.get("sort")).map((l) => leadView(c.db, l)), c.query));

route("GET", "/leads/facets", "viewer", (c) => {
  const field = c.query.get("field") ?? "industry";
  const counts = new Map<string, number>();
  for (const l of byOrg(c.db.leads, c)) {
    const vals = field === "tag" ? l.tags : [l[field]];
    for (const v of vals) if (v) counts.set(v, (counts.get(v) ?? 0) + 1);
  }
  return ok([...counts].map(([value, count]) => ({ value, count })).sort((a, b) => b.count - a.count || a.value.localeCompare(b.value)).slice(0, 200));
});

route("GET", "/leads/:id", "viewer", (c) => {
  const l = getLead(c);
  const icp = c.db.icps.find((i) => i.organization_id === c.orgId && i.is_default);
  const valid = c.db.contacts.some((x) => x.lead_id === l.id && x.email_status === "valid");
  const s = scoreLead(l, valid);
  return ok({ ...leadView(c.db, l), contacts: c.db.contacts.filter((x) => x.lead_id === l.id), score: { score: s.score, reasons: s.reasons, score_version: 1 },
    icp_fit_detail: icp ? icpFit(icp.criteria, l) : null });
});

route("POST", "/leads", "member", (c) => {
  const l = insertLead(c, { ...c.body, source: c.body.source || "manual" });
  audit(c, "lead.created", "lead", l.id);
  return created(leadView(c.db, l));
});

route("PUT", "/leads/:id", "member", (c) => {
  const l = getLead(c);
  const before = l.status;
  for (const k of ["company_name", "industry", "description", "country", "state", "city", "address", "phone", "business_email", "linkedin_url", "employee_count", "founded_year", "status", "tags"]) {
    if (k in c.body) l[k] = c.body[k];
  }
  if ("website" in c.body) Object.assign(l, normalizeWebsite(c.body.website));
  l.updated_at = stamp();
  recomputeLead(c.db, l);
  if (before !== l.status) leadEvent(c, l.id, "status_changed", { from: before, to: l.status });
  return ok(leadView(c.db, l));
});

route("DELETE", "/leads/:id", "member", (c) => {
  const l = getLead(c);
  c.db.leads = c.db.leads.filter((x) => x.id !== l.id);
  c.db.contacts = c.db.contacts.filter((x) => x.lead_id !== l.id);
  c.db.campaignLeads = c.db.campaignLeads.filter((x) => x.lead_id !== l.id);
  return noContent();
});

route("POST", "/leads/bulk", "member", (c) => {
  const { action, ids, filter, tags, status } = c.body;
  const targets = ids?.length ? byOrg(c.db.leads, c).filter((l) => ids.includes(l.id)) : scopedLeads(c, filter ?? {});
  for (const l of targets) {
    if (action === "add_tags") l.tags = [...new Set([...l.tags, ...(tags ?? [])])];
    if (action === "remove_tags") l.tags = l.tags.filter((t: string) => !(tags ?? []).includes(t));
    if (action === "set_status" && status) { leadEvent(c, l.id, "status_changed", { from: l.status, to: status }); l.status = status; }
    if (action === "assign") l.owner_id = c.body.owner_id ?? null;
    l.updated_at = stamp();
    recomputeLead(c.db, l);
  }
  if (action === "delete") {
    const del = new Set(targets.map((l) => l.id));
    c.db.leads = c.db.leads.filter((l) => !del.has(l.id));
    c.db.contacts = c.db.contacts.filter((x) => !del.has(x.lead_id));
  }
  return ok({ affected: targets.length });
});

route("GET", "/leads/:id/events", "viewer", (c) => {
  getLead(c);
  return ok(c.db.events.filter((e) => e.lead_id === c.params.id).sort((a, b) => b.created_at.localeCompare(a.created_at)));
});

route("GET", "/leads/:id/enrichment", "viewer", (c) => {
  const l = getLead(c);
  const cat = c.db.catalog.find((x) => x.name === l.company_name && x.city === l.city);
  return ok(cat ? { company_db: { company_id: cat.id, verification_status: cat.verification_status, verification: cat.verification, website: cat.website_report ?? {}, website_status: cat.website_status } } : {});
});

// ───────────── contacts ─────────────

route("POST", "/leads/:id/contacts", "member", (c) => { const l = getLead(c); const ct = addContact(c, l.id, c.body); recomputeLead(c.db, l); return created(ct); });
route("PUT", "/contacts/:id", "member", (c) => {
  const ct = byOrg(c.db.contacts, c).find((x) => x.id === c.params.id);
  if (!ct) throw notFound("contact");
  Object.assign(ct, { first_name: c.body.first_name ?? ct.first_name, last_name: c.body.last_name ?? ct.last_name, job_title: c.body.job_title ?? ct.job_title,
    email: c.body.email ? lc(c.body.email) : ct.email, linkedin_url: c.body.linkedin_url ?? ct.linkedin_url });
  return ok(ct);
});
route("DELETE", "/contacts/:id", "member", (c) => { c.db.contacts = c.db.contacts.filter((x) => !(x.id === c.params.id && x.organization_id === c.orgId)); return noContent(); });
route("POST", "/contacts/verify", "member", (c) => {
  const ids: string[] = c.body.contact_ids ?? [];
  for (const ct of byOrg(c.db.contacts, c).filter((x) => ids.includes(x.id) && x.email)) { ct.email_status = "valid"; ct.email_verified_at = stamp(); }
  return ok({ queued: ids.length });
});

// ───────────── notes, tasks, pipeline ─────────────

route("GET", "/leads/:id/notes", "viewer", (c) =>
  ok(c.db.notes.filter((n) => n.lead_id === c.params.id && n.organization_id === c.orgId).sort((a, b) => b.created_at.localeCompare(a.created_at))
    .map((n) => ({ id: n.id, body: n.body, created_at: n.created_at, author_name: c.db.users.find((u) => u.id === n.author_id)?.full_name ?? null }))));
route("POST", "/leads/:id/notes", "member", (c) => {
  const l = getLead(c);
  if (!String(c.body.body ?? "").trim()) throw invalid("validation failed", { body: "is required" });
  const n = { id: id(), organization_id: c.orgId, lead_id: l.id, author_id: c.userId, body: c.body.body, created_at: stamp() };
  c.db.notes.push(n);
  leadEvent(c, l.id, "note_added");
  return created({ ...n, author_name: c.user!.full_name });
});
route("DELETE", "/notes/:id", "member", (c) => { c.db.notes = c.db.notes.filter((n) => n.id !== c.params.id); return noContent(); });

function taskView(db: MockDB, t: Row) {
  return { id: t.id, lead_id: t.lead_id, company_name: db.leads.find((l) => l.id === t.lead_id)?.company_name ?? null, assignee_id: t.assignee_id,
    assignee_name: db.users.find((u) => u.id === t.assignee_id)?.full_name ?? null, title: t.title, notes: t.notes ?? "", due_at: t.due_at, completed_at: t.completed_at, created_at: t.created_at };
}
route("GET", "/tasks", "viewer", (c) => {
  const assignee = c.query.get("assignee"), lead = c.query.get("lead_id"), status = c.query.get("status");
  const rows = byOrg(c.db.tasks, c).filter((t) =>
    (!assignee || (assignee === "me" ? t.assignee_id === c.userId : t.assignee_id === assignee)) && (!lead || t.lead_id === lead) &&
    (status !== "open" || !t.completed_at) && (status !== "done" || t.completed_at));
  rows.sort((a, b) => Number(!!a.completed_at) - Number(!!b.completed_at) || String(a.due_at ?? "9").localeCompare(String(b.due_at ?? "9")));
  return ok(rows.map((t) => taskView(c.db, t)));
});
route("POST", "/tasks", "member", (c) => {
  if (!String(c.body.title ?? "").trim()) throw invalid("validation failed", { title: "is required" });
  const t = { id: id(), organization_id: c.orgId, lead_id: c.body.lead_id ?? null, assignee_id: c.body.assignee_id ?? c.userId, created_by: c.userId,
    title: c.body.title, notes: c.body.notes ?? "", due_at: c.body.due_at ?? null, completed_at: null, created_at: stamp() };
  c.db.tasks.push(t);
  return created(taskView(c.db, t));
});
route("PUT", "/tasks/:id", "member", (c) => {
  const t = byOrg(c.db.tasks, c).find((x) => x.id === c.params.id);
  if (!t) throw notFound("task");
  Object.assign(t, { title: c.body.title ?? t.title, notes: c.body.notes ?? t.notes, due_at: c.body.due_at ?? t.due_at, assignee_id: c.body.assignee_id ?? t.assignee_id,
    completed_at: c.body.completed === undefined ? t.completed_at : c.body.completed ? stamp() : null });
  return ok(taskView(c.db, t));
});
route("DELETE", "/tasks/:id", "member", (c) => { c.db.tasks = c.db.tasks.filter((t) => t.id !== c.params.id); return noContent(); });

const PIPELINE = ["replied", "interested", "qualified", "meeting", "won", "lost"];
route("GET", "/crm/pipeline", "viewer", (c) => {
  const owner = c.query.get("owner");
  const leads = byOrg(c.db.leads, c).filter((l) => owner !== "me" || l.owner_id === c.userId);
  return ok(PIPELINE.map((status) => {
    const col = leads.filter((l) => l.status === status).sort((a, b) => b.updated_at.localeCompare(a.updated_at));
    return { status, count: col.length, leads: col.slice(0, 50).map((l) => ({ id: l.id, company_name: l.company_name, city: l.city, lead_score: l.lead_score,
      owner_name: c.db.users.find((u) => u.id === l.owner_id)?.full_name ?? null, owner_id: l.owner_id,
      open_tasks: c.db.tasks.filter((t) => t.lead_id === l.id && !t.completed_at).length, updated_at: l.updated_at })) };
  }));
});
route("POST", "/crm/assign", "member", (c) => {
  for (const l of byOrg(c.db.leads, c).filter((x) => (c.body.lead_ids ?? []).includes(x.id))) { l.owner_id = c.body.owner_id ?? null; leadEvent(c, l.id, "owner_changed"); }
  return ok({ assigned: (c.body.lead_ids ?? []).length });
});
route("POST", "/crm/sync", "member", (c) => {
  const crms = byOrg(c.db.integrations, c).filter((i) => ["hubspot", "salesforce", "pipedrive", "zoho_crm"].includes(i.kind) && i.status !== "disabled");
  if (!crms.length) throw invalid("connect a CRM under Integrations first");
  for (const l of byOrg(c.db.leads, c).filter((x) => (c.body.lead_ids ?? []).includes(x.id))) leadEvent(c, l.id, "crm_synced", { provider: crms[0].kind });
  return { data: { queued: (c.body.lead_ids ?? []).length }, status: 202 };
});

// ───────────── CSV import ─────────────

const FIELD_ALIASES: Record<string, string> = {
  company: "company_name", company_name: "company_name", name: "company_name", business: "company_name", organisation: "company_name", organization: "company_name",
  website: "website", url: "website", domain: "website", industry: "industry", category: "industry", city: "city", town: "city", state: "state", country: "country",
  phone: "phone", telephone: "phone", mobile: "phone", email: "email", "e-mail": "email", first_name: "first_name", firstname: "first_name", last_name: "last_name",
  lastname: "last_name", title: "job_title", job_title: "job_title", address: "address", tags: "tags", description: "description",
};

function parseCSV(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [], cell = "", quoted = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (quoted) {
      if (ch === '"' && text[i + 1] === '"') { cell += '"'; i++; } else if (ch === '"') quoted = false; else cell += ch;
    } else if (ch === '"') quoted = true;
    else if (ch === ",") { row.push(cell); cell = ""; }
    else if (ch === "\n" || ch === "\r") { if (ch === "\r" && text[i + 1] === "\n") i++; row.push(cell); rows.push(row); row = []; cell = ""; }
    else cell += ch;
  }
  if (cell || row.length) { row.push(cell); rows.push(row); }
  return rows.filter((r) => r.some((v) => v.trim()));
}

function buildPreview(db: MockDB, imp: Row) {
  const nameCol = Object.entries(imp.mapping).find(([, f]) => f === "company_name")?.[0];
  const rows: Row[] = [], problems: Row[] = [];
  let valid = 0, invalidN = 0, dup = 0;
  imp.rows.forEach((r: string[], i: number) => {
    const values: Record<string, string> = {};
    for (const [col, field] of Object.entries(imp.mapping as Record<string, string>)) if (field) values[field] = r[Number(col)] ?? "";
    const errors: string[] = [];
    if (!nameCol || !String(values.company_name ?? "").trim()) errors.push("company_name is required");
    if (values.email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(values.email)) errors.push("email is not valid");
    const duplicate = db.leads.find((l) => l.organization_id === imp.organization_id && lc(l.company_name) === lc(values.company_name) && lc(l.city) === lc(values.city))?.company_name;
    const item = { row: i + 2, values, errors: errors.length ? errors : undefined, duplicate };
    if (errors.length) { invalidN++; problems.push(item); } else if (duplicate) { dup++; problems.push(item); } else valid++;
    if (rows.length < 20) rows.push(item);
  });
  Object.assign(imp, { total_rows: imp.rows.length, valid_rows: valid, invalid_rows: invalidN, duplicate_rows: dup, preview: { rows, problems: problems.slice(0, 50) }, status: "previewed" });
}

const importView = (imp: Row) => { const { rows: _r, ...rest } = imp; void _r; return rest; };

route("GET", "/imports", "viewer", (c) => ok(byOrg(c.db.imports, c).sort((a, b) => b.created_at.localeCompare(a.created_at)).map(importView)));
route("GET", "/imports/:id", "viewer", (c) => {
  const imp = byOrg(c.db.imports, c).find((x) => x.id === c.params.id);
  if (!imp) throw notFound("import");
  return ok(importView(imp));
});
route("POST", "/imports", "member", async (c) => {
  const file = c.form?.get("file");
  if (!(file instanceof File)) throw invalid("validation failed", { file: "choose a CSV file" });
  const rows = parseCSV(await file.text());
  if (rows.length < 2) throw invalid("the file has no data rows");
  const headers = rows[0].map((h) => h.trim());
  const mapping: Record<string, string> = {};
  headers.forEach((h, i) => { const f = FIELD_ALIASES[lc(h).replace(/[\s-]+/g, "_")]; if (f && !Object.values(mapping).includes(f)) mapping[String(i)] = f; });
  const imp: Row = { id: id(), organization_id: c.orgId, filename: file.name, status: "uploaded", mapping, headers, source: String(c.form?.get("source") || "csv"),
    consent_basis: String(c.form?.get("consent_basis") || "legitimate_interest"), total_rows: rows.length - 1, valid_rows: 0, invalid_rows: 0, duplicate_rows: 0,
    imported_rows: 0, preview: null, error: null, created_at: stamp(), rows: rows.slice(1, 50001) };
  buildPreview(c.db, imp);
  c.db.imports.push(imp);
  return created(importView(imp));
});
route("PUT", "/imports/:id/mapping", "member", (c) => {
  const imp = byOrg(c.db.imports, c).find((x) => x.id === c.params.id);
  if (!imp) throw notFound("import");
  Object.assign(imp, { mapping: c.body.mapping ?? imp.mapping, source: c.body.source ?? imp.source, consent_basis: c.body.consent_basis ?? imp.consent_basis });
  buildPreview(c.db, imp);
  return ok(importView(imp));
});
route("POST", "/imports/:id/commit", "member", (c) => {
  const imp = byOrg(c.db.imports, c).find((x) => x.id === c.params.id);
  if (!imp) throw notFound("import");
  let n = 0;
  for (const r of imp.rows as string[][]) {
    const v: Row = {};
    for (const [col, field] of Object.entries(imp.mapping as Record<string, string>)) if (field) v[field] = (r[Number(col)] ?? "").trim();
    if (!v.company_name) continue;
    if (c.db.leads.some((l) => l.organization_id === c.orgId && lc(l.company_name) === lc(v.company_name) && lc(l.city) === lc(v.city))) continue;
    insertLead(c, { ...v, source: imp.source, tags: v.tags ? String(v.tags).split(/[;,]/).map((t) => t.trim()).filter(Boolean) : [],
      contact: v.email || v.first_name ? { email: v.email, first_name: v.first_name, last_name: v.last_name, job_title: v.job_title } : undefined }, { import_id: imp.id });
    n++;
  }
  Object.assign(imp, { status: "completed", imported_rows: n, rows: [] });
  audit(c, "import.completed", "import", imp.id, { imported: n });
  return ok(importView(imp));
});

export {};
