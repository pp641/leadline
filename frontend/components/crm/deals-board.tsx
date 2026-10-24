"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowDown, ArrowUp, CalendarDays, CheckSquare, Clock, Plus, Settings2, Trash2, Zap } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { FilterSelect } from "@/components/filter-select";
import { dateTime } from "@/components/shared";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { Deal, DealBoard, DealEvent, DealInput, DealPipeline, DealStage, Lead, Member, WorkflowMeta } from "@/types/api";

export function money(v: number, currency = "INR", compact = false) {
  try {
    return new Intl.NumberFormat("en-IN", { style: "currency", currency, maximumFractionDigits: 0, notation: compact ? "compact" : "standard" }).format(v);
  } catch {
    return `${currency} ${Math.round(v).toLocaleString()}`;
  }
}

const initials = (name: string | null) => (name ?? "?").split(/\s+/).filter(Boolean).slice(0, 2).map((p) => p[0]!.toUpperCase()).join("") || "?";

const KIND_TONE: Record<string, string> = {
  won: "border-t-emerald-500",
  lost: "border-t-zinc-400",
  open: "border-t-primary/60",
};

function useMembers() {
  return useQuery({ queryKey: ["members"], queryFn: () => api.get<Member[]>("/organizations/current/members") });
}

function useLostReasons() {
  const meta = useQuery({ queryKey: ["workflows", "meta"], queryFn: () => api.get<WorkflowMeta>("/workflows/meta"), staleTime: Infinity });
  return meta.data?.lost_reasons ?? ["Price", "Timing", "Went with a competitor", "No budget", "No response", "Not a fit", "Other"];
}

export function DealsBoard({ owner, dealId, onOpenDeal }: { owner: string; dealId: string | null; onOpenDeal: (id: string | null) => void }) {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const isAdmin = useCan("admin");
  const [pipelineId, setPipelineId] = useState("");
  const [newDeal, setNewDeal] = useState<string | null>(null); // stage id, or "" for default
  const [editStages, setEditStages] = useState(false);
  const [lostFor, setLostFor] = useState<{ deal: Deal; stageId: string } | null>(null);
  const [dragId, setDragId] = useState<string | null>(null);
  const [overStage, setOverStage] = useState<string | null>(null);

  const pipelines = useQuery({ queryKey: ["pipelines"], queryFn: () => api.get<DealPipeline[]>("/pipelines") });
  const boardKey = ["deals", "board", pipelineId, owner];
  const board = useQuery({ queryKey: boardKey, queryFn: () => api.get<DealBoard>("/deals/board", { pipeline_id: pipelineId || undefined, owner: owner || undefined }) });

  const move = useMutation({
    mutationFn: ({ id, stageId, lostReason }: { id: string; stageId: string; lostReason?: string }) =>
      api.post<Deal>(`/deals/${id}/move`, { stage_id: stageId, lost_reason: lostReason }),
    onMutate: async ({ id, stageId }) => {
      await qc.cancelQueries({ queryKey: boardKey });
      const prev = qc.getQueryData<DealBoard>(boardKey);
      if (prev) {
        const deal = prev.columns.flatMap((c) => c.deals).find((d) => d.id === id);
        if (deal) {
          qc.setQueryData<DealBoard>(boardKey, {
            ...prev,
            columns: prev.columns.map((c) => {
              const without = c.deals.filter((d) => d.id !== id);
              const deals = c.stage.id === stageId ? [{ ...deal, stage_id: stageId, stage_name: c.stage.name, days_in_stage: 0, stale: false }, ...without] : without;
              return { ...c, deals, count: deals.length, value: deals.reduce((s, d) => s + d.value, 0) };
            }),
          });
        }
      }
      return { prev };
    },
    onError: (e, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(boardKey, ctx.prev);
      toast.error(errorMessage(e));
    },
    onSuccess: (d) => {
      if (d.status === "won") toast.success(`${d.name} won 🎉`);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["deals"] });
      qc.invalidateQueries({ queryKey: ["tasks"] });
    },
  });

  const requestMove = (deal: Deal, stage: DealStage) => {
    if (deal.stage_id === stage.id) return;
    if (stage.kind === "lost") setLostFor({ deal, stageId: stage.id });
    else move.mutate({ id: deal.id, stageId: stage.id });
  };

  const b = board.data;
  const pipeOptions = (pipelines.data ?? []).map((p) => ({ value: p.id, label: p.name }));

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        {pipeOptions.length > 1 ? (
          <FilterSelect value={pipelineId || (b?.pipeline.id ?? "")} onChange={setPipelineId} placeholder="Default pipeline" className="w-48" options={pipeOptions} />
        ) : (
          <span className="text-sm font-medium">{b?.pipeline.name ?? "Sales pipeline"}</span>
        )}
        <div className="ml-auto flex gap-2">
          {isAdmin && b && (
            <Button variant="outline" size="sm" onClick={() => setEditStages(true)}><Settings2 className="size-4" />Stages</Button>
          )}
          {canEdit && <Button size="sm" onClick={() => setNewDeal("")}><Plus className="size-4" />New deal</Button>}
        </div>
      </div>

      {b && (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <Stat label="Open pipeline" value={money(b.totals.open_value, "INR", true)} hint={`${b.totals.open_count} open deal${b.totals.open_count === 1 ? "" : "s"}`} />
          <Stat label="Weighted forecast" value={money(b.totals.weighted_value, "INR", true)} hint="value × stage probability" />
          <Stat label="Won · last 30 days" value={money(b.totals.won_value_30d, "INR", true)} hint={`${b.totals.won_count_30d} deal${b.totals.won_count_30d === 1 ? "" : "s"}`} />
          <Stat label="Stuck deals" value={String(b.totals.stale_count)} hint="past their stage's time limit" tone={b.totals.stale_count > 0 ? "warn" : undefined} />
        </div>
      )}

      {board.isLoading ? (
        <div className="flex gap-3 overflow-hidden">{Array.from({ length: 5 }, (_, i) => <Skeleton key={i} className="h-72 w-72 shrink-0" />)}</div>
      ) : (
        <div className="flex gap-3 overflow-x-auto pb-3">
          {b?.columns.map((col) => (
            <div
              key={col.stage.id}
              onDragOver={(e) => { if (dragId) { e.preventDefault(); setOverStage(col.stage.id); } }}
              onDragLeave={() => setOverStage((s) => (s === col.stage.id ? null : s))}
              onDrop={(e) => {
                e.preventDefault();
                const deal = b.columns.flatMap((c) => c.deals).find((d) => d.id === dragId);
                setDragId(null); setOverStage(null);
                if (deal) requestMove(deal, col.stage);
              }}
              className={cn("flex w-72 shrink-0 flex-col rounded-lg border-t-2 bg-muted/50 transition-colors", KIND_TONE[col.stage.kind], overStage === col.stage.id && "bg-primary/10 ring-2 ring-primary/30")}
            >
              <div className="space-y-0.5 px-3 pb-2 pt-2.5">
                <div className="flex items-center justify-between gap-2 text-sm font-medium">
                  <span className="truncate">{col.stage.name}</span>
                  <span className="rounded-full bg-background px-2 text-xs tabular-nums text-muted-foreground">{col.count}</span>
                </div>
                <p className="text-xs text-muted-foreground tabular-nums">
                  {money(col.value, "INR", true)}
                  {col.stage.kind === "open" && <> · {col.stage.probability}% · {money(col.weighted, "INR", true)} weighted</>}
                  {col.stage.kind !== "open" && " · last 90 days"}
                </p>
              </div>
              <div className="min-h-24 flex-1 space-y-2 px-2 pb-2">
                {col.deals.map((d) => (
                  <DealCard key={d.id} deal={d} draggable={canEdit} dragging={dragId === d.id}
                    onDragStart={() => setDragId(d.id)} onDragEnd={() => { setDragId(null); setOverStage(null); }} onOpen={() => onOpenDeal(d.id)} />
                ))}
                {col.deals.length === 0 && <p className="rounded-md border border-dashed px-3 py-6 text-center text-xs text-muted-foreground">{canEdit ? "Drop a deal here" : "No deals"}</p>}
                {canEdit && col.stage.kind === "open" && (
                  <button type="button" onClick={() => setNewDeal(col.stage.id)} className="flex w-full items-center gap-1 rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:bg-background hover:text-foreground">
                    <Plus className="size-3.5" />Add deal
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {newDeal !== null && b && <NewDealDialog pipeline={b.pipeline} stageId={newDeal} onClose={() => setNewDeal(null)} />}
      {editStages && b && <StagesDialog pipeline={b.pipeline} onClose={() => setEditStages(false)} />}
      <LostReasonDialog open={!!lostFor} dealName={lostFor?.deal.name ?? ""} onClose={() => setLostFor(null)}
        onConfirm={(reason) => { if (lostFor) move.mutate({ id: lostFor.deal.id, stageId: lostFor.stageId, lostReason: reason }); setLostFor(null); }} />
      <DealSheet dealId={dealId} pipeline={b?.pipeline} onClose={() => onOpenDeal(null)} />
    </div>
  );
}

function Stat({ label, value, hint, tone }: { label: string; value: string; hint: string; tone?: "warn" }) {
  return (
    <div className={cn("rounded-lg border bg-card p-3", tone === "warn" && "border-amber-300 bg-amber-50 dark:border-amber-500/40 dark:bg-amber-500/10")}>
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="mt-0.5 text-xl font-semibold tabular-nums">{value}</p>
      <p className="text-xs text-muted-foreground">{hint}</p>
    </div>
  );
}

function DealCard({ deal: d, draggable, dragging, onDragStart, onDragEnd, onOpen }: {
  deal: Deal; draggable: boolean; dragging: boolean; onDragStart: () => void; onDragEnd: () => void; onOpen: () => void;
}) {
  const overdue = d.expected_close && d.status === "open" && new Date(d.expected_close) < new Date(new Date().toDateString());
  return (
    <div
      role="button" tabIndex={0}
      draggable={draggable}
      onDragStart={(e) => { e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", d.id); onDragStart(); }}
      onDragEnd={onDragEnd}
      onClick={onOpen}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpen(); } }}
      className={cn("cursor-pointer space-y-1.5 rounded-md border bg-card p-2.5 text-left shadow-xs transition hover:border-primary/40 hover:shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        draggable && "active:cursor-grabbing", dragging && "opacity-40", d.stale && "border-amber-300 dark:border-amber-500/50")}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{d.name}</p>
          {d.company_name && d.company_name !== d.name && <p className="truncate text-xs text-muted-foreground">{d.company_name}</p>}
        </div>
        <span title={d.owner_name ?? "Unassigned"} className="flex size-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-[10px] font-semibold text-primary">{initials(d.owner_name)}</span>
      </div>
      <p className="text-sm font-semibold tabular-nums">{money(d.value, d.currency)}</p>
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-muted-foreground">
        {d.status === "open" && (
          <span className={cn("inline-flex items-center gap-1", d.stale && "font-medium text-amber-700 dark:text-amber-300")}>
            {d.stale ? <AlertTriangle className="size-3" /> : <Clock className="size-3" />}{d.days_in_stage}d in stage
          </span>
        )}
        {d.status === "lost" && d.lost_reason && <span>Lost · {d.lost_reason}</span>}
        {d.expected_close && d.status === "open" && (
          <span className={cn("inline-flex items-center gap-1", overdue && "text-destructive")}><CalendarDays className="size-3" />{new Date(d.expected_close).toLocaleDateString(undefined, { day: "numeric", month: "short" })}</span>
        )}
        {d.open_tasks > 0 && <span className="inline-flex items-center gap-1"><CheckSquare className="size-3" />{d.open_tasks}</span>}
        {d.source === "workflow" && <span className="inline-flex items-center gap-1" title="Created by a workflow"><Zap className="size-3" />auto</span>}
      </div>
    </div>
  );
}

function LostReasonDialog({ open, dealName, onClose, onConfirm }: { open: boolean; dealName: string; onClose: () => void; onConfirm: (reason: string) => void }) {
  const reasons = useLostReasons();
  const [reason, setReason] = useState("");
  const [other, setOther] = useState("");
  const final = reason === "Other" ? other.trim() || "Other" : reason;
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) { onClose(); setReason(""); setOther(""); } }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Why was {dealName || "this deal"} lost?</DialogTitle>
          <DialogDescription>Loss reasons show up in reporting and can trigger workflows (e.g. revisit deals lost on timing).</DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap gap-2">
          {reasons.map((r) => (
            <button key={r} type="button" onClick={() => setReason(r)}
              className={cn("rounded-full border px-3 py-1 text-sm transition", reason === r ? "border-primary bg-primary text-primary-foreground" : "hover:bg-muted")}>{r}</button>
          ))}
        </div>
        {reason === "Other" && <Input autoFocus placeholder="What happened?" value={other} onChange={(e) => setOther(e.target.value)} />}
        <DialogFooter className="mx-0! mb-0!">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button variant="destructive" disabled={!reason} onClick={() => { onConfirm(final); setReason(""); setOther(""); }}>Mark as lost</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function LeadPicker({ value, onChange }: { value: Lead | null; onChange: (l: Lead | null) => void }) {
  const [q, setQ] = useState("");
  const results = useQuery({ queryKey: ["leads", "pick", q], queryFn: () => api.get<Lead[]>("/leads", { q, limit: 6 }), enabled: !value });
  if (value) {
    return (
      <div className="flex items-center justify-between rounded-md border px-3 py-2 text-sm">
        <span><span className="font-medium">{value.company_name}</span>{value.city && <span className="text-muted-foreground"> · {value.city}</span>}</span>
        <Button variant="ghost" size="sm" type="button" onClick={() => onChange(null)}>Change</Button>
      </div>
    );
  }
  return (
    <div className="space-y-1">
      <Input placeholder="Search leads by company…" value={q} onChange={(e) => setQ(e.target.value)} />
      {(results.data?.length ?? 0) > 0 && (
        <div className="max-h-44 overflow-y-auto rounded-md border">
          {results.data!.map((l) => (
            <button key={l.id} type="button" onClick={() => onChange(l)} className="flex w-full items-center justify-between px-3 py-1.5 text-left text-sm hover:bg-muted">
              <span className="truncate">{l.company_name}</span><span className="ml-2 shrink-0 text-xs text-muted-foreground">{l.city}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function NewDealDialog({ pipeline, stageId, lead: fixedLead, onClose }: { pipeline?: DealPipeline; stageId?: string; lead?: Lead | null; onClose: () => void }) {
  const qc = useQueryClient();
  const members = useMembers();
  const pipes = useQuery({ queryKey: ["pipelines"], queryFn: () => api.get<DealPipeline[]>("/pipelines"), enabled: !pipeline });
  const pipe = pipeline ?? pipes.data?.find((p) => p.is_default) ?? pipes.data?.[0];
  const openStages = (pipe?.stages ?? []).filter((s) => s.kind === "open");
  const [lead, setLead] = useState<Lead | null>(fixedLead ?? null);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [stage, setStage] = useState(stageId ?? "");
  const [close, setClose] = useState("");
  const [owner, setOwner] = useState("");
  const create = useMutation({
    mutationFn: () => {
      const body: DealInput = {
        pipeline_id: pipe?.id, stage_id: stage || openStages[0]?.id, lead_id: lead?.id, name: name.trim() || undefined,
        value: value ? Number(value) : 0, expected_close: close || undefined, owner_id: owner || undefined,
      };
      return api.post<Deal>("/deals", body);
    },
    onSuccess: (d) => {
      toast.success(`Deal “${d.name}” created`);
      qc.invalidateQueries({ queryKey: ["deals"] });
      onClose();
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const canSave = (!!lead || name.trim() !== "") && !create.isPending;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>New deal</DialogTitle>
          <DialogDescription>Track an opportunity with a value and an expected close date.</DialogDescription>
        </DialogHeader>
        <form className="grid gap-3" onSubmit={(e) => { e.preventDefault(); if (canSave) create.mutate(); }}>
          {!fixedLead && (
            <div className="grid gap-1.5"><Label>Lead <span className="font-normal text-muted-foreground">(optional)</span></Label><LeadPicker value={lead} onChange={setLead} /></div>
          )}
          <div className="grid gap-1.5">
            <Label htmlFor="deal-name">Deal name</Label>
            <Input id="deal-name" placeholder={lead ? lead.company_name : "e.g. Acme — annual plan"} value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-1.5">
              <Label htmlFor="deal-value">Value (₹)</Label>
              <Input id="deal-value" type="number" min={0} step="1" inputMode="numeric" placeholder="0" value={value} onChange={(e) => setValue(e.target.value)} />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="deal-close">Expected close</Label>
              <Input id="deal-close" type="date" value={close} onChange={(e) => setClose(e.target.value)} />
            </div>
            <div className="grid gap-1.5">
              <Label>Stage</Label>
              <FilterSelect value={stage} onChange={setStage} placeholder={openStages[0]?.name ?? "First stage"} className="w-full" options={openStages.slice(1).map((s) => ({ value: s.id, label: s.name }))} />
            </div>
            <div className="grid gap-1.5">
              <Label>Owner</Label>
              <FilterSelect value={owner} onChange={setOwner} placeholder={lead?.owner_name ? `Lead owner (${lead.owner_name})` : "Me"} className="w-full"
                options={(members.data ?? []).filter((m) => m.role !== "viewer").map((m) => ({ value: m.user_id, label: m.full_name }))} />
            </div>
          </div>
          <DialogFooter className="mx-0! mb-0! mt-2">
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={!canSave}>{create.isPending ? "Creating…" : "Create deal"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const EVENT_TEXT: Record<string, (d: Record<string, unknown>) => string> = {
  created: (d) => `created the deal${d.value ? ` worth ${money(Number(d.value))}` : ""}`,
  stage_changed: (d) => `moved it from ${d.from} to ${d.to}${d.lost_reason ? ` — ${d.lost_reason}` : ""}`,
  updated: (d) => `updated ${Object.keys(d).filter((k) => k !== "workflow").map((k) => k.replace("_id", "").replaceAll("_", " ")).join(", ")}`,
  note: () => "added a note",
};

function DealSheet({ dealId, pipeline, onClose }: { dealId: string | null; pipeline?: DealPipeline; onClose: () => void }) {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const members = useMembers();
  const q = useQuery({ queryKey: ["deals", "detail", dealId], queryFn: () => api.get<{ deal: Deal; events: DealEvent[] }>(`/deals/${dealId}`), enabled: !!dealId });
  const d = q.data?.deal;
  const pipes = useQuery({ queryKey: ["pipelines"], queryFn: () => api.get<DealPipeline[]>("/pipelines"), enabled: !!d && pipeline?.id !== d?.pipeline_id });
  const pipe = pipeline?.id === d?.pipeline_id ? pipeline : pipes.data?.find((p) => p.id === d?.pipeline_id);
  const [lostStage, setLostStage] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [draft, setDraft] = useState<{ id: string; name: string; value: string; close: string; nextStep: string } | null>(null);
  // Edits start from the loaded deal; a different deal resets the form.
  const form = draft && draft.id === d?.id ? draft : d ? { id: d.id, name: d.name, value: String(d.value), close: d.expected_close ?? "", nextStep: d.next_step } : null;
  const dirty = !!d && !!form && (form.name !== d.name || Number(form.value) !== d.value || form.close !== (d.expected_close ?? "") || form.nextStep !== d.next_step);

  const refresh = () => { qc.invalidateQueries({ queryKey: ["deals"] }); qc.invalidateQueries({ queryKey: ["tasks"] }); };
  const save = useMutation({
    mutationFn: (body: DealInput) => api.put<Deal>(`/deals/${dealId}`, body),
    onSuccess: () => { setDraft(null); refresh(); toast.success("Deal saved"); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const move = useMutation({
    mutationFn: ({ stageId, lostReason }: { stageId: string; lostReason?: string }) => api.post<Deal>(`/deals/${dealId}/move`, { stage_id: stageId, lost_reason: lostReason }),
    onSuccess: refresh,
    onError: (e) => toast.error(errorMessage(e)),
  });
  const addNote = useMutation({
    mutationFn: () => api.post(`/deals/${dealId}/notes`, { body: note }),
    onSuccess: () => { setNote(""); refresh(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const del = useMutation({
    mutationFn: () => api.del(`/deals/${dealId}`),
    onSuccess: () => { toast.success("Deal deleted"); refresh(); onClose(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const ownerOptions = (members.data ?? []).filter((m) => m.role !== "viewer").map((m) => ({ value: m.user_id, label: m.full_name }));

  return (
    <Sheet open={!!dealId} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="flex w-full flex-col gap-0 p-0 sm:max-w-lg">
        {!d ? (
          <div className="space-y-3 p-5"><SheetTitle className="sr-only">Deal</SheetTitle><Skeleton className="h-6 w-2/3" /><Skeleton className="h-40 w-full" /></div>
        ) : (
          <>
            <SheetHeader className="border-b p-5">
              <div className="flex flex-wrap items-center gap-2 pr-8">
                <Badge variant="outline" className={cn("capitalize", d.status === "won" && "border-emerald-300 bg-emerald-50 text-emerald-800 dark:bg-emerald-500/10 dark:text-emerald-200", d.status === "lost" && "text-muted-foreground")}>{d.status}</Badge>
                {d.stale && <Badge variant="outline" className="border-amber-300 bg-amber-50 text-amber-800 dark:bg-amber-500/10 dark:text-amber-200">Stuck {d.days_in_stage}d</Badge>}
                {d.source === "workflow" && <span className="inline-flex items-center gap-1 text-xs text-muted-foreground"><Zap className="size-3" />Created by a workflow</span>}
              </div>
              <SheetTitle className="text-lg leading-snug">{d.name}</SheetTitle>
              <SheetDescription>
                {money(d.value, d.currency)} · {d.probability}% · {d.stage_name}
                {d.lead_id && <> · <Link href={`/leads/${d.lead_id}`} className="underline-offset-2 hover:underline">{d.company_name || "View lead"}</Link></>}
              </SheetDescription>
            </SheetHeader>
            <div className="min-h-0 flex-1 space-y-6 overflow-y-auto p-5">
              {pipe && (
                <section className="space-y-2">
                  <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Stage</h4>
                  <div className="flex flex-wrap gap-1.5">
                    {pipe.stages.map((s) => (
                      <button key={s.id} type="button" disabled={!canEdit || move.isPending}
                        onClick={() => { if (s.id === d.stage_id) return; if (s.kind === "lost") setLostStage(s.id); else move.mutate({ stageId: s.id }); }}
                        className={cn("rounded-md border px-2.5 py-1 text-xs transition disabled:cursor-default",
                          s.id === d.stage_id ? (s.kind === "won" ? "border-emerald-600 bg-emerald-600 text-white" : s.kind === "lost" ? "border-zinc-500 bg-zinc-500 text-white" : "border-primary bg-primary text-primary-foreground") : "hover:bg-muted")}>
                        {s.name}
                      </button>
                    ))}
                  </div>
                  {d.status === "lost" && d.lost_reason && <p className="text-sm text-muted-foreground">Lost reason: {d.lost_reason}</p>}
                </section>
              )}

              {form && (
                <section className="space-y-3">
                  <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Details</h4>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="d-name">Name</Label><Input id="d-name" disabled={!canEdit} value={form.name} onChange={(e) => setDraft({ ...form, name: e.target.value })} /></div>
                    <div className="grid gap-1.5"><Label htmlFor="d-value">Value ({d.currency})</Label><Input id="d-value" type="number" min={0} disabled={!canEdit} value={form.value} onChange={(e) => setDraft({ ...form, value: e.target.value })} /></div>
                    <div className="grid gap-1.5"><Label htmlFor="d-close">Expected close</Label><Input id="d-close" type="date" disabled={!canEdit} value={form.close} onChange={(e) => setDraft({ ...form, close: e.target.value })} /></div>
                    <div className="grid gap-1.5 sm:col-span-2"><Label htmlFor="d-next">Next step</Label><Input id="d-next" disabled={!canEdit} placeholder="e.g. Send revised quote" value={form.nextStep} onChange={(e) => setDraft({ ...form, nextStep: e.target.value })} /></div>
                    <div className="grid gap-1.5">
                      <Label>Owner</Label>
                      <FilterSelect value={d.owner_id ?? ""} onChange={(v) => save.mutate({ owner_id: v || "00000000-0000-0000-0000-000000000000" })} placeholder="Unassigned" className="w-full" options={ownerOptions} />
                    </div>
                  </div>
                  {canEdit && dirty && (
                    <div className="flex gap-2">
                      <Button size="sm" disabled={save.isPending} onClick={() => save.mutate({ name: form.name, value: Number(form.value) || 0, expected_close: form.close, next_step: form.nextStep })}>Save changes</Button>
                      <Button size="sm" variant="ghost" onClick={() => setDraft(null)}>Discard</Button>
                    </div>
                  )}
                </section>
              )}

              <section className="space-y-3">
                <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Timeline</h4>
                {canEdit && (
                  <div className="space-y-2">
                    <Textarea rows={2} placeholder="Log a call, meeting or note…" value={note} onChange={(e) => setNote(e.target.value)} />
                    <Button size="sm" variant="outline" disabled={!note.trim() || addNote.isPending} onClick={() => addNote.mutate()}>Add note</Button>
                  </div>
                )}
                <ol className="space-y-3 border-l pl-4">
                  {(q.data?.events ?? []).map((ev) => {
                    const wf = ev.data.workflow as string | undefined;
                    const who = wf ? `Workflow “${wf}”` : ev.actor_name ?? "Someone";
                    return (
                      <li key={ev.id} className="relative text-sm">
                        <span className={cn("absolute -left-[21px] top-1.5 size-2 rounded-full", wf ? "bg-amber-500" : "bg-primary")} />
                        <p><span className="font-medium">{who}</span> {(EVENT_TEXT[ev.event_type] ?? (() => ev.event_type))(ev.data)}</p>
                        {ev.event_type === "note" && <p className="mt-1 whitespace-pre-wrap rounded-md bg-muted px-3 py-2">{String(ev.data.body ?? "")}</p>}
                        <p className="text-xs text-muted-foreground">{dateTime(ev.created_at)}</p>
                      </li>
                    );
                  })}
                </ol>
              </section>

              {canEdit && (
                <Button variant="ghost" size="sm" className="text-destructive hover:text-destructive" onClick={() => { if (confirm(`Delete “${d.name}”? This can't be undone.`)) del.mutate(); }}>
                  <Trash2 className="size-4" />Delete deal
                </Button>
              )}
            </div>
          </>
        )}
        <LostReasonDialog open={!!lostStage} dealName={d?.name ?? ""} onClose={() => setLostStage(null)}
          onConfirm={(reason) => { if (lostStage) move.mutate({ stageId: lostStage, lostReason: reason }); setLostStage(null); }} />
      </SheetContent>
    </Sheet>
  );
}

type StageDraft = Omit<DealStage, "id" | "position"> & { id?: string; key: string };

function StagesDialog({ pipeline, onClose }: { pipeline: DealPipeline; onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(pipeline.name);
  const [stages, setStages] = useState<StageDraft[]>(() => pipeline.stages.map((s) => ({ ...s, key: s.id })));
  const open = stages.filter((s) => s.kind === "open");
  const closed = useMemo(() => stages.filter((s) => s.kind !== "open"), [stages]);
  const update = (key: string, patch: Partial<StageDraft>) => setStages((ss) => ss.map((s) => (s.key === key ? { ...s, ...patch } : s)));
  const moveStage = (i: number, dir: -1 | 1) => {
    const next = [...open];
    [next[i], next[i + dir]] = [next[i + dir]!, next[i]!];
    setStages([...next, ...closed]);
  };
  const save = useMutation({
    mutationFn: () => api.put<DealPipeline>(`/pipelines/${pipeline.id}`, {
      name, stages: [...open, ...closed].map((s) => ({ id: s.id, name: s.name, probability: s.probability, kind: s.kind, stale_days: s.stale_days || null })),
    }),
    onSuccess: () => { toast.success("Pipeline saved"); qc.invalidateQueries({ queryKey: ["pipelines"] }); qc.invalidateQueries({ queryKey: ["deals"] }); onClose(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Pipeline stages</DialogTitle>
          <DialogDescription>Probability weights the forecast. “Stuck after” flags deals that sit in a stage too long and fires stuck-deal workflows.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-1.5"><Label htmlFor="pipe-name">Pipeline name</Label><Input id="pipe-name" value={name} onChange={(e) => setName(e.target.value)} /></div>
        <div className="max-h-[50vh] space-y-2 overflow-y-auto pr-1">
          <div className="grid grid-cols-[1fr_88px_100px_72px] gap-2 px-1 text-xs text-muted-foreground"><span>Stage</span><span>Probability</span><span>Stuck after</span><span /></div>
          {open.map((s, i) => (
            <div key={s.key} className="grid grid-cols-[1fr_88px_100px_72px] items-center gap-2">
              <Input aria-label="Stage name" value={s.name} onChange={(e) => update(s.key, { name: e.target.value })} />
              <div className="relative"><Input aria-label="Probability" type="number" min={0} max={100} value={s.probability} onChange={(e) => update(s.key, { probability: Number(e.target.value) })} className="pr-6" /><span className="absolute right-2 top-1/2 -translate-y-1/2 text-xs text-muted-foreground">%</span></div>
              <div className="relative"><Input aria-label="Stuck after days" type="number" min={1} placeholder="—" value={s.stale_days ?? ""} onChange={(e) => update(s.key, { stale_days: e.target.value ? Number(e.target.value) : null })} className="pr-8" /><span className="absolute right-2 top-1/2 -translate-y-1/2 text-xs text-muted-foreground">days</span></div>
              <div className="flex">
                <Button variant="ghost" size="icon" className="size-7" disabled={i === 0} aria-label="Move up" onClick={() => moveStage(i, -1)}><ArrowUp className="size-3.5" /></Button>
                <Button variant="ghost" size="icon" className="size-7" disabled={i === open.length - 1} aria-label="Move down" onClick={() => moveStage(i, 1)}><ArrowDown className="size-3.5" /></Button>
                <Button variant="ghost" size="icon" className="size-7" disabled={open.length === 1} aria-label="Remove stage" onClick={() => setStages((ss) => ss.filter((x) => x.key !== s.key))}><Trash2 className="size-3.5" /></Button>
              </div>
            </div>
          ))}
          <Button variant="outline" size="sm" onClick={() => setStages([...open, { key: crypto.randomUUID(), name: "", probability: 50, kind: "open", stale_days: 7 }, ...closed])}><Plus className="size-4" />Add stage</Button>
          <div className="space-y-2 border-t pt-3">
            {closed.map((s) => (
              <div key={s.key} className="grid grid-cols-[1fr_88px_100px_72px] items-center gap-2">
                <Input aria-label="Stage name" value={s.name} onChange={(e) => update(s.key, { name: e.target.value })} />
                <span className="text-sm text-muted-foreground">{s.kind === "won" ? "100%" : "0%"}</span>
                <span className="text-xs capitalize text-muted-foreground">{s.kind} stage</span>
              </div>
            ))}
          </div>
        </div>
        <DialogFooter className="mx-0! mb-0!">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button disabled={save.isPending || !name.trim() || stages.some((s) => !s.name.trim())} onClick={() => save.mutate()}>Save stages</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Deals for one lead, shown on the lead page. */
export function LeadDeals({ lead }: { lead: Lead }) {
  const canEdit = useCan("member");
  const [creating, setCreating] = useState(false);
  const q = useQuery({ queryKey: ["deals", "lead", lead.id], queryFn: () => api.get<Deal[]>("/deals", { lead_id: lead.id }) });
  return (
    <div className="rounded-xl border bg-card">
      <div className="flex items-center justify-between px-4 pt-4">
        <h3 className="text-base font-semibold">Deals</h3>
        {canEdit && <Button size="sm" variant="outline" onClick={() => setCreating(true)}><Plus className="size-4" />Create deal</Button>}
      </div>
      <div className="space-y-2 p-4">
        {(q.data ?? []).length === 0 && <p className="text-sm text-muted-foreground">No deals yet. Create one when this lead turns into a real opportunity.</p>}
        {(q.data ?? []).map((d) => (
          <Link key={d.id} href={`/crm?deal=${d.id}`} className="flex items-center justify-between rounded-md border px-3 py-2 text-sm hover:bg-muted">
            <span className="min-w-0"><span className="block truncate font-medium">{d.name}</span><span className="text-xs text-muted-foreground">{d.stage_name} · {d.days_in_stage}d in stage</span></span>
            <span className="shrink-0 font-semibold tabular-nums">{money(d.value, d.currency)}</span>
          </Link>
        ))}
      </div>
      {creating && <NewDealDialog lead={lead} onClose={() => setCreating(false)} />}
    </div>
  );
}
