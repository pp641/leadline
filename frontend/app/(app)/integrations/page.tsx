"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CircleAlert, CircleCheck, Copy, Database, ExternalLink, Eye, EyeOff, KeyRound, Loader2, Mail, PlugZap, Search, Users, Workflow,
} from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/shared";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { Integration, IntegrationProvider, ProviderField } from "@/types/api";

const CATEGORY_ICON: Record<string, React.ElementType> = {
  "Lead data": Database, "Email finding & verification": Search, CRM: Users, "Email sending": Mail, Workflows: Workflow,
};
const CATEGORY_BLURB: Record<string, string> = {
  "Lead data": "Find companies and decision makers. Connected sources appear under Leads → Import.",
  "Email finding & verification": "Find the people and email addresses behind a company domain.",
  CRM: "Push leads to your CRM. Turn on auto-sync to send interested leads automatically.",
  "Email sending": "Deliver campaign email through your provider. Used by mailboxes without their own SMTP server.",
  Workflows: "Bring leads in from enrichment workflows.",
};
const CAPABILITY_LABEL: Record<string, string> = { import: "Lead import", crm_sync: "CRM sync", email_send: "Sending", inbound: "Inbound API" };

export function ProviderLogo({ p, size = "size-10" }: { p: Pick<IntegrationProvider, "name" | "color">; size?: string }) {
  const initials = p.name.replace(/[^A-Za-z0-9 ]/g, "").split(" ").filter(Boolean).slice(0, 2).map((w) => w[0]).join("").toUpperCase();
  return (
    <span className={`flex ${size} shrink-0 items-center justify-center rounded-lg text-sm font-bold text-white shadow-sm`}
      style={{ background: p.color }} aria-hidden>{initials}</span>
  );
}

function statusOf(i?: Integration): { label: string; tone: string } {
  if (!i) return { label: "Not connected", tone: "text-muted-foreground" };
  if (i.status === "disabled") return { label: "Paused", tone: "text-amber-600 dark:text-amber-400" };
  if (i.status === "error") return { label: "Needs attention", tone: "text-destructive" };
  return { label: "Connected", tone: "text-emerald-600 dark:text-emerald-400" };
}

export default function IntegrationsPage() {
  const canAdmin = useCan("admin");
  const q = useQuery({ queryKey: ["integrations"], queryFn: () => api.page<Integration[]>("/integrations") });
  const catalog = (q.data?.meta.catalog ?? []) as IntegrationProvider[];
  const categories = (q.data?.meta.categories ?? []) as string[];
  const byKind = useMemo(() => new Map((q.data?.data ?? []).map((i) => [i.kind, i])), [q.data]);
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("all");
  const [openKind, setOpenKind] = useState<string | null>(() => {
    if (typeof window === "undefined") return null;
    return new URLSearchParams(window.location.search).get("connect");
  });
  const term = search.trim().toLowerCase();
  const visible = catalog.filter((p) => (category === "all" || p.category === category) &&
    (!term || [p.name, p.tagline, p.description, p.category].some((x) => x.toLowerCase().includes(term))));
  const connectedCount = catalog.filter((p) => byKind.has(p.kind)).length;
  const open = catalog.find((p) => p.kind === openKind);

  return (
    <>
      <PageHeader title="Integrations"
        description="Connect data providers, email finders, your CRM and your sending service. Keys are encrypted at rest and never shown again."
        actions={<Badge variant="secondary" className="h-7 px-3 text-sm">{connectedCount} connected</Badge>} />

      {q.data?.meta.encryption_enabled === false && (
        <Alert variant="destructive" className="mb-4"><AlertTitle>Credentials can&apos;t be stored</AlertTitle><AlertDescription>Set ENCRYPTION_KEY on the server to connect integrations.</AlertDescription></Alert>
      )}

      <div className="mb-5 flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input className="w-64 pl-8" placeholder="Search integrations" value={search} onChange={(e) => setSearch(e.target.value)} />
        </div>
        <div className="flex flex-wrap gap-1">
          {["all", ...categories].map((c) => (
            <button key={c} type="button" onClick={() => setCategory(c)}
              className={`rounded-full border px-3 py-1 text-sm transition ${category === c ? "border-primary bg-primary text-primary-foreground" : "hover:bg-muted"}`}>
              {c === "all" ? "All" : c}
            </button>
          ))}
        </div>
      </div>

      <div className="space-y-8">
        {categories.filter((c) => visible.some((p) => p.category === c)).map((c) => {
          const Icon = CATEGORY_ICON[c] ?? PlugZap;
          return (
            <section key={c} className="space-y-3">
              <div>
                <h2 className="flex items-center gap-2 font-semibold"><Icon className="size-4 text-muted-foreground" /> {c}</h2>
                <p className="text-sm text-muted-foreground">{CATEGORY_BLURB[c]}</p>
              </div>
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                {visible.filter((p) => p.category === c).map((p) => {
                  const st = statusOf(byKind.get(p.kind));
                  return (
                    <button key={p.kind} type="button" onClick={() => setOpenKind(p.kind)}
                      className="group flex flex-col gap-3 rounded-xl border bg-card p-4 text-left shadow-xs transition hover:border-primary/50 hover:shadow-md">
                      <span className="flex items-start gap-3">
                        <ProviderLogo p={p} />
                        <span className="min-w-0 flex-1">
                          <span className="block font-medium">{p.name}</span>
                          <span className="block text-sm text-muted-foreground">{p.tagline}</span>
                        </span>
                      </span>
                      <span className="line-clamp-2 text-sm text-muted-foreground">{p.description}</span>
                      <span className="mt-auto flex items-center justify-between border-t pt-3 text-xs">
                        <span className={`flex items-center gap-1.5 font-medium ${st.tone}`}>
                          {st.label === "Connected" ? <CircleCheck className="size-3.5" /> : st.label === "Needs attention" ? <CircleAlert className="size-3.5" /> : <span className="size-2 rounded-full bg-current opacity-50" />}
                          {st.label}
                        </span>
                        <span className="flex gap-1">{p.capabilities.map((cap) => <span key={cap} className="rounded bg-muted px-1.5 py-0.5 text-muted-foreground">{CAPABILITY_LABEL[cap] ?? cap}</span>)}</span>
                      </span>
                    </button>
                  );
                })}
              </div>
            </section>
          );
        })}
        {q.isSuccess && visible.length === 0 && <p className="py-10 text-center text-sm text-muted-foreground">No integrations match “{search}”.</p>}
      </div>

      {open && (open.kind === "clay"
        ? <ClayDialog provider={open} onClose={() => setOpenKind(null)} />
        : <ConnectDialog key={open.kind} provider={open} current={byKind.get(open.kind)} canAdmin={canAdmin} onClose={() => setOpenKind(null)} />)}
    </>
  );
}

function FieldInput({ f, value, onChange, savedSecret, disabled }: {
  f: ProviderField; value: string; onChange: (v: string) => void; savedSecret?: boolean; disabled?: boolean;
}) {
  const [show, setShow] = useState(false);
  if (f.type === "toggle") {
    return (
      <label className="flex cursor-pointer items-center justify-between gap-3 rounded-lg border px-3 py-2.5 text-sm">
        <span>{f.label}{f.help && <span className="block text-xs text-muted-foreground">{f.help}</span>}</span>
        <Switch checked={value === "true"} disabled={disabled} onCheckedChange={(x) => onChange(String(x))} />
      </label>
    );
  }
  return (
    <div className="space-y-1.5">
      <Label htmlFor={`f-${f.key}`}>{f.label}{f.required && <span className="text-destructive"> *</span>}</Label>
      {f.type === "select" ? (
        <Select value={value || undefined} onValueChange={onChange} disabled={disabled}>
          <SelectTrigger id={`f-${f.key}`} className="w-full"><SelectValue placeholder="Choose…" /></SelectTrigger>
          <SelectContent>{(f.options ?? []).map((o) => <SelectItem key={o.value || "_"} value={o.value || "_"}>{o.label}</SelectItem>)}</SelectContent>
        </Select>
      ) : f.type === "textarea" ? (
        <Textarea id={`f-${f.key}`} rows={4} value={value} placeholder={f.placeholder} disabled={disabled} onChange={(e) => onChange(e.target.value)} />
      ) : (
        <div className="relative">
          <Input id={`f-${f.key}`} autoComplete="off" disabled={disabled}
            type={f.type === "secret" && !show ? "password" : f.type === "number" ? "number" : f.type === "email" ? "email" : "text"}
            value={value} onChange={(e) => onChange(e.target.value)}
            placeholder={f.type === "secret" && savedSecret ? "•••••••••••• saved — leave empty to keep" : f.placeholder}
            className={f.type === "secret" ? "pr-9 font-mono" : ""} />
          {f.type === "secret" && (
            <button type="button" className="absolute top-1/2 right-2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              aria-label={show ? "Hide" : "Show"} onClick={() => setShow((x) => !x)}>{show ? <EyeOff className="size-4" /> : <Eye className="size-4" />}</button>
          )}
        </div>
      )}
      {f.help && <p className="text-xs text-muted-foreground">{f.help}</p>}
    </div>
  );
}

function ConnectDialog({ provider: p, current, canAdmin, onClose }: { provider: IntegrationProvider; current?: Integration; canAdmin: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const [values, setValues] = useState<Record<string, string>>(() => Object.fromEntries(p.fields.map((f) => {
    const cfg = current?.config?.[f.key];
    if (f.type === "secret") return [f.key, ""];
    if (cfg !== undefined && cfg !== null && cfg !== "") return [f.key, String(cfg)];
    return [f.key, f.default ?? (f.type === "toggle" ? "false" : "")];
  })));
  const [active, setActive] = useState(current?.status !== "disabled");
  const refresh = () => qc.invalidateQueries({ queryKey: ["integrations"] });
  const missing = p.fields.filter((f) => f.required && f.type !== "toggle" &&
    !(values[f.key]?.trim() || (f.type === "secret" && current?.secrets_set?.includes(f.key))));
  const payload = () => ({
    values: Object.fromEntries(p.fields.map((f) => [f.key, f.type === "toggle" ? values[f.key] === "true" : (values[f.key] ?? "").replace(/^_$/, "")])),
    status: active ? "active" : "disabled",
  });
  const save = useMutation({
    mutationFn: async (thenTest: boolean) => {
      await api.put(`/integrations/${p.kind}`, payload());
      if (thenTest) await api.post(`/integrations/${p.kind}/test`);
    },
    onSuccess: (_, tested) => { toast.success(tested ? `${p.name} connected — connection works` : `${p.name} saved`); refresh(); onClose(); },
    onError: (e) => { toast.error(errorMessage(e)); refresh(); },
  });
  const test = useMutation({
    mutationFn: () => api.post(`/integrations/${p.kind}/test`),
    onSuccess: () => { toast.success("Connection works"); refresh(); },
    onError: (e) => { toast.error(errorMessage(e)); refresh(); },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/integrations/${p.kind}`),
    onSuccess: () => { toast.success(`${p.name} disconnected`); refresh(); onClose(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const st = statusOf(current);
  const busy = save.isPending || test.isPending || remove.isPending;

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex max-h-[90svh] flex-col gap-0 p-0 sm:max-w-lg">
        <DialogHeader className="border-b p-5">
          <div className="flex items-start gap-3">
            <ProviderLogo p={p} size="size-11" />
            <div className="min-w-0 flex-1 space-y-1">
              <DialogTitle className="flex flex-wrap items-center gap-2">{p.name} <span className={`text-xs font-medium ${st.tone}`}>· {st.label}</span></DialogTitle>
              <DialogDescription>{p.description}</DialogDescription>
              <div className="flex flex-wrap gap-3 pt-1 text-xs">
                {p.docs_url && <a className="inline-flex items-center gap-1 text-primary hover:underline" href={p.docs_url} target="_blank" rel="noreferrer noopener">Setup guide <ExternalLink className="size-3" /></a>}
                {p.website && <a className="inline-flex items-center gap-1 text-muted-foreground hover:underline" href={p.website} target="_blank" rel="noreferrer noopener">{new URL(p.website).hostname.replace(/^www\./, "")} <ExternalLink className="size-3" /></a>}
              </div>
            </div>
          </div>
        </DialogHeader>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-5">
          {current?.status === "error" && current.last_error && (
            <Alert variant="destructive"><CircleAlert /><AlertTitle>Last connection test failed</AlertTitle><AlertDescription className="break-words">{current.last_error}</AlertDescription></Alert>
          )}
          {!canAdmin && <p className="rounded-md bg-muted p-3 text-sm text-muted-foreground">Only admins can change integrations.</p>}
          <div className="flex items-center gap-2 text-sm font-medium"><KeyRound className="size-4 text-muted-foreground" /> Credentials</div>
          {p.fields.map((f) => (
            <FieldInput key={f.key} f={f} value={values[f.key] ?? ""} disabled={!canAdmin}
              savedSecret={current?.secrets_set?.includes(f.key)} onChange={(v) => setValues((x) => ({ ...x, [f.key]: v }))} />
          ))}
          {current && (
            <label className="flex cursor-pointer items-center justify-between gap-3 rounded-lg border px-3 py-2.5 text-sm">
              <span>Enabled<span className="block text-xs text-muted-foreground">Pause to stop using it without removing the keys.</span></span>
              <Switch checked={active} disabled={!canAdmin} onCheckedChange={setActive} />
            </label>
          )}
          {(p.notes ?? []).map((n) => <p key={n} className="rounded-md bg-muted/60 p-3 text-xs text-muted-foreground">{n}</p>)}
          {p.capabilities.includes("import") && (
            <p className="text-xs text-muted-foreground">Once connected, import from {p.name} under <Link className="text-primary underline" href="/leads">Leads → Import</Link>.</p>
          )}
        </div>

        {canAdmin && (
          <DialogFooter className="mx-0! mb-0! flex-row flex-wrap items-center gap-2 rounded-b-xl border-t p-4 sm:justify-between">
            <div className="flex gap-1">
              {current && <Button variant="ghost" className="text-destructive hover:text-destructive" disabled={busy}
                onClick={() => confirm(`Disconnect ${p.name}? Stored keys are deleted.`) && remove.mutate()}>Disconnect</Button>}
              {current && <Button variant="outline" disabled={busy} onClick={() => test.mutate()}>{test.isPending && <Loader2 className="animate-spin" />} Test</Button>}
            </div>
            <div className="flex gap-2">
              <Button variant="outline" disabled={busy || missing.length > 0} onClick={() => save.mutate(false)}>Save</Button>
              <Button disabled={busy || missing.length > 0} onClick={() => save.mutate(true)}>
                {save.isPending && <Loader2 className="animate-spin" />} {current ? "Save & test" : "Connect"}
              </Button>
            </div>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

function ClayDialog({ provider: p, onClose }: { provider: IntegrationProvider; onClose: () => void }) {
  const endpoint = typeof window !== "undefined" ? `${window.location.origin}/api/v1/leads` : "/api/v1/leads";
  const sample = JSON.stringify({
    company_name: "{{Company Name}}", website: "{{Website}}", industry: "{{Industry}}", city: "{{City}}", country: "{{Country}}",
    phone: "{{Phone}}", source: "clay", tags: ["clay"],
    contact: { first_name: "{{First Name}}", last_name: "{{Last Name}}", job_title: "{{Title}}", email: "{{Work Email}}" },
  }, null, 2);
  const copy = (s: string) => navigator.clipboard.writeText(s).then(() => toast.success("Copied"));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <div className="flex items-start gap-3">
            <ProviderLogo p={p} size="size-11" />
            <div className="space-y-1"><DialogTitle>{p.name}</DialogTitle><DialogDescription>{p.description}</DialogDescription></div>
          </div>
        </DialogHeader>
        <ol className="list-decimal space-y-3 pl-5 text-sm">
          <li>Create an API key with the <b>member</b> role under <Link className="text-primary underline" href="/settings?tab=developer">Settings → Developer</Link>.</li>
          <li>In your Clay table, add an <b>HTTP API</b> enrichment column: method <code>POST</code>, header <code>Authorization: Bearer ll_…</code>, and this endpoint:
            <div className="mt-1.5 flex items-center gap-2 rounded-md bg-muted px-2 py-1.5 font-mono text-xs"><span className="flex-1 break-all">{endpoint}</span>
              <Button size="icon-sm" variant="ghost" aria-label="Copy endpoint" onClick={() => copy(endpoint)}><Copy /></Button></div>
          </li>
          <li>Use this body, mapping the <code>{"{{…}}"}</code> placeholders to your Clay columns:
            <div className="relative mt-1.5">
              <pre className="max-h-56 overflow-auto rounded-md bg-muted p-3 text-xs">{sample}</pre>
              <Button size="icon-sm" variant="ghost" className="absolute top-1 right-1" aria-label="Copy body" onClick={() => copy(sample)}><Copy /></Button>
            </div>
          </li>
          <li>Run the column. Each row becomes a lead tagged <code>clay</code>; its contact is verified and the lead is scored automatically.</li>
        </ol>
        <DialogFooter>
          {p.docs_url && <Button variant="outline" asChild><a href={p.docs_url} target="_blank" rel="noreferrer noopener">Clay HTTP API docs <ExternalLink /></a></Button>}
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
