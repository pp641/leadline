"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Globe, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { TaskList } from "@/components/crm/task-list";
import { FilterSelect } from "@/components/filter-select";
import { dateTime } from "@/components/shared";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { LeadDetail, Member, Note } from "@/types/api";

export function OwnerSelect({ lead }: { lead: LeadDetail }) {
  const qc = useQueryClient();
  const members = useQuery({ queryKey: ["members"], queryFn: () => api.get<Member[]>("/organizations/current/members") });
  const assign = useMutation({
    mutationFn: (owner: string) => api.post("/crm/assign", { lead_ids: [lead.id], owner_id: owner || null }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["lead", lead.id] }); qc.invalidateQueries({ queryKey: ["leads"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const options = (members.data ?? []).map((m) => ({ value: m.user_id, label: m.full_name }));
  if (lead.owner_id && !options.some((o) => o.value === lead.owner_id)) options.push({ value: lead.owner_id, label: lead.owner_name ?? "Owner" });
  return <FilterSelect value={lead.owner_id ?? ""} onChange={(v) => assign.mutate(v)} placeholder="Unassigned" className="w-40" options={options} />;
}

export function FitCard({ lead }: { lead: LeadDetail }) {
  const f = lead.icp_fit_detail;
  if (!f) return null;
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="text-base">ICP fit</CardTitle>
        <span className="text-sm font-semibold tabular-nums">{f.fit}%</span>
      </CardHeader>
      <CardContent className="space-y-1 text-sm">
        {f.matched.map((m) => <p key={m}>✓ {m}</p>)}
        {f.missed.map((m) => <p key={m} className="text-muted-foreground">✗ {m}</p>)}
      </CardContent>
    </Card>
  );
}

interface WebsiteReport { quality: string; issues: string[]; tech_stack: string[]; social_urls: string[]; title?: string; blocked_by?: string; load_ms: number }
interface Check { passed: boolean | null; reason?: string; status?: string }
interface CompanyDBReport {
  verification_status: string;
  verification?: { checks?: Record<string, Check> };
  website?: WebsiteReport | null;
}

export function EnrichmentCard({ lead }: { lead: LeadDetail }) {
  const canEdit = useCan("member");
  const q = useQuery({ queryKey: ["lead", lead.id, "enrichment"], queryFn: () => api.get<Record<string, unknown>>(`/leads/${lead.id}/enrichment`) });
  const sync = useMutation({
    mutationFn: () => api.post("/crm/sync", { lead_ids: [lead.id] }),
    onSuccess: () => toast.success("Queued for HubSpot"),
    onError: (e) => toast.error(errorMessage(e)),
  });
  // Leads imported from the company database carry its report; older leads may have a "website" one.
  const db = q.data?.company_db as CompanyDBReport | undefined;
  const w = db?.website ?? (q.data?.website as WebsiteReport | undefined);
  const checks = Object.entries(db?.verification?.checks ?? {});
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="flex items-center gap-2 text-base"><Globe className="size-4" /> Website & verification</CardTitle>
        {canEdit && <Button size="sm" variant="ghost" onClick={() => sync.mutate()}>HubSpot</Button>}
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        {db && (
          <div className="space-y-1">
            <p>Verification: <Badge variant="secondary" className="capitalize">{db.verification_status}</Badge></p>
            {checks.length > 0 && (
              <ul className="space-y-0.5 text-xs text-muted-foreground">
                {checks.map(([name, c]) => (
                  <li key={name}><span className={c.passed === true ? "text-green-600" : c.passed === false ? "text-destructive" : ""}>{c.passed === true ? "✓" : c.passed === false ? "✗" : "–"}</span> <span className="capitalize">{name}</span>: {c.reason ?? c.status}</li>
                ))}
              </ul>
            )}
          </div>
        )}
        {!w ? <p className="text-muted-foreground">{db ? "No website analysis." : "Not from the company database, so there is no website analysis."}</p> : w.blocked_by ? (
          <p className="text-muted-foreground">The site&apos;s robots.txt asks crawlers not to visit, so it was not analysed.</p>
        ) : (
          <>
            <p>Website: <Badge variant="secondary" className="capitalize">{w.quality || "unknown"}</Badge>{w.load_ms > 0 && <span className="text-xs text-muted-foreground"> · {(w.load_ms / 1000).toFixed(1)}s</span>}</p>
            {w.issues?.length > 0 && <ul className="list-disc pl-5 text-muted-foreground">{w.issues.map((i) => <li key={i}>{i}</li>)}</ul>}
            {w.tech_stack?.length > 0 && <p className="flex flex-wrap gap-1">{w.tech_stack.map((t) => <Badge key={t} variant="outline">{t}</Badge>)}</p>}
            {w.social_urls?.length > 0 && <p className="truncate text-xs">{w.social_urls.map((s) => <a key={s} className="mr-2 underline" href={s} target="_blank" rel="noreferrer noopener">{new URL(s).hostname.replace("www.", "")}</a>)}</p>}
          </>
        )}
        {lead.enriched_at && <p className="text-xs text-muted-foreground">Checked by company-scraper {dateTime(lead.enriched_at)}</p>}
      </CardContent>
    </Card>
  );
}

export function NotesCard({ leadId }: { leadId: string }) {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const [body, setBody] = useState("");
  const notes = useQuery({ queryKey: ["lead", leadId, "notes"], queryFn: () => api.get<Note[]>(`/leads/${leadId}/notes`) });
  const add = useMutation({
    mutationFn: () => api.post(`/leads/${leadId}/notes`, { body }),
    onSuccess: () => { setBody(""); qc.invalidateQueries({ queryKey: ["lead", leadId] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const del = useMutation({
    mutationFn: (id: string) => api.del(`/notes/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["lead", leadId, "notes"] }),
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <Card>
      <CardHeader><CardTitle className="text-base">Notes</CardTitle></CardHeader>
      <CardContent className="space-y-3">
        {canEdit && (
          <div className="space-y-2">
            <Textarea rows={2} placeholder="Add a note…" value={body} onChange={(e) => setBody(e.target.value)} />
            <Button size="sm" disabled={!body.trim() || add.isPending} onClick={() => add.mutate()}>Add note</Button>
          </div>
        )}
        {(notes.data ?? []).map((n) => (
          <div key={n.id} className="group border-l-2 pl-3 text-sm">
            <p className="whitespace-pre-wrap">{n.body}</p>
            <p className="text-xs text-muted-foreground">
              {n.author_name ?? "Someone"} · {dateTime(n.created_at)}
              {canEdit && <button className="ml-2 hidden group-hover:inline" aria-label="Delete note" onClick={() => del.mutate(n.id)}><Trash2 className="inline size-3" /></button>}
            </p>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

export function LeadTasks({ leadId }: { leadId: string }) {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const [title, setTitle] = useState("");
  const [due, setDue] = useState("");
  const add = useMutation({
    mutationFn: () => api.post("/tasks", { lead_id: leadId, title, due_at: due ? new Date(due).toISOString() : null }),
    onSuccess: () => { setTitle(""); setDue(""); qc.invalidateQueries({ queryKey: ["tasks"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <div className="space-y-2">
      {canEdit && (
        <form className="flex flex-wrap gap-2" onSubmit={(e) => { e.preventDefault(); if (title.trim()) add.mutate(); }}>
          <Input className="min-w-40 flex-1" placeholder="New task, e.g. Call to book demo" value={title} onChange={(e) => setTitle(e.target.value)} />
          <Input className="w-48" type="datetime-local" value={due} onChange={(e) => setDue(e.target.value)} />
          <Button type="submit" size="sm" disabled={!title.trim()}><Plus /> Task</Button>
        </form>
      )}
      <TaskList leadId={leadId} />
    </div>
  );
}
