"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, apiFetch, refreshSession, session } from "@/lib/api";
import type { Organization, User } from "@/types/api";

interface AuthState {
  status: "loading" | "anonymous" | "authenticated";
  user: User | null;
  orgs: Organization[];
  org: Organization | null;
  login(email: string, password: string): Promise<void>;
  register(fullName: string, email: string, password: string, acceptTerms: boolean): Promise<void>;
  /** Re-reads the signed-in user (e.g. after verifying their email). */
  refreshUser(): Promise<void>;
  /** Signs out and loads `to` (default: the public home page). */
  logout(to?: string): Promise<void>;
  selectOrg(id: string): void;
  reloadOrgs(): Promise<Organization[]>;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const qc = useQueryClient();
  const [status, setStatus] = useState<AuthState["status"]>("loading");
  const [user, setUser] = useState<User | null>(null);
  const [orgs, setOrgs] = useState<Organization[]>([]);
  const [orgId, setOrgId] = useState<string | null>(null);

  const reloadOrgs = useCallback(async () => {
    const list = await api.get<Organization[]>("/organizations");
    setOrgs(list);
    const saved = session.orgId;
    const pick = list.find((o) => o.id === saved) ?? list[0] ?? null;
    session.orgId = pick?.id ?? null;
    setOrgId(pick?.id ?? null);
    return list;
  }, []);

  const loadSession = useCallback(async () => {
    const me = await api.get<User>("/auth/me");
    setUser(me);
    await reloadOrgs();
    setStatus("authenticated");
  }, [reloadOrgs]);

  // On first load, try to restore the session from the refresh cookie.
  useEffect(() => {
    session.onLost(() => {
      setStatus("anonymous");
      setUser(null);
    });
    refreshSession()
      .then((ok) => (ok ? loadSession() : setStatus("anonymous")))
      .catch(() => setStatus("anonymous"));
  }, [loadSession]);

  const value = useMemo<AuthState>(
    () => ({
      status,
      user,
      orgs,
      org: orgs.find((o) => o.id === orgId) ?? null,
      async login(email, password) {
        const res = await api.post<{ access_token: string }>("/auth/login", { email, password });
        session.setToken(res.access_token);
        await loadSession();
      },
      async register(full_name, email, password, accept_terms) {
        const res = await api.post<{ access_token: string }>("/auth/register", { full_name, email, password, accept_terms });
        session.setToken(res.access_token);
        await loadSession();
      },
      async logout(to = "/") {
        await apiFetch("/api/v1/auth/logout", { method: "POST", headers: { "X-Requested-With": "fetch" } }).catch(() => {});
        session.setToken(null);
        session.orgId = null;
        qc.clear();
        // A full navigation to the public home page (rather than flipping state)
        // so protected pages don't race to redirect to /login first.
         
        window.location.assign(to);
      },
      selectOrg(id) {
        session.orgId = id;
        setOrgId(id);
        qc.clear(); // every cached query belongs to the previous tenant
      },
      reloadOrgs,
      async refreshUser() {
        setUser(await api.get<User>("/auth/me"));
      },
    }),
    [status, user, orgs, orgId, loadSession, reloadOrgs, qc],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside AuthProvider");
  return ctx;
}

const rank = { viewer: 1, member: 2, admin: 3, owner: 4 } as const;

/** Whether the current user's role in the current org is at least `min`. */
export function useCan(min: keyof typeof rank): boolean {
  const { org } = useAuth();
  return !!org?.role && rank[org.role] >= rank[min];
}
