"use client";

import { useEffect, useMemo, useState } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft, ArrowRight, BarChart3, ChevronRight, Columns3, Copy, Gauge, GripVertical, LayoutDashboard, LineChart, Loader2,
  MoreHorizontal, Pencil, PieChart, Plus, Save, Table2, Trash2, Workflow, X,
} from "lucide-react";
import { toast } from "sonner";
import {
  ComparisonChart, Columns, COMPARE_DIMS, Donut, Empty, METRICS, NumRange, RankedBars, Trend, TriState, cap, label,
  toOptions, useFacet, STATUS_OPTIONS, WEBSITE_OPTIONS, type Metric,
} from "@/components/lead-insights";
import { MultiSelect } from "@/components/multi-select";
import { EmptyState, num, pct } from "@/components/shared";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { InsightsFilter, LeadInsights } from "@/types/api";

// ───────────── model ─────────────

type WidgetType = "kpi" | "bar" | "donut" | "column" | "trend" | "stages" | "compare";
type Width = 3 | 4 | 6 | 8 | 12;

interface Widget {
  id: string;
  type: WidgetType;
  title: string;
  dimension?: string;
  metric?: string;
  width: Width;
  filter?: InsightsFilter; // applies to this widget only, on top of the steps
}

type StepField = "industry" | "city" | "state" | "country" | "source" | "tag" | "status" | "website"
  | "founded" | "employees" | "score" | "icp_fit" | "has_email" | "has_phone" | "has_website" | "added";

interface Step {
  id: string;
  field: StepField;
  values?: string[];
  min?: number | null;
  max?: number | null;
  bool?: boolean;
  from?: string;
  to?: string;
}

interface Dashboard {
  id: string;
  name: string;
  widgets: Widget[];
  steps: Step[];
  updated_at: string;
}

const uid = () => Math.random().toString(36).slice(2, 10);

const WIDGET_TYPES: { type: WidgetType; name: string; blurb: string; icon: React.ElementType; width: Width }[] = [
  { type: "kpi", name: "Number", blurb: "One headline metric", icon: Gauge, width: 3 },
  { type: "bar", name: "Bar chart", blurb: "Top values of a dimension", icon: BarChart3, width: 6 },
  { type: "donut", name: "Pie / donut", blurb: "Part-to-whole, up to 6 parts", icon: PieChart, width: 4 },
  { type: "column", name: "Distribution", blurb: "Founded year, size, score, fit", icon: Columns3, width: 6 },
  { type: "trend", name: "Trend", blurb: "Leads added over time", icon: LineChart, width: 8 },
  { type: "stages", name: "Pipeline", blurb: "Leads per funnel stage", icon: Workflow, width: 4 },
  { type: "compare", name: "Comparison", blurb: "Segments side by side + table", icon: Table2, width: 12 },
];

const KPI_METRICS = [
  { value: "leads", label: "Leads" }, { value: "with_email", label: "Leads with email" }, { value: "email_rate", label: "% with email" },
  { value: "with_website", label: "Leads with website" }, { value: "with_phone", label: "Leads with phone" },
  { value: "contacted", label: "Contacted" }, { value: "engaged", label: "Engaged (replied+)" }, { value: "won", label: "Meetings / won" },
  { value: "avg_score", label: "Avg lead score" }, { value: "avg_icp_fit", label: "Avg ICP fit" },
];
const RANKED_DIMS = [
  { value: "industry", label: "Industry" }, { value: "city", label: "City" }, { value: "state", label: "State" }, { value: "country", label: "Country" },
  { value: "source", label: "Source" }, { value: "tag", label: "Tag" }, { value: "website", label: "Website" },
];
const DONUT_DIMS = [{ value: "source", label: "Source" }, { value: "website", label: "Website" }, { value: "industry", label: "Industry" },
  { value: "country", label: "Country" }, { value: "tag", label: "Tag" }];
const COLUMN_DIMS = [{ value: "founded_year", label: "Founded year" }, { value: "size", label: "Company size" },
  { value: "score", label: "Lead score" }, { value: "icp_fit", label: "ICP fit" }];
const WIDTHS: { value: Width; label: string }[] = [{ value: 3, label: "¼ width" }, { value: 4, label: "⅓ width" }, { value: 6, label: "½ width" },
  { value: 8, label: "⅔ width" }, { value: 12, label: "Full width" }];
const SPAN: Record<Width, string> = { 3: "md:col-span-3", 4: "md:col-span-4", 6: "md:col-span-6", 8: "md:col-span-8", 12: "md:col-span-12" };

const TEMPLATES: { key: string; name: string; blurb: string; widgets: () => Widget[] }[] = [
  {
    key: "overview", name: "Lead overview", blurb: "Headline numbers, trend, pipeline, industries, sources and quality",
    widgets: () => [
      { id: uid(), type: "kpi", title: "Leads", metric: "leads", width: 3 },
      { id: uid(), type: "kpi", title: "With email", metric: "email_rate", width: 3 },
      { id: uid(), type: "kpi", title: "Avg lead score", metric: "avg_score", width: 3 },
      { id: uid(), type: "kpi", title: "Avg ICP fit", metric: "avg_icp_fit", width: 3 },
      { id: uid(), type: "trend", title: "Leads added over time", width: 8 },
      { id: uid(), type: "stages", title: "Pipeline stage", width: 4 },
      { id: uid(), type: "bar", title: "Top industries", dimension: "industry", width: 8 },
      { id: uid(), type: "donut", title: "Lead source", dimension: "source", width: 4 },
      { id: uid(), type: "column", title: "Founded year", dimension: "founded_year", width: 6 },
      { id: uid(), type: "column", title: "Lead score", dimension: "score", width: 6 },
      { id: uid(), type: "compare", title: "Industries compared", dimension: "industry", metric: "avg_score", width: 12 },
    ],
  },
  {
    key: "geo", name: "Locations", blurb: "Cities, states and how each location compares",
    widgets: () => [
      { id: uid(), type: "kpi", title: "Leads", metric: "leads", width: 4 },
      { id: uid(), type: "kpi", title: "With phone", metric: "with_phone", width: 4 },
      { id: uid(), type: "kpi", title: "With website", metric: "with_website", width: 4 },
      { id: uid(), type: "bar", title: "Top cities", dimension: "city", width: 6 },
      { id: uid(), type: "bar", title: "By state", dimension: "state", width: 6 },
      { id: uid(), type: "compare", title: "Cities compared", dimension: "city", metric: "email_rate", width: 12 },
    ],
  },
  { key: "blank", name: "Blank", blurb: "Start empty and add your own widgets", widgets: () => [] },
];

// ───────────── steps ─────────────

const STEP_FIELDS: { value: StepField; label: string; kind: "list" | "range" | "min" | "bool" | "date"; facet?: string; dim?: string }[] = [
  { value: "industry", label: "Industry", kind: "list", facet: "industry", dim: "industry" },
  { value: "city", label: "City", kind: "list", facet: "city", dim: "city" },
  { value: "state", label: "State", kind: "list", facet: "state", dim: "state" },
  { value: "country", label: "Country", kind: "list", facet: "country", dim: "country" },
  { value: "source", label: "Source", kind: "list", facet: "source", dim: "source" },
  { value: "tag", label: "Tag", kind: "list", facet: "tag", dim: "tag" },
  { value: "status", label: "Status", kind: "list" },
  { value: "website", label: "Website", kind: "list", dim: "website" },
  { value: "founded", label: "Founded year", kind: "range" },
  { value: "employees", label: "Employees", kind: "range" },
  { value: "score", label: "Lead score", kind: "range" },
  { value: "icp_fit", label: "ICP fit (min %)", kind: "min" },
  { value: "has_email", label: "Has email", kind: "bool" },
  { value: "has_phone", label: "Has phone", kind: "bool" },
  { value: "has_website", label: "Has website", kind: "bool" },
  { value: "added", label: "Date added", kind: "date" },
];
const fieldDef = (f: StepField) => STEP_FIELDS.find((x) => x.value === f)!;

function stepToFilter(s: Step): InsightsFilter {
  const v = s.values ?? [];
  switch (s.field) {
    case "industry": return { industries: v };
    case "city": return { cities: v };
    case "state": return { states: v };
    case "country": return { countries: v };
    case "source": return { sources: v };
    case "tag": return { tags: v };
    case "status": return { statuses: v };
    case "website": return { website_quality: v };
    case "founded": return { founded_from: s.min ?? null, founded_to: s.max ?? null };
    case "employees": return { employees_min: s.min ?? null, employees_max: s.max ?? null };
    case "score": return { score_min: s.min ?? null, score_max: s.max ?? null };
    case "icp_fit": return { icp_fit_min: s.min ?? null };
    case "has_email": return { has_email: s.bool ?? true };
    case "has_phone": return { has_phone: s.bool ?? true };
    case "has_website": return { has_website: s.bool ?? true };
    case "added": return { created_from: s.from || undefined, created_to: s.to || undefined };
  }
}

function stepText(s: Step): string {
  const d = fieldDef(s.field);
  switch (d.kind) {
    case "list": {
      const v = (s.values ?? []).map((x) => (d.dim ? label(d.dim, x) : cap(x)));
      return v.length ? `${d.label}: ${v.length > 2 ? `${v.slice(0, 2).join(", ")} +${v.length - 2}` : v.join(", ")}` : `${d.label}: any`;
    }
    case "range": return `${d.label}: ${s.min ?? "…"}–${s.max ?? "…"}`;
    case "min": return `ICP fit ≥ ${s.min ?? 0}%`;
    case "bool": return `${s.bool === false ? "No" : "Has"} ${d.label.replace("Has ", "")}`;
    case "date": return `Added ${s.from || "…"} → ${s.to || "today"}`;
  }
}

function StepEditor({ step, onChange, onDone }: { step: Step; onChange: (s: Step) => void; onDone: () => void }) {
  const d = fieldDef(step.field);
  const facet = useFacet(d.facet ?? "industry");
  const options = d.facet ? toOptions(facet.data, d.dim ?? d.facet)
    : step.field === "status" ? STATUS_OPTIONS.map((x) => ({ value: x, label: cap(x) }))
      : step.field === "website" ? WEBSITE_OPTIONS.map((x) => ({ value: x, label: cap(x) })) : [];
  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <Label className="text-xs">Field</Label>
        <Select value={step.field} onValueChange={(f) => onChange({ id: step.id, field: f as StepField, bool: true })}>
          <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
          <SelectContent>{STEP_FIELDS.map((f) => <SelectItem key={f.value} value={f.value}>{f.label}</SelectItem>)}</SelectContent>
        </Select>
      </div>
      {d.kind === "list" && (
        <div className="space-y-1"><Label className="text-xs">Is any of</Label>
          <MultiSelect value={step.values ?? []} onChange={(values) => onChange({ ...step, values })} options={options}
            creatable={!!d.facet} placeholder={`Choose ${d.label.toLowerCase()}…`} /></div>
      )}
      {d.kind === "range" && <NumRange title="Between" lo={step.min} hi={step.max} placeholder={["min", "max"]} onChange={(min, max) => onChange({ ...step, min, max })} />}
      {d.kind === "min" && (
        <div className="space-y-1"><Label className="text-xs">At least (%)</Label>
          <Input type="number" min={0} max={100} value={step.min ?? ""} onChange={(e) => onChange({ ...step, min: e.target.value === "" ? null : Number(e.target.value) })} /></div>
      )}
      {d.kind === "bool" && <TriState title="Value" value={step.bool ?? true} onChange={(v) => onChange({ ...step, bool: v ?? true })} />}
      {d.kind === "date" && (
        <div className="grid grid-cols-2 gap-2">
          <div className="space-y-1"><Label className="text-xs">From</Label><Input type="date" value={step.from ?? ""} onChange={(e) => onChange({ ...step, from: e.target.value })} /></div>
          <div className="space-y-1"><Label className="text-xs">To</Label><Input type="date" value={step.to ?? ""} onChange={(e) => onChange({ ...step, to: e.target.value })} /></div>
        </div>
      )}
      <Button size="sm" className="w-full" onClick={onDone}>Apply</Button>
    </div>
  );
}

/** Step-by-step filters: each step narrows the previous one; counts show what's left. */
function StepsBar({ steps, setSteps, canEdit }: { steps: Step[]; setSteps: (s: Step[]) => void; canEdit: boolean }) {
  const filters = steps.map(stepToFilter);
  const counts = useQuery({
    queryKey: ["analytics", "steps", filters],
    queryFn: () => api.post<{ step: number; count: number }[]>("/analytics/leads/steps", { steps: filters }),
    placeholderData: keepPreviousData,
  });
  const c = counts.data ?? [];
  const total = c[0]?.count ?? 0;
  const [openId, setOpenId] = useState<string | null>(null);
  const update = (s: Step) => setSteps(steps.map((x) => (x.id === s.id ? s : x)));
  const move = (i: number, by: number) => {
    const next = [...steps];
    const [s] = next.splice(i, 1);
    next.splice(i + by, 0, s);
    setSteps(next);
  };
  const addStep = () => {
    const s: Step = { id: uid(), field: "industry", values: [] };
    setSteps([...steps, s]);
    setOpenId(s.id);
  };
  return (
    <div className="rounded-xl border bg-card p-3">
      <div className="mb-2 flex items-center gap-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">
        <Workflow className="size-3.5" /> Step-by-step filters
        {counts.isFetching && <Loader2 className="size-3 animate-spin" />}
        {steps.length > 0 && canEdit && <button type="button" className="ml-auto normal-case hover:text-foreground" onClick={() => setSteps([])}>Clear all</button>}
      </div>
      <div className="flex flex-wrap items-stretch gap-1.5">
        <div className="flex flex-col justify-center rounded-lg bg-muted px-3 py-1.5">
          <span className="text-xs text-muted-foreground">All leads</span>
          <span className="text-sm font-semibold tabular-nums">{num(total)}</span>
        </div>
        {steps.map((s, i) => {
          const n = c[i + 1]?.count;
          return (
            <div key={s.id} className="flex items-stretch">
              <ChevronRight className="mx-0.5 size-4 self-center text-muted-foreground" />
              <Popover open={openId === s.id} onOpenChange={(o) => setOpenId(o ? s.id : null)}>
                <PopoverTrigger asChild disabled={!canEdit}>
                  <button type="button" className="group flex max-w-72 flex-col justify-center rounded-lg border bg-background px-3 py-1.5 text-left transition hover:border-primary/50">
                    <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
                      <span className="flex size-4 items-center justify-center rounded-full bg-primary text-[10px] font-semibold text-primary-foreground">{i + 1}</span>
                      <span className="truncate">{stepText(s)}</span>
                    </span>
                    <span className="text-sm font-semibold tabular-nums">
                      {n == null ? "…" : num(n)} <span className="text-xs font-normal text-muted-foreground">{n != null && total ? pct(n / total) : ""}</span>
                    </span>
                  </button>
                </PopoverTrigger>
                <PopoverContent align="start" className="w-80">
                  <div className="flex items-center justify-between">
                    <span className="text-sm font-medium">Step {i + 1}</span>
                    <span className="flex gap-0.5">
                      <Button size="icon-sm" variant="ghost" aria-label="Move earlier" disabled={i === 0} onClick={() => move(i, -1)}><ArrowLeft /></Button>
                      <Button size="icon-sm" variant="ghost" aria-label="Move later" disabled={i === steps.length - 1} onClick={() => move(i, 1)}><ArrowRight /></Button>
                      <Button size="icon-sm" variant="ghost" aria-label="Remove step" onClick={() => { setSteps(steps.filter((x) => x.id !== s.id)); setOpenId(null); }}><Trash2 /></Button>
                    </span>
                  </div>
                  <StepEditor step={s} onChange={update} onDone={() => setOpenId(null)} />
                </PopoverContent>
              </Popover>
            </div>
          );
        })}
        {canEdit && (
          <>
            {steps.length > 0 && <ChevronRight className="mx-0.5 size-4 self-center text-muted-foreground" />}
            <Button variant="outline" className="h-auto min-h-11 border-dashed" onClick={addStep}><Plus /> Add step</Button>
          </>
        )}
      </div>
    </div>
  );
}

// ───────────── widgets ─────────────

function kpiValue(t: LeadInsights["totals"], metric: string): { value: string; hint?: string } {
  const share = (n: number) => (t.leads ? `${pct(n / t.leads)} of ${num(t.leads)}` : undefined);
  switch (metric) {
    case "email_rate": return { value: t.leads ? pct(t.with_email / t.leads) : "—", hint: `${num(t.with_email)} leads` };
    case "avg_score": return { value: t.avg_score.toFixed(1), hint: "out of 100" };
    case "avg_icp_fit": return { value: t.avg_icp_fit == null ? "—" : `${t.avg_icp_fit.toFixed(0)}%`, hint: "default ICP" };
    case "leads": return { value: num(t.leads) };
    default: {
      const n = (t as unknown as Record<string, number>)[metric] ?? 0;
      return { value: num(n), hint: share(n) };
    }
  }
}

function WidgetBody({ w, steps }: { w: Widget; steps: InsightsFilter[] }) {
  const compareBy = w.type === "compare" ? w.dimension ?? "industry" : "industry";
  const body = { filter: w.filter ?? {}, steps, compare_by: compareBy, top: 10 };
  const q = useQuery({ queryKey: ["analytics", "leads", body], queryFn: () => api.post<LeadInsights>("/analytics/leads", body), placeholderData: keepPreviousData });
  const d = q.data;
  if (!d) return <Skeleton className="h-40" />;
  const t = d.totals;
  switch (w.type) {
    case "kpi": {
      const k = kpiValue(t, w.metric ?? "leads");
      return <div className="py-2"><p className="text-3xl font-semibold tracking-tight">{k.value}</p>{k.hint && <p className="mt-1 text-xs text-muted-foreground">{k.hint}</p>}</div>;
    }
    case "bar": {
      const dim = (w.dimension ?? "industry") as keyof LeadInsights["breakdowns"];
      return <RankedBars dim={dim} data={d.breakdowns[dim] ?? []} total={t.leads} />;
    }
    case "donut": {
      const dim = (w.dimension ?? "source") as keyof LeadInsights["breakdowns"];
      return <Donut dim={dim} data={d.breakdowns[dim] ?? []} />;
    }
    case "column": {
      const dim = (w.dimension ?? "founded_year") as keyof LeadInsights["breakdowns"];
      return <Columns dim={dim} data={d.breakdowns[dim] ?? []} unitLabel={COLUMN_DIMS.find((x) => x.value === dim)?.label ?? dim} />;
    }
    case "trend": return <Trend data={d.trend} unit={d.trend_unit} />;
    case "stages": return <Donut dim="stage" data={d.stages} keepOrder />;
    case "compare": return d.comparison.length ? <ComparisonChart data={d} compareBy={compareBy} metric={(w.metric ?? "leads") as Metric} /> : <Empty />;
  }
}

function filterSummary(f?: InsightsFilter): string | null {
  if (!f) return null;
  const parts = [
    f.industries?.length ? `Industry: ${f.industries.map((x) => label("industry", x)).join(", ")}` : "",
    f.cities?.length ? `City: ${f.cities.join(", ")}` : "",
    f.tags?.length ? `Tag: ${f.tags.join(", ")}` : "",
    f.sources?.length ? `Source: ${f.sources.map((x) => label("source", x)).join(", ")}` : "",
  ].filter(Boolean);
  return parts.length ? parts.join(" · ") : null;
}

function WidgetCard({ w, steps, editing, dragging, over, onEdit, onRemove, onDuplicate, onWidth, onMove, index, count, dragProps }: {
  w: Widget; steps: InsightsFilter[]; editing: boolean; dragging: boolean; over: boolean; index: number; count: number;
  onEdit: () => void; onRemove: () => void; onDuplicate: () => void; onWidth: (w: Width) => void; onMove: (by: number) => void;
  dragProps: React.HTMLAttributes<HTMLDivElement> & { draggable?: boolean };
}) {
  const sub = filterSummary(w.filter);
  return (
    <div {...dragProps} className={`col-span-12 ${SPAN[w.width]} transition ${dragging ? "opacity-40" : ""} ${over ? "rounded-xl ring-2 ring-primary ring-offset-2 ring-offset-background" : ""}`}>
      <Card className={`h-full gap-3 ${editing ? "border-dashed" : ""}`}>
        <CardHeader className="flex flex-row items-start gap-2">
          {editing && <GripVertical className="mt-0.5 size-4 shrink-0 cursor-grab text-muted-foreground active:cursor-grabbing" aria-label="Drag to move" />}
          <div className="min-w-0 flex-1">
            <CardTitle className="truncate text-base">{w.title || WIDGET_TYPES.find((x) => x.type === w.type)?.name}</CardTitle>
            {sub && <p className="mt-0.5 truncate text-xs text-muted-foreground">{sub}</p>}
          </div>
          {editing && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild><Button size="icon-sm" variant="ghost" aria-label="Widget options"><MoreHorizontal /></Button></DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-48">
                <DropdownMenuItem onSelect={onEdit}><Pencil /> Edit widget</DropdownMenuItem>
                <DropdownMenuItem onSelect={onDuplicate}><Copy /> Duplicate</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuLabel className="text-xs text-muted-foreground">Width</DropdownMenuLabel>
                {WIDTHS.map((x) => <DropdownMenuItem key={x.value} onSelect={() => onWidth(x.value)}>{x.label}{w.width === x.value && <span className="ml-auto">✓</span>}</DropdownMenuItem>)}
                <DropdownMenuSeparator />
                <DropdownMenuItem disabled={index === 0} onSelect={() => onMove(-1)}><ArrowLeft /> Move earlier</DropdownMenuItem>
                <DropdownMenuItem disabled={index === count - 1} onSelect={() => onMove(1)}><ArrowRight /> Move later</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onSelect={onRemove}><Trash2 /> Remove</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
        </CardHeader>
        <CardContent className={editing ? "pointer-events-none select-none" : ""}><WidgetBody w={w} steps={steps} /></CardContent>
      </Card>
    </div>
  );
}

// ───────────── widget editor ─────────────

function WidgetDialog({ initial, onSave, onClose }: { initial: Widget | null; onSave: (w: Widget) => void; onClose: () => void }) {
  const [w, setW] = useState<Widget>(() => initial ?? { id: uid(), type: "bar", title: "", dimension: "industry", width: 6 });
  const set = (p: Partial<Widget>) => setW((x) => ({ ...x, ...p }));
  const industries = useFacet("industry"), cities = useFacet("city"), tags = useFacet("tag"), sources = useFacet("source");
  const pickType = (type: WidgetType) => {
    const def = WIDGET_TYPES.find((x) => x.type === type)!;
    set({
      type, width: initial?.type === type ? w.width : def.width,
      dimension: type === "bar" ? "industry" : type === "donut" ? "source" : type === "column" ? "founded_year" : type === "compare" ? "industry" : undefined,
      metric: type === "kpi" ? "leads" : type === "compare" ? "leads" : undefined,
    });
  };
  const dims = w.type === "bar" ? RANKED_DIMS : w.type === "donut" ? DONUT_DIMS : w.type === "column" ? COLUMN_DIMS : w.type === "compare" ? COMPARE_DIMS : [];
  const metrics = w.type === "kpi" ? KPI_METRICS : w.type === "compare" ? METRICS.map((m) => ({ value: m.value, label: m.label })) : [];
  const autoTitle = () => {
    const dim = dims.find((x) => x.value === w.dimension)?.label;
    const met = metrics.find((x) => x.value === w.metric)?.label;
    switch (w.type) {
      case "kpi": return met ?? "Leads";
      case "trend": return "Leads added over time";
      case "stages": return "Pipeline stage";
      case "compare": return `${dim ?? "Segments"} compared`;
      case "column": return `${dim} distribution`;
      default: return `By ${dim?.toLowerCase()}`;
    }
  };
  const f = w.filter ?? {};
  const setF = (p: Partial<InsightsFilter>) => set({ filter: { ...f, ...p } });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex max-h-[90svh] flex-col gap-0 p-0 sm:max-w-2xl">
        <DialogHeader className="border-b p-5">
          <DialogTitle>{initial ? "Edit widget" : "Add widget"}</DialogTitle>
          <DialogDescription>Widgets use the dashboard&apos;s step-by-step filters, plus an optional filter of their own.</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-5">
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {WIDGET_TYPES.map((t) => (
              <button key={t.type} type="button" onClick={() => pickType(t.type)}
                className={`flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition hover:border-primary/50 ${w.type === t.type ? "border-primary bg-primary/5 ring-1 ring-primary/30" : ""}`}>
                <t.icon className="size-5 text-primary" />
                <span className="text-sm font-medium">{t.name}</span>
                <span className="text-xs text-muted-foreground">{t.blurb}</span>
              </button>
            ))}
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1 sm:col-span-2"><Label className="text-xs">Title</Label>
              <Input value={w.title} placeholder={autoTitle()} onChange={(e) => set({ title: e.target.value })} /></div>
            {dims.length > 0 && (
              <div className="space-y-1"><Label className="text-xs">{w.type === "compare" ? "Compare by" : "Dimension"}</Label>
                <Select value={w.dimension} onValueChange={(v) => set({ dimension: v })}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>{dims.map((d) => <SelectItem key={d.value} value={d.value}>{d.label}</SelectItem>)}</SelectContent>
                </Select></div>
            )}
            {metrics.length > 0 && (
              <div className="space-y-1"><Label className="text-xs">Metric</Label>
                <Select value={w.metric} onValueChange={(v) => set({ metric: v })}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>{metrics.map((d) => <SelectItem key={d.value} value={d.value}>{d.label}</SelectItem>)}</SelectContent>
                </Select></div>
            )}
            <div className="space-y-1"><Label className="text-xs">Width</Label>
              <Select value={String(w.width)} onValueChange={(v) => set({ width: Number(v) as Width })}>
                <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>{WIDTHS.map((d) => <SelectItem key={d.value} value={String(d.value)}>{d.label}</SelectItem>)}</SelectContent>
              </Select></div>
          </div>
          <div className="space-y-2 rounded-lg border p-3">
            <p className="text-sm font-medium">Widget filter <span className="font-normal text-muted-foreground">(optional)</span></p>
            <div className="grid gap-2 sm:grid-cols-2">
              <MultiSelect value={f.industries ?? []} onChange={(v) => setF({ industries: v })} options={toOptions(industries.data, "industry")} placeholder="Any industry" />
              <MultiSelect value={f.cities ?? []} onChange={(v) => setF({ cities: v })} options={toOptions(cities.data, "city")} placeholder="Any city" />
              <MultiSelect value={f.tags ?? []} onChange={(v) => setF({ tags: v })} options={toOptions(tags.data, "tag")} placeholder="Any tag" />
              <MultiSelect value={f.sources ?? []} onChange={(v) => setF({ sources: v })} options={toOptions(sources.data, "source")} placeholder="Any source" />
            </div>
          </div>
        </div>
        <DialogFooter className="mx-0! mb-0! rounded-b-xl border-t p-4">
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={() => onSave({ ...w, title: w.title.trim() || autoTitle() })}>{initial ? "Save widget" : "Add widget"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ───────────── dashboard ─────────────

function DashboardView({ dash, canEdit, onDeleted }: { dash: Dashboard; canEdit: boolean; onDeleted: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(dash.name);
  const [widgets, setWidgets] = useState<Widget[]>(dash.widgets ?? []);
  const [steps, setSteps] = useState<Step[]>(dash.steps ?? []);
  const [editing, setEditing] = useState((dash.widgets ?? []).length === 0 && canEdit);
  const [dialog, setDialog] = useState<Widget | "new" | null>(null);
  const [dragId, setDragId] = useState<string | null>(null);
  const [overId, setOverId] = useState<string | null>(null);
  const saved = JSON.stringify({ n: dash.name, w: dash.widgets ?? [], s: dash.steps ?? [] });
  const dirty = JSON.stringify({ n: name, w: widgets, s: steps }) !== saved;
  const stepFilters = useMemo(() => steps.map(stepToFilter), [steps]);

  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => { if (dirty) e.preventDefault(); };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const save = useMutation({
    mutationFn: () => api.put<Dashboard>(`/dashboards/${dash.id}`, { name: name.trim() || "Untitled dashboard", widgets, steps }),
    onSuccess: (d) => { qc.setQueryData<Dashboard[]>(["dashboards"], (list) => list?.map((x) => (x.id === d.id ? d : x))); toast.success("Dashboard saved"); setEditing(false); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const del = useMutation({
    mutationFn: () => api.del(`/dashboards/${dash.id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["dashboards"] }); toast.success("Dashboard deleted"); onDeleted(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const duplicate = useMutation({
    mutationFn: () => api.post<Dashboard>("/dashboards", { name: `${name} (copy)`, widgets, steps }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["dashboards"] }); toast.success("Dashboard duplicated"); },
    onError: (e) => toast.error(errorMessage(e)),
  });

  const move = (from: number, to: number) => setWidgets((ws) => {
    if (to < 0 || to >= ws.length || from === to) return ws;
    const next = [...ws];
    const [w] = next.splice(from, 1);
    next.splice(to, 0, w);
    return next;
  });

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        {editing ? (
          <Input className="h-9 w-72 text-base font-semibold" value={name} onChange={(e) => setName(e.target.value)} aria-label="Dashboard name" />
        ) : (
          <h2 className="text-lg font-semibold">{name}</h2>
        )}
        {dirty && <Badge variant="outline" className="border-amber-500/50 text-amber-700 dark:text-amber-400">Unsaved changes</Badge>}
        <span className="flex-1" />
        {canEdit && (
          <>
            {editing && <Button variant="outline" onClick={() => setDialog("new")}><Plus /> Add widget</Button>}
            <Button variant={editing ? "secondary" : "outline"} onClick={() => setEditing((x) => !x)}><Pencil /> {editing ? "Done editing" : "Edit layout"}</Button>
            <Button disabled={!dirty || save.isPending} onClick={() => save.mutate()}>{save.isPending ? <Loader2 className="animate-spin" /> : <Save />} Save</Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild><Button size="icon" variant="ghost" aria-label="Dashboard options"><MoreHorizontal /></Button></DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => duplicate.mutate()}><Copy /> Duplicate dashboard</DropdownMenuItem>
                {dirty && <DropdownMenuItem onSelect={() => { setName(dash.name); setWidgets(dash.widgets ?? []); setSteps(dash.steps ?? []); }}><X /> Discard changes</DropdownMenuItem>}
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onSelect={() => confirm(`Delete “${dash.name}”?`) && del.mutate()}><Trash2 /> Delete dashboard</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        )}
      </div>

      <StepsBar steps={steps} setSteps={setSteps} canEdit={canEdit} />

      {editing && widgets.length > 0 && (
        <p className="text-xs text-muted-foreground">Drag widgets by their handle to rearrange; use each widget&apos;s ⋯ menu to resize, edit, duplicate or remove.</p>
      )}

      {widgets.length === 0 ? (
        <div className="flex flex-col items-center gap-3 rounded-xl border border-dashed py-16 text-center">
          <LayoutDashboard className="size-8 text-muted-foreground" />
          <p className="font-medium">This dashboard is empty</p>
          <p className="max-w-sm text-sm text-muted-foreground">Add numbers, bar and pie charts, distributions, trends and comparisons. They all follow the step-by-step filters above.</p>
          {canEdit && <Button onClick={() => { setEditing(true); setDialog("new"); }}><Plus /> Add your first widget</Button>}
        </div>
      ) : (
        <div className="grid grid-cols-12 gap-4">
          {widgets.map((w, i) => (
            <WidgetCard key={w.id} w={w} steps={stepFilters} editing={editing} index={i} count={widgets.length}
              dragging={dragId === w.id} over={overId === w.id && dragId !== w.id}
              onEdit={() => setDialog(w)} onRemove={() => setWidgets((ws) => ws.filter((x) => x.id !== w.id))}
              onDuplicate={() => setWidgets((ws) => [...ws.slice(0, i + 1), { ...w, id: uid() }, ...ws.slice(i + 1)])}
              onWidth={(width) => setWidgets((ws) => ws.map((x) => (x.id === w.id ? { ...x, width } : x)))}
              onMove={(by) => move(i, i + by)}
              dragProps={editing ? {
                draggable: true,
                onDragStart: (e) => { setDragId(w.id); e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", w.id); },
                onDragOver: (e) => { e.preventDefault(); e.dataTransfer.dropEffect = "move"; if (overId !== w.id) setOverId(w.id); },
                onDragLeave: () => setOverId((x) => (x === w.id ? null : x)),
                onDrop: (e) => { e.preventDefault(); const from = widgets.findIndex((x) => x.id === dragId); if (from >= 0) move(from, i); setDragId(null); setOverId(null); },
                onDragEnd: () => { setDragId(null); setOverId(null); },
              } : {}} />
          ))}
          {editing && (
            <button type="button" onClick={() => setDialog("new")}
              className="col-span-12 flex min-h-32 flex-col items-center justify-center gap-2 rounded-xl border border-dashed text-sm text-muted-foreground transition hover:border-primary/50 hover:text-foreground md:col-span-4">
              <Plus className="size-5" /> Add widget
            </button>
          )}
        </div>
      )}

      {dialog && (
        <WidgetDialog initial={dialog === "new" ? null : dialog} onClose={() => setDialog(null)}
          onSave={(w) => { setWidgets((ws) => (ws.some((x) => x.id === w.id) ? ws.map((x) => (x.id === w.id ? w : x)) : [...ws, w])); setDialog(null); }} />
      )}
    </div>
  );
}

function NewDashboardDialog({ onCreated, onClose }: { onCreated: (d: Dashboard) => void; onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [template, setTemplate] = useState("overview");
  const create = useMutation({
    mutationFn: () => {
      const t = TEMPLATES.find((x) => x.key === template)!;
      return api.post<Dashboard>("/dashboards", { name: name.trim() || t.name, widgets: t.widgets(), steps: [] });
    },
    onSuccess: (d) => { qc.invalidateQueries({ queryKey: ["dashboards"] }); onCreated(d); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>New dashboard</DialogTitle>
          <DialogDescription>Dashboards are shared with everyone in your organization.</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1"><Label className="text-xs">Name</Label>
            <Input autoFocus value={name} placeholder={TEMPLATES.find((x) => x.key === template)?.name} onChange={(e) => setName(e.target.value)} /></div>
          <div className="space-y-2">
            <Label className="text-xs">Start from</Label>
            {TEMPLATES.map((t) => (
              <button key={t.key} type="button" onClick={() => setTemplate(t.key)}
                className={`flex w-full items-start gap-3 rounded-lg border p-3 text-left transition hover:border-primary/50 ${template === t.key ? "border-primary bg-primary/5 ring-1 ring-primary/30" : ""}`}>
                <LayoutDashboard className="mt-0.5 size-4 text-primary" />
                <span><span className="block text-sm font-medium">{t.name}</span><span className="block text-xs text-muted-foreground">{t.blurb}</span></span>
              </button>
            ))}
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button disabled={create.isPending} onClick={() => create.mutate()}>{create.isPending && <Loader2 className="animate-spin" />} Create dashboard</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The Analytics → Dashboards tab. */
export function DashboardsPanel() {
  const canEdit = useCan("member");
  const list = useQuery({ queryKey: ["dashboards"], queryFn: () => api.get<Dashboard[]>("/dashboards") });
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const dashboards = list.data ?? [];
  const current = dashboards.find((d) => d.id === selected) ?? dashboards[0];

  if (list.isLoading) return <Skeleton className="h-96" />;
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-wrap gap-1 rounded-lg bg-muted p-1">
          {dashboards.map((d) => (
            <button key={d.id} type="button" onClick={() => setSelected(d.id)}
              className={`flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm transition ${current?.id === d.id ? "bg-background font-medium shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>
              <LayoutDashboard className="size-3.5" /> {d.name}
            </button>
          ))}
        </div>
        {canEdit && <Button variant="outline" onClick={() => setCreating(true)}><Plus /> New dashboard</Button>}
      </div>
      {!current ? (
        <EmptyState icon={LayoutDashboard} title="No dashboards yet">
          <p className="text-sm text-muted-foreground">Build your own views of the lead base with drag-and-drop widgets and step-by-step filters.</p>
          {canEdit && <Button className="mt-2" onClick={() => setCreating(true)}><Plus /> Create a dashboard</Button>}
        </EmptyState>
      ) : (
        <DashboardView key={current.id + current.updated_at} dash={current} canEdit={canEdit} onDeleted={() => setSelected(null)} />
      )}
      {creating && <NewDashboardDialog onClose={() => setCreating(false)} onCreated={(d) => { setSelected(d.id); setCreating(false); }} />}
    </div>
  );
}
