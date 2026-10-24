/* eslint-disable @typescript-eslint/no-explicit-any */
// Demo-mode deals pipeline and workflow engine, mirroring internal/deals and
// internal/workflows. Workflows run synchronously in the browser.
import { DEMO_ORG, iso, saveDB, type MockDB, type Row } from "../db";
import { audit, byOrg, conflict, created, id, invalid, leadEvent, noContent, notFound, ok, route, stamp, type Ctx } from "../server";

// ───────────── setup ─────────────

const DEFAULT_STAGES = [
  { name: "Engaged", probability: 10, kind: "open", stale_days: 7 },
  { name: "Discovery call", probability: 20, kind: "open", stale_days: 7 },
  { name: "Qualified", probability: 40, kind: "open", stale_days: 10 },
  { name: "Proposal sent", probability: 60, kind: "open", stale_days: 7 },
  { name: "Negotiation", probability: 80, kind: "open", stale_days: 10 },
  { name: "Won", probability: 100, kind: "won", stale_days: null },
  { name: "Lost", probability: 0, kind: "lost", stale_days: null },
];

const daysAgo = (n: number) => iso(new Date(Date.now() - n * 86_400_000));
const daysBetween = (from: string) => Math.max(0, Math.floor((Date.now() - new Date(from).getTime()) / 86_400_000));

function ensure(c: Ctx) {
  const db = c.db;
  db.pipelines ??= []; db.deals ??= []; db.dealEvents ??= []; db.workflows ??= []; db.workflowRuns ??= [];
  if (!c.orgId || db.pipelines.some((p) => p.organization_id === c.orgId)) return;
  const pid = id();
  db.pipelines.push({ id: pid, organization_id: c.orgId, name: "Sales pipeline", is_default: true, created_at: stamp(),
    stages: DEFAULT_STAGES.map((s, i) => ({ ...s, id: id(), position: i })) });
  if (c.orgId === DEMO_ORG) seedDemo(db, c.orgId);
  saveDB(); // may run inside a GET, which the router doesn't persist
}

/** A realistic demo pipeline built from the demo leads' statuses. */
function seedDemo(db: MockDB, org: string) {
  const pipe = db.pipelines!.find((p) => p.organization_id === org)!;
  const st = (name: string) => pipe.stages.find((s: Row) => s.name === name)!;
  const leads = db.leads.filter((l) => l.organization_id === org);
  const plan: [string, string, number][] = [];
  const byStatus = (s: string, n: number) => leads.filter((l) => l.status === s).slice(0, n);
  // Mostly fresh deals with a few stuck ones, plus recent wins and losses.
  byStatus("interested", 8).forEach((l, i) => plan.push([l.id, i % 2 ? "Engaged" : "Discovery call", i === 3 ? 9 : 1 + (i % 4)]));
  byStatus("qualified", 6).forEach((l, i) => plan.push([l.id, i % 2 ? "Qualified" : "Proposal sent", i === 2 ? 12 : 2 + i]));
  byStatus("meeting", 5).forEach((l, i) => plan.push([l.id, i % 2 ? "Negotiation" : "Proposal sent", 1 + i]));
  byStatus("won", 4).forEach((l, i) => plan.push([l.id, "Won", 3 + i * 6]));
  byStatus("lost", 3).forEach((l, i) => plan.push([l.id, "Lost", 6 + i * 7]));
  const values = [45000, 120000, 75000, 30000, 250000, 60000, 90000, 150000, 38000, 180000];
  const reasons = ["Price", "Timing", "Went with a competitor", "No budget"];
  plan.forEach(([leadId, stageName, ago], i) => {
    const l = leads.find((x) => x.id === leadId)!;
    const s = st(stageName);
    const status = s.kind;
    const createdAt = daysAgo(ago + 6);
    const d: Row = {
      id: id(), organization_id: org, pipeline_id: pipe.id, stage_id: s.id, lead_id: l.id, name: `${l.company_name} — website`,
      value: values[i % values.length], currency: "INR", expected_close: status === "open" ? daysAgo(-(7 + (i % 5) * 6)).slice(0, 10) : null,
      owner_id: l.owner_id, status, lost_reason: status === "lost" ? reasons[i % reasons.length] : "", next_step: status === "open" && i % 3 === 0 ? "Share revised quote" : "",
      next_step_at: null, source: i % 4 === 0 ? "workflow" : "manual", stage_entered_at: daysAgo(ago), last_activity_at: daysAgo(Math.max(0, ago - 1)),
      closed_at: status === "open" ? null : daysAgo(ago), created_at: createdAt, updated_at: daysAgo(ago),
    };
    db.deals!.push(d);
    db.dealEvents!.push({ id: ++db.seq, organization_id: org, deal_id: d.id, event_type: "created", actor_id: d.source === "workflow" ? null : d.owner_id,
      data: { value: d.value, ...(d.source === "workflow" ? { workflow: "Interested reply → deal + discovery call" } : {}) }, created_at: createdAt });
    if (stageName !== "Engaged") {
      db.dealEvents!.push({ id: ++db.seq, organization_id: org, deal_id: d.id, event_type: "stage_changed", actor_id: d.owner_id,
        data: { from: "Engaged", to: stageName, lost_reason: d.lost_reason }, created_at: d.stage_entered_at });
    }
  });
  for (const key of ["interested_reply", "proposal_follow_up", "stuck_deal_alert", "won_onboarding"]) {
    const t = TEMPLATES.find((x) => x.key === key)!;
    const w = workflowFromTemplate(db, org, t, null);
    // Stuck-deal runs come from the simulated scanner, against the real seeded deals.
    w.run_count = key === "interested_reply" ? 9 : key === "won_onboarding" ? 3 : key === "stuck_deal_alert" ? 0 : 4;
    w.last_run_at = w.run_count ? daysAgo(key === "interested_reply" ? 1 : 3) : null;
    w.created_at = daysAgo(40);
    db.workflows!.push(w);
    const sample = db.deals!.filter((d) => d.organization_id === org).slice(0, w.run_count);
    sample.forEach((d, i) => db.workflowRuns!.push({
      id: ++db.seq, organization_id: org, workflow_id: w.id, event_key: `seed:${w.id}:${i}`, trigger: w.trigger, lead_id: d.lead_id, deal_id: d.id,
      subject: d.name, status: "succeeded", created_at: daysAgo(1 + i * 3),
      results: w.actions.map((a: Row) => ({ type: a.type, status: a.type === "sync_crm" ? "skipped" : "ok", message: a.type === "sync_crm" ? "no CRM connected" : actionSummary(a, d.name) })),
    }));
  }
}

function actionSummary(a: Row, subject: string) {
  const p = a.params ?? {};
  const fill = (t: string) => String(t ?? "").replaceAll("{{company}}", subject.replace(/ — website$/, "")).replaceAll("{{deal}}", subject);
  switch (a.type) {
    case "create_task": return `task "${fill(p.title)}"`;
    case "create_deal": return `opened deal "${subject}" in Engaged`;
    case "notify": return "emailed the owner";
    case "add_tag": return `tagged ${p.tag}`;
    default: return a.type.replaceAll("_", " ");
  }
}

// ───────────── shaping ─────────────

function stageById(db: MockDB, stageId: string): { stage: Row; pipeline: Row } | null {
  for (const p of db.pipelines!) {
    const s = p.stages.find((x: Row) => x.id === stageId);
    if (s) return { stage: s, pipeline: p };
  }
  return null;
}

function dealView(db: MockDB, d: Row): Row {
  const s = stageById(db, d.stage_id)?.stage ?? { name: "?", probability: 0, stale_days: null };
  const lead = d.lead_id ? db.leads.find((l) => l.id === d.lead_id) : null;
  const days = daysBetween(d.stage_entered_at);
  return {
    ...d, organization_id: undefined, stage_name: s.name, probability: s.probability, company_name: lead?.company_name ?? "", city: lead?.city ?? "",
    owner_name: db.users.find((u) => u.id === d.owner_id)?.full_name ?? null, days_in_stage: days,
    stale: d.status === "open" && s.stale_days != null && days >= s.stale_days,
    open_tasks: d.lead_id ? db.tasks.filter((t) => t.lead_id === d.lead_id && !t.completed_at).length : 0,
  };
}

function pipelineView(p: Row) {
  return { id: p.id, name: p.name, is_default: p.is_default, created_at: p.created_at, stages: [...p.stages].sort((a, b) => a.position - b.position) };
}

function dealEvent(c: Ctx, dealId: string, event_type: string, data: Row, workflow?: string) {
  c.db.dealEvents!.push({ id: ++c.db.seq, organization_id: c.orgId, deal_id: dealId, event_type, actor_id: workflow ? null : c.userId,
    data: workflow ? { ...data, workflow } : data, created_at: stamp() });
}

// ───────────── pipelines ─────────────

function validatePipeline(body: Row) {
  const name = String(body.name ?? "").trim();
  const stages: Row[] = body.stages ?? [];
  const errs: Record<string, string> = {};
  if (!name) errs.name = "is required";
  const kinds = stages.map((s) => s.kind || "open");
  if (!kinds.includes("open") || kinds.filter((k) => k === "won").length !== 1 || kinds.filter((k) => k === "lost").length !== 1) {
    errs.stages = "a pipeline needs at least one open stage, exactly one Won stage and exactly one Lost stage";
  }
  if (stages.some((s) => !String(s.name ?? "").trim())) errs.stages = "every stage needs a name";
  if (Object.keys(errs).length) throw invalid("validation failed", errs);
  return { name, stages: stages.map((s, i) => ({ id: s.id || id(), name: String(s.name).trim(), position: i, kind: s.kind || "open",
    probability: s.kind === "won" ? 100 : s.kind === "lost" ? 0 : Math.max(0, Math.min(100, Number(s.probability) || 0)), stale_days: s.stale_days || null })) };
}

route("GET", "/pipelines", "viewer", (c) => { ensure(c); return ok(byOrg(c.db.pipelines!, c).map(pipelineView)); });
route("POST", "/pipelines", "admin", (c) => {
  ensure(c);
  const v = validatePipeline({ ...c.body, stages: c.body.stages?.length ? c.body.stages : DEFAULT_STAGES });
  const p = { id: id(), organization_id: c.orgId, is_default: false, created_at: stamp(), ...v };
  c.db.pipelines!.push(p);
  audit(c, "pipeline.created", "pipeline", p.id);
  return created(pipelineView(p));
});
route("PUT", "/pipelines/:id", "admin", (c) => {
  ensure(c);
  const p = byOrg(c.db.pipelines!, c).find((x) => x.id === c.params.id);
  if (!p) throw notFound("pipeline");
  const v = validatePipeline(c.body);
  const keep = new Set(v.stages.map((s) => s.id));
  if (c.db.deals!.some((d) => d.pipeline_id === p.id && !keep.has(d.stage_id))) throw conflict("stage_in_use", "move the deals out of a stage before removing it");
  Object.assign(p, v);
  return ok(pipelineView(p));
});
route("DELETE", "/pipelines/:id", "admin", (c) => {
  ensure(c);
  const p = byOrg(c.db.pipelines!, c).find((x) => x.id === c.params.id);
  if (!p) throw notFound("pipeline");
  if (p.is_default) throw conflict("default_pipeline", "the default pipeline can't be deleted");
  if (c.db.deals!.some((d) => d.pipeline_id === p.id)) throw conflict("pipeline_in_use", "move or delete this pipeline's deals first");
  c.db.pipelines = c.db.pipelines!.filter((x) => x.id !== p.id);
  return noContent();
});

// ───────────── deals ─────────────

function listDeals(c: Ctx, q: URLSearchParams = c.query) {
  const owner = q.get("owner"), text = (q.get("q") ?? "").toLowerCase();
  return byOrg(c.db.deals!, c).filter((d) =>
    (!q.get("pipeline_id") || d.pipeline_id === q.get("pipeline_id")) && (!q.get("lead_id") || d.lead_id === q.get("lead_id")) &&
    (!owner || d.owner_id === (owner === "me" ? c.userId : owner)) && (!q.get("status") || d.status === q.get("status")) &&
    (!text || d.name.toLowerCase().includes(text)))
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at));
}

route("GET", "/deals", "viewer", (c) => { ensure(c); return ok(listDeals(c).map((d) => dealView(c.db, d))); });

route("GET", "/deals/board", "viewer", (c) => {
  ensure(c);
  scanStale(c); // the worker's ticker, simulated on load
  const pipes = byOrg(c.db.pipelines!, c);
  const pid = c.query.get("pipeline_id");
  const pipe = pid ? pipes.find((p) => p.id === pid) : pipes.find((p) => p.is_default) ?? pipes[0];
  if (!pipe) throw notFound("pipeline");
  const deals = listDeals(c, new URLSearchParams({ pipeline_id: pipe.id, owner: c.query.get("owner") ?? "" })).map((d) => dealView(c.db, d));
  const cutoff90 = Date.now() - 90 * 86_400_000, cutoff30 = Date.now() - 30 * 86_400_000;
  const totals = { open_count: 0, open_value: 0, weighted_value: 0, won_value_30d: 0, won_count_30d: 0, stale_count: 0 };
  const columns = pipelineView(pipe).stages.map((stage: Row) => {
    const col = deals.filter((d) => d.stage_id === stage.id && (d.status === "open" || !d.closed_at || new Date(d.closed_at).getTime() >= cutoff90));
    return { stage, deals: col, count: col.length, value: col.reduce((s, d) => s + d.value, 0), weighted: col.reduce((s, d) => s + (d.value * d.probability) / 100, 0) };
  });
  for (const d of columns.flatMap((x: Row) => x.deals)) {
    if (d.status === "open") {
      totals.open_count++; totals.open_value += d.value; totals.weighted_value += (d.value * d.probability) / 100;
      if (d.stale) totals.stale_count++;
    } else if (d.status === "won" && d.closed_at && new Date(d.closed_at).getTime() >= cutoff30) {
      totals.won_value_30d += d.value; totals.won_count_30d++;
    }
  }
  return ok({ pipeline: pipelineView(pipe), columns, totals });
});

function createDeal(c: Ctx, body: Row, workflow?: string): Row {
  ensure(c);
  const pipes = byOrg(c.db.pipelines!, c);
  let pipe: Row | undefined, stage: Row | undefined;
  if (body.stage_id) {
    const found = stageById(c.db, body.stage_id);
    if (!found || found.pipeline.organization_id !== c.orgId) throw notFound("stage");
    ({ pipeline: pipe, stage } = found);
  } else {
    pipe = body.pipeline_id ? pipes.find((p) => p.id === body.pipeline_id) : pipes.find((p) => p.is_default);
    if (!pipe) throw notFound("pipeline");
    stage = pipelineView(pipe).stages.find((s: Row) => s.kind === "open");
  }
  const lead = body.lead_id ? byOrg(c.db.leads, c).find((l) => l.id === body.lead_id) : null;
  if (body.lead_id && !lead) throw notFound("lead");
  const name = String(body.name ?? "").trim() || lead?.company_name || "";
  if (!name) throw invalid("validation failed", { name: "is required" });
  if (Number(body.value) < 0) throw invalid("validation failed", { value: "can't be negative" });
  const now = stamp();
  const d: Row = {
    id: id(), organization_id: c.orgId, pipeline_id: pipe!.id, stage_id: stage!.id, lead_id: lead?.id ?? null, name, value: Number(body.value) || 0,
    currency: (body.currency || "INR").toUpperCase(), expected_close: body.expected_close || null, owner_id: body.owner_id ?? lead?.owner_id ?? (workflow ? null : c.userId),
    status: "open", lost_reason: "", next_step: body.next_step ?? "", next_step_at: body.next_step_at ?? null, source: body.source || "manual",
    stage_entered_at: now, last_activity_at: now, closed_at: null, created_at: now, updated_at: now,
  };
  c.db.deals!.push(d);
  dealEvent(c, d.id, "created", { value: d.value, source: d.source }, workflow);
  if (lead) leadEvent(c, lead.id, "deal_created", { deal_id: d.id, name });
  emitDeal(c, "deal.created", d);
  return d;
}

route("POST", "/deals", "member", (c) => created(dealView(c.db, createDeal(c, c.body))));

function getDeal(c: Ctx, dealId: string) {
  ensure(c);
  const d = byOrg(c.db.deals!, c).find((x) => x.id === dealId);
  if (!d) throw notFound("deal");
  return d;
}

route("GET", "/deals/:id", "viewer", (c) => {
  const d = getDeal(c, c.params.id);
  const events = c.db.dealEvents!.filter((e) => e.deal_id === d.id).sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id - a.id)
    .map((e) => ({ id: e.id, event_type: e.event_type, actor_name: c.db.users.find((u) => u.id === e.actor_id)?.full_name ?? null, data: e.data, created_at: e.created_at }));
  return ok({ deal: dealView(c.db, d), events });
});

function updateDeal(c: Ctx, d: Row, body: Row, workflow?: string) {
  const changes: Row = {};
  if (body.name && body.name.trim() !== d.name) d.name = changes.name = body.name.trim();
  if (body.value !== undefined && Number(body.value) !== d.value) {
    if (Number(body.value) < 0) throw invalid("validation failed", { value: "can't be negative" });
    d.value = changes.value = Number(body.value);
  }
  if (body.expected_close !== undefined) d.expected_close = changes.expected_close = body.expected_close || null;
  if (body.owner_id !== undefined && body.owner_id !== d.owner_id) {
    d.owner_id = changes.owner_id = body.owner_id === "00000000-0000-0000-0000-000000000000" ? null : body.owner_id;
  }
  if (body.next_step !== undefined) { d.next_step = changes.next_step = body.next_step; d.next_step_at = body.next_step_at ?? null; }
  d.updated_at = d.last_activity_at = stamp();
  if (Object.keys(changes).length) dealEvent(c, d.id, "updated", changes, workflow);
}

route("PUT", "/deals/:id", "member", (c) => {
  const d = getDeal(c, c.params.id);
  updateDeal(c, d, c.body);
  return ok(dealView(c.db, d));
});
route("DELETE", "/deals/:id", "member", (c) => {
  const d = getDeal(c, c.params.id);
  c.db.deals = c.db.deals!.filter((x) => x.id !== d.id);
  audit(c, "deal.deleted", "deal", d.id);
  return noContent();
});

function moveDeal(c: Ctx, d: Row, stageId: string, lostReason = "", workflow?: string): Row {
  if (d.stage_id === stageId) return d;
  const found = stageById(c.db, stageId);
  if (!found || found.pipeline.organization_id !== c.orgId) throw notFound("stage");
  if (found.pipeline.id !== d.pipeline_id) throw invalid("validation failed", { stage_id: "stage belongs to another pipeline" });
  const s = found.stage;
  if (s.kind === "lost" && !lostReason.trim()) throw invalid("validation failed", { lost_reason: "say why the deal was lost" });
  const from = stageById(c.db, d.stage_id)?.stage.name;
  Object.assign(d, { stage_id: s.id, status: s.kind, lost_reason: s.kind === "lost" ? lostReason.trim() : "", stage_entered_at: stamp(), last_activity_at: stamp(),
    updated_at: stamp(), closed_at: s.kind === "open" ? null : stamp() });
  dealEvent(c, d.id, "stage_changed", { from, to: s.name, lost_reason: d.lost_reason }, workflow);
  const lead = d.lead_id ? c.db.leads.find((l) => l.id === d.lead_id) : null;
  if (lead && s.kind !== "open") { lead.status = s.kind; lead.updated_at = stamp(); leadEvent(c, lead.id, "status_changed", { to: s.kind }); }
  emitDeal(c, "deal.stage_changed", d);
  if (s.kind === "won") emitDeal(c, "deal.won", d);
  if (s.kind === "lost") emitDeal(c, "deal.lost", d);
  return d;
}

route("POST", "/deals/:id/move", "member", (c) => {
  const d = getDeal(c, c.params.id);
  return ok(dealView(c.db, moveDeal(c, d, c.body.stage_id, c.body.lost_reason ?? "")));
});
route("POST", "/deals/:id/notes", "member", (c) => {
  const d = getDeal(c, c.params.id);
  if (!String(c.body.body ?? "").trim()) throw invalid("validation failed", { body: "is required" });
  d.last_activity_at = stamp();
  dealEvent(c, d.id, "note", { body: c.body.body.trim() });
  return noContent();
});

// ───────────── workflows: vocabulary ─────────────

const CLASSIFICATIONS = [["interested", "Interested"], ["positive", "Positive"], ["question", "Question"], ["not_now", "Not now"], ["not_interested", "Not interested"],
  ["unsubscribe", "Unsubscribe"], ["wrong_person", "Wrong person"], ["out_of_office", "Out of office"], ["spam", "Spam"], ["unknown", "Unknown"]].map(([value, label]) => ({ value, label }));
const LEAD_STATUSES = ["new", "contacted", "replied", "interested", "qualified", "meeting", "won", "lost", "unsubscribed"].map((v) => ({ value: v, label: v[0].toUpperCase() + v.slice(1) }));

const META = {
  triggers: [
    { trigger: "reply.classified", label: "A reply is classified", description: "A prospect replies to a campaign and the reply gets a classification (interested, not now, …).", group: "Engagement" },
    { trigger: "deal.created", label: "A deal is created", description: "A deal is added to a pipeline, by a person or another workflow.", group: "Deals" },
    { trigger: "deal.stage_changed", label: "A deal changes stage", description: "A deal moves to another stage. Optionally only when it enters a specific stage.", group: "Deals" },
    { trigger: "deal.won", label: "A deal is won", description: "A deal enters a Won stage.", group: "Deals" },
    { trigger: "deal.lost", label: "A deal is lost", description: "A deal enters a Lost stage.", group: "Deals" },
    { trigger: "deal.stale", label: "A deal is stuck in a stage", description: "An open deal has sat in one stage longer than the stage's limit (or a number of days you set).", group: "Time-based" },
    { trigger: "deal.inactive", label: "A deal has no activity", description: "Nobody has touched an open deal for a number of days.", group: "Time-based" },
  ],
  fields: [
    { field: "reply.classification", label: "Reply classification", type: "enum", options: CLASSIFICATIONS },
    { field: "lead.industry", label: "Lead industry", type: "text" }, { field: "lead.city", label: "Lead city", type: "text" },
    { field: "lead.state", label: "Lead state", type: "text" }, { field: "lead.country", label: "Lead country", type: "text" },
    { field: "lead.source", label: "Lead source", type: "text" }, { field: "lead.status", label: "Lead status", type: "enum", options: LEAD_STATUSES },
    { field: "lead.tags", label: "Lead tags", type: "tags" }, { field: "lead.lead_score", label: "Lead score", type: "number" },
    { field: "lead.icp_fit", label: "ICP fit", type: "number" }, { field: "lead.owner_id", label: "Lead owner", type: "user" },
    { field: "deal.value", label: "Deal value", type: "number" }, { field: "deal.stage_id", label: "Deal stage", type: "stage" },
    { field: "deal.status", label: "Deal status", type: "enum", options: [{ value: "open", label: "Open" }, { value: "won", label: "Won" }, { value: "lost", label: "Lost" }] },
    { field: "deal.owner_id", label: "Deal owner", type: "user" }, { field: "deal.lost_reason", label: "Lost reason", type: "text" },
    { field: "deal.source", label: "Deal source", type: "text" }, { field: "deal.days_in_stage", label: "Days in stage", type: "number" },
  ],
  ops: [["eq", "is"], ["neq", "is not"], ["in", "is any of"], ["not_in", "is none of"], ["contains", "contains"], ["gte", "is at least"], ["lte", "is at most"],
    ["is_set", "is set"], ["is_empty", "is empty"]].map(([value, label]) => ({ value, label })),
  actions: [
    { type: "create_deal", label: "Create a deal", description: "Open a deal for the lead (skipped if the lead already has an open deal).", needs_deal: false },
    { type: "move_deal", label: "Move the deal", description: "Move the deal to another stage.", needs_deal: true },
    { type: "assign_owner", label: "Assign an owner", description: "Give the lead and its deal an owner — a specific teammate or round-robin.", needs_deal: false },
    { type: "create_task", label: "Create a task", description: "Add a task with a due date for the owner or a teammate.", needs_deal: false },
    { type: "add_note", label: "Add a note", description: "Log a note on the lead's or deal's timeline.", needs_deal: false },
    { type: "add_tag", label: "Tag the lead", description: "Add a tag to the lead.", needs_deal: false },
    { type: "set_lead_status", label: "Set lead status", description: "Change the lead's status.", needs_deal: false },
    { type: "stop_sequences", label: "Stop sequences", description: "Stop every campaign sequence still emailing the lead.", needs_deal: false },
    { type: "notify", label: "Notify by email", description: "Email the owner or the workspace admins.", needs_deal: false },
    { type: "sync_crm", label: "Sync to CRM", description: "Push the lead to your connected CRMs.", needs_deal: false },
  ],
  variables: ["{{company}}", "{{deal}}", "{{stage}}", "{{value}}", "{{days}}", "{{owner}}", "{{classification}}"],
  lost_reasons: ["Price", "Timing", "Went with a competitor", "No budget", "No response", "Not a fit", "Other"],
};

const TEMPLATES: Row[] = [
  { key: "interested_reply", name: "Interested reply → deal + discovery call", category: "Engagement",
    description: "When someone replies interested, open a deal, assign an owner round-robin and create a task to book the discovery call.",
    trigger: "reply.classified", trigger_config: { classifications: ["interested", "positive"] }, conditions: null, actions: [
      { type: "create_deal", params: { owner: "round_robin" } },
      { type: "create_task", params: { title: "Book a discovery call with {{company}}", due_in_days: 1, assignee: "owner" } }] },
  { key: "not_now_nurture", name: "Not now → nurture and follow up later", category: "Engagement",
    description: "When a prospect says not now, tag them for nurture, stop their sequences and set a reminder to reach out in 60 days.",
    trigger: "reply.classified", trigger_config: { classifications: ["not_now"] }, conditions: null, actions: [
      { type: "add_tag", params: { tag: "nurture" } }, { type: "stop_sequences", params: {} },
      { type: "create_task", params: { title: "Check back in with {{company}}", due_in_days: 60, assignee: "owner" } }] },
  { key: "proposal_follow_up", name: "Proposal sent → follow up in 3 days", category: "Deals",
    description: "When a deal enters Proposal sent, remind the owner to follow up three days later.",
    trigger: "deal.stage_changed", trigger_config: { to_stage: "Proposal sent" }, conditions: null, actions: [
      { type: "create_task", params: { title: "Follow up on the proposal for {{deal}}", due_in_days: 3, assignee: "owner" } }] },
  { key: "stuck_deal_alert", name: "Stuck deal alert", category: "Time-based",
    description: "When an open deal sits in a stage longer than the stage allows, email the owner and add a task to unblock it.",
    trigger: "deal.stale", trigger_config: {}, conditions: null, actions: [
      { type: "notify", params: { to: "owner", message: "{{deal}} has been in {{stage}} for {{days}} days." } },
      { type: "create_task", params: { title: "Unblock {{deal}} ({{days}} days in {{stage}})", due_in_days: 1, assignee: "owner" } }] },
  { key: "won_onboarding", name: "Deal won → onboarding handoff", category: "Deals",
    description: "When a deal is won, create kickoff tasks, note the win and push the customer to your CRM.",
    trigger: "deal.won", trigger_config: {}, conditions: null, actions: [
      { type: "create_task", params: { title: "Send the welcome pack to {{company}}", due_in_days: 1, assignee: "owner" } },
      { type: "create_task", params: { title: "Schedule the kickoff call with {{company}}", due_in_days: 3, assignee: "owner" } },
      { type: "add_tag", params: { tag: "customer" } }, { type: "sync_crm", params: {} }] },
  { key: "lost_timing_revisit", name: "Lost on timing → revisit in 90 days", category: "Deals",
    description: "When a deal is lost because of timing, tag the lead and set a reminder to revisit it in three months.",
    trigger: "deal.lost", trigger_config: {}, conditions: [{ field: "deal.lost_reason", op: "eq", value: "Timing" }], actions: [
      { type: "add_tag", params: { tag: "revisit" } },
      { type: "create_task", params: { title: "Revisit {{company}} — lost on timing 90 days ago", due_in_days: 90, assignee: "owner" } }] },
];

function workflowFromTemplate(db: MockDB, org: string, t: Row, userId: string | null): Row {
  const cfg = { ...t.trigger_config };
  if (cfg.to_stage) {
    const pipe = (db.pipelines ?? []).find((p) => p.organization_id === org && p.is_default);
    const s = pipe?.stages.find((x: Row) => x.name.toLowerCase() === String(cfg.to_stage).toLowerCase());
    delete cfg.to_stage;
    if (s) cfg.to_stage_id = s.id;
  }
  return { id: id(), organization_id: org, name: t.name, description: t.description, enabled: true, trigger: t.trigger, trigger_config: cfg,
    conditions: structuredClone(t.conditions ?? []), actions: structuredClone(t.actions), template: t.key, run_count: 0, last_run_at: null,
    created_by: userId, created_at: stamp(), updated_at: stamp() };
}

const wfView = ({ organization_id: _o, created_by: _c, ...w }: Row) => { void _o; void _c; return w; };

function validateWorkflow(b: Row) {
  const errs: Record<string, string> = {};
  if (!String(b.name ?? "").trim()) errs.name = "is required";
  if (!META.triggers.some((t) => t.trigger === b.trigger)) errs.trigger = "unknown trigger";
  if (!b.actions?.length) errs.actions = "add at least one action";
  (b.actions ?? []).forEach((a: Row, i: number) => {
    const p = a.params ?? {};
    if (!META.actions.some((x) => x.type === a.type)) errs[`actions.${i}`] = `unknown action ${a.type}`;
    else if (a.type === "create_task" && !String(p.title ?? "").trim()) errs[`actions.${i}`] = "task title is required";
    else if (a.type === "add_tag" && !String(p.tag ?? "").trim()) errs[`actions.${i}`] = "tag is required";
    else if (a.type === "add_note" && !String(p.body ?? "").trim()) errs[`actions.${i}`] = "note text is required";
    else if (a.type === "notify" && !String(p.message ?? "").trim()) errs[`actions.${i}`] = "message is required";
    else if (a.type === "move_deal" && !p.stage_id) errs[`actions.${i}`] = "pick a stage";
  });
  if (Object.keys(errs).length) throw invalid("validation failed", errs);
}

// ───────────── workflows: routes ─────────────

route("GET", "/workflows/meta", "viewer", () => ok(META));
route("GET", "/workflows/templates", "viewer", () => ok(TEMPLATES));
route("GET", "/workflows", "viewer", (c) => { ensure(c); return ok(byOrg(c.db.workflows!, c).sort((a, b) => a.created_at.localeCompare(b.created_at)).map(wfView)); });
route("POST", "/workflows", "admin", (c) => {
  ensure(c);
  let w: Row;
  if (c.body.template && !c.body.trigger) {
    const t = TEMPLATES.find((x) => x.key === c.body.template);
    if (!t) throw invalid("validation failed", { template: "unknown template" });
    w = workflowFromTemplate(c.db, c.orgId!, t, c.userId);
  } else {
    validateWorkflow(c.body);
    w = { id: id(), organization_id: c.orgId, name: c.body.name.trim(), description: c.body.description ?? "", enabled: c.body.enabled ?? true, trigger: c.body.trigger,
      trigger_config: c.body.trigger_config ?? {}, conditions: c.body.conditions ?? [], actions: c.body.actions, template: "", run_count: 0, last_run_at: null,
      created_by: c.userId, created_at: stamp(), updated_at: stamp() };
  }
  c.db.workflows!.push(w);
  audit(c, "workflow.created", "workflow", w.id, { name: w.name });
  return created(wfView(w));
});
function getWorkflow(c: Ctx) {
  ensure(c);
  const w = byOrg(c.db.workflows!, c).find((x) => x.id === c.params.id);
  if (!w) throw notFound("workflow");
  return w;
}
route("GET", "/workflows/:id", "viewer", (c) => ok(wfView(getWorkflow(c))));
route("PUT", "/workflows/:id", "admin", (c) => {
  const w = getWorkflow(c);
  const b = c.body;
  if (b.trigger || b.name || b.actions) {
    validateWorkflow(b);
    Object.assign(w, { name: b.name.trim(), description: b.description ?? "", trigger: b.trigger, trigger_config: b.trigger_config ?? {}, conditions: b.conditions ?? [], actions: b.actions });
  }
  if (typeof b.enabled === "boolean") w.enabled = b.enabled;
  w.updated_at = stamp();
  return ok(wfView(w));
});
route("DELETE", "/workflows/:id", "admin", (c) => {
  const w = getWorkflow(c);
  c.db.workflows = c.db.workflows!.filter((x) => x.id !== w.id);
  c.db.workflowRuns = c.db.workflowRuns!.filter((r) => r.workflow_id !== w.id);
  return noContent();
});
route("GET", "/workflows/:id/runs", "viewer", (c) => {
  const w = getWorkflow(c);
  return ok(c.db.workflowRuns!.filter((r) => r.workflow_id === w.id).sort((a, b) => b.created_at.localeCompare(a.created_at)).slice(0, 50)
    .map((r) => {
      const { organization_id, event_key, ...rest } = r;
      void organization_id; void event_key;
      return { ...rest, company_name: c.db.leads.find((l) => l.id === r.lead_id)?.company_name ?? null };
    }));
});
route("POST", "/workflows/:id/test", "member", (c) => {
  const w = getWorkflow(c);
  if (!c.body.lead_id && !c.body.deal_id) throw invalid("validation failed", { lead_id: "pick a lead or a deal to test against" });
  const s = subject(c, c.body.lead_id ?? null, c.body.deal_id ?? null, { classification: c.body.classification ?? "" });
  if (s.deal) Object.assign(s.extra, { stage_id: s.deal.stage_id, pipeline_id: s.deal.pipeline_id });
  const triggerMatches = matchesTrigger(w, s.extra);
  const conditions = (w.conditions ?? []).map((cond: Row) => ({ ...cond, actual: s.facts[cond.field] ?? null, passed: check(cond, s.facts[cond.field]) }));
  const matched = triggerMatches && conditions.every((x: Row) => x.passed);
  return ok({ trigger_matches: triggerMatches, matched, subject: s.label, conditions,
    actions: w.actions.map((a: Row) => ({ type: a.type, status: "would_run", message: render(s, String(a.params?.title ?? a.params?.body ?? a.params?.message ?? a.params?.tag ?? "")) })) });
});

// ───────────── engine ─────────────

interface Subject { lead: Row | null; deal: Row | null; facts: Row; vars: Record<string, string>; extra: Record<string, string>; label: string }

function subject(c: Ctx, leadId: string | null, dealId: string | null, extra: Record<string, string>): Subject {
  const deal = dealId ? byOrg(c.db.deals ?? [], c).find((d) => d.id === dealId) ?? null : null;
  const lead = byOrg(c.db.leads, c).find((l) => l.id === (leadId ?? deal?.lead_id)) ?? null;
  const facts: Row = {}, vars: Record<string, string> = {};
  if (lead) {
    Object.assign(facts, { "lead.industry": lead.industry, "lead.city": lead.city, "lead.state": lead.state, "lead.country": lead.country, "lead.source": lead.source,
      "lead.status": lead.status, "lead.tags": lead.tags ?? [], "lead.lead_score": lead.lead_score, "lead.icp_fit": lead.icp_fit, "lead.owner_id": lead.owner_id });
    vars.company = lead.company_name;
    vars.owner = c.db.users.find((u) => u.id === lead.owner_id)?.full_name ?? "";
  }
  if (deal) {
    const v = dealView(c.db, deal);
    Object.assign(facts, { "deal.value": v.value, "deal.stage_id": v.stage_id, "deal.status": v.status, "deal.owner_id": v.owner_id, "deal.lost_reason": v.lost_reason,
      "deal.source": v.source, "deal.days_in_stage": v.days_in_stage });
    Object.assign(vars, { deal: v.name, stage: v.stage_name, days: String(v.days_in_stage), value: `${v.currency} ${v.value}` });
    if (v.owner_name) vars.owner = v.owner_name;
    vars.company ||= v.name;
  }
  if (extra.classification) { facts["reply.classification"] = extra.classification; vars.classification = extra.classification.replace("_", " "); }
  if (extra.days) vars.days = extra.days;
  return { lead, deal, facts, vars, extra, label: deal?.name ?? lead?.company_name ?? "" };
}

const render = (s: Subject, t: string) => Object.entries(s.vars).reduce((acc, [k, v]) => acc.replaceAll(`{{${k}}}`, v ?? ""), t);
const asList = (v: unknown): string[] => (Array.isArray(v) ? v.map(String) : typeof v === "string" && v ? v.split(",").map((x) => x.trim()).filter(Boolean) : v == null ? [] : [String(v)]);

function check(cond: Row, actual: unknown): boolean {
  const vals = Array.isArray(actual) ? actual.map(String) : actual == null ? [] : [String(actual)];
  const eq = () => vals.some((v) => asList(cond.value).some((w) => v.trim().toLowerCase() === w.trim().toLowerCase()));
  switch (cond.op) {
    case "is_set": return vals.length > 0 && vals[0] !== "";
    case "is_empty": return vals.length === 0 || vals[0] === "";
    case "eq": case "in": return eq();
    case "neq": case "not_in": return !eq();
    case "contains": return vals.some((v) => v.toLowerCase().includes(String(cond.value).toLowerCase()));
    case "gte": return actual != null && Number(actual) >= Number(cond.value);
    case "lte": return actual != null && Number(actual) <= Number(cond.value);
  }
  return false;
}

function matchesTrigger(w: Row, extra: Record<string, string>) {
  const cfg = w.trigger_config ?? {};
  if (w.trigger === "reply.classified" && asList(cfg.classifications).length) return asList(cfg.classifications).includes(extra.classification);
  if (w.trigger === "deal.stage_changed" && cfg.to_stage_id) return cfg.to_stage_id === extra.stage_id;
  if (cfg.pipeline_id && extra.pipeline_id) return cfg.pipeline_id === extra.pipeline_id;
  return true;
}

let depth = 0;

function emit(c: Ctx, trigger: string, eventKey: string, leadId: string | null, dealId: string | null, extra: Record<string, string>, only?: Row) {
  ensure(c);
  if (depth >= 3) return;
  const wfs = only ? [only] : byOrg(c.db.workflows!, c).filter((w) => w.enabled && w.trigger === trigger);
  for (const w of wfs) {
    if (!matchesTrigger(w, extra)) continue;
    if (c.db.workflowRuns!.some((r) => r.workflow_id === w.id && r.event_key === eventKey)) continue;
    const s = subject(c, leadId, dealId, extra);
    if (s.deal && !matchesTrigger(w, { ...extra, pipeline_id: s.deal.pipeline_id })) continue;
    const run: Row = { id: ++c.db.seq, organization_id: c.orgId, workflow_id: w.id, event_key: eventKey, trigger, lead_id: s.lead?.id ?? null,
      deal_id: s.deal?.id ?? null, subject: s.label, status: "skipped", results: [], created_at: stamp() };
    c.db.workflowRuns!.push(run);
    if (!(w.conditions ?? []).every((cond: Row) => check(cond, s.facts[cond.field]))) {
      run.results = [{ type: "conditions", status: "skipped", message: "conditions didn't match" }];
      continue;
    }
    depth++;
    try {
      let okN = 0, failN = 0;
      for (const a of w.actions) {
        try {
          const r = runAction(c, w, s, a);
          run.results.push({ type: a.type, ...r });
          if (r.status === "ok") okN++;
        } catch (e: any) {
          failN++;
          run.results.push({ type: a.type, status: "failed", message: e?.message ?? String(e) });
        }
      }
      run.status = failN === 0 ? "succeeded" : okN === 0 ? "failed" : "partial";
      run.deal_id = s.deal?.id ?? null;
      w.run_count++; w.last_run_at = stamp();
    } finally {
      depth--;
    }
  }
}

function emitDeal(c: Ctx, trigger: string, d: Row) {
  const key = trigger === "deal.created" ? `${trigger}:${d.id}` : `${trigger}:${d.id}:${d.stage_id}:${d.stage_entered_at}`;
  emit(c, trigger, key, d.lead_id, d.id, { stage_id: d.stage_id });
}

/** Called by the inbox mock when a reply gets a classification. */
export function onReplyClassified(c: Ctx, leadId: string, classification: string, eventId: string) {
  if (classification) emit(c, "reply.classified", `reply:${eventId}`, leadId, null, { classification });
}

function scanStale(c: Ctx) {
  const before = c.db.workflowRuns!.length;
  for (const w of byOrg(c.db.workflows!, c).filter((x) => x.enabled && (x.trigger === "deal.stale" || x.trigger === "deal.inactive"))) {
    const days = Number(w.trigger_config?.days) || 0;
    for (const d of byOrg(c.db.deals!, c).filter((x) => x.status === "open")) {
      if (w.trigger === "deal.stale") {
        const limit = days || stageById(c.db, d.stage_id)?.stage.stale_days;
        const n = daysBetween(d.stage_entered_at);
        if (limit && n >= limit) emit(c, w.trigger, `${w.trigger}:${d.id}:${d.stage_entered_at}`, d.lead_id, d.id, { stage_id: d.stage_id, days: String(n) }, w);
      } else {
        const n = daysBetween(d.last_activity_at);
        if (n >= (days || 14)) emit(c, w.trigger, `${w.trigger}:${d.id}:${d.last_activity_at}`, d.lead_id, d.id, { stage_id: d.stage_id, days: String(n) }, w);
      }
    }
  }
  if (c.db.workflowRuns!.length !== before) saveDB();
}

function roundRobin(c: Ctx): string | null {
  const members = c.db.members.filter((m) => m.organization_id === c.orgId && m.role !== "viewer");
  const load = (u: string) => c.db.deals!.filter((d) => d.organization_id === c.orgId && d.owner_id === u && d.status === "open").length;
  return members.sort((a, b) => load(a.user_id) - load(b.user_id) || a.created_at.localeCompare(b.created_at))[0]?.user_id ?? null;
}

function runAction(c: Ctx, w: Row, s: Subject, a: Row): { status: string; message: string } {
  const p = a.params ?? {};
  const skip = (message: string) => ({ status: "skipped", message });
  const done = (message: string) => ({ status: "ok", message });
  const needLead = () => { if (!s.lead) throw new Error("this event has no lead"); return s.lead; };
  const ownerId = () => s.deal?.owner_id ?? s.lead?.owner_id ?? null;
  const ensureDeal = () => {
    s.deal ??= s.lead ? byOrg(c.db.deals!, c).find((d) => d.lead_id === s.lead!.id && d.status === "open") ?? null : null;
    if (!s.deal) throw new Error("the lead has no open deal");
    return s.deal;
  };
  switch (a.type) {
    case "create_deal": {
      const lead = needLead();
      const open = byOrg(c.db.deals!, c).find((d) => d.lead_id === lead.id && d.status === "open");
      if (open) { s.deal ??= open; return skip("the lead already has an open deal"); }
      const owner = p.owner === "round_robin" ? roundRobin(c) : p.owner === "user" ? p.user_id : undefined;
      const d = createDeal(c, { lead_id: lead.id, name: render(s, p.name ?? ""), value: p.value, stage_id: p.stage_id, owner_id: owner, source: "workflow" }, w.name);
      if (owner && !lead.owner_id) lead.owner_id = owner;
      s.deal = d;
      const v = dealView(c.db, d);
      Object.assign(s.vars, { deal: v.name, stage: v.stage_name, owner: v.owner_name ?? s.vars.owner ?? "" });
      return done(`opened deal "${v.name}" in ${v.stage_name}`);
    }
    case "move_deal": {
      const d = ensureDeal();
      if (d.stage_id === p.stage_id) return skip("already in that stage");
      moveDeal(c, d, p.stage_id, p.lost_reason ?? "", w.name);
      return done(`moved to ${stageById(c.db, p.stage_id)?.stage.name}`);
    }
    case "assign_owner": {
      const owner = p.mode === "user" ? p.user_id : roundRobin(c);
      if (!owner) return skip("no teammate to assign");
      if (s.lead) s.lead.owner_id = owner;
      if (s.deal) updateDeal(c, s.deal, { owner_id: owner }, w.name);
      const name = c.db.users.find((u) => u.id === owner)?.full_name ?? "";
      s.vars.owner = name;
      return done(`assigned to ${name}`);
    }
    case "create_task": {
      const title = render(s, String(p.title ?? ""));
      const due = new Date(Date.now() + (Number(p.due_in_days) || 0) * 86_400_000);
      c.db.tasks.push({ id: id(), organization_id: c.orgId, lead_id: s.lead?.id ?? null, assignee_id: p.assignee === "user" ? p.user_id : ownerId(), created_by: null,
        title, notes: `Created by the workflow “${w.name}”.`, due_at: due.toISOString(), completed_at: null, created_at: stamp() });
      return done(`task "${title}" due ${due.toLocaleDateString(undefined, { day: "numeric", month: "short" })}`);
    }
    case "add_note": {
      const body = render(s, String(p.body ?? ""));
      if (s.lead) c.db.notes.push({ id: id(), organization_id: c.orgId, lead_id: s.lead.id, author_id: null, body, created_at: stamp() });
      if (s.deal) dealEvent(c, s.deal.id, "note", { body }, w.name);
      if (!s.lead && !s.deal) throw new Error("nothing to note on");
      return done("note added");
    }
    case "add_tag": {
      const lead = needLead();
      const tag = render(s, String(p.tag ?? "")).trim().toLowerCase();
      lead.tags = [...new Set([...(lead.tags ?? []), tag])];
      return done(`tagged ${tag}`);
    }
    case "set_lead_status": {
      const lead = needLead();
      lead.status = p.status; lead.updated_at = stamp();
      leadEvent(c, lead.id, "status_changed", { to: p.status, workflow: w.name });
      return done(`status set to ${p.status}`);
    }
    case "stop_sequences": {
      const lead = needLead();
      const active = c.db.campaignLeads.filter((k) => k.lead_id === lead.id && ["pending", "active", "sending"].includes(k.status));
      if (!active.length) return skip("no active sequences");
      for (const k of active) { k.status = "stopped"; k.stop_reason = "workflow"; k.next_send_at = null; }
      return done(`stopped ${active.length} sequence(s)`);
    }
    case "notify": {
      const owner = p.to !== "admins" ? ownerId() : null;
      const to = owner ? [c.db.users.find((u) => u.id === owner)?.email]
        : c.db.members.filter((m) => m.organization_id === c.orgId && ["owner", "admin"].includes(m.role)).map((m) => c.db.users.find((u) => u.id === m.user_id)?.email);
      const list = to.filter(Boolean);
      if (!list.length) return skip("no one to notify");
      return done(`emailed ${list.join(", ")} (demo: not actually sent)`);
    }
    case "sync_crm": {
      needLead();
      const crm = byOrg(c.db.integrations, c).find((i) => ["hubspot", "salesforce", "pipedrive", "zoho_crm"].includes(i.kind) && i.status !== "disabled");
      if (!crm) return skip("no CRM connected");
      leadEvent(c, s.lead!.id, "crm_synced", { provider: crm.kind });
      return done("queued CRM sync");
    }
  }
  throw new Error(`unknown action ${a.type}`);
}
