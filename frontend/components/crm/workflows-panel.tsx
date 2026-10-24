"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowLeft, ArrowUp, CheckCircle2, CircleSlash, FlaskConical, History, Pencil, Plus, Sparkles, Trash2, Workflow as WorkflowIcon, XCircle, Zap } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { FilterSelect } from "@/components/filter-select";
import { MultiSelect, flag, type MultiOption } from "@/components/multi-select";
import { EmptyState, dateTime } from "@/components/shared";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import { leadsService } from "@/services/leads";
import type {
  DealPipeline, Lead, Member, Workflow, WorkflowAction, WorkflowCondition, WorkflowInput, WorkflowMeta, WorkflowRun, WorkflowTemplate, WorkflowTestResult,
} from "@/types/api";

type Meta = WorkflowMeta;
type Ctx = { meta: Meta; stages: { id: string; name: string; kind: string; pipeline: string }[]; members: Member[] };

function useCtx(): Ctx | null {
  const meta = useQuery({ queryKey: ["workflows", "meta"], queryFn: () => api.get<Meta>("/workflows/meta"), staleTime: Infinity });
  const pipes = useQuery({ queryKey: ["pipelines"], queryFn: () => api.get<DealPipeline[]>("/pipelines") });
  const members = useQuery({ queryKey: ["members"], queryFn: () => api.get<Member[]>("/organizations/current/members") });
  if (!meta.data) return null;
  const multi = (pipes.data?.length ?? 0) > 1;
  const stages = (pipes.data ?? []).flatMap((p) => p.stages.map((s) => ({ id: s.id, name: multi ? `${p.name} · ${s.name}` : s.name, kind: s.kind, pipeline: p.id })));
  return { meta: meta.data, stages, members: members.data ?? [] };
}

const asList = (v: unknown): string[] => (Array.isArray(v) ? v.map(String) : typeof v === "string" && v ? v.split(",").map((s) => s.trim()).filter(Boolean) : []);

/** One-line plain-English summary of a workflow. */
function summarize(w: Pick<Workflow, "trigger" | "trigger_config" | "conditions" | "actions">, c: Ctx) {
  const t = c.meta.triggers.find((x) => x.trigger === w.trigger);
  let when = t?.label ?? w.trigger;
  const cfg = w.trigger_config ?? {};
  if (w.trigger === "reply.classified" && asList(cfg.classifications).length) {
    const opts = c.meta.fields.find((f) => f.field === "reply.classification")?.options ?? [];
    when = `A reply is classified ${asList(cfg.classifications).map((v) => opts.find((o) => o.value === v)?.label ?? v).join(" or ")}`;
  }
  if (w.trigger === "deal.stage_changed" && cfg.to_stage_id) when = `A deal enters ${c.stages.find((s) => s.id === cfg.to_stage_id)?.name ?? "a stage"}`;
  if (w.trigger === "deal.stale" && Number(cfg.days) > 0) when = `A deal is stuck in a stage for ${cfg.days}+ days`;
  if (w.trigger === "deal.inactive") when = `A deal has no activity for ${Number(cfg.days) || 14} days`;
  const then = w.actions.map((a) => c.meta.actions.find((x) => x.type === a.type)?.label ?? a.type);
  return { when, conditions: w.conditions?.length ?? 0, then };
}

export function WorkflowsPanel() {
  const qc = useQueryClient();
  const isAdmin = useCan("admin");
  const ctx = useCtx();
  const list = useQuery({ queryKey: ["workflows"], queryFn: () => api.get<Workflow[]>("/workflows") });
  const [history, setHistory] = useState<Workflow | null>(null);
  const [gallery, setGallery] = useState(false);

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.put<Workflow>(`/workflows/${id}`, { enabled }),
    onMutate: ({ id, enabled }) => qc.setQueryData<Workflow[]>(["workflows"], (ws) => ws?.map((w) => (w.id === id ? { ...w, enabled } : w))),
    onSuccess: (w) => toast.success(`${w.name} ${w.enabled ? "turned on" : "paused"}`),
    onError: (e) => { toast.error(errorMessage(e)); qc.invalidateQueries({ queryKey: ["workflows"] }); },
  });
  const del = useMutation({
    mutationFn: (id: string) => api.del(`/workflows/${id}`),
    onSuccess: () => { toast.success("Workflow deleted"); qc.invalidateQueries({ queryKey: ["workflows"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });

  if (!ctx || list.isLoading) return <div className="space-y-3">{Array.from({ length: 3 }, (_, i) => <Skeleton key={i} className="h-24 w-full" />)}</div>;
  const workflows = list.data ?? [];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <p className="max-w-2xl text-sm text-muted-foreground">
          Workflows run automatically: <span className="font-medium text-foreground">when</span> something happens, <span className="font-medium text-foreground">if</span> the lead or deal matches, <span className="font-medium text-foreground">then</span> do the follow-up for you.
        </p>
        {isAdmin && (
          <div className="ml-auto flex gap-2">
            <Button variant="outline" size="sm" onClick={() => setGallery(true)}><Sparkles className="size-4" />Templates</Button>
            <Button size="sm" asChild><Link href="/crm/workflows/new"><Plus className="size-4" />New workflow</Link></Button>
          </div>
        )}
      </div>

      {workflows.length === 0 ? (
        <div className="space-y-4">
          <EmptyState icon={WorkflowIcon} title="No workflows yet">
            Start from a proven template below — each one can be edited after you add it.
          </EmptyState>
          <TemplateGrid onDone={() => undefined} disabled={!isAdmin} />
        </div>
      ) : (
        <div className="grid gap-3">
          {workflows.map((w) => {
            const s = summarize(w, ctx);
            return (
              <Card key={w.id} className={cn("py-4", !w.enabled && "opacity-70")}>
                <CardContent className="flex flex-col gap-3 px-4 sm:flex-row sm:items-center">
                  <Switch checked={w.enabled} disabled={!isAdmin} aria-label={w.enabled ? "Pause workflow" : "Turn on workflow"} onCheckedChange={(enabled) => toggle.mutate({ id: w.id, enabled })} />
                  <div className="min-w-0 flex-1 space-y-1.5">
                    <div className="flex flex-wrap items-center gap-2">
                      <Link href={`/crm/workflows/${w.id}`} className="font-medium hover:underline">{w.name}</Link>
                      {!w.enabled && <Badge variant="outline" className="font-normal">Paused</Badge>}
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5 text-xs">
                      <span className="inline-flex items-center gap-1 rounded-md bg-amber-500/15 px-2 py-0.5 text-amber-800 dark:text-amber-200"><Zap className="size-3" />{s.when}</span>
                      {s.conditions > 0 && <span className="rounded-md bg-sky-500/15 px-2 py-0.5 text-sky-800 dark:text-sky-200">if {s.conditions} condition{s.conditions > 1 ? "s" : ""}</span>}
                      <span className="text-muted-foreground">→</span>
                      {s.then.map((t, i) => <span key={i} className="rounded-md bg-muted px-2 py-0.5">{t}</span>)}
                    </div>
                  </div>
                  <div className="flex items-center gap-1 sm:ml-auto">
                    <div className="mr-2 text-right text-xs text-muted-foreground">
                      <p className="tabular-nums"><span className="font-medium text-foreground">{w.run_count}</span> run{w.run_count === 1 ? "" : "s"}</p>
                      <p>{w.last_run_at ? `last ${dateTime(w.last_run_at)}` : "never run"}</p>
                    </div>
                    <Button variant="ghost" size="icon" aria-label="Run history" onClick={() => setHistory(w)}><History className="size-4" /></Button>
                    <Button variant="ghost" size="icon" aria-label={isAdmin ? "Edit workflow" : "View workflow"} asChild><Link href={`/crm/workflows/${w.id}`}><Pencil className="size-4" /></Link></Button>
                    {isAdmin && <Button variant="ghost" size="icon" aria-label="Delete workflow" onClick={() => { if (confirm(`Delete “${w.name}” and its run history?`)) del.mutate(w.id); }}><Trash2 className="size-4" /></Button>}
                  </div>
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      <Dialog open={gallery} onOpenChange={setGallery}>
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>Workflow templates</DialogTitle>
            <DialogDescription>Common sales automations. Adding one turns it on; you can edit or pause it any time.</DialogDescription>
          </DialogHeader>
          <div className="max-h-[65vh] overflow-y-auto pr-1"><TemplateGrid onDone={() => setGallery(false)} /></div>
        </DialogContent>
      </Dialog>
      <RunsSheet workflow={history} onClose={() => setHistory(null)} />
    </div>
  );
}

function TemplateGrid({ onDone, disabled }: { onDone: () => void; disabled?: boolean }) {
  const qc = useQueryClient();
  const ctx = useCtx();
  const templates = useQuery({ queryKey: ["workflows", "templates"], queryFn: () => api.get<WorkflowTemplate[]>("/workflows/templates"), staleTime: Infinity });
  const existing = useQuery({ queryKey: ["workflows"], queryFn: () => api.get<Workflow[]>("/workflows") });
  const add = useMutation({
    mutationFn: (key: string) => api.post<Workflow>("/workflows", { template: key }),
    onSuccess: (w) => { toast.success(`“${w.name}” is on`); qc.invalidateQueries({ queryKey: ["workflows"] }); onDone(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {(templates.data ?? []).map((t) => {
        const used = existing.data?.some((w) => w.template === t.key);
        const s = ctx ? summarize({ ...t, conditions: t.conditions ?? [] }, ctx) : null;
        return (
          <div key={t.key} className="flex flex-col gap-2 rounded-lg border bg-card p-4">
            <div className="flex items-center justify-between gap-2">
              <Badge variant="outline" className="font-normal">{t.category}</Badge>
              {used && <span className="text-xs text-muted-foreground">Added</span>}
            </div>
            <p className="font-medium leading-snug">{t.name}</p>
            <p className="flex-1 text-sm text-muted-foreground">{t.description}</p>
            {s && <p className="text-xs text-muted-foreground"><Zap className="mr-1 inline size-3 text-amber-600" />{s.then.join(" · ")}</p>}
            <Button size="sm" variant={used ? "outline" : "default"} disabled={disabled || add.isPending} onClick={() => add.mutate(t.key)} className="self-start">
              {used ? "Add another" : "Use template"}
            </Button>
          </div>
        );
      })}
    </div>
  );
}

// ───────────── editor ─────────────

const BLANK: WorkflowInput = { name: "", description: "", trigger: "reply.classified", trigger_config: { classifications: ["interested"] }, conditions: [], actions: [{ type: "create_task", params: { title: "Follow up with {{company}}", due_in_days: 1, assignee: "owner" } }] };

/** Full-page workflow builder: /crm/workflows/new and /crm/workflows/[id]. */
export function WorkflowEditorPage({ id }: { id: string | null }) {
  const ctx = useCtx();
  const isAdmin = useCan("admin");
  const wf = useQuery({ queryKey: ["workflows", id], queryFn: () => api.get<Workflow>(`/workflows/${id}`), enabled: !!id });
  if (!ctx || (id && !wf.data)) {
    return wf.isError ? (
      <EmptyState icon={WorkflowIcon} title="Workflow not found">
        <Link href="/crm?tab=workflows" className="underline underline-offset-2">Back to workflows</Link>
      </EmptyState>
    ) : (
      <div className="space-y-4"><Skeleton className="h-10 w-72" /><Skeleton className="h-96 w-full" /></div>
    );
  }
  // Keyed so the form starts from the loaded workflow without syncing state in an effect.
  return <WorkflowEditor key={wf.data?.updated_at ?? "new"} ctx={ctx} workflow={wf.data ?? null} readOnly={!isAdmin} />;
}

function WorkflowEditor({ ctx, workflow, readOnly }: { ctx: Ctx; workflow: Workflow | null; readOnly: boolean }) {
  const qc = useQueryClient();
  const router = useRouter();
  const [w, setW] = useState<WorkflowInput>(() => workflow
    ? { name: workflow.name, description: workflow.description, trigger: workflow.trigger, trigger_config: workflow.trigger_config, conditions: workflow.conditions, actions: workflow.actions, enabled: workflow.enabled }
    : BLANK);
  const [dirty, setDirty] = useState(false);
  const set = (patch: Partial<WorkflowInput>) => { setW((cur) => ({ ...cur, ...patch })); setDirty(true); };
  const back = () => router.push("/crm?tab=workflows");
  const save = useMutation({
    mutationFn: () => (workflow ? api.put<Workflow>(`/workflows/${workflow.id}`, w) : api.post<Workflow>("/workflows", { ...w, enabled: true })),
    onSuccess: (saved) => {
      toast.success(`“${saved.name}” saved`);
      setDirty(false);
      qc.invalidateQueries({ queryKey: ["workflows"] });
      if (!workflow) router.replace(`/crm/workflows/${saved.id}`);
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const trig = ctx.meta.triggers.find((t) => t.trigger === w.trigger);
  const groups = [...new Set(ctx.meta.triggers.map((t) => t.group))];
  const summary = summarize(w, ctx);
  const canSave = !readOnly && !save.isPending && !!w.name.trim() && w.actions.length > 0 && (dirty || !workflow);

  return (
    <div className="space-y-6">
      <div className="sticky top-12 z-20 -mx-4 -mt-6 flex flex-wrap items-center gap-3 border-b bg-background/95 px-4 py-3 backdrop-blur md:top-0 md:-mx-8 md:px-8">
        <Button variant="ghost" size="sm" onClick={back}><ArrowLeft className="size-4" />Workflows</Button>
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-lg font-semibold">{w.name.trim() || (workflow ? "Untitled workflow" : "New workflow")}</h1>
          <p className="text-xs text-muted-foreground">
            {workflow ? <>{workflow.run_count} run{workflow.run_count === 1 ? "" : "s"} · {workflow.last_run_at ? `last ${dateTime(workflow.last_run_at)}` : "never run"}</> : "Actions run top to bottom. Each event runs a workflow at most once."}
            {dirty && <span className="ml-2 text-amber-700 dark:text-amber-300">· Unsaved changes</span>}
          </p>
        </div>
        {workflow && (
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={w.enabled ?? true} disabled={readOnly} onCheckedChange={(enabled) => set({ enabled })} />{w.enabled ?? true ? "On" : "Paused"}
          </label>
        )}
        <Button variant="outline" onClick={back}>{dirty ? "Discard" : "Close"}</Button>
        {!readOnly && <Button disabled={!canSave} onClick={() => save.mutate()}>{save.isPending ? "Saving…" : workflow ? "Save changes" : "Create workflow"}</Button>}
      </div>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
        <fieldset disabled={readOnly} className="min-w-0 space-y-4">
          <Card className="py-5">
            <CardContent className="px-5">
              <Step n={1} label="When" tone="amber" hint="The event that starts this workflow.">
                <div className="space-y-4">
                  {groups.map((g) => (
                    <div key={g} className="space-y-1.5">
                      <p className="text-xs font-medium text-muted-foreground">{g}</p>
                      <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
                        {ctx.meta.triggers.filter((t) => t.group === g).map((t) => (
                          <button key={t.trigger} type="button" onClick={() => set({ trigger: t.trigger, trigger_config: {} })}
                            className={cn("rounded-md border px-3 py-2.5 text-left text-sm transition", w.trigger === t.trigger ? "border-primary bg-primary/5 ring-1 ring-primary" : "hover:bg-muted")}>
                            {t.label}
                          </button>
                        ))}
                      </div>
                    </div>
                  ))}
                  {trig && <p className="text-sm text-muted-foreground">{trig.description}</p>}
                  <TriggerConfig ctx={ctx} w={w} set={set} />
                </div>
              </Step>
            </CardContent>
          </Card>
          <Connector />
          <Card className="py-5">
            <CardContent className="px-5">
              <Step n={2} label="If" tone="sky" hint="All conditions must match. Leave empty to run every time.">
                <div className="space-y-2">
                  {w.conditions.map((c, i) => (
                    <ConditionRow key={i} ctx={ctx} c={c} onChange={(nc) => set({ conditions: w.conditions.map((x, j) => (j === i ? nc : x)) })}
                      onRemove={() => set({ conditions: w.conditions.filter((_, j) => j !== i) })} />
                  ))}
                  <Button variant="outline" size="sm" onClick={() => set({ conditions: [...w.conditions, { field: "lead.industry", op: "eq", value: "" }] })}><Plus className="size-4" />Add condition</Button>
                </div>
              </Step>
            </CardContent>
          </Card>
          <Connector />
          <Card className="py-5">
            <CardContent className="px-5">
              <Step n={3} label="Then" tone="emerald" hint={`Text fields can use ${ctx.meta.variables.join(" ")}`}>
                <div className="space-y-2">
                  {w.actions.map((a, i) => (
                    <ActionRow key={i} ctx={ctx} a={a} index={i} total={w.actions.length}
                      onChange={(na) => set({ actions: w.actions.map((x, j) => (j === i ? na : x)) })}
                      onMove={(dir) => { const next = [...w.actions]; [next[i], next[i + dir]] = [next[i + dir]!, next[i]!]; set({ actions: next }); }}
                      onRemove={() => set({ actions: w.actions.filter((_, j) => j !== i) })} />
                  ))}
                  <FilterSelect value="" onChange={(t) => t && set({ actions: [...w.actions, { type: t, params: defaultParams(t) }] })} placeholder="+ Add action" className="w-52"
                    options={ctx.meta.actions.map((a) => ({ value: a.type, label: a.label }))} />
                </div>
              </Step>
            </CardContent>
          </Card>
        </fieldset>

        <aside className="space-y-4 lg:sticky lg:top-20 lg:self-start">
          <Card className="py-5">
            <CardContent className="grid gap-3 px-5">
              <div className="grid gap-1.5"><Label htmlFor="wf-name">Name</Label><Input id="wf-name" disabled={readOnly} placeholder="e.g. Interested reply → deal" value={w.name} onChange={(e) => set({ name: e.target.value })} /></div>
              <div className="grid gap-1.5"><Label htmlFor="wf-desc">Description <span className="font-normal text-muted-foreground">(optional)</span></Label><Textarea id="wf-desc" rows={3} disabled={readOnly} value={w.description} onChange={(e) => set({ description: e.target.value })} /></div>
            </CardContent>
          </Card>
          <Card className="py-5">
            <CardContent className="space-y-2 px-5 text-sm">
              <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Summary</p>
              <p><span className="font-medium">When</span> {summary.when.charAt(0).toLowerCase() + summary.when.slice(1)}</p>
              {summary.conditions > 0 && <p><span className="font-medium">If</span> {summary.conditions} condition{summary.conditions > 1 ? "s match" : " matches"}</p>}
              <div><span className="font-medium">Then</span>
                <ol className="mt-1 list-decimal space-y-0.5 pl-5 text-muted-foreground">{summary.then.map((t, i) => <li key={i}>{t}</li>)}</ol>
              </div>
            </CardContent>
          </Card>
          {workflow && <TestPanel workflow={workflow} />}
          {workflow && (
            <Card className="py-5">
              <CardContent className="space-y-3 px-5">
                <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Recent runs</p>
                <RunsList workflowId={workflow.id} limit={5} />
              </CardContent>
            </Card>
          )}
        </aside>
      </div>
    </div>
  );
}

function Connector() {
  return <div className="ml-8 h-4 w-px bg-border" aria-hidden />;
}

function Step({ n, label, tone, hint, children }: { n: number; label: string; tone: "amber" | "sky" | "emerald"; hint?: string; children: React.ReactNode }) {
  const toneCls = { amber: "bg-amber-500/15 text-amber-800 dark:text-amber-200", sky: "bg-sky-500/15 text-sky-800 dark:text-sky-200", emerald: "bg-emerald-500/15 text-emerald-800 dark:text-emerald-200" }[tone];
  return (
    <section className="space-y-2">
      <div className="flex items-baseline gap-2">
        <span className={cn("shrink-0 whitespace-nowrap rounded-md px-2 py-0.5 text-xs font-semibold uppercase tracking-wider", toneCls)}>{n} · {label}</span>
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </section>
  );
}

function Chips({ options, value, onChange }: { options: { value: string; label: string }[]; value: string[]; onChange: (v: string[]) => void }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((o) => {
        const on = value.includes(o.value);
        return (
          <button key={o.value} type="button" onClick={() => onChange(on ? value.filter((v) => v !== o.value) : [...value, o.value])}
            className={cn("rounded-full border px-2.5 py-0.5 text-xs transition", on ? "border-primary bg-primary text-primary-foreground" : "hover:bg-muted")}>{o.label}</button>
        );
      })}
    </div>
  );
}

function TriggerConfig({ ctx, w, set }: { ctx: Ctx; w: WorkflowInput; set: (p: Partial<WorkflowInput>) => void }) {
  const cfg = w.trigger_config ?? {};
  const patch = (p: Record<string, unknown>) => set({ trigger_config: { ...cfg, ...p } });
  switch (w.trigger) {
    case "reply.classified":
      return (
        <div className="space-y-1.5">
          <Label>Only these classifications <span className="font-normal text-muted-foreground">(none selected = any)</span></Label>
          <Chips options={ctx.meta.fields.find((f) => f.field === "reply.classification")?.options ?? []} value={asList(cfg.classifications)} onChange={(v) => patch({ classifications: v })} />
        </div>
      );
    case "deal.stage_changed":
      return (
        <div className="grid gap-1.5">
          <Label>Entering stage</Label>
          <FilterSelect value={String(cfg.to_stage_id ?? "")} onChange={(v) => patch({ to_stage_id: v || undefined })} placeholder="Any stage" className="w-64" options={ctx.stages.map((s) => ({ value: s.id, label: s.name }))} />
        </div>
      );
    case "deal.stale":
    case "deal.inactive":
      return (
        <div className="grid gap-1.5">
          <Label htmlFor="wf-days">{w.trigger === "deal.stale" ? "Days in one stage" : "Days without activity"}</Label>
          <Input id="wf-days" type="number" min={1} className="w-40" placeholder={w.trigger === "deal.stale" ? "Stage limit" : "14"} value={String(cfg.days ?? "")} onChange={(e) => patch({ days: e.target.value ? Number(e.target.value) : undefined })} />
          {w.trigger === "deal.stale" && <p className="text-xs text-muted-foreground">Leave empty to use each stage’s own “stuck after” limit.</p>}
        </div>
      );
  }
  return null;
}

// Lead fields whose values come from the workspace's own leads (GET /leads/facets).
const FACETS: Record<string, string> = {
  "lead.industry": "industry", "lead.city": "city", "lead.state": "state", "lead.country": "country", "lead.source": "source", "lead.tags": "tag",
};

// Country names → ISO codes, for flags ("India" → "IN").
const COUNTRY_CODES = (() => {
  const out = new Map<string, string>();
  try {
    const names = new Intl.DisplayNames(["en"], { type: "region" });
    for (let a = 65; a <= 90; a++) for (let b = 65; b <= 90; b++) {
      const code = String.fromCharCode(a, b);
      const name = names.of(code);
      if (name && name !== code) out.set(name.toLowerCase(), code);
    }
  } catch { /* no Intl.DisplayNames: no flags */ }
  return out;
})();
const countryFlag = (v: string) => flag(/^[A-Za-z]{2}$/.test(v) ? v : COUNTRY_CODES.get(v.toLowerCase()) ?? "");

function useFacet(facet: string | undefined) {
  return useQuery({ queryKey: ["leads", "facets", facet], queryFn: () => leadsService.facets(facet!), enabled: !!facet, staleTime: 60_000 });
}

/** Options for a condition field, or null when the value is free text or a number. */
function useFieldOptions(ctx: Ctx, fieldName: string): MultiOption[] | null {
  const field = ctx.meta.fields.find((f) => f.field === fieldName);
  const facet = useFacet(FACETS[fieldName]);
  if (FACETS[fieldName]) {
    return (facet.data ?? []).map((f) => ({
      value: f.value, label: f.value, hint: f.count.toLocaleString(), icon: fieldName === "lead.country" ? countryFlag(f.value) : undefined,
    }));
  }
  switch (field?.type) {
    case "enum": return field.options ?? [];
    case "stage": return ctx.stages.map((s) => ({ value: s.id, label: s.name }));
    case "user": return ctx.members.map((m) => ({ value: m.user_id, label: m.full_name, hint: m.role }));
  }
  if (fieldName === "deal.lost_reason") return ctx.meta.lost_reasons.map((r) => ({ value: r, label: r }));
  if (fieldName === "deal.source") return [{ value: "manual", label: "Manual" }, { value: "workflow", label: "Workflow" }];
  return null;
}

function ConditionRow({ ctx, c, onChange, onRemove }: { ctx: Ctx; c: WorkflowCondition; onChange: (c: WorkflowCondition) => void; onRemove: () => void }) {
  const field = ctx.meta.fields.find((f) => f.field === c.field);
  const options = useFieldOptions(ctx, c.field);
  const noValue = c.op === "is_set" || c.op === "is_empty";
  // With a pick-list, "is"/"is not" accept several values; they're stored as in/not_in.
  const negated = c.op === "neq" || c.op === "not_in";
  const shownOp = options && (c.op === "in" || c.op === "not_in") ? (negated ? "neq" : "eq") : c.op;
  const opValues = field?.type === "number" ? ["eq", "neq", "gte", "lte", "is_set", "is_empty"]
    : options ? ["eq", "neq", "contains", "is_set", "is_empty"] : ["eq", "neq", "in", "not_in", "contains", "is_set", "is_empty"];
  const ops = ctx.meta.ops.filter((o) => opValues.includes(o.value));
  const pick = (vals: string[]) => onChange({ ...c, op: vals.length > 1 ? (negated ? "not_in" : "in") : negated ? "neq" : "eq", value: vals.length > 1 ? vals : vals[0] ?? "" });
  const multi = c.op === "in" || c.op === "not_in";
  let input: React.ReactNode = null;
  if (!noValue) {
    if (options && c.op !== "contains") {
      const creatable = !!FACETS[c.field] || c.field === "deal.lost_reason";
      input = <MultiSelect value={asList(c.value)} onChange={pick} options={options} creatable={creatable} className="w-full"
        placeholder={`Pick ${field?.label.toLowerCase() ?? "values"}…`} emptyText={creatable ? "No matches — type to add a value" : "No matches"} />;
    } else {
      input = <Input type={field?.type === "number" ? "number" : "text"} placeholder={multi ? "comma, separated, values" : "value"} value={Array.isArray(c.value) ? c.value.join(", ") : String(c.value ?? "")}
        onChange={(e) => onChange({ ...c, value: field?.type === "number" ? Number(e.target.value) : multi ? asList(e.target.value) : e.target.value })} />;
    }
  }
  const setOp = (o: string) => {
    if (!o) return;
    const list = asList(c.value);
    if (options && (o === "eq" || o === "neq")) {
      const neg = o === "neq";
      onChange({ ...c, op: list.length > 1 ? (neg ? "not_in" : "in") : o, value: list.length > 1 ? list : list[0] ?? "" });
    } else {
      onChange({ ...c, op: o, value: o === "in" || o === "not_in" ? list : list[0] ?? "" });
    }
  };
  return (
    <div className="grid items-start gap-2 rounded-md border bg-muted/30 p-2 sm:grid-cols-[180px_130px_1fr_auto]">
      <FilterSelect value={c.field} onChange={(f) => f && onChange({ field: f, op: "eq", value: "" })} placeholder="Field" className="w-full" options={ctx.meta.fields.map((f) => ({ value: f.field, label: f.label }))} />
      <FilterSelect value={shownOp} onChange={setOp} placeholder="Operator" className="w-full" options={ops} />
      <div className="min-w-0">{input}</div>
      <Button variant="ghost" size="icon" aria-label="Remove condition" onClick={onRemove}><Trash2 className="size-4" /></Button>
    </div>
  );
}

function defaultParams(type: string): Record<string, unknown> {
  switch (type) {
    case "create_deal": return { owner: "round_robin" };
    case "assign_owner": return { mode: "round_robin" };
    case "create_task": return { title: "Follow up with {{company}}", due_in_days: 1, assignee: "owner" };
    case "add_note": return { body: "" };
    case "add_tag": return { tag: "" };
    case "set_lead_status": return { status: "qualified" };
    case "notify": return { to: "owner", message: "{{deal}} needs attention." };
    default: return {};
  }
}

function ActionRow({ ctx, a, index, total, onChange, onMove, onRemove }: {
  ctx: Ctx; a: WorkflowAction; index: number; total: number; onChange: (a: WorkflowAction) => void; onMove: (dir: -1 | 1) => void; onRemove: () => void;
}) {
  const def = ctx.meta.actions.find((x) => x.type === a.type);
  const p = a.params ?? {};
  const setP = (patch: Record<string, unknown>) => onChange({ ...a, params: { ...p, ...patch } });
  const memberOpts = ctx.members.filter((m) => m.role !== "viewer").map((m) => ({ value: m.user_id, label: m.full_name }));
  const stageOpts = ctx.stages.map((s) => ({ value: s.id, label: s.name }));
  const statusOpts = ctx.meta.fields.find((f) => f.field === "lead.status")?.options ?? [];
  const field = (label: string, node: React.ReactNode, wide = false) => <div className={cn("grid gap-1", wide && "sm:col-span-2")}><Label className="text-xs font-normal text-muted-foreground">{label}</Label>{node}</div>;

  let body: React.ReactNode = null;
  switch (a.type) {
    case "create_deal":
      body = <>
        {field("Owner", <FilterSelect value={String(p.owner ?? "")} onChange={(v) => setP({ owner: v })} placeholder="Lead's owner" className="w-full" options={[{ value: "round_robin", label: "Round-robin" }, { value: "user", label: "Specific teammate" }]} />)}
        {p.owner === "user" && field("Teammate", <FilterSelect value={String(p.user_id ?? "")} onChange={(v) => setP({ user_id: v })} placeholder="Pick…" className="w-full" options={memberOpts} />)}
        {field("Starting stage", <FilterSelect value={String(p.stage_id ?? "")} onChange={(v) => setP({ stage_id: v || undefined })} placeholder="First stage" className="w-full" options={stageOpts.filter((s) => ctx.stages.find((x) => x.id === s.value)?.kind === "open")} />)}
        {field("Value (₹)", <Input type="number" min={0} value={String(p.value ?? "")} onChange={(e) => setP({ value: e.target.value ? Number(e.target.value) : undefined })} />)}
      </>;
      break;
    case "move_deal": {
      const lost = ctx.stages.find((s) => s.id === p.stage_id)?.kind === "lost";
      body = <>
        {field("Stage", <FilterSelect value={String(p.stage_id ?? "")} onChange={(v) => setP({ stage_id: v })} placeholder="Pick…" className="w-full" options={stageOpts} />)}
        {lost && field("Lost reason", <FilterSelect value={String(p.lost_reason ?? "")} onChange={(v) => setP({ lost_reason: v })} placeholder="Pick…" className="w-full" options={ctx.meta.lost_reasons.map((r) => ({ value: r, label: r }))} />)}
      </>;
      break;
    }
    case "assign_owner":
      body = <>
        {field("Assign to", <FilterSelect value={p.mode === "user" ? "user" : ""} onChange={(v) => setP({ mode: v || "round_robin" })} placeholder="Round-robin" className="w-full" options={[{ value: "user", label: "Specific teammate" }]} />)}
        {p.mode === "user" && field("Teammate", <FilterSelect value={String(p.user_id ?? "")} onChange={(v) => setP({ user_id: v })} placeholder="Pick…" className="w-full" options={memberOpts} />)}
      </>;
      break;
    case "create_task":
      body = <>
        {field("Task", <Input value={String(p.title ?? "")} onChange={(e) => setP({ title: e.target.value })} />, true)}
        {field("Due in (days)", <Input type="number" min={0} value={String(p.due_in_days ?? 1)} onChange={(e) => setP({ due_in_days: Number(e.target.value) })} />)}
        {field("Assignee", <FilterSelect value={p.assignee === "user" ? "user" : ""} onChange={(v) => setP({ assignee: v || "owner" })} placeholder="Deal / lead owner" className="w-full" options={[{ value: "user", label: "Specific teammate" }]} />)}
        {p.assignee === "user" && field("Teammate", <FilterSelect value={String(p.user_id ?? "")} onChange={(v) => setP({ user_id: v })} placeholder="Pick…" className="w-full" options={memberOpts} />)}
      </>;
      break;
    case "add_note":
      body = field("Note", <Textarea rows={2} value={String(p.body ?? "")} onChange={(e) => setP({ body: e.target.value })} />, true);
      break;
    case "add_tag":
      body = field("Tag", <TagPicker value={String(p.tag ?? "")} onChange={(tag) => setP({ tag })} />);
      break;
    case "set_lead_status":
      body = field("Status", <FilterSelect value={String(p.status ?? "")} onChange={(v) => setP({ status: v })} placeholder="Pick…" className="w-full" options={statusOpts} />);
      break;
    case "notify":
      body = <>
        {field("Email", <FilterSelect value={p.to === "admins" ? "admins" : ""} onChange={(v) => setP({ to: v || "owner" })} placeholder="The owner" className="w-full" options={[{ value: "admins", label: "Workspace admins" }]} />)}
        {field("Message", <Input value={String(p.message ?? "")} onChange={(e) => setP({ message: e.target.value })} />, true)}
      </>;
      break;
  }
  return (
    <div className="space-y-2 rounded-md border p-3">
      <div className="flex items-center gap-2">
        <span className="flex size-5 items-center justify-center rounded-full bg-emerald-500/15 text-xs font-semibold text-emerald-800 dark:text-emerald-200">{index + 1}</span>
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium">{def?.label ?? a.type}</p>
          <p className="truncate text-xs text-muted-foreground">{def?.description}</p>
        </div>
        <Button variant="ghost" size="icon" className="size-7" disabled={index === 0} aria-label="Move up" onClick={() => onMove(-1)}><ArrowUp className="size-3.5" /></Button>
        <Button variant="ghost" size="icon" className="size-7" disabled={index === total - 1} aria-label="Move down" onClick={() => onMove(1)}><ArrowDown className="size-3.5" /></Button>
        <Button variant="ghost" size="icon" className="size-7" aria-label="Remove action" onClick={onRemove}><Trash2 className="size-3.5" /></Button>
      </div>
      {body && <div className="grid gap-2 sm:grid-cols-2">{body}</div>}
    </div>
  );
}

/** One tag: pick an existing tag or type a new one. */
function TagPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const tags = useFacet("tag");
  const options = (tags.data ?? []).map((t) => ({ value: t.value, label: t.value, hint: t.count.toLocaleString() }));
  return <MultiSelect value={value ? [value] : []} onChange={(v) => onChange(v.filter((x) => x !== value).at(-1) ?? (v.length ? value : ""))}
    options={options} creatable placeholder="Pick or type a tag…" emptyText="No tags yet — type to create one" className="w-full" />;
}

function TestPanel({ workflow }: { workflow: Workflow }) {
  const [q, setQ] = useState("");
  const [lead, setLead] = useState<Lead | null>(null);
  const [classification, setClassification] = useState("interested");
  const leads = useQuery({ queryKey: ["leads", "pick", q], queryFn: () => api.get<Lead[]>("/leads", { q, limit: 5 }), enabled: !lead && q.length > 0 });
  const test = useMutation({
    mutationFn: () => api.post<WorkflowTestResult>(`/workflows/${workflow.id}/test`, { lead_id: lead?.id, classification }),
    onError: (e) => toast.error(errorMessage(e)),
  });
  const r = test.data;
  return (
    <section className="space-y-2 rounded-xl border border-dashed bg-card p-5">
      <p className="flex items-center gap-2 text-sm font-medium"><FlaskConical className="size-4" />Test against a lead</p>
      <p className="text-xs text-muted-foreground">Uses the saved version and changes nothing.</p>
      <div className="flex flex-wrap items-start gap-2">
        {lead ? (
          <Button variant="outline" size="sm" onClick={() => { setLead(null); test.reset(); }}>{lead.company_name} ✕</Button>
        ) : (
          <div className="relative w-full">
            <Input placeholder="Search a lead…" value={q} onChange={(e) => setQ(e.target.value)} />
            {(leads.data?.length ?? 0) > 0 && (
              <div className="absolute z-10 mt-1 w-full rounded-md border bg-popover shadow-md">
                {leads.data!.map((l) => <button key={l.id} type="button" onClick={() => setLead(l)} className="block w-full truncate px-3 py-1.5 text-left text-sm hover:bg-muted">{l.company_name}</button>)}
              </div>
            )}
          </div>
        )}
        {workflow.trigger === "reply.classified" && (
          <FilterSelect value={classification} onChange={setClassification} placeholder="No classification" className="w-44"
            options={["interested", "positive", "question", "not_now", "not_interested", "unsubscribe"].map((v) => ({ value: v, label: v.replace("_", " ") }))} />
        )}
        <Button size="sm" variant="secondary" disabled={!lead || test.isPending} onClick={() => test.mutate()}>Run test</Button>
      </div>
      {r && (
        <div className="space-y-2 text-sm">
          <p className={cn("flex items-center gap-1.5 font-medium", r.matched ? "text-emerald-700 dark:text-emerald-300" : "text-muted-foreground")}>
            {r.matched ? <CheckCircle2 className="size-4" /> : <CircleSlash className="size-4" />}
            {r.matched ? `Would run for ${r.subject}` : !r.trigger_matches ? "The trigger options don't match this event" : "Conditions don't match"}
          </p>
          {r.conditions.map((c, i) => (
            <p key={i} className="flex items-center gap-1.5 text-xs">
              {c.passed ? <CheckCircle2 className="size-3.5 text-emerald-600" /> : <XCircle className="size-3.5 text-destructive" />}
              <span className="font-mono">{c.field}</span> {c.op} {JSON.stringify(c.value)} <span className="text-muted-foreground">(actual: {JSON.stringify(c.actual ?? null)})</span>
            </p>
          ))}
          {r.matched && <ul className="list-disc space-y-0.5 pl-5 text-xs text-muted-foreground">{r.actions.map((a, i) => <li key={i}>{a.type.replaceAll("_", " ")}{a.message && `: ${a.message}`}</li>)}</ul>}
        </div>
      )}
    </section>
  );
}

const RUN_TONE: Record<WorkflowRun["status"], string> = {
  succeeded: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:bg-emerald-500/10 dark:text-emerald-200",
  partial: "border-amber-300 bg-amber-50 text-amber-800 dark:bg-amber-500/10 dark:text-amber-200",
  failed: "border-red-300 bg-red-50 text-red-800 dark:bg-red-500/10 dark:text-red-200",
  skipped: "text-muted-foreground",
};

function RunsList({ workflowId, limit }: { workflowId: string; limit?: number }) {
  const runs = useQuery({ queryKey: ["workflows", workflowId, "runs"], queryFn: () => api.get<WorkflowRun[]>(`/workflows/${workflowId}/runs`) });
  const list = limit ? runs.data?.slice(0, limit) : runs.data;
  return (
    <div className="space-y-3">
      {runs.isLoading && <Skeleton className="h-24 w-full" />}
      {runs.data?.length === 0 && <p className="text-sm text-muted-foreground">This workflow hasn’t run yet. It runs automatically the next time its trigger happens.</p>}
      {list?.map((r) => (
        <div key={r.id} className="space-y-1.5 rounded-md border p-3">
          <div className="flex items-center justify-between gap-2">
            <p className="truncate text-sm font-medium">{r.subject || r.company_name || "—"}</p>
            <Badge variant="outline" className={cn("capitalize", RUN_TONE[r.status])}>{r.status}</Badge>
          </div>
          <p className="text-xs text-muted-foreground">{dateTime(r.created_at)}</p>
          <ul className="space-y-0.5 text-xs">
            {r.results.map((res, i) => (
              <li key={i} className="flex gap-1.5">
                {res.status === "ok" ? <CheckCircle2 className="mt-px size-3.5 shrink-0 text-emerald-600" /> : res.status === "failed" ? <XCircle className="mt-px size-3.5 shrink-0 text-destructive" /> : <CircleSlash className="mt-px size-3.5 shrink-0 text-muted-foreground" />}
                <span><span className="capitalize">{res.type.replaceAll("_", " ")}</span>{res.message && <span className="text-muted-foreground"> — {res.message}</span>}</span>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}

function RunsSheet({ workflow, onClose }: { workflow: Workflow | null; onClose: () => void }) {
  return (
    <Sheet open={!!workflow} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="flex w-full flex-col gap-0 p-0 sm:max-w-lg">
        <SheetHeader className="border-b p-5">
          <SheetTitle>Run history</SheetTitle>
          <SheetDescription>{workflow?.name}</SheetDescription>
        </SheetHeader>
        <div className="min-h-0 flex-1 overflow-y-auto p-5">{workflow && <RunsList workflowId={workflow.id} />}</div>
      </SheetContent>
    </Sheet>
  );
}
