import { MOCK_MODE } from "./mock/config";

// Thin client for the Go API. Every response uses the {data, error, meta}
// envelope. The access token lives only in memory; the refresh token is an
// httpOnly cookie the browser sends to /api/v1/auth/refresh.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public fields: Record<string, string> = {},
  ) {
    super(message);
  }
}

interface Envelope<T> {
  data: T;
  error: { code: string; message: string; fields?: Record<string, string> } | null;
  meta: Record<string, unknown>;
}

let mockFetchP: Promise<typeof fetch> | null = null;

/**
 * fetch() for the API. In demo mode it is served by the in-browser mock
 * backend, loaded lazily so regular builds never download it.
 */
export function apiFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  if (!MOCK_MODE) return fetch(input, init);
  mockFetchP ??= import("./mock").then((m) => m.mockFetch as typeof fetch);
  return mockFetchP.then((f) => f(input, init));
}

let accessToken: string | null = null;
let refreshing: Promise<boolean> | null = null;
let onSessionLost: (() => void) | null = null;

const ORG_KEY = "leadline.org";

export const session = {
  setToken(t: string | null) {
    accessToken = t;
  },
  hasToken: () => accessToken !== null,
  onLost(fn: () => void) {
    onSessionLost = fn;
  },
  get orgId(): string | null {
    try {
      return localStorage.getItem(ORG_KEY);
    } catch {
      return null;
    }
  },
  set orgId(id: string | null) {
    try {
      if (id) localStorage.setItem(ORG_KEY, id);
      else localStorage.removeItem(ORG_KEY);
    } catch {
      /* storage unavailable: org falls back to the first membership */
    }
  },
};

/** Exchanges the refresh cookie for a new access token. Concurrent callers share one request. */
export function refreshSession(): Promise<boolean> {
  refreshing ??= apiFetch("/api/v1/auth/refresh", {
    method: "POST",
    headers: { "X-Requested-With": "fetch" },
    credentials: "same-origin",
  })
    .then(async (r) => {
      if (!r.ok) return false;
      const body = (await r.json()) as Envelope<{ access_token: string }>;
      accessToken = body.data.access_token;
      return true;
    })
    .catch(() => false)
    .finally(() => {
      refreshing = null;
    });
  return refreshing;
}

type Query = Record<string, string | number | boolean | string[] | undefined | null>;

export function toQuery(q: Query): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) {
    if (v === undefined || v === null || v === "") continue;
    if (Array.isArray(v)) {
      if (v.length) p.set(k, v.join(","));
    } else p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
}

async function raw(path: string, init: RequestInit = {}, retry = true): Promise<Response> {
  const headers = new Headers(init.headers);
  if (accessToken) headers.set("Authorization", `Bearer ${accessToken}`);
  const org = session.orgId;
  if (org) headers.set("X-Organization-ID", org);
  if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await apiFetch(`/api/v1${path}`, { ...init, headers, credentials: "same-origin" });
  if (res.status === 401 && retry && !path.startsWith("/auth/")) {
    if (await refreshSession()) return raw(path, init, false);
    onSessionLost?.();
  }
  return res;
}

async function parse<T>(res: Response): Promise<{ data: T; meta: Record<string, unknown> }> {
  if (res.status === 204) return { data: undefined as T, meta: {} };
  let body: Envelope<T>;
  try {
    body = await res.json();
  } catch {
    throw new ApiError(res.status, "bad_response", `Unexpected response (${res.status})`);
  }
  if (!res.ok || body.error) {
    const e = body.error ?? { code: "http_error", message: res.statusText };
    throw new ApiError(res.status, e.code, e.message, e.fields ?? {});
  }
  return { data: body.data, meta: body.meta ?? {} };
}

export const api = {
  async get<T>(path: string, query: Query = {}): Promise<T> {
    return (await parse<T>(await raw(path + toQuery(query)))).data;
  },
  async page<T>(path: string, query: Query = {}): Promise<{ data: T; meta: Record<string, unknown> }> {
    return parse<T>(await raw(path + toQuery(query)));
  },
  async post<T>(path: string, body?: unknown): Promise<T> {
    const init: RequestInit = { method: "POST" };
    if (body instanceof FormData) init.body = body;
    else if (body !== undefined) init.body = JSON.stringify(body);
    return (await parse<T>(await raw(path, init))).data;
  },
  async put<T>(path: string, body: unknown): Promise<T> {
    return (await parse<T>(await raw(path, { method: "PUT", body: JSON.stringify(body) }))).data;
  },
  async patch<T>(path: string, body: unknown): Promise<T> {
    return (await parse<T>(await raw(path, { method: "PATCH", body: JSON.stringify(body) }))).data;
  },
  async del(path: string): Promise<void> {
    await parse<void>(await raw(path, { method: "DELETE" }));
  },
  /** Downloads a file (e.g. CSV export) with auth headers. */
  async download(path: string, query: Query, filename: string): Promise<void> {
    const res = await raw(path + toQuery(query));
    if (!res.ok) await parse(res);
    const url = URL.createObjectURL(await res.blob());
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.click();
    URL.revokeObjectURL(url);
  },
};

/** Human message for any thrown error, including field errors. */
export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    const fields = Object.values(e.fields);
    return fields.length ? `${e.message}: ${fields.join("; ")}` : e.message;
  }
  return e instanceof Error ? e.message : "Something went wrong";
}
