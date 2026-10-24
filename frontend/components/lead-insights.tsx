"use client";

// Lead insights: who is in the lead base under any filter. Charts follow the
// same rules as components/charts.tsx — one y-axis, thin marks, a 2px surface
// gap between fills, categorical colors in fixed slot order (never cycled,
// "Other" in neutral gray), text in text tokens, and a hover tooltip on every
// chart.
import { useMemo, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import {
  Bar, BarChart, CartesianGrid, Cell, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis,
} from "recharts";
import { Bookmark, Filter, Loader2, Plus, RotateCcw, Trash2, X } from "lucide-react";
import { StatTile } from "@/components/charts";
import { FilterSelect } from "@/components/filter-select";
import { MultiSelect, type MultiOption } from "@/components/multi-select";
import { num, pct } from "@/components/shared";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { Facet, InsightBucket, InsightsFilter, LeadInsights } from "@/types/api";

// ───────────── labels ─────────────

export const STAGES = ["new", "in_outreach", "engaged", "won", "closed"] as const;
export const STAGE_LABEL: Record<string, string> = { new: "New", in_outreach: "In outreach", engaged: "Engaged", won: "Meeting / won", closed: "Lost / unsubscribed" };
export const STATUS_OPTIONS = ["new", "contacted", "replied", "interested", "qualified", "meeting", "won", "lost", "unsubscribed"];
export const SOURCE_LABEL: Record<string, string> = {
  google_places: "Google Places", openstreetmap: "OpenStreetMap", website: "Website", seed: "Demo data", csv: "CSV import", manual: "Manual",
  apollo: "Apollo", peopledatalabs: "People Data Labs", hunter: "Hunter", crunchbase: "Crunchbase", lusha: "Lusha", snov: "Snov.io", clay: "Clay",
};
export const WEBSITE_OPTIONS = ["good", "poor", "not checked", "no website"];
export const cap = (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);
export const label = (dim: string, key: string) =>
  dim === "stage" ? STAGE_LABEL[key] ?? key : dim === "source" ? SOURCE_LABEL[key] ?? cap(key.replaceAll("_", " ")) : dim === "industry" || dim === "website" ? cap(key) : key;

// Categorical slots in fixed order; "Other" is neutral.
export const slot = (i: number) => `var(--series-${(i % 6) + 1})`;
const OTHER = "var(--series-other)";
const axis = { stroke: "var(--muted-foreground)", fontSize: 11, tickLine: false, axisLine: false } as const;

// ───────────── chart pieces ─────────────

export function TipBox({ title, rows }: { title: string; rows: { label: string; value: string; color?: string }[] }) {
  return (
    <div className="rounded-lg border bg-popover px-3 py-2 text-xs shadow-md">
      <p className="mb-1 font-medium text-popover-foreground">{title}</p>
      {rows.map((r) => (
        <p key={r.label} className="flex items-center gap-2 text-muted-foreground">
          {r.color && <span className="inline-block size-2 rounded-full" style={{ background: r.color }} />}
          <span>{r.label}</span><span className="ml-auto pl-3 font-medium tabular-nums text-popover-foreground">{r.value}</span>
        </p>
      ))}
    </div>
  );
}

export function ChartCard({ title, description, children, className = "", action }: {
  title: string; description?: string; children: React.ReactNode; className?: string; action?: React.ReactNode;
}) {
  return (
    <Card className={className}>
      <CardHeader className="flex flex-row items-start justify-between gap-3">
        <div className="space-y-1"><CardTitle className="text-base">{title}</CardTitle>{description && <CardDescription>{description}</CardDescription>}</div>
        {action}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

export const Empty = () => <p className="flex h-48 items-center justify-center text-sm text-muted-foreground">No data for these filters.</p>;

/** Horizontal bars for a ranked breakdown; clicking a bar narrows the filters to it. */
export function RankedBars({ dim, data, total, onPick }: { dim: string; data: InsightBucket[]; total: number; onPick?: (key: string) => void }) {
  if (!data.length) return <Empty />;
  const rows = data.map((b) => ({ ...b, name: b.key === "Other" ? "Other" : label(dim, b.key) }));
  return (
    <>
      <div style={{ height: Math.max(160, rows.length * 30 + 16) }}>
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={rows} layout="vertical" margin={{ top: 0, right: 40, left: 0, bottom: 0 }} barCategoryGap={4}>
            <CartesianGrid horizontal={false} stroke="var(--border)" />
            <XAxis type="number" allowDecimals={false} {...axis} />
            <YAxis type="category" dataKey="name" width={130} interval={0} {...axis} tick={{ fill: "var(--foreground)", fontSize: 12 }}
              tickFormatter={(v: string) => (v.length > 18 ? v.slice(0, 17) + "…" : v)} />
            <Tooltip cursor={{ fill: "var(--muted)", opacity: 0.6 }} content={({ active, payload }) => {
              const p = payload?.[0]?.payload as (typeof rows)[number] | undefined;
              if (!active || !p) return null;
              return <TipBox title={p.name} rows={[{ label: "Leads", value: num(p.count) }, { label: "Share", value: pct(p.count / Math.max(total, 1)) }]} />;
            }} />
            <Bar dataKey="count" name="Leads" radius={[0, 4, 4, 0]} maxBarSize={20}
              label={{ position: "right", fontSize: 11, fill: "var(--muted-foreground)", formatter: (v: unknown) => num(Number(v)) }}
              onClick={(d: unknown) => { const k = (d as { key?: string })?.key; if (onPick && k && k !== "Other") onPick(k); }}
              className={onPick ? "cursor-pointer" : ""}>
              {rows.map((r) => <Cell key={r.key} fill={r.key === "Other" ? OTHER : "var(--series-1)"} />)}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>
    </>
  );
}

/** Ordinal columns (years, sizes, score bands) in their natural order. */
export function Columns({ data, color = "var(--series-1)", unitLabel }: { dim: string; data: InsightBucket[]; color?: string; unitLabel: string }) {
  if (!data.length) return <Empty />;
  return (
    <>
      <div className="h-52">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} margin={{ top: 4, right: 4, left: 0, bottom: 0 }} barCategoryGap={2}>
            <CartesianGrid vertical={false} stroke="var(--border)" />
            <XAxis dataKey="key" interval="preserveStartEnd" minTickGap={8} {...axis} />
            <YAxis allowDecimals={false} width={44} {...axis} />
            <Tooltip cursor={{ fill: "var(--muted)", opacity: 0.6 }} content={({ active, payload }) => {
              const p = payload?.[0]?.payload as InsightBucket | undefined;
              return active && p ? <TipBox title={`${unitLabel} ${p.key}`} rows={[{ label: "Leads", value: num(p.count), color }]} /> : null;
            }} />
            <Bar dataKey="count" name="Leads" fill={color} radius={[4, 4, 0, 0]} maxBarSize={36} />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </>
  );
}

/** Part-to-whole for ≤ 6 parts (+ Other): donut with a legend carrying values. */
export function Donut({ dim, data, keepOrder }: { dim: string; data: InsightBucket[]; keepOrder?: boolean }) {
  const total = data.reduce((n, b) => n + b.count, 0);
  const rows = useMemo(() => {
    const sorted = keepOrder ? data : [...data].sort((a, b) => (a.key === "Other" ? 1 : b.key === "Other" ? -1 : b.count - a.count));
    const top = sorted.filter((b) => b.key !== "Other").slice(0, 6);
    const rest = sorted.filter((b) => !top.includes(b)).reduce((n, b) => n + b.count, 0);
    const out = top.map((b, i) => ({ ...b, name: label(dim, b.key), color: slot(i) }));
    if (rest > 0) out.push({ key: "Other", count: rest, name: "Other", color: OTHER });
    return out;
  }, [data, dim, keepOrder]);
  if (!total) return <Empty />;
  return (
    <>
      <div className="flex flex-col items-center gap-4">
        <div className="relative size-44 shrink-0">
          <ResponsiveContainer width="100%" height="100%">
            <PieChart>
              <Pie data={rows.filter((r) => r.count > 0)} dataKey="count" nameKey="name" innerRadius="62%" outerRadius="100%"
                stroke="var(--card)" strokeWidth={2} startAngle={90} endAngle={-270} isAnimationActive={false}>
                {rows.filter((r) => r.count > 0).map((r) => <Cell key={r.key} fill={r.color} />)}
              </Pie>
              <Tooltip content={({ active, payload }) => {
                const p = payload?.[0]?.payload as (typeof rows)[number] | undefined;
                return active && p ? <TipBox title={p.name} rows={[{ label: "Leads", value: num(p.count), color: p.color }, { label: "Share", value: pct(p.count / total) }]} /> : null;
              }} />
            </PieChart>
          </ResponsiveContainer>
          <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
            <span className="text-xl font-semibold">{num(total)}</span><span className="text-xs text-muted-foreground">leads</span>
          </div>
        </div>
        <ul className="w-full space-y-1.5 text-sm">
          {rows.map((r) => (
            <li key={r.key} className="flex items-center gap-2">
              <span className="size-2.5 shrink-0 rounded-sm" style={{ background: r.color }} />
              <span className="min-w-0 flex-1 truncate">{r.name}</span>
              <span className="tabular-nums text-muted-foreground">{pct(r.count / total)}</span>
              <span className="w-12 text-right font-medium tabular-nums">{num(r.count)}</span>
            </li>
          ))}
        </ul>
      </div>
    </>
  );
}

export function Trend({ data, unit }: { data: LeadInsights["trend"]; unit: string }) {
  if (!data.length) return <Empty />;
  const fmt = (d: string) => new Date(d + "T00:00:00").toLocaleDateString(undefined, unit === "month" ? { month: "short", year: "2-digit" } : { month: "short", day: "numeric" });
  return (
    <>
      <div className="h-56">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} margin={{ top: 4, right: 4, left: 0, bottom: 0 }} barCategoryGap={2}>
            <CartesianGrid vertical={false} stroke="var(--border)" />
            <XAxis dataKey="date" tickFormatter={fmt} minTickGap={24} {...axis} />
            <YAxis allowDecimals={false} width={44} {...axis} />
            <Tooltip cursor={{ fill: "var(--muted)", opacity: 0.6 }} content={({ active, payload }) => {
              const p = payload?.[0]?.payload as { date: string; count: number } | undefined;
              return active && p ? <TipBox title={`${unit === "week" ? "Week of " : ""}${fmt(p.date)}`} rows={[{ label: "Leads added", value: num(p.count), color: "var(--series-1)" }]} /> : null;
            }} />
            <Bar dataKey="count" name="Leads added" fill="var(--series-1)" radius={[4, 4, 0, 0]} maxBarSize={28} />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </>
  );
}

// ───────────── comparison ─────────────

export const COMPARE_DIMS = [
  { value: "industry", label: "Industry" }, { value: "city", label: "City" }, { value: "state", label: "State" }, { value: "country", label: "Country" },
  { value: "source", label: "Source" }, { value: "tag", label: "Tag" }, { value: "website", label: "Website" }, { value: "founded_year", label: "Founded year" },
  { value: "size", label: "Company size" }, { value: "score", label: "Lead score band" },
];
export const METRICS = [
  { value: "mix", label: "Pipeline mix" }, { value: "leads", label: "Leads" }, { value: "avg_score", label: "Avg score" },
  { value: "avg_icp_fit", label: "Avg ICP fit" }, { value: "email_rate", label: "% with email" },
] as const;
export type Metric = (typeof METRICS)[number]["value"];

export function ComparisonChart({ data, compareBy, metric, showTable = true }: { data: LeadInsights; compareBy: string; metric: Metric; showTable?: boolean }) {
  const rows = data.comparison.map((r) => ({
    ...r, name: label(compareBy, r.key),
    email_rate: r.with_email / Math.max(r.leads, 1),
    ...Object.fromEntries(STAGES.map((s) => [`stage_${s}`, r.stages[s] ?? 0])),
  }));
  const presentStages = STAGES.filter((s) => rows.some((r) => (r.stages[s] ?? 0) > 0));
  const value = (r: (typeof rows)[number]) =>
    metric === "avg_score" ? r.avg_score : metric === "avg_icp_fit" ? r.avg_icp_fit ?? 0 : metric === "email_rate" ? r.email_rate * 100 : r.leads;
  const fmtV = (v: number) => (metric === "email_rate" ? `${v.toFixed(0)}%` : metric === "leads" || metric === "mix" ? num(v) : v.toFixed(1));
  return (
    <>
      {!rows.length ? <Empty /> : (
        <>
          {metric === "mix" && (
            <div className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
              {presentStages.map((s) => (
                <span key={s} className="flex items-center gap-1.5"><span className="size-2.5 rounded-sm" style={{ background: slot(STAGES.indexOf(s)) }} />{STAGE_LABEL[s]}</span>
              ))}
            </div>
          )}
          <div style={{ height: Math.max(180, rows.length * 32 + 16) }}>
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={rows} layout="vertical" margin={{ top: 0, right: 48, left: 0, bottom: 0 }} barCategoryGap={5}>
                <CartesianGrid horizontal={false} stroke="var(--border)" />
                <XAxis type="number" allowDecimals={metric !== "leads" && metric !== "mix"} domain={metric === "email_rate" ? [0, 100] : undefined}
                  tickFormatter={(v: number) => (metric === "email_rate" ? `${v}%` : num(v))} {...axis} />
                <YAxis type="category" dataKey="name" width={140} interval={0} {...axis} tick={{ fill: "var(--foreground)", fontSize: 12 }}
                  tickFormatter={(v: string) => (v.length > 20 ? v.slice(0, 19) + "…" : v)} />
                <Tooltip cursor={{ fill: "var(--muted)", opacity: 0.6 }} content={({ active, payload }) => {
                  const r = payload?.[0]?.payload as (typeof rows)[number] | undefined;
                  if (!active || !r) return null;
                  return <TipBox title={r.name} rows={[
                    { label: "Leads", value: num(r.leads) },
                    ...presentStages.map((s) => ({ label: STAGE_LABEL[s], value: num(r.stages[s] ?? 0), color: slot(STAGES.indexOf(s)) })),
                    { label: "With email", value: pct(r.email_rate) },
                    { label: "Avg score", value: r.avg_score.toFixed(1) },
                    { label: "Avg ICP fit", value: r.avg_icp_fit == null ? "—" : `${r.avg_icp_fit.toFixed(0)}%` },
                  ]} />;
                }} />
                {metric === "mix"
                  ? presentStages.map((s, i) => (
                    <Bar key={s} dataKey={`stage_${s}`} name={STAGE_LABEL[s]} stackId="mix" fill={slot(STAGES.indexOf(s))} stroke="var(--card)" strokeWidth={1}
                      radius={i === presentStages.length - 1 ? [0, 4, 4, 0] : 0} maxBarSize={20} />
                  ))
                  : <Bar dataKey={value} name={METRICS.find((m) => m.value === metric)?.label} fill="var(--series-1)" radius={[0, 4, 4, 0]} maxBarSize={20}
                    label={{ position: "right", fontSize: 11, fill: "var(--muted-foreground)", formatter: (v: unknown) => fmtV(Number(v)) }} />}
              </BarChart>
            </ResponsiveContainer>
          </div>
          {showTable && <div className="mt-4 overflow-x-auto rounded-lg border">
            <Table className="text-[13px]">
              <TableHeader className="bg-muted/40">
                <TableRow>
                  <TableHead>{COMPARE_DIMS.find((d) => d.value === compareBy)?.label}</TableHead>
                  <TableHead className="text-right">Leads</TableHead><TableHead className="text-right">With email</TableHead>
                  <TableHead className="text-right">Contacted</TableHead><TableHead className="text-right">Engaged</TableHead>
                  <TableHead className="text-right">Avg score</TableHead><TableHead className="text-right">Avg ICP fit</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((r) => (
                  <TableRow key={r.key}>
                    <TableCell className="font-medium">{r.name}</TableCell>
                    <TableCell className="text-right tabular-nums">{num(r.leads)}</TableCell>
                    <TableCell className="text-right tabular-nums">{pct(r.email_rate)}</TableCell>
                    <TableCell className="text-right tabular-nums">{pct(r.contacted / Math.max(r.leads, 1))}</TableCell>
                    <TableCell className="text-right tabular-nums">{pct(r.engaged / Math.max(r.leads, 1))}</TableCell>
                    <TableCell className="text-right tabular-nums">{r.avg_score.toFixed(1)}</TableCell>
                    <TableCell className="text-right tabular-nums">{r.avg_icp_fit == null ? "—" : `${r.avg_icp_fit.toFixed(0)}%`}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>}
        </>
      )}
    </>
  );
}

function Comparison({ data, compareBy, setCompareBy }: { data: LeadInsights; compareBy: string; setCompareBy: (v: string) => void }) {
  const [metric, setMetric] = useState<Metric>("mix");
  return (
    <ChartCard title="Compare segments" description="Leads side by side by any dimension — counts, pipeline mix, quality and fit."
      className="lg:col-span-2"
      action={
        <div className="flex flex-wrap items-center gap-2">
          <FilterSelect className="w-40" value={compareBy} onChange={(v) => setCompareBy(v || "industry")} placeholder="Compare by…" options={COMPARE_DIMS} />
          <div className="inline-flex rounded-lg bg-muted p-0.5 text-xs">
            {METRICS.map((m) => (
              <button key={m.value} type="button" onClick={() => setMetric(m.value)}
                className={`rounded-md px-2.5 py-1 transition ${metric === m.value ? "bg-background font-medium shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>{m.label}</button>
            ))}
          </div>
        </div>
      }>
      <ComparisonChart data={data} compareBy={compareBy} metric={metric} />
    </ChartCard>
  );
}

// ───────────── filters ─────────────

const DATE_RANGES = [
  { value: "7", label: "Added in last 7 days" }, { value: "30", label: "Added in last 30 days" },
  { value: "90", label: "Added in last 90 days" }, { value: "365", label: "Added in last 12 months" }, { value: "custom", label: "Custom range…" },
];
export const isoDay = (d: Date) => d.toISOString().slice(0, 10);

export function useFacet(field: string) {
  return useQuery({ queryKey: ["facets", field], queryFn: () => api.get<Facet[]>("/leads/facets", { field }), staleTime: 60_000 });
}
export const toOptions = (f: Facet[] | undefined, dim: string): MultiOption[] =>
  (f ?? []).map((x) => ({ value: x.value, label: label(dim, x.value), hint: num(x.count) }));

type Range = { from: string; to: string };

export function NumRange({ title, lo, hi, onChange, placeholder }: {
  title: string; lo?: number | null; hi?: number | null; onChange: (lo: number | null, hi: number | null) => void; placeholder: [string, string];
}) {
  const parse = (s: string) => (s.trim() === "" ? null : Number(s));
  return (
    <div className="space-y-1">
      <Label className="text-xs">{title}</Label>
      <div className="flex items-center gap-1.5">
        <Input type="number" className="h-8" placeholder={placeholder[0]} value={lo ?? ""} onChange={(e) => onChange(parse(e.target.value), hi ?? null)} />
        <span className="text-muted-foreground">–</span>
        <Input type="number" className="h-8" placeholder={placeholder[1]} value={hi ?? ""} onChange={(e) => onChange(lo ?? null, parse(e.target.value))} />
      </div>
    </div>
  );
}

export function TriState({ title, value, onChange }: { title: string; value?: boolean | null; onChange: (v: boolean | null) => void }) {
  return (
    <div className="space-y-1">
      <Label className="text-xs">{title}</Label>
      <div className="inline-flex w-full rounded-md border p-0.5 text-xs">
        {([[null, "Any"], [true, "Yes"], [false, "No"]] as const).map(([v, l]) => (
          <button key={l} type="button" onClick={() => onChange(v)}
            className={`flex-1 rounded px-2 py-1 ${value === v || (v === null && value == null) ? "bg-primary text-primary-foreground" : "hover:bg-muted"}`}>{l}</button>
        ))}
      </div>
    </div>
  );
}

const EXTRA_KEYS: (keyof InsightsFilter)[] = ["states", "countries", "sources", "website_quality", "founded_from", "founded_to", "employees_min",
  "employees_max", "score_min", "score_max", "icp_fit_min", "has_email", "has_phone", "has_website"];

function chipsFor(f: InsightsFilter): { key: string; text: string; clear: Partial<InsightsFilter> }[] {
  const out: { key: string; text: string; clear: Partial<InsightsFilter> }[] = [];
  const list = (k: keyof InsightsFilter, name: string, dim: string) => {
    const v = f[k] as string[] | undefined;
    if (v?.length) out.push({ key: k, text: `${name}: ${v.map((x) => label(dim, x)).join(", ")}`, clear: { [k]: [] } });
  };
  list("states", "State", "state"); list("countries", "Country", "country"); list("sources", "Source", "source"); list("website_quality", "Website", "website");
  const rng = (lo: keyof InsightsFilter, hi: keyof InsightsFilter, name: string, unit = "") => {
    const a = f[lo] as number | null | undefined, b = f[hi] as number | null | undefined;
    if (a != null || b != null) out.push({ key: name, text: `${name}: ${a ?? "…"}–${b ?? "…"}${unit}`, clear: { [lo]: null, [hi]: null } });
  };
  rng("founded_from", "founded_to", "Founded"); rng("employees_min", "employees_max", "Employees"); rng("score_min", "score_max", "Score");
  if (f.icp_fit_min != null) out.push({ key: "icp", text: `ICP fit ≥ ${f.icp_fit_min}%`, clear: { icp_fit_min: null } });
  for (const [k, n] of [["has_email", "Email"], ["has_phone", "Phone"], ["has_website", "Website"]] as const) {
    if (f[k] != null) out.push({ key: k, text: `${f[k] ? "Has" : "No"} ${n.toLowerCase()}`, clear: { [k]: null } });
  }
  return out;
}

type SavedView = { name: string; filter: InsightsFilter; range: string; custom: Range };
const VIEWS_KEY = "leadline.insights.views";
const loadViews = (): SavedView[] => { try { return JSON.parse(localStorage.getItem(VIEWS_KEY) ?? "[]"); } catch { return []; } };
const storeViews = (v: SavedView[]) => { try { localStorage.setItem(VIEWS_KEY, JSON.stringify(v)); } catch { /* storage unavailable */ } };

// ───────────── main ─────────────

export function LeadInsightsPanel() {
  const [filter, setFilter] = useState<InsightsFilter>({});
  const [range, setRange] = useState("");
  const [custom, setCustom] = useState<Range>({ from: "", to: "" });
  const [compareBy, setCompareBy] = useState("industry");
  const [views, setViews] = useState<SavedView[]>(loadViews);
  const set = (p: Partial<InsightsFilter>) => setFilter((f) => ({ ...f, ...p }));
  const add = (k: "industries" | "cities" | "tags" | "states" | "sources", v: string) =>
    setFilter((f) => ({ ...f, [k]: [...new Set([...(f[k] ?? []), v])] }));

  const dates = useMemo(() => {
    if (range === "custom") return { created_from: custom.from || undefined, created_to: custom.to || undefined };
    if (!range) return {};
    const from = new Date(); from.setUTCDate(from.getUTCDate() - Number(range) + 1);
    return { created_from: isoDay(from) };
  }, [range, custom]);
  const body = { filter: { ...filter, ...dates }, compare_by: compareBy, top: 10 };
  const q = useQuery({ queryKey: ["analytics", "leads", body], queryFn: () => api.post<LeadInsights>("/analytics/leads", body), placeholderData: keepPreviousData });

  const industries = useFacet("industry"), cities = useFacet("city"), tags = useFacet("tag"), states = useFacet("state"),
    countries = useFacet("country"), sources = useFacet("source");
  const chips = chipsFor(filter);
  const extraActive = EXTRA_KEYS.filter((k) => { const v = filter[k]; return Array.isArray(v) ? v.length > 0 : v != null; }).length;
  const anyFilter = range !== "" || chips.length > 0 || ["industries", "cities", "tags", "statuses"].some((k) => (filter[k as keyof InsightsFilter] as string[] | undefined)?.length);
  const d = q.data;
  const t = d?.totals;
  const share = (n: number) => (t && t.leads ? pct(n / t.leads) : "—");

  const saveView = () => {
    const name = prompt("Name this view", "My view")?.trim();
    if (!name) return;
    const next = [...views.filter((v) => v.name !== name), { name, filter, range, custom }];
    setViews(next); storeViews(next);
  };

  return (
    <div className="space-y-4">
      {/* Filters: one row of the common ones, the rest under "More filters" */}
      <div className="sticky top-0 z-20 -mx-1 space-y-2 rounded-xl border bg-background/95 p-3 shadow-xs backdrop-blur">
        <div className="flex flex-wrap items-center gap-2">
          <FilterSelect className="w-48" value={range} onChange={setRange} placeholder="All time" options={DATE_RANGES} />
          {range === "custom" && (
            <span className="flex items-center gap-1">
              <Input type="date" className="h-9 w-36" value={custom.from} onChange={(e) => setCustom((c) => ({ ...c, from: e.target.value }))} />
              <span className="text-muted-foreground">–</span>
              <Input type="date" className="h-9 w-36" value={custom.to} onChange={(e) => setCustom((c) => ({ ...c, to: e.target.value }))} />
            </span>
          )}
          <MultiSelect className="w-44" value={filter.industries ?? []} onChange={(v) => set({ industries: v })} options={toOptions(industries.data, "industry")} placeholder="All industries" />
          <MultiSelect className="w-44" value={filter.cities ?? []} onChange={(v) => set({ cities: v })} options={toOptions(cities.data, "city")} placeholder="All cities" />
          <MultiSelect className="w-40" value={filter.tags ?? []} onChange={(v) => set({ tags: v })} options={toOptions(tags.data, "tag")} placeholder="All tags" />
          <MultiSelect className="w-40" value={filter.statuses ?? []} onChange={(v) => set({ statuses: v })}
            options={STATUS_OPTIONS.map((s) => ({ value: s, label: cap(s) }))} placeholder="Any status" />
          <Popover>
            <PopoverTrigger asChild>
              <Button variant="outline"><Filter /> More filters {extraActive > 0 && <Badge className="ml-0.5 h-5 px-1.5">{extraActive}</Badge>}</Button>
            </PopoverTrigger>
            <PopoverContent align="start" className="w-[min(92vw,560px)] p-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1"><Label className="text-xs">State</Label>
                  <MultiSelect value={filter.states ?? []} onChange={(v) => set({ states: v })} options={toOptions(states.data, "state")} placeholder="Any state" /></div>
                <div className="space-y-1"><Label className="text-xs">Country</Label>
                  <MultiSelect value={filter.countries ?? []} onChange={(v) => set({ countries: v })} options={toOptions(countries.data, "country")} placeholder="Any country" /></div>
                <div className="space-y-1"><Label className="text-xs">Source</Label>
                  <MultiSelect value={filter.sources ?? []} onChange={(v) => set({ sources: v })} options={toOptions(sources.data, "source")} placeholder="Any source" /></div>
                <div className="space-y-1"><Label className="text-xs">Website</Label>
                  <MultiSelect value={filter.website_quality ?? []} onChange={(v) => set({ website_quality: v })}
                    options={WEBSITE_OPTIONS.map((w) => ({ value: w, label: cap(w) }))} placeholder="Any website" /></div>
                <NumRange title="Founded / opened (year)" lo={filter.founded_from} hi={filter.founded_to} placeholder={["from", "to"]}
                  onChange={(a, b) => set({ founded_from: a, founded_to: b })} />
                <NumRange title="Employees" lo={filter.employees_min} hi={filter.employees_max} placeholder={["min", "max"]}
                  onChange={(a, b) => set({ employees_min: a, employees_max: b })} />
                <NumRange title="Lead score" lo={filter.score_min} hi={filter.score_max} placeholder={["0", "100"]}
                  onChange={(a, b) => set({ score_min: a, score_max: b })} />
                <div className="space-y-1"><Label className="text-xs">Minimum ICP fit (%)</Label>
                  <Input type="number" className="h-8" min={0} max={100} placeholder="e.g. 70" value={filter.icp_fit_min ?? ""}
                    onChange={(e) => set({ icp_fit_min: e.target.value === "" ? null : Number(e.target.value) })} /></div>
                <TriState title="Has email" value={filter.has_email} onChange={(v) => set({ has_email: v })} />
                <TriState title="Has phone" value={filter.has_phone} onChange={(v) => set({ has_phone: v })} />
                <TriState title="Has website" value={filter.has_website} onChange={(v) => set({ has_website: v })} />
              </div>
            </PopoverContent>
          </Popover>
          <span className="flex-1" />
          {q.isFetching && <Loader2 className="size-4 animate-spin text-muted-foreground" />}
          <Popover>
            <PopoverTrigger asChild><Button variant="ghost"><Bookmark /> Views {views.length > 0 && <span className="text-muted-foreground">{views.length}</span>}</Button></PopoverTrigger>
            <PopoverContent align="end" className="w-64 p-1">
              {views.length === 0 && <p className="px-3 py-2 text-xs text-muted-foreground">Save filter combinations you use often.</p>}
              {views.map((v) => (
                <div key={v.name} className="flex items-center gap-1 rounded-md px-1 hover:bg-muted">
                  <button type="button" className="flex-1 truncate px-2 py-1.5 text-left text-sm" onClick={() => { setFilter(v.filter); setRange(v.range); setCustom(v.custom); }}>{v.name}</button>
                  <button type="button" aria-label={`Delete ${v.name}`} className="p-1 text-muted-foreground hover:text-destructive"
                    onClick={() => { const next = views.filter((x) => x.name !== v.name); setViews(next); storeViews(next); }}><Trash2 className="size-3.5" /></button>
                </div>
              ))}
              <button type="button" disabled={!anyFilter} onClick={saveView}
                className="mt-1 flex w-full items-center gap-2 rounded-md border-t px-3 py-2 text-sm hover:bg-muted disabled:opacity-50"><Plus className="size-4" /> Save current filters</button>
            </PopoverContent>
          </Popover>
          {anyFilter && <Button variant="ghost" onClick={() => { setFilter({}); setRange(""); }}><RotateCcw /> Reset</Button>}
        </div>
        {chips.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {chips.map((c) => (
              <span key={c.key} className="inline-flex items-center gap-1 rounded-full border bg-muted/50 py-0.5 pr-1 pl-2.5 text-xs">
                {c.text}
                <button type="button" aria-label={`Remove ${c.text}`} className="rounded-full p-0.5 hover:bg-background" onClick={() => set(c.clear)}><X className="size-3" /></button>
              </span>
            ))}
          </div>
        )}
      </div>

      {!d || !t ? <Skeleton className="h-96" /> : (
        <div className={`space-y-4 transition-opacity ${q.isFetching ? "opacity-70" : ""}`}>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-7">
            <StatTile label="Leads" value={num(t.leads)} />
            <StatTile label="With email" value={num(t.with_email)} hint={share(t.with_email)} />
            <StatTile label="With website" value={num(t.with_website)} hint={share(t.with_website)} />
            <StatTile label="With phone" value={num(t.with_phone)} hint={share(t.with_phone)} />
            <StatTile label="Contacted" value={num(t.contacted)} hint={share(t.contacted)} />
            <StatTile label="Avg lead score" value={t.avg_score.toFixed(1)} hint="out of 100" />
            <StatTile label="Avg ICP fit" value={t.avg_icp_fit == null ? "—" : `${t.avg_icp_fit.toFixed(0)}%`} hint="default ICP" />
          </div>

          <div className="grid gap-4 lg:grid-cols-3">
            <ChartCard title="Leads added over time" description={`Per ${d.trend_unit}, by date added`} className="lg:col-span-2">
              <Trend data={d.trend} unit={d.trend_unit} />
            </ChartCard>
            <ChartCard title="Pipeline stage" description="Where these leads are in your funnel">
              <Donut dim="stage" data={d.stages} keepOrder />
            </ChartCard>
          </div>

          <div className="grid gap-4 lg:grid-cols-3">
            <ChartCard title="By industry" description="Top industries — click a bar to filter" className="lg:col-span-2">
              <RankedBars dim="industry" data={d.breakdowns.industry} total={t.leads} onPick={(k) => add("industries", k)} />
            </ChartCard>
            <div className="space-y-4">
              <ChartCard title="Lead source" description="Where the leads came from"><Donut dim="source" data={d.breakdowns.source} /></ChartCard>
              <ChartCard title="Website presence" description="From the website check"><Donut dim="website" data={d.breakdowns.website} /></ChartCard>
            </div>
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <ChartCard title="By city" description="Top cities — click a bar to filter">
              <RankedBars dim="city" data={d.breakdowns.city} total={t.leads} onPick={(k) => add("cities", k)} />
            </ChartCard>
            <ChartCard title="By tag" description="Click a bar to filter">
              <RankedBars dim="tag" data={d.breakdowns.tag} total={t.leads} onPick={(k) => add("tags", k)} />
            </ChartCard>
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <ChartCard title="Founded / opened year" description="Leads with a known founding year">
              <Columns dim="founded_year" data={d.breakdowns.founded_year} unitLabel="Year" />
            </ChartCard>
            <ChartCard title="Company size" description="Employees, where known">
              <Columns dim="size" data={d.breakdowns.size} unitLabel="Employees" />
            </ChartCard>
            <ChartCard title="Lead score distribution" description="Score bands of 10 points">
              <Columns dim="score" data={d.breakdowns.score} unitLabel="Score" />
            </ChartCard>
            <ChartCard title="ICP fit distribution" description="Fit against your default ICP">
              <Columns dim="icp_fit" data={d.breakdowns.icp_fit} unitLabel="Fit %" />
            </ChartCard>
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <ChartCard title="By state" description="Click a bar to filter">
              <RankedBars dim="state" data={d.breakdowns.state} total={t.leads} onPick={(k) => add("states", k)} />
            </ChartCard>
            <ChartCard title="By source" description="Click a bar to filter">
              <RankedBars dim="source" data={d.breakdowns.source} total={t.leads} onPick={(k) => add("sources", k)} />
            </ChartCard>
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Comparison data={d} compareBy={compareBy} setCompareBy={setCompareBy} />
          </div>
        </div>
      )}
    </div>
  );
}
