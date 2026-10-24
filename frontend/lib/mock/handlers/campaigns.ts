 
import { icpFit, type MockDB, type Row } from "../db";
import { onReplyClassified } from "./deals";
import { insertLead, scopedLeads } from "./leads";
import {
  audit, byOrg, conflict, created, id, invalid, leadEvent, noContent, notFound, ok, paginate, route, stamp, type Ctx,
} from "../server";

const getCampaign = (c: Ctx, cid = c.params.id) => {
  const x = byOrg(c.db.campaigns, c).find((k) => k.id === cid);
  if (!x) throw notFound("campaign");
  return x;
};
const steps = (db: MockDB, cid: string) => db.steps.filter((s) => s.campaign_id === cid).sort((a, b) => a.step_number - b.step_number);

export function campaignSummary(db: MockDB, cid: string) {
  const cl = db.campaignLeads.filter((x) => x.campaign_id === cid);
  const sent = cl.reduce((n, x) => n + (x.sent_steps ?? 0), 0);
  return { leads: cl.length, active: cl.filter((x) => ["active", "queued"].includes(x.status)).length, sent,
    replied: cl.filter((x) => x.status === "replied").length, bounced: cl.filter((x) => x.status === "bounced").length };
}
function view(db: MockDB, x: Row, withSteps = false) {
  const { organization_id: _o, ...rest } = x;
  void _o;
  return { ...rest, summary: campaignSummary(db, x.id), ...(withSteps ? { steps: steps(db, x.id) } : {}) };
}

const DEFAULTS = {
  daily_limit: 100, start_at: null, timezone: "Asia/Kolkata", send_window_start: "09:00", send_window_end: "17:00", send_days: [1, 2, 3, 4, 5],
  allowed_email_statuses: ["valid"], max_bounce_rate: 0.05, stop_on_reply: true, mailbox_ids: [], ai_personalization: false, personalization_brief: "",
  require_personalization_approval: true,
};
const SETTINGS = Object.keys(DEFAULTS).concat("name", "mailbox_id");

route("GET", "/campaigns", "viewer", (c) =>
  paginate(byOrg(c.db.campaigns, c).sort((a, b) => b.created_at.localeCompare(a.created_at)).map((x) => view(c.db, x)), c.query, 200));
route("POST", "/campaigns", "member", (c) => {
  if (!String(c.body.name ?? "").trim()) throw invalid("validation failed", { name: "is required" });
  const mb = byOrg(c.db.mailboxes, c)[0];
  const x: Row = { id: id(), organization_id: c.orgId, ...DEFAULTS, name: c.body.name.trim(), mailbox_id: c.body.mailbox_id ?? mb?.id ?? null,
    mailbox_ids: c.body.mailbox_ids ?? (mb ? [mb.id] : []), status: "draft", paused_reason: null, launched_at: null, completed_at: null, created_at: stamp() };
  for (const k of SETTINGS) if (k in c.body && c.body[k] !== undefined) x[k] = c.body[k];
  c.db.campaigns.push(x);
  audit(c, "campaign.created", "campaign", x.id);
  return created(view(c.db, x, true));
});
route("GET", "/campaigns/:id", "viewer", (c) => ok(view(c.db, getCampaign(c), true)));
route("PUT", "/campaigns/:id", "member", (c) => {
  const x = getCampaign(c);
  for (const k of SETTINGS) if (k in c.body) x[k] = c.body[k];
  return ok(view(c.db, x, true));
});
route("DELETE", "/campaigns/:id", "member", (c) => {
  const x = getCampaign(c);
  if (x.status === "running") throw conflict("campaign_running", "pause or cancel the campaign before deleting it");
  c.db.campaigns = c.db.campaigns.filter((k) => k.id !== x.id);
  c.db.campaignLeads = c.db.campaignLeads.filter((k) => k.campaign_id !== x.id);
  return noContent();
});

route("GET", "/campaigns/:id/steps", "viewer", (c) => ok(steps(c.db, getCampaign(c).id)));
route("PUT", "/campaigns/:id/steps", "member", (c) => {
  const x = getCampaign(c);
  const list = (c.body.steps ?? []) as Row[];
  list.forEach((s, i) => { if (!String(s.subject_template ?? "").trim() && i === 0) throw invalid("validation failed", { [`steps[${i}].subject_template`]: "is required" }); });
  c.db.steps = c.db.steps.filter((s) => s.campaign_id !== x.id);
  const saved = list.map((s, i) => ({ id: id(), campaign_id: x.id, step_number: i + 1, delay_days: Number(s.delay_days) || 0, subject_template: s.subject_template ?? "", body_template: s.body_template ?? "" }));
  c.db.steps.push(...saved);
  return ok(saved);
});

function render(tpl: string, vars: Record<string, string>, empty: Set<string>) {
  return tpl.replace(/\{\{\s*([a-z_]+)(?:\|([^}]*))?\s*\}\}/g, (_, k, fallback) => {
    const v = vars[k];
    if (v) return v;
    if (fallback !== undefined) return fallback;
    empty.add(k);
    return "";
  });
}
route("POST", "/campaigns/:id/preview", "viewer", (c) => {
  const x = getCampaign(c);
  const st = steps(c.db, x.id).find((s) => s.step_number === (c.body.step_number ?? 1));
  const cl = c.db.campaignLeads.find((k) => k.campaign_id === x.id);
  const lead = (cl ? c.db.leads.find((l) => l.id === cl.lead_id) : null) ?? byOrg(c.db.leads, c).find((l) => c.db.contacts.some((ct) => ct.lead_id === l.id && ct.email));
  const contact = lead ? c.db.contacts.find((ct) => ct.lead_id === lead.id && ct.email) : null;
  const mb = c.db.mailboxes.find((m) => m.id === (x.mailbox_ids?.[0] ?? x.mailbox_id)) ?? byOrg(c.db.mailboxes, c)[0];
  const p = lead ? c.db.personalizations.find((k) => k.campaign_id === x.id && k.lead_id === lead.id && k.status === "approved") : null;
  const vars: Record<string, string> = {
    company_name: lead?.company_name ?? "Acme Cafe", first_name: contact?.first_name ?? "", last_name: contact?.last_name ?? "", city: lead?.city ?? "",
    industry: lead?.industry ?? "", website: lead?.website ?? "", job_title: contact?.job_title ?? "", sender_name: mb?.from_name ?? c.user!.full_name,
    sender_company: c.org!.legal_name || c.org!.name, personalized_line: p?.content ?? "",
  };
  const empty = new Set<string>();
  const subject = render(c.body.subject_template ?? st?.subject_template ?? "", vars, empty);
  const text = render(c.body.body_template ?? st?.body_template ?? "", vars, empty) +
    `\n\n—\n${c.org!.legal_name || c.org!.name} · ${c.org!.physical_address || "(set your postal address in Settings)"}\nUnsubscribe: https://app.leadline.test/u/…`;
  return ok({ subject, text, html: `<div style="font-family:sans-serif;white-space:pre-wrap">${text.replace(/</g, "&lt;")}</div>`,
    empty_variables: [...empty].filter((k) => k !== "personalized_line"), to: contact?.email ?? "", from: mb ? `${mb.from_name} <${mb.from_email}>` : "", company_name: vars.company_name });
});

// ───────────── audience ─────────────

function campaignLeadView(db: MockDB, x: Row) {
  const l = db.leads.find((k) => k.id === x.lead_id);
  const ct = db.contacts.find((k) => k.id === x.contact_id);
  return { id: x.id, lead_id: x.lead_id, company_name: l?.company_name ?? "", contact_name: ct ? `${ct.first_name} ${ct.last_name}`.trim() : "", email: ct?.email ?? "",
    email_status: ct?.email_status ?? "unverified", status: x.status, next_step_number: x.next_step_number, next_send_at: x.next_send_at, last_sent_at: x.last_sent_at, stop_reason: x.stop_reason };
}
route("GET", "/campaigns/:id/leads", "viewer", (c) => {
  const x = getCampaign(c);
  const status = c.query.get("status"), q = (c.query.get("q") ?? "").toLowerCase();
  const rows = c.db.campaignLeads.filter((k) => k.campaign_id === x.id && (!status || k.status === status)).map((k) => campaignLeadView(c.db, k))
    .filter((k) => !q || `${k.company_name} ${k.email} ${k.contact_name}`.toLowerCase().includes(q));
  return paginate(rows, c.query);
});
const PERSONAL = new Set(["gmail.com", "googlemail.com", "yahoo.com", "yahoo.in", "outlook.com", "hotmail.com", "live.com", "icloud.com", "proton.me", "rediffmail.com"]);
const title = (s: string) => s.split(/[.\-_ ]+/).filter(Boolean).map((w) => w[0].toUpperCase() + w.slice(1)).join(" ");

route("POST", "/campaigns/:id/leads", "member", (c) => {
  const x = getCampaign(c);
  const b = c.body;
  const dry = !!b.dry_run;
  const res = { requested: 0, added: 0, skipped: 0, already_in_campaign: 0, no_email: 0, suppressed: 0, created: 0, invalid_emails: [] as string[], dry_run: dry };
  const ids = new Set<string>();
  if (b.lead_ids?.length) byOrg(c.db.leads, c).filter((l) => b.lead_ids.includes(l.id)).forEach((l) => ids.add(l.id));
  else if (b.filter && !b.icp_id) scopedLeads(c, b.filter).forEach((l) => ids.add(l.id));
  if (b.icp_id) {
    const icp = byOrg(c.db.icps, c).find((i) => i.id === b.icp_id);
    if (!icp) throw notFound("ICP");
    const minFit = Math.max(0, Math.min(100, b.min_fit ?? 70));
    scopedLeads(c, b.filter ?? {}).filter((l) => (icpFit(icp.criteria, l)?.fit ?? 0) >= minFit).forEach((l) => ids.add(l.id));
  }
  for (const ct of (b.contacts ?? []) as Row[]) {
    const email = String(ct.email ?? "").trim().toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) { if (String(ct.email ?? "").trim()) res.invalid_emails.push(String(ct.email).trim()); continue; }
    const existing = c.db.contacts.find((k) => k.organization_id === c.orgId && k.email === email)?.lead_id ?? byOrg(c.db.leads, c).find((l) => l.business_email === email)?.id;
    if (existing) { ids.add(existing); continue; }
    if ([...ids].some((i) => c.db.contacts.some((k) => k.lead_id === i && k.email === email))) continue;
    res.created++;
    if (dry) continue;
    const [local, domain] = email.split("@");
    const company = ct.company || (!PERSONAL.has(domain) ? title(domain.split(".")[0]) : "") || [ct.first_name, ct.last_name].filter(Boolean).join(" ") || title(local);
    const l = insertLead(c, { company_name: company, website: PERSONAL.has(domain) ? "" : domain, source: "manual", tags: ["added-to-campaign"],
      contact: { email, first_name: ct.first_name || (!/\d/.test(local) && !["info", "hello", "contact", "sales"].includes(local) ? title(local.split(".")[0]) : ""), last_name: ct.last_name ?? "" } });
    ids.add(l.id);
  }
  const suppressed = new Set(byOrg(c.db.suppressions, c).map((s) => s.email));
  for (const leadId of ids) {
    const l = c.db.leads.find((q) => q.id === leadId)!;
    if (c.db.campaignLeads.some((k) => k.campaign_id === x.id && k.lead_id === leadId)) { res.already_in_campaign++; continue; }
    const ct = c.db.contacts.filter((k) => k.lead_id === leadId && k.email && !["invalid", "disposable"].includes(k.email_status))
      .sort((a, z) => Number(z.email_status === "valid") - Number(a.email_status === "valid"))[0];
    const email = ct?.email ?? l.business_email;
    if (!email || l.status === "unsubscribed") { res.no_email++; continue; }
    if (suppressed.has(email)) { res.suppressed++; continue; }
    res.added++;
    if (dry) continue;
    c.db.campaignLeads.push({ id: id(), organization_id: c.orgId, campaign_id: x.id, lead_id: leadId, contact_id: ct?.id ?? null, status: x.status === "running" ? "active" : "queued",
      next_step_number: 1, next_send_at: null, last_sent_at: null, stop_reason: null, sent_steps: 0, mailbox_id: x.mailbox_ids?.[0] ?? x.mailbox_id, created_at: stamp() });
  }
  if (!ids.size && !res.created && !b.lead_ids && !b.filter && !b.icp_id && !b.contacts) throw invalid("validation failed", { lead_ids: "pick leads, a filter, an ICP or add email addresses" });
  if (dry) res.added += res.created;
  res.requested = ids.size + (dry ? res.created : 0);
  res.skipped = res.requested - res.added;
  return ok(res);
});
route("POST", "/campaigns/:id/leads/remove", "member", (c) => {
  const x = getCampaign(c);
  const ids: string[] = c.body.lead_ids ?? [];
  const before = c.db.campaignLeads.length;
  c.db.campaignLeads = c.db.campaignLeads.filter((k) => !(k.campaign_id === x.id && (ids.includes(k.lead_id) || ids.includes(k.id))));
  return ok({ removed: before - c.db.campaignLeads.length });
});

// ───────────── lifecycle ─────────────

route("POST", "/campaigns/:id/:action", "member", (c) => {
  const x = getCampaign(c);
  const action = c.params.action;
  if (action === "launch") {
    const problems: Record<string, string> = {};
    if (!steps(c.db, x.id).length) problems.steps = "add at least one email step";
    if (!c.db.campaignLeads.some((k) => k.campaign_id === x.id)) problems.audience = "add leads to the audience";
    if (!(x.mailbox_ids?.length || x.mailbox_id)) problems.mailbox = "choose a sending mailbox";
    if (!String(c.org!.physical_address ?? "").trim()) problems.physical_address = "set your organization's postal address in Settings (required in every email footer)";
    if (Object.keys(problems).length) throw invalid("the campaign isn't ready to launch", problems);
    Object.assign(x, { status: "running", launched_at: stamp(), paused_reason: null });
    // Demo: the first email goes out to everyone straight away.
    for (const k of c.db.campaignLeads.filter((k) => k.campaign_id === x.id && ["queued", "active"].includes(k.status))) {
      Object.assign(k, { status: "active", sent_steps: Math.max(1, k.sent_steps ?? 0), last_sent_at: stamp(), next_step_number: 2,
        next_send_at: new Date(Date.now() + 3 * 864e5).toISOString() });
      const l = c.db.leads.find((q) => q.id === k.lead_id);
      if (l) { if (l.status === "new") l.status = "contacted"; l.latest_campaign = x.name; leadEvent(c, l.id, "email_sent", { campaign: x.name, step: 1 }); }
    }
    const today = new Date().toISOString().slice(0, 10);
    const d = c.db.daily.find((q) => q.organization_id === c.orgId && q.date === today);
    const n = c.db.campaignLeads.filter((k) => k.campaign_id === x.id).length;
    if (d) d.sent += n; else c.db.daily.push({ organization_id: c.orgId, date: today, sent: n, bounced: 0, replied: 0 });
  } else if (action === "pause") {
    if (x.status !== "running") throw conflict("not_running", "only a running campaign can be paused");
    Object.assign(x, { status: "paused", paused_reason: `Paused by ${c.user!.full_name}` });
  } else if (action === "resume") {
    if (x.status !== "paused") throw conflict("not_paused", "only a paused campaign can be resumed");
    Object.assign(x, { status: "running", paused_reason: null });
  } else if (action === "cancel") {
    Object.assign(x, { status: "cancelled", completed_at: stamp() });
  } else throw notFound("action");
  audit(c, `campaign.${action === "launch" ? "launched" : action + (action.endsWith("e") ? "d" : "ed")}`, "campaign", x.id);
  return ok(view(c.db, x, true));
});

// ───────────── personalization ─────────────

function personalizationView(db: MockDB, p: Row) {
  const l = db.leads.find((k) => k.id === p.lead_id);
  const ct = db.contacts.find((k) => k.lead_id === p.lead_id && k.email);
  return { id: p.id, lead_id: p.lead_id, company_name: l?.company_name ?? "", contact_name: ct ? `${ct.first_name} ${ct.last_name}`.trim() : "", content: p.content, status: p.status, error: null };
}
route("GET", "/campaigns/:id/personalizations", "viewer", (c) => {
  const x = getCampaign(c);
  const status = c.query.get("status");
  const all = c.db.personalizations.filter((p) => p.campaign_id === x.id);
  const counts: Record<string, number> = {};
  all.forEach((p) => (counts[p.status] = (counts[p.status] ?? 0) + 1));
  const res = paginate(all.filter((p) => !status || p.status === status).map((p) => personalizationView(c.db, p)), c.query);
  return { ...res, meta: { ...res.meta, counts } };
});
route("POST", "/campaigns/:id/personalizations/generate", "member", (c) => {
  const x = getCampaign(c);
  let n = 0;
  for (const k of c.db.campaignLeads.filter((q) => q.campaign_id === x.id)) {
    const existing = c.db.personalizations.find((p) => p.campaign_id === x.id && p.lead_id === k.lead_id);
    if (existing && (existing.status === "approved" || !c.body.regenerate)) continue;
    const l = c.db.leads.find((q) => q.id === k.lead_id)!;
    const line = !l.website ? `I couldn't find a website for ${l.company_name} — customers in ${l.city} search on Google before they visit.`
      : l.website_quality === "poor" ? `${l.company_name}'s site looks great on desktop, but it's hard to use on a phone.`
        : `Loved seeing what ${l.company_name} is doing for ${l.industry} customers in ${l.city}.`;
    const row = existing ?? { id: id(), organization_id: c.orgId, campaign_id: x.id, lead_id: k.lead_id };
    Object.assign(row, { content: line, status: x.require_personalization_approval ? "generated" : "approved" });
    if (!existing) c.db.personalizations.push(row);
    n++;
  }
  return { data: { queued: n }, status: 202 };
});
route("POST", "/campaigns/:id/personalizations/approve-all", "member", (c) => {
  const x = getCampaign(c);
  let n = 0;
  c.db.personalizations.filter((p) => p.campaign_id === x.id && p.status === "generated").forEach((p) => { p.status = "approved"; n++; });
  return ok({ approved: n });
});
route("PUT", "/personalizations/:id", "member", (c) => {
  const p = byOrg(c.db.personalizations, c).find((x) => x.id === c.params.id);
  if (!p) throw notFound("personalization");
  Object.assign(p, { content: c.body.content ?? p.content, status: c.body.status ?? p.status });
  return ok(personalizationView(c.db, p));
});

// ───────────── inbox & events ─────────────

const POSITIVE = ["positive", "interested"];
route("GET", "/inbox", "viewer", (c) => {
  const cls = c.query.get("classification"), status = c.query.get("status"), kind = c.query.get("kind");
  const rows = byOrg(c.db.inbox, c).filter((m) => (!cls || m.classification === cls) && (!status || m.status === status) && (!kind || m.kind === kind))
    .sort((a, b) => b.received_at.localeCompare(a.received_at)).map(({ organization_id: _o, ...m }) => { void _o; return m; });
  const res = paginate(rows, c.query, 100);
  return { ...res, meta: { ...res.meta, classifications: ["positive", "interested", "question", "not_now", "not_interested", "unsubscribe", "wrong_person", "out_of_office", "spam", "unknown"] } };
});
route("PATCH", "/inbox/:id", "member", (c) => {
  const m = byOrg(c.db.inbox, c).find((x) => x.id === c.params.id);
  if (!m) throw notFound("message");
  if (c.body.status) m.status = c.body.status;
  if (c.body.classification) {
    m.classification = c.body.classification; m.classified_by = "manual";
    const l = c.db.leads.find((x) => x.id === m.lead_id);
    if (l && POSITIVE.includes(m.classification) && ["new", "contacted", "replied"].includes(l.status)) { l.status = "interested"; leadEvent(c, l.id, "status_changed", { to: "interested" }); }
    if (l && m.classification === "unsubscribe") {
      l.status = "unsubscribed";
      c.db.suppressions.push({ id: id(), organization_id: c.orgId, email: m.from_email, reason: "unsubscribed", source: "reply", created_at: stamp() });
    }
  }
  if (c.body.classification && m.lead_id) onReplyClassified(c, m.lead_id, m.classification, `${m.id}:${m.classification}`);
  const { organization_id: _o, ...rest } = m;
  void _o;
  return ok(rest);
});
route("POST", "/email-events", "member", (c) => {
  const k = c.db.campaignLeads.find((x) => x.id === c.body.campaign_lead_id && x.organization_id === c.orgId);
  if (!k) throw notFound("campaign lead");
  k.status = "replied"; k.stop_reason = "replied";
  const l = c.db.leads.find((x) => x.id === k.lead_id)!;
  const ct = c.db.contacts.find((x) => x.id === k.contact_id);
  const camp = c.db.campaigns.find((x) => x.id === k.campaign_id);
  const cls = c.body.classification || "unknown";
  l.status = POSITIVE.includes(cls) ? "interested" : cls === "unsubscribe" ? "unsubscribed" : "replied";
  c.db.inbox.push({ id: id(), organization_id: c.orgId, lead_id: l.id, company_name: l.company_name, campaign_id: k.campaign_id, campaign_name: camp?.name ?? null,
    from_email: ct?.email ?? "", from_name: ct ? `${ct.first_name} ${ct.last_name}` : "", subject: "Re: (recorded manually)", body_text: c.body.note || "Reply recorded manually.",
    received_at: stamp(), kind: "reply", classification: cls, confidence: 1, summary: "", classified_by: "manual", status: "new" });
  leadEvent(c, l.id, "replied", { classification: cls });
  onReplyClassified(c, l.id, cls, c.db.inbox[c.db.inbox.length - 1].id);
  return created({ ok: true });
});

export {};
