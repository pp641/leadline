"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, KeyRound, Plus, Send, Trash2, Webhook } from "lucide-react";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { StatusBadge, dateTime } from "@/components/shared";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { APIKey, WebhookDelivery, WebhookEndpoint } from "@/types/api";

/** API keys and outbound webhooks (admins only). */
export function DeveloperTab() {
  const canAdmin = useCan("admin");
  if (!canAdmin) return <p className="text-sm text-muted-foreground">Only admins can manage API keys and webhooks.</p>;
  return (
    <div className="space-y-6">
      <APIKeys />
      <Webhooks />
    </div>
  );
}

function APIKeys() {
  const qc = useQueryClient();
  const keys = useQuery({ queryKey: ["api-keys"], queryFn: () => api.get<APIKey[]>("/api-keys") });
  const [name, setName] = useState("");
  const [role, setRole] = useState("member");
  const [created, setCreated] = useState<APIKey | null>(null);
  const create = useMutation({
    mutationFn: () => api.post<APIKey>("/api-keys", { name, role }),
    onSuccess: (k) => { setCreated(k); setName(""); qc.invalidateQueries({ queryKey: ["api-keys"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api.del(`/api-keys/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["api-keys"] }),
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base"><KeyRound className="size-4" /> API keys</CardTitle>
        <CardDescription>Use <code>Authorization: Bearer ll_…</code> against <code>/api/v1</code>. Keys are scoped to this organization; no X-Organization-ID header needed. See docs/openapi.yaml.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <form className="flex flex-wrap gap-2" onSubmit={(e) => { e.preventDefault(); if (name.trim()) create.mutate(); }}>
          <Input className="w-56" placeholder="Key name (e.g. Zapier)" value={name} onChange={(e) => setName(e.target.value)} />
          <FilterSelect value={role} onChange={(v) => v && setRole(v)} placeholder="Role" className="w-32"
            options={["viewer", "member", "admin"].map((r) => ({ value: r, label: r }))} />
          <Button type="submit" disabled={!name.trim()}><Plus /> Create key</Button>
        </form>
        {created?.token && (
          <Alert>
            <AlertTitle>Copy your key now — it won&apos;t be shown again</AlertTitle>
            <AlertDescription className="flex items-center gap-2">
              <code className="break-all">{created.token}</code>
              <Button size="icon-sm" variant="ghost" aria-label="Copy" onClick={() => navigator.clipboard.writeText(created.token!).then(() => toast.success("Copied"))}><Copy /></Button>
            </AlertDescription>
          </Alert>
        )}
        <Table>
          <TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Key</TableHead><TableHead>Role</TableHead><TableHead>Last used</TableHead><TableHead /></TableRow></TableHeader>
          <TableBody>
            {(keys.data ?? []).map((k) => (
              <TableRow key={k.id} className={k.revoked_at ? "opacity-50" : ""}>
                <TableCell>{k.name}</TableCell>
                <TableCell><code className="text-xs">{k.prefix}_…</code></TableCell>
                <TableCell className="capitalize">{k.role}</TableCell>
                <TableCell>{k.revoked_at ? "Revoked" : dateTime(k.last_used_at)}</TableCell>
                <TableCell className="text-right">{!k.revoked_at && <Button size="sm" variant="ghost" onClick={() => confirm("Revoke this key?") && revoke.mutate(k.id)}>Revoke</Button>}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

function Webhooks() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["webhooks"], queryFn: () => api.page<WebhookEndpoint[]>("/webhook-endpoints") });
  const types = (q.data?.meta.event_types ?? []) as string[];
  const [url, setUrl] = useState("");
  const [events, setEvents] = useState<string[]>([]);
  const [secret, setSecret] = useState<string | null>(null);
  const [logFor, setLogFor] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => api.post<WebhookEndpoint>("/webhook-endpoints", { url, events }),
    onSuccess: (e) => { setSecret(e.secret ?? null); setUrl(""); setEvents([]); qc.invalidateQueries({ queryKey: ["webhooks"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const act = useMutation({
    mutationFn: ({ id, kind }: { id: string; kind: "test" | "delete" }) => (kind === "test" ? api.post(`/webhook-endpoints/${id}/test`) : api.del(`/webhook-endpoints/${id}`)),
    onSuccess: (_, v) => { toast.success(v.kind === "test" ? "Test event queued" : "Deleted"); qc.invalidateQueries({ queryKey: ["webhooks"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base"><Webhook className="size-4" /> Webhooks</CardTitle>
        <CardDescription>
          We POST JSON events to your URL, signed with <code>X-Leadline-Signature: t=…,v1=hex(HMAC-SHA256(secret, &quot;t.body&quot;))</code>. Failed deliveries are retried with backoff.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="space-y-2 rounded-lg border p-3">
          <Input placeholder="https://example.com/hooks/leadline" value={url} onChange={(e) => setUrl(e.target.value)} />
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            {types.map((t) => (
              <label key={t} className="flex items-center gap-1.5 text-sm">
                <input type="checkbox" checked={events.includes(t)} onChange={(e) => setEvents((x) => (e.target.checked ? [...x, t] : x.filter((y) => y !== t)))} />
                <code className="text-xs">{t}</code>
              </label>
            ))}
          </div>
          <Button size="sm" disabled={!url || events.length === 0 || create.isPending} onClick={() => create.mutate()}><Plus /> Add endpoint</Button>
        </div>
        {secret && (
          <Alert>
            <AlertTitle>Signing secret — copy it now</AlertTitle>
            <AlertDescription className="flex items-center gap-2"><code className="break-all">{secret}</code>
              <Button size="icon-sm" variant="ghost" aria-label="Copy" onClick={() => navigator.clipboard.writeText(secret).then(() => toast.success("Copied"))}><Copy /></Button>
            </AlertDescription>
          </Alert>
        )}
        {(q.data?.data ?? []).map((e) => (
          <div key={e.id} className="rounded-lg border p-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <code className="break-all">{e.url}</code>
              <div className="ml-auto flex gap-1">
                <Button size="sm" variant="outline" onClick={() => act.mutate({ id: e.id, kind: "test" })}><Send /> Test</Button>
                <Button size="sm" variant="ghost" onClick={() => setLogFor(logFor === e.id ? null : e.id)}>Deliveries</Button>
                <Button size="icon-sm" variant="ghost" aria-label="Delete" onClick={() => confirm("Delete this endpoint?") && act.mutate({ id: e.id, kind: "delete" })}><Trash2 /></Button>
              </div>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">{e.events.join(", ")}</p>
            {logFor === e.id && <Deliveries id={e.id} />}
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function Deliveries({ id }: { id: string }) {
  const q = useQuery({ queryKey: ["webhooks", id, "deliveries"], queryFn: () => api.get<WebhookDelivery[]>(`/webhook-endpoints/${id}/deliveries`), refetchInterval: 5000 });
  return (
    <Table className="mt-2">
      <TableHeader><TableRow><TableHead>Event</TableHead><TableHead>Status</TableHead><TableHead>HTTP</TableHead><TableHead>Attempts</TableHead><TableHead>When</TableHead></TableRow></TableHeader>
      <TableBody>
        {(q.data ?? []).map((d) => (
          <TableRow key={d.id}>
            <TableCell><code className="text-xs">{d.event_type}</code></TableCell>
            <TableCell><StatusBadge status={d.status === "succeeded" ? "completed" : d.status === "failed" ? "failed" : "queued"} />{d.last_error && <span className="block max-w-56 truncate text-xs text-muted-foreground" title={d.last_error}>{d.last_error}</span>}</TableCell>
            <TableCell>{d.response_status ?? "—"}</TableCell>
            <TableCell>{d.attempts}</TableCell>
            <TableCell>{dateTime(d.created_at)}</TableCell>
          </TableRow>
        ))}
        {q.data?.length === 0 && <TableRow><TableCell colSpan={5} className="text-center text-muted-foreground">No deliveries yet</TableCell></TableRow>}
      </TableBody>
    </Table>
  );
}
