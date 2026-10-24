/* eslint-disable @typescript-eslint/no-explicit-any */
// Demo-mode API: a tiny in-browser router that answers /api/v1 requests from
// the mock database with the same {data, error, meta} envelope as the Go API.
import { getDB, iso, mockSession, now, saveDB, uuid, type MockDB, type Row } from "./db";

export class MockError extends Error {
  constructor(public status: number, public code: string, message: string, public fields?: Record<string, string>) {
    super(message);
  }
}
export const invalid = (message: string, fields?: Record<string, string>) => new MockError(422, "invalid_request", message, fields);
export const notFound = (what: string) => new MockError(404, "not_found", `${what} not found`);
export const conflict = (code: string, message: string) => new MockError(409, code, message);

export interface Ctx {
  params: Record<string, string>;
  query: URLSearchParams;
  body: any;
  form: FormData | null;
  db: MockDB;
  userId: string | null;
  orgId: string | null;
  user: Row | null;
  org: Row | null;
  role: string | null;
}

export interface Result {
  data?: unknown;
  meta?: Record<string, unknown>;
  status?: number;
}

type Handler = (c: Ctx) => Result | Promise<Result> | unknown;
type Auth = "public" | "user" | "viewer" | "member" | "admin" | "owner";
const routes: { method: string; re: RegExp; keys: string[]; auth: Auth; handler: Handler }[] = [];

/** Registers a route. Patterns use :param segments, e.g. "/leads/:id". */
export function route(method: string, pattern: string, auth: Auth, handler: Handler) {
  const keys: string[] = [];
  const re = new RegExp("^" + pattern.replace(/:([a-z_]+)/g, (_, k) => { keys.push(k); return "([^/]+)"; }) + "$");
  routes.push({ method, re, keys, auth, handler });
}

const RANK: Record<string, number> = { viewer: 1, member: 2, admin: 3, owner: 4 };

// ───────────── helpers used by handlers ─────────────

export const ok = (data: unknown, meta?: Record<string, unknown>): Result => ({ data, meta });
export const created = (data: unknown): Result => ({ data, status: 201 });
export const noContent = (): Result => ({ status: 204 });
export const stamp = () => iso(now());
export const id = () => uuid();

export function paginate<T>(list: T[], q: URLSearchParams, def = 50): Result {
  const per = Math.min(Math.max(Number(q.get("per_page")) || def, 1), 500);
  const page = Math.max(Number(q.get("page")) || 1, 1);
  return ok(list.slice((page - 1) * per, page * per), { total: list.length, page, per_page: per });
}

export function audit(c: Ctx, action: string, entity_type = "", entity_id = "", metadata: Record<string, unknown> = {}) {
  c.db.audit.unshift({ id: ++c.db.seq, organization_id: c.orgId, user_id: c.userId, user_email: c.user?.email ?? null, action, entity_type, entity_id, metadata, ip: "127.0.0.1", created_at: stamp() });
}

export function leadEvent(c: Ctx, leadId: string, event_type: string, data: Record<string, unknown> = {}) {
  c.db.events.push({ id: ++c.db.seq, organization_id: c.orgId, lead_id: leadId, event_type, actor_name: c.user?.full_name ?? null, data, created_at: stamp() });
}

export const byOrg = <T extends Row>(rows: T[], c: Ctx) => rows.filter((r) => r.organization_id === c.orgId);

export function publicUser(u: Row) {
  return { id: u.id, email: u.email, full_name: u.full_name, last_login_at: u.last_login_at, created_at: u.created_at, email_verified: !!u.email_verified };
}

// ───────────── dispatch ─────────────

export async function handle(method: string, url: URL, headers: Headers, body: BodyInit | null | undefined): Promise<{ status: number; json: unknown }> {
  const db = getDB();
  const path = url.pathname.replace(/^\/api\/v1/, "") || "/";
  const auth = headers.get("Authorization") ?? "";
  const token = auth.startsWith("Bearer mock.") ? auth.slice("Bearer mock.".length) : null;
  const user = token ? db.users.find((u) => u.id === token) ?? null : null;
  const orgHeader = headers.get("X-Organization-ID");
  const membership = user && orgHeader ? db.members.find((m) => m.user_id === user.id && m.organization_id === orgHeader) : null;

  let parsed: any = undefined;
  let form: FormData | null = null;
  if (body instanceof FormData) form = body;
  else if (typeof body === "string" && body) { try { parsed = JSON.parse(body); } catch { /* not JSON */ } }

  const ctx: Ctx = {
    params: {}, query: url.searchParams, body: parsed ?? {}, form, db,
    userId: user?.id ?? null, orgId: membership ? orgHeader : null, user, org: membership ? db.orgs.find((o) => o.id === orgHeader) ?? null : null,
    role: membership?.role ?? null,
  };

  for (const r of routes) {
    if (r.method !== method) continue;
    const m = path.match(r.re);
    if (!m) continue;
    r.keys.forEach((k, i) => (ctx.params[k] = decodeURIComponent(m[i + 1])));
    try {
      if (r.auth !== "public") {
        if (!user) throw new MockError(401, "unauthorized", "missing or invalid token");
        if (r.auth !== "user") {
          if (!orgHeader) throw invalid("X-Organization-ID header is required");
          if (!membership) throw new MockError(403, "forbidden", "not a member of this organization");
          if (RANK[membership.role] < RANK[r.auth]) throw new MockError(403, "forbidden", `requires the ${r.auth} role`);
        }
      }
      const out = (await r.handler(ctx)) as Result | undefined;
      if (method !== "GET") saveDB();
      const res: Result = out && typeof out === "object" && ("data" in out || "status" in out || "meta" in out) ? out : { data: out };
      if (res.status === 204) return { status: 204, json: null };
      return { status: res.status ?? 200, json: { data: res.data ?? null, error: null, meta: res.meta ?? {} } };
    } catch (e) {
      if (e instanceof MockError) {
        return { status: e.status, json: { data: null, error: { code: e.code, message: e.message, fields: e.fields }, meta: {} } };
      }
      console.error("[demo mode]", method, path, e);
      return { status: 500, json: { data: null, error: { code: "internal", message: "Demo mode: this action failed in the simulated backend" }, meta: {} } };
    }
  }
  // Unknown endpoint: answer harmlessly so the UI keeps working.
  console.warn("[demo mode] no mock for", method, path);
  if (method === "GET") return { status: 200, json: { data: [], error: null, meta: {} } };
  return { status: 200, json: { data: {}, error: null, meta: {} } };
}

/** A fetch() replacement that serves /api/v1 from the mock backend. */
export async function mockFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, window.location.origin);
  const headers = new Headers(init.headers);
  const method = (init.method ?? "GET").toUpperCase();
  // Feels like a network round trip, so loading states are visible.
  await new Promise((r) => setTimeout(r, 120 + Math.random() * 220));
  if (url.pathname === "/api/v1/auth/refresh") {
    const uid = mockSession.get();
    const db = getDB();
    if (!uid || !db.users.some((u) => u.id === uid)) return new Response(JSON.stringify({ data: null, error: { code: "unauthorized", message: "no session" }, meta: {} }), { status: 401 });
    return Response.json({ data: { access_token: "mock." + uid }, error: null, meta: {} });
  }
  if (url.pathname === "/api/v1/leads/export") return exportCSV(url, headers);
  const { status, json } = await handle(method, url, headers, init.body);
  if (status === 204) return new Response(null, { status: 204 });
  return new Response(JSON.stringify(json), { status, headers: { "Content-Type": "application/json" } });
}

async function exportCSV(url: URL, headers: Headers): Promise<Response> {
  const { json } = await handle("GET", new URL(url.href.replace("/leads/export", "/leads") + "&per_page=500", url.origin), headers, null);
  const rows = ((json as any)?.data ?? []) as Row[];
  const cols = ["company_name", "website", "industry", "city", "state", "country", "phone", "business_email", "lead_score", "status"];
  const esc = (v: unknown) => `"${String(v ?? "").replace(/"/g, '""')}"`;
  const csv = [cols.join(","), ...rows.map((r) => cols.map((c) => esc(r[c])).join(","))].join("\n");
  return new Response(csv, { status: 200, headers: { "Content-Type": "text/csv" } });
}
