 
import { toast } from "sonner";
import { mockSession, type Row } from "../db";
import {
  MockError, audit, byOrg, conflict, created, id, invalid, noContent, notFound, ok, paginate, publicUser, route, stamp, type Ctx,
} from "../server";

const emailRe = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const norm = (e: string) => String(e ?? "").trim().toLowerCase();

/** Demo mode can't send email, so codes and links are shown on screen instead. */
function demoMail(title: string, detail: string) {
  toast.info(title, { description: detail, duration: 20000 });
  console.info(`[demo mode] ${title}: ${detail}`);
}

function sendCode(c: Ctx, userId: string) {
  const code = String(Math.floor(100000 + Math.random() * 900000));
  c.db.verifications = c.db.verifications.filter((v) => v.user_id !== userId);
  c.db.verifications.push({ user_id: userId, code, attempts: 0, sent_at: stamp(), expires_at: new Date(Date.now() + 15 * 60_000).toISOString() });
  demoMail("Demo mode — your verification code", `Use ${code} (in a real deployment this is emailed).`);
}

function orgWithRole(o: Row, role: string) {
  const { onboarding: _o, ...rest } = o;
  void _o;
  return { ...rest, role };
}

function checklist(c: Ctx) {
  const db = c.db, org = c.org!;
  const has = (rows: Row[]) => rows.some((r) => r.organization_id === org.id);
  const items = [
    ["verify_email", "Verify your email", "Confirm the code we emailed you", "/onboarding", !!c.user?.email_verified],
    ["company", "Complete your company profile", "Name and the postal address shown in every email footer", "/settings?tab=organization", !!String(org.physical_address ?? "").trim()],
    ["icp", "Define your ideal customer", "Countries, cities, industries and signals", "/icp", has(db.icps)],
    ["sources", "Connect a data source", "Apollo, Google Maps, Hunter and more", "/integrations", db.integrations.some((i) => i.organization_id === org.id && i.status !== "disabled")],
    ["leads", "Add your first leads", "Import a CSV or search a connected source", "/leads", has(db.leads)],
    ["mailbox", "Connect a sending mailbox", "The address campaigns are sent from", "/settings?tab=mailboxes", has(db.mailboxes)],
    ["team", "Invite your team", "Teammates join with the role you choose", "/settings?tab=members",
      db.members.filter((m) => m.organization_id === org.id).length > 1 || db.invitations.some((i) => i.organization_id === org.id && !i.revoked_at)],
    ["campaign", "Launch your first campaign", "A compliant, paced email sequence", "/campaigns", db.campaigns.some((x) => x.organization_id === org.id && x.status !== "draft")],
  ].map(([key, title, hint, href, done]) => ({ key, title, hint, href, done }));
  return {
    state: org.onboarding ?? {}, checklist: items, completed: items.filter((i) => i.done).length, email_verified: !!c.user?.email_verified,
    organization: { name: org.name, legal_name: org.legal_name, physical_address: org.physical_address },
  };
}

// ───────────── auth ─────────────

route("POST", "/auth/register", "public", (c) => {
  const { email, password, full_name, accept_terms } = c.body;
  const fields: Record<string, string> = {};
  if (!emailRe.test(norm(email))) fields.email = "must be a valid email address";
  if (String(password ?? "").length < 10) fields.password = "must be at least 10 characters";
  if (!String(full_name ?? "").trim()) fields.full_name = "is required";
  if (!accept_terms) fields.accept_terms = "you must accept the terms of service and privacy policy";
  if (Object.keys(fields).length) throw invalid("validation failed", fields);
  if (c.db.users.some((u) => u.email === norm(email))) throw conflict("email_taken", "an account with this email already exists");
  const u = { id: id(), email: norm(email), full_name: String(full_name).trim(), password, email_verified: false, created_at: stamp(), last_login_at: stamp() };
  c.db.users.push(u);
  mockSession.set(u.id);
  sendCode(c, u.id);
  return created({ user: publicUser(u), access_token: "mock." + u.id });
});

route("POST", "/auth/login", "public", (c) => {
  const u = c.db.users.find((x) => x.email === norm(c.body.email));
  if (!u || u.password !== c.body.password) throw new MockError(401, "unauthorized", "invalid email or password");
  u.last_login_at = stamp();
  mockSession.set(u.id);
  return ok({ user: publicUser(u), access_token: "mock." + u.id });
});

route("POST", "/auth/logout", "public", () => { mockSession.set(null); return noContent(); });
route("GET", "/auth/me", "user", (c) => ok(publicUser(c.user!)));

route("POST", "/auth/verify-email/send", "user", (c) => {
  if (c.user!.email_verified) throw conflict("already_verified", "your email address is already verified");
  sendCode(c, c.user!.id);
  return ok({ status: "sent" });
});

route("POST", "/auth/verify-email", "user", (c) => {
  const v = c.db.verifications.find((x) => x.user_id === c.userId);
  if (!v) throw invalid("request a new code first", { code: "no active code" });
  if (String(c.body.code ?? "").trim() !== v.code && String(c.body.code ?? "").trim() !== "000000") {
    v.attempts++;
    throw invalid(`that code isn't right (${Math.max(0, 5 - v.attempts)} attempts left)`, { code: "incorrect" });
  }
  c.user!.email_verified = true;
  c.db.verifications = c.db.verifications.filter((x) => x.user_id !== c.userId);
  return ok({ verified: true });
});

// ───────────── organizations & members ─────────────

route("GET", "/organizations", "user", (c) =>
  ok(c.db.members.filter((m) => m.user_id === c.userId).map((m) => {
    const o = c.db.orgs.find((x) => x.id === m.organization_id);
    return o ? orgWithRole(o, m.role) : null;
  }).filter(Boolean)));

route("POST", "/organizations", "user", (c) => {
  const name = String(c.body.name ?? "").trim();
  if (!name) throw invalid("validation failed", { name: "is required" });
  const o = { id: id(), name, slug: name.toLowerCase().replace(/[^a-z0-9]+/g, "-"), legal_name: c.body.legal_name ?? "", physical_address: c.body.physical_address ?? "",
    daily_send_limit: 500, data_retention_days: null, created_at: stamp(), parent_id: null, is_agency: false, onboarding: {} };
  c.db.orgs.push(o);
  c.db.members.push({ organization_id: o.id, user_id: c.userId, role: "owner", created_at: stamp() });
  c.db.scoring[o.id] = structuredClone(c.db.scoring[Object.keys(c.db.scoring)[0]] ?? { version: 1, rules: [] });
  return created(orgWithRole(o, "owner"));
});

route("GET", "/organizations/current", "viewer", (c) => ok(orgWithRole(c.org!, c.role!)));
route("PATCH", "/organizations/current", "admin", (c) => {
  for (const k of ["name", "legal_name", "physical_address", "daily_send_limit", "data_retention_days"]) if (k in c.body) c.org![k] = c.body[k];
  audit(c, "organization.updated", "organization", c.orgId!);
  return ok(orgWithRole(c.org!, c.role!));
});
route("DELETE", "/organizations/current", "owner", () => { throw conflict("demo", "Deleting the organization is disabled in demo mode"); });

route("GET", "/organizations/current/members", "viewer", (c) =>
  ok(c.db.members.filter((m) => m.organization_id === c.orgId).map((m) => {
    const u = c.db.users.find((x) => x.id === m.user_id)!;
    return { user_id: u.id, email: u.email, full_name: u.full_name, role: m.role, created_at: m.created_at };
  })));
route("POST", "/organizations/current/members", "admin", (c) => {
  const u = c.db.users.find((x) => x.email === norm(c.body.email));
  if (!u) throw notFound("user");
  if (!c.db.members.some((m) => m.organization_id === c.orgId && m.user_id === u.id)) c.db.members.push({ organization_id: c.orgId, user_id: u.id, role: c.body.role ?? "member", created_at: stamp() });
  return created({ user_id: u.id });
});
route("PATCH", "/organizations/current/members/:user", "admin", (c) => {
  const m = c.db.members.find((x) => x.organization_id === c.orgId && x.user_id === c.params.user);
  if (!m) throw notFound("member");
  m.role = c.body.role;
  return ok({ user_id: m.user_id, role: m.role });
});
route("DELETE", "/organizations/current/members/:user", "admin", (c) => {
  if (c.params.user === c.userId) throw conflict("self", "you can't remove yourself");
  c.db.members = c.db.members.filter((x) => !(x.organization_id === c.orgId && x.user_id === c.params.user));
  return noContent();
});
route("GET", "/organizations/current/audit-logs", "admin", (c) => paginate(byOrg(c.db.audit, c), c.query, 100));
route("POST", "/compliance/erase", "admin", (c) => {
  const email = norm(c.body.email);
  c.db.contacts = c.db.contacts.filter((x) => !(x.organization_id === c.orgId && x.email === email));
  c.db.suppressions.push({ id: id(), organization_id: c.orgId, email, reason: "erased", source: "compliance", created_at: stamp() });
  audit(c, "compliance.erased", "contact", email);
  return ok({ erased: true });
});

// ───────────── onboarding & invitations ─────────────

route("GET", "/onboarding", "viewer", (c) => ok(checklist(c)));
route("PUT", "/onboarding", "admin", (c) => {
  c.org!.onboarding = { ...(c.org!.onboarding ?? {}), ...c.body };
  return ok(checklist(c));
});

const inviteStatus = (i: Row) => (i.accepted_at ? "accepted" : new Date(i.expires_at) < new Date() ? "expired" : "pending");

route("GET", "/invitations", "admin", (c) =>
  ok(byOrg(c.db.invitations, c).filter((i) => !i.revoked_at).map((i) => ({
    id: i.id, email: i.email, role: i.role, invited_by: c.db.users.find((u) => u.id === i.invited_by)?.full_name ?? null,
    created_at: i.created_at, expires_at: i.expires_at, accepted_at: i.accepted_at, status: inviteStatus(i),
  }))));

route("POST", "/invitations", "admin", (c) => {
  if (!c.user!.email_verified) throw new MockError(403, "forbidden", "verify your email address before inviting teammates");
  const role = c.body.role || "member";
  const res = { invited: [] as string[], already_members: [] as string[], invalid: [] as string[] };
  for (const raw of (c.body.emails ?? []) as string[]) {
    const email = norm(raw);
    if (!emailRe.test(email)) { res.invalid.push(raw); continue; }
    const u = c.db.users.find((x) => x.email === email);
    if (u && c.db.members.some((m) => m.organization_id === c.orgId && m.user_id === u.id)) { res.already_members.push(email); continue; }
    c.db.invitations.filter((i) => i.organization_id === c.orgId && i.email === email && !i.accepted_at).forEach((i) => (i.revoked_at = stamp()));
    const token = id().replace(/-/g, "") + id().replace(/-/g, "");
    c.db.invitations.push({ id: id(), organization_id: c.orgId, email, role, token, invited_by: c.userId, created_at: stamp(),
      expires_at: new Date(Date.now() + 7 * 864e5).toISOString(), accepted_at: null, revoked_at: null });
    res.invited.push(email);
    demoMail(`Demo mode — invitation for ${email}`, `${window.location.origin}/invite?token=${token}`);
  }
  if (res.invited.length) audit(c, "invitation.sent", "organization", c.orgId!, { emails: res.invited });
  return ok(res);
});
route("DELETE", "/invitations/:id", "admin", (c) => {
  const i = byOrg(c.db.invitations, c).find((x) => x.id === c.params.id && !x.accepted_at && !x.revoked_at);
  if (!i) throw notFound("invitation");
  i.revoked_at = stamp();
  return noContent();
});

function lookup(c: Ctx, token: string) {
  const i = c.db.invitations.find((x) => x.token === token);
  if (!i) throw notFound("invitation");
  if (i.revoked_at) throw conflict("invitation_revoked", "this invitation was withdrawn — ask for a new one");
  if (i.accepted_at) throw conflict("invitation_used", "this invitation has already been accepted — sign in instead");
  if (new Date(i.expires_at) < new Date()) throw conflict("invitation_expired", "this invitation has expired — ask for a new one");
  return i;
}
route("GET", "/invitations/lookup", "public", (c) => {
  const i = lookup(c, c.query.get("token") ?? "");
  return ok({ organization: c.db.orgs.find((o) => o.id === i.organization_id)?.name ?? "", email: i.email, role: i.role,
    invited_by: c.db.users.find((u) => u.id === i.invited_by)?.full_name ?? "", expires_at: i.expires_at, account_exists: c.db.users.some((u) => u.email === i.email) });
});
route("POST", "/invitations/accept", "user", (c) => {
  const i = lookup(c, c.body.token ?? "");
  if (i.email !== c.user!.email) throw new MockError(403, "forbidden", `this invitation was sent to ${i.email} — sign in with that address`);
  if (!c.db.members.some((m) => m.organization_id === i.organization_id && m.user_id === c.userId)) {
    c.db.members.push({ organization_id: i.organization_id, user_id: c.userId, role: i.role, created_at: stamp() });
  }
  i.accepted_at = stamp();
  c.user!.email_verified = true;
  return ok({ organization_id: i.organization_id });
});

// ───────────── agency ─────────────

route("PUT", "/agency", "owner", (c) => { c.org!.is_agency = !!c.body.enabled; return ok(orgWithRole(c.org!, c.role!)); });
route("GET", "/agency/clients", "viewer", (c) =>
  ok(c.db.orgs.filter((o) => o.parent_id === c.orgId).map((o) => ({
    id: o.id, name: o.name, leads: c.db.leads.filter((l) => l.organization_id === o.id).length, active_campaigns: 0, sent_30d: 0, replies_30d: 0, bounced_30d: 0,
  }))));
route("POST", "/agency/clients", "admin", (c) => {
  if (!c.org!.is_agency) throw conflict("not_agency", "turn on agency mode first");
  const o = { id: id(), name: String(c.body.name ?? "Client"), slug: "", legal_name: "", physical_address: c.body.physical_address ?? "", daily_send_limit: 500,
    data_retention_days: null, created_at: stamp(), parent_id: c.orgId, is_agency: false, onboarding: { completed: true } };
  c.db.orgs.push(o);
  c.db.members.push({ organization_id: o.id, user_id: c.userId, role: "owner", created_at: stamp() });
  c.db.scoring[o.id] = structuredClone(c.db.scoring[c.orgId!]);
  return created(orgWithRole(o, "owner"));
});

export {};
