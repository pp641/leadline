"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { createContext, useContext, useMemo, useState } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowUpRight, Check, ChevronLeft, ChevronRight, CircleCheck, CircleX, Download, ExternalLink, Filter, List, Loader2, Mail, MapPin, Phone, Plus, RefreshCw, RotateCcw, Save, Search,
  SlidersHorizontal, Star, Target, Trash2, X,
} from "lucide-react";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { MultiSelect, flag, type MultiOption } from "@/components/multi-select";
import { EmptyState, PageHeader, num } from "@/components/shared";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { CatalogFacets, CatalogResult, CatalogStatus, ICP, ICPCriteria } from "@/types/api";

const list = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);
const lc = (s: string) => s.trim().toLowerCase();

const WEBSITE = [
  { value: "missing", label: "No website" }, { value: "missing_or_poor", label: "No website or outdated" },
  { value: "poor", label: "Outdated website" }, { value: "present", label: "Has a website" }, { value: "good", label: "Modern website" },
];
const websiteLabel = (v?: string) => WEBSITE.find((w) => w.value === v)?.label;

const SIZES = [
  { value: "1-10", label: "1–10 employees" }, { value: "11-50", label: "11–50 employees" }, { value: "51-200", label: "51–200 employees" },
  { value: "201-1000", label: "201–1,000 employees" }, { value: "1001-", label: "1,000+ employees" },
];

const TECH_PRESETS = ["wordpress", "wix", "shopify", "squarespace", "webflow", "godaddy", "react", "next.js", "woocommerce", "magento", "jquery", "bootstrap"];
const SIGNAL_PRESETS = ["now open", "newly opened", "grand opening", "coming soon", "new branch", "hiring", "now hiring", "under new management", "renovated", "expanding"];

const count = (n: number) => (n > 0 ? num(n) : undefined);
const withPresets = (facet: { value: string; count: number }[] = [], presets: string[] = []): MultiOption[] => {
  const seen = new Set(facet.map((f) => f.value));
  return [
    ...facet.map((f) => ({ value: f.value, label: f.value, hint: count(f.count) })),
    ...presets.filter((p) => !seen.has(p)).map((p) => ({ value: p, label: p })),
  ];
};

function useFacets() {
  return useQuery({ queryKey: ["catalog-facets"], queryFn: () => api.get<CatalogFacets>("/catalog/facets"), staleTime: 60_000 });
}

function countryCode(name: string, facets?: CatalogFacets) {
  return facets?.countries.find((c) => lc(c.name) === lc(name) || lc(c.code) === lc(name))?.code ?? "";
}

/** Short human-readable chips describing an ICP's criteria. */
function criteriaChips(c: ICPCriteria, facets?: CatalogFacets): { key: string; label: string; icon?: string }[] {
  const out: { key: string; label: string; icon?: string }[] = [];
  (c.countries ?? []).forEach((n) => out.push({ key: `co-${n}`, label: n, icon: flag(countryCode(n, facets)) }));
  (c.states ?? []).forEach((n) => out.push({ key: `st-${n}`, label: n }));
  (c.cities ?? []).forEach((n) => out.push({ key: `ci-${n}`, label: n }));
  (c.industries ?? []).forEach((n) => out.push({ key: `in-${n}`, label: n }));
  if (c.employees_min != null || c.employees_max != null) out.push({ key: "size", label: `${c.employees_min ?? 0}–${c.employees_max ?? "∞"} employees` });
  if (c.founded_after_year) out.push({ key: "year", label: `Opened ${c.founded_after_year}+` });
  if (c.website && c.website !== "any") out.push({ key: "web", label: websiteLabel(c.website) ?? c.website });
  (c.tech_stack ?? []).forEach((n) => out.push({ key: `te-${n}`, label: n }));
  (c.keywords ?? []).forEach((n) => out.push({ key: `kw-${n}`, label: `“${n}”` }));
  (c.buying_signals ?? []).forEach((n) => out.push({ key: `bs-${n}`, label: n }));
  if (c.require_email) out.push({ key: "email", label: "Has email" });
  if (c.require_social) out.push({ key: "social", label: "Has social" });
  return out;
}

function Chips({ c, facets, max = 99 }: { c: ICPCriteria; facets?: CatalogFacets; max?: number }) {
  const chips = criteriaChips(c, facets);
  if (!chips.length) return <span className="text-xs text-muted-foreground">No filters — matches every company</span>;
  return (
    <span className="flex flex-wrap gap-1">
      {chips.slice(0, max).map((x) => (
        <span key={x.key} className="inline-flex items-center gap-1 rounded-md bg-muted px-1.5 py-0.5 text-xs capitalize text-muted-foreground">
          {x.icon && <span>{x.icon}</span>}{x.label}
        </span>
      ))}
      {chips.length > max && <span className="px-1 text-xs text-muted-foreground">+{chips.length - max} more</span>}
    </span>
  );
}

type Tab = "list" | "details";

export default function ICPPage() {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const icps = useQuery({ queryKey: ["icps"], queryFn: () => api.get<ICP[]>("/icps") });
  const facets = useFacets();
  const [tab, setTab] = useState<Tab>("list");
  const [selected, setSelected] = useState<string | "new" | null>(null);
  const current = selected && selected !== "new" ? (icps.data ?? []).find((i) => i.id === selected) ?? null : null;
  const hasDetails = selected === "new" || !!current;
  const open = (id: string | "new") => { setSelected(id); setTab("details"); };

  const tabs = (
    <TabsList>
      <TabsTrigger value="list" className="px-3"><List /> ICP list <span className="tabular-nums text-muted-foreground">{icps.data?.length ?? ""}</span></TabsTrigger>
      <TabsTrigger value="details" className="px-3" disabled={!hasDetails}><Target /> ICP details</TabsTrigger>
    </TabsList>
  );

  return (
    <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)} className="gap-0">
      {tab === "details" && hasDetails ? (
        <ICPDetails key={current?.id ?? "new"} icp={current} canEdit={canEdit} facets={facets.data} tabs={tabs}
          onClose={() => setTab("list")} onNew={() => open("new")}
          onSaved={(i) => { qc.invalidateQueries({ queryKey: ["icps"] }); setSelected(i.id); }}
          onDeleted={() => { qc.invalidateQueries({ queryKey: ["icps"] }); setSelected(null); setTab("list"); }} />
      ) : (
        <>
          <PageHeader title="ICP (Ideal Customer Profile)"
            description="Define who your ideal customers are. Open a profile to see the matching companies from your company database and refine its filters."
            actions={canEdit && <Button onClick={() => open("new")}><Plus /> New ICP</Button>} />
          <div className="mb-4">{tabs}</div>
          {icps.isLoading ? (
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-40 rounded-xl" />)}</div>
          ) : icps.data?.length === 0 ? (
            <EmptyState icon={Target} title="No ideal customer profile yet">
              {canEdit && <Button className="mt-2" onClick={() => open("new")}><Plus /> Create your first ICP</Button>}
            </EmptyState>
          ) : (
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              {(icps.data ?? []).map((i) => (
                <button key={i.id} type="button" onClick={() => open(i.id)}
                  className="group flex flex-col gap-3 rounded-xl border bg-card p-4 text-left shadow-xs transition hover:border-primary/50 hover:shadow-md">
                  <span className="flex items-start gap-3">
                    <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary"><Target className="size-4" /></span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-medium">{i.name}</span>
                      <span className="block text-xs text-muted-foreground">Updated {new Date(i.updated_at).toLocaleDateString(undefined, { dateStyle: "medium" })}</span>
                    </span>
                    {i.is_default && <Badge variant="secondary"><Star className="fill-current" /> Default</Badge>}
                  </span>
                  <Chips c={i.criteria} facets={facets.data} max={8} />
                  <span className="mt-auto flex items-center justify-between border-t pt-3 text-xs text-muted-foreground">
                    <span>{criteriaChips(i.criteria).length} filters</span>
                    <span className="font-medium text-primary group-hover:underline">Open profile →</span>
                  </span>
                </button>
              ))}
              {canEdit && (
                <button type="button" onClick={() => open("new")}
                  className="flex min-h-40 flex-col items-center justify-center gap-2 rounded-xl border border-dashed text-sm text-muted-foreground transition hover:border-primary/50 hover:text-foreground">
                  <Plus className="size-5" /> New ICP
                </button>
              )}
            </div>
          )}
        </>
      )}
    </Tabs>
  );
}

// ───────────── Filter form state (shared by the top bar's Save and the filter panel) ─────────────

function toForm(icp: ICP | null) {
  const c = icp?.criteria ?? {};
  return {
    name: icp?.name ?? "", is_default: icp?.is_default ?? false,
    industries: c.industries ?? [], countries: c.countries ?? [], states: c.states ?? [], cities: c.cities ?? [],
    size: c.employees_min != null || c.employees_max != null ? `${c.employees_min ?? ""}-${c.employees_max ?? ""}` : "",
    founded_after_year: c.founded_after_year?.toString() ?? "", website: c.website && c.website !== "any" ? c.website : "",
    keywords: c.keywords ?? [], tech_stack: c.tech_stack ?? [], buying_signals: c.buying_signals ?? [],
    require_social: !!c.require_social, require_email: !!c.require_email,
  };
}
type Form = ReturnType<typeof toForm>;
const EMPTY_FILTERS: Partial<Form> = {
  industries: [], countries: [], states: [], cities: [], size: "", founded_after_year: "", website: "",
  keywords: [], tech_stack: [], buying_signals: [], require_social: false, require_email: false,
};

function toCriteria(v: Form): ICPCriteria {
  const [min, max] = v.size ? v.size.split("-") : ["", ""];
  return {
    industries: v.industries, countries: v.countries, states: v.states, cities: v.cities,
    employees_min: min ? Number(min) : null, employees_max: max ? Number(max) : null,
    founded_after_year: v.founded_after_year ? Number(v.founded_after_year) : null,
    website: (v.website || "any") as ICPCriteria["website"],
    keywords: v.keywords, tech_stack: v.tech_stack, buying_signals: v.buying_signals,
    require_social: v.require_social, require_email: v.require_email,
  };
}

function ICPDetails({ icp, canEdit, facets, tabs, onClose, onNew, onSaved, onDeleted }: {
  icp: ICP | null; canEdit: boolean; facets?: CatalogFacets; tabs: React.ReactNode;
  onClose: () => void; onNew: () => void; onSaved: (i: ICP) => void; onDeleted: () => void;
}) {
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const initial = useMemo(() => toForm(icp), [icp?.id, icp?.updated_at]);
  const [v, setV] = useState(initial);
  const [lastInitial, setLastInitial] = useState(initial);
  if (initial !== lastInitial) { setLastInitial(initial); setV(initial); }
  const set = (p: Partial<Form>) => setV((x) => ({ ...x, ...p }));
  const dirty = JSON.stringify(v) !== JSON.stringify(initial);
  const [showFilters, setShowFilters] = useState(true);
  const [query, setQuery] = useState("");

  const save = useMutation({
    mutationFn: () => {
      const body = { name: v.name, is_default: v.is_default, criteria: toCriteria(v) };
      return icp ? api.put<ICP>(`/icps/${icp.id}`, body) : api.post<ICP>("/icps", body);
    },
    onSuccess: (i) => { toast.success("Filters applied — companies updated"); onSaved(i); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const del = useMutation({ mutationFn: () => api.del(`/icps/${icp!.id}`), onSuccess: onDeleted, onError: (e) => toast.error(errorMessage(e)) });

  return (
    <div className="-mx-4 -my-6 flex flex-col bg-background md:-mx-8 md:h-svh">
      {/* Top bar: tabs, profile, search, save */}
      <div className="flex flex-wrap items-center gap-3 border-b px-4 py-2.5">
        {tabs}
        <span className="inline-flex min-w-0 items-center gap-2 rounded-md border border-amber-300 bg-amber-50 px-2.5 py-1 text-sm text-amber-900 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200">
          <span className="truncate font-medium italic">{dirty ? "* " : ""}{v.name || "New ICP"}</span>
          {icp?.is_default && <Star className="size-3.5 shrink-0 fill-current" aria-label="Default" />}
          <button type="button" aria-label="Close profile" onClick={onClose} className="rounded-sm opacity-70 hover:opacity-100"><X className="size-3.5" /></button>
        </span>
        <span className="flex-1" />
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input className="h-8 w-56 rounded-full pl-8" placeholder="Search companies" value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>
        {canEdit && (
          <>
            {icp && <Button variant="ghost" size="icon" aria-label="Delete ICP" onClick={() => confirm("Delete this ICP?") && del.mutate()}><Trash2 /></Button>}
            {dirty && <Button variant="ghost" size="sm" onClick={() => setV(initial)}>Discard</Button>}
            <Button size="sm" onClick={() => save.mutate()} disabled={!v.name.trim() || save.isPending || (!!icp && !dirty)}>
              {save.isPending ? <Loader2 className="animate-spin" /> : icp && !dirty ? <Check /> : <Save />} {icp && !dirty ? "Saved" : "Save & apply"}
            </Button>
            <Button size="icon" variant="outline" className="rounded-full" aria-label="New ICP" onClick={onNew}><Plus /></Button>
          </>
        )}
      </div>

      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        <main className="flex min-h-[60svh] min-w-0 flex-1 flex-col">
          {icp ? (
            <CompanyResults icp={icp} canEdit={canEdit} facets={facets} query={query}
              showFilters={showFilters} onToggleFilters={() => setShowFilters((x) => !x)} dirty={dirty} />
          ) : (
            <div className="flex flex-1 flex-col items-center justify-center gap-2 p-10 text-center text-sm text-muted-foreground">
              <SlidersHorizontal className="size-6" />
              Name the profile, set its filters on the right, then <span className="font-medium text-foreground">Save &amp; apply</span> to see matching companies.
            </div>
          )}
        </main>
        {showFilters && (
          <aside className="flex w-full shrink-0 flex-col border-t bg-muted/20 md:w-80 md:border-t-0 md:border-l">
            <FiltersPanel v={v} set={set} canEdit={canEdit} facets={facets} onReset={() => set(EMPTY_FILTERS)} />
          </aside>
        )}
      </div>
    </div>
  );
}

// ───────────── Filter panel ─────────────

const FieldQuery = createContext("");
const stem = (s: string) => s.toLowerCase().split(/[^a-z]+/).map((w) => w.replace(/ies$/, "y").replace(/s$/, "")).join(" ");
const matches = (label: string, q: string) => !q || stem(label).includes(stem(q).trim()) || label.toLowerCase().includes(q);

function Section({ title, labels, children }: { title: string; labels: string[]; children: React.ReactNode }) {
  const q = useContext(FieldQuery);
  if (!labels.some((l) => matches(l, q)) && !matches(title, q)) return null;
  return (
    <section className="space-y-3 border-b px-4 py-3.5 last:border-b-0">
      <h3 className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{title}</h3>
      {children}
    </section>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  const q = useContext(FieldQuery);
  if (!matches(label, q)) return null;
  return (
    <div className="space-y-1">
      <Label className="text-xs font-normal text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
}

function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (x: boolean) => void }) {
  const q = useContext(FieldQuery);
  if (!matches(label, q)) return null;
  return (
    <label className="flex cursor-pointer items-center justify-between gap-2 text-sm">
      {label} <Switch checked={checked} onCheckedChange={onChange} />
    </label>
  );
}

const ALL_FIELDS = ["Profile", "Name", "Default ICP", "Location", "Countries", "States / regions", "Cities", "Business", "Industries", "Company size",
  "Founded / opened", "Online presence", "Website", "Technology", "Has a contact email", "Has social presence", "Signals", "Keywords", "Buying signals"];

function FiltersPanel({ v, set, canEdit, facets, onReset }: {
  v: Form; set: (p: Partial<Form>) => void; canEdit: boolean; facets?: CatalogFacets; onReset: () => void;
}) {
  const [q, setQ] = useState("");
  const ro = !canEdit;

  // ── Location cascade: countries narrow states, states narrow cities ──
  const codes = useMemo(() => new Set(v.countries.map((n) => countryCode(n, facets)).filter(Boolean)), [v.countries, facets]);
  const stateSet = useMemo(() => new Set(v.states.map(lc)), [v.states]);
  const countryOptions: MultiOption[] = useMemo(() => (facets?.countries ?? []).map((x) => ({
    value: x.name, label: x.name, icon: flag(x.code), hint: x.count ? `${num(x.count)}` : undefined,
  })), [facets]);
  const stateOptions: MultiOption[] = useMemo(() => (facets?.states ?? [])
    .filter((s) => codes.size === 0 || codes.has(s.country))
    .map((s) => ({ value: s.name, label: s.name, icon: s.country ? flag(s.country) : undefined, hint: count(s.count) })), [facets, codes]);
  const cityOptions: MultiOption[] = useMemo(() => {
    const seen = new Set<string>();
    return (facets?.cities ?? [])
      .filter((x) => (codes.size === 0 || codes.has(x.country)) && (stateSet.size === 0 || stateSet.has(lc(x.state))))
      .filter((x) => !seen.has(lc(x.name)) && !!seen.add(lc(x.name)))
      .map((x) => ({ value: x.name, label: x.name, group: x.state || undefined, hint: count(x.count) }));
  }, [facets, codes, stateSet]);

  // When a parent is removed, drop known children that no longer belong to it.
  const setCountries = (countries: string[]) => {
    const keep = new Set(countries.map((n) => countryCode(n, facets)).filter(Boolean));
    const fits = (code: string | undefined) => keep.size === 0 || !code || keep.has(code);
    const stateCountry = (s: string) => facets?.states.find((x) => lc(x.name) === lc(s))?.country;
    const cityCountry = (s: string) => facets?.cities.find((x) => lc(x.name) === lc(s))?.country;
    set({ countries, states: v.states.filter((s) => fits(stateCountry(s))), cities: v.cities.filter((s) => fits(cityCountry(s))) });
  };
  const setStates = (states: string[]) => {
    const keep = new Set(states.map(lc));
    const cityState = (s: string) => facets?.cities.find((x) => lc(x.name) === lc(s))?.state;
    set({ states, cities: v.cities.filter((s) => { const st = cityState(s); return keep.size === 0 || !st || keep.has(lc(st)); }) });
  };

  const sizeOptions = SIZES.some((s) => s.value === v.size) || !v.size ? SIZES : [...SIZES, { value: v.size, label: `Custom: ${v.size.replace("-", "–")}` }];
  const thisYear = new Date().getFullYear();
  const years = Array.from({ length: 16 }, (_, i) => String(thisYear - i)).map((y) => ({ value: y, label: `${y} or later` }));
  if (v.founded_after_year && !years.some((y) => y.value === v.founded_after_year)) years.push({ value: v.founded_after_year, label: `${v.founded_after_year} or later` });
  const active = criteriaChips(toCriteria(v)).length;

  return (
    <>
      <div className="shrink-0 space-y-2.5 border-b px-4 py-3">
        <div className="flex items-center justify-between">
          <span className="flex items-center gap-2 text-sm font-semibold">Filter {active > 0 && <Badge variant="secondary" className="tabular-nums">{active}</Badge>}</span>
          {canEdit && <button type="button" onClick={onReset} className="flex items-center gap-1 text-sm font-medium text-primary hover:underline"><RotateCcw className="size-3.5" /> Reset</button>}
        </div>
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input className="h-8 bg-background pl-8 text-sm" placeholder="Search fields" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <FieldQuery.Provider value={q.trim().toLowerCase()}>
          <fieldset disabled={ro} className="min-w-0">
            <Section title="Profile" labels={["Name", "Default ICP"]}>
              <Field label="Name">
                <Input className="h-8 bg-background" value={v.name} placeholder="e.g. Newly opened Delhi NCR restaurants" onChange={(e) => set({ name: e.target.value })} />
              </Field>
              <Toggle label="Default ICP (scores every lead)" checked={v.is_default} onChange={(x) => set({ is_default: x })} />
            </Section>
            <Section title="Location" labels={["Countries", "States / regions", "Cities"]}>
              <Field label="Countries">
                <MultiSelect className="bg-background" value={v.countries} onChange={setCountries} options={countryOptions} disabled={ro} placeholder="Any country" />
              </Field>
              <Field label="States / regions">
                <MultiSelect className="bg-background" value={v.states} onChange={setStates} options={stateOptions} creatable disabled={ro} placeholder={codes.size ? "Any state in selected countries" : "Any state"} emptyText="No states in your data yet — type to add one" />
              </Field>
              <Field label="Cities">
                <MultiSelect className="bg-background" value={v.cities} onChange={(cities) => set({ cities })} options={cityOptions} creatable disabled={ro} placeholder={stateSet.size ? "Any city in selected states" : "Any city"} emptyText="No cities in your data yet — type to add one" />
              </Field>
            </Section>
            <Section title="Business" labels={["Industries", "Company size", "Founded / opened"]}>
              <Field label="Industries">
                <MultiSelect className="bg-background" value={v.industries} onChange={(industries) => set({ industries })} options={withPresets(facets?.industries)} creatable disabled={ro} placeholder="Any industry" />
              </Field>
              <Field label="Company size">
                <FilterSelect className="w-full bg-background" value={v.size} onChange={(size) => set({ size })} placeholder="Any size" options={sizeOptions} />
              </Field>
              <Field label="Founded / opened">
                <FilterSelect className="w-full bg-background" value={v.founded_after_year} onChange={(founded_after_year) => set({ founded_after_year })} placeholder="Any year" options={years} />
              </Field>
            </Section>
            <Section title="Online presence" labels={["Website", "Technology", "Has a contact email", "Has social presence"]}>
              <Field label="Website">
                <FilterSelect className="w-full bg-background" value={v.website} onChange={(website) => set({ website })} placeholder="Any" options={WEBSITE} />
              </Field>
              <Field label="Technology">
                <MultiSelect className="bg-background" value={v.tech_stack} onChange={(tech_stack) => set({ tech_stack })} options={withPresets(facets?.tech_stack, TECH_PRESETS)} creatable disabled={ro} placeholder="Any technology" />
              </Field>
              <Toggle label="Has a contact email" checked={v.require_email} onChange={(x) => set({ require_email: x })} />
              <Toggle label="Has social presence" checked={v.require_social} onChange={(x) => set({ require_social: x })} />
            </Section>
            {q.trim() && ![...ALL_FIELDS].some((l) => matches(l, q.trim().toLowerCase())) && (
              <p className="px-4 py-8 text-center text-sm text-muted-foreground">No fields match “{q.trim()}”.</p>
            )}
            <Section title="Signals" labels={["Keywords", "Buying signals"]}>
              <Field label="Keywords">
                <MultiSelect className="bg-background" value={v.keywords} onChange={(keywords) => set({ keywords })} options={[]} creatable disabled={ro} placeholder="e.g. family, organic" emptyText="Type a keyword and press Enter" />
              </Field>
              <Field label="Buying signals">
                <MultiSelect className="bg-background" value={v.buying_signals} onChange={(buying_signals) => set({ buying_signals })} options={withPresets([], SIGNAL_PRESETS)} creatable disabled={ro} placeholder="e.g. now open, hiring" />
              </Field>
            </Section>
          </fieldset>
        </FieldQuery.Provider>
      </div>
    </>
  );
}

// ───────────── Companies ─────────────

const VERIFICATION_TONE: Record<string, "default" | "secondary" | "destructive" | "outline"> = {
  verified: "default", partial: "secondary", failed: "destructive", unverified: "outline",
};
const QUALITY_TONE: Record<string, string> = {
  good: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400", ok: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
  poor: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
};

const LIMITS = ["50", "100", "250", "500"].map((n) => ({ value: n, label: `Up to ${n}` }));
const SORTS = [
  { value: "name", label: "Name (A–Z)" }, { value: "city", label: "City" },
  { value: "verified", label: "Verification" }, { value: "contact", label: "Has contact details" },
];
const VERIFY_RANK: Record<string, number> = { verified: 0, partial: 1, unverified: 2, failed: 3 };
const PAGE = 50;
type View = "all" | "new" | "existing";

/** Companies in the company database (filled by company-scraper) matching the ICP's saved filters; loads automatically. */
function CompanyResults({ icp, canEdit, facets, query, showFilters, onToggleFilters, dirty }: {
  icp: ICP; canEdit: boolean; facets?: CatalogFacets; query: string; showFilters: boolean; onToggleFilters: () => void; dirty: boolean;
}) {
  const qc = useQueryClient();
  const [limit, setLimit] = useState("100");
  const [verifiedOnly, setVerifiedOnly] = useState(false);
  const [sort, setSort] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [tags, setTags] = useState("");
  const [view, setView] = useState<View>("all");
  const [page, setPage] = useState(0);
  const [openId, setOpenId] = useState<string | null>(null);
  const router = useRouter();
  const status = useQuery({ queryKey: ["catalog-status"], queryFn: () => api.get<CatalogStatus>("/catalog/status") });
  const offline = status.data && (!status.data.configured || !status.data.reachable);
  const search = useQuery({
    queryKey: ["catalog-search", icp.id, icp.updated_at, limit, verifiedOnly],
    queryFn: () => api.post<CatalogResult[]>("/catalog/search", { icp_id: icp.id, limit: Number(limit), verified_only: verifiedOnly }),
    enabled: !offline,
    placeholderData: keepPreviousData,
  });
  const [lastData, setLastData] = useState(search.data);
  if (search.data !== lastData) { setLastData(search.data); setPicked(new Set()); setPage(0); }
  const [lastKey, setLastKey] = useState(`${query}|${sort}|${view}`);
  if (lastKey !== `${query}|${sort}|${view}`) { setLastKey(`${query}|${sort}|${view}`); setPage(0); }

  const results = useMemo(() => search.data ?? [], [search.data]);
  const importable = results.filter((r) => !r.duplicate_lead_id);
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    let out = view === "new" ? results.filter((r) => !r.duplicate_lead_id) : view === "existing" ? results.filter((r) => r.duplicate_lead_id) : results;
    if (q) out = out.filter(({ company: c }) => [c.name, c.city, c.state, c.industry, c.domain, c.email ?? "", c.phone].some((x) => x?.toLowerCase().includes(q)));
    const by: Record<string, (a: CatalogResult, b: CatalogResult) => number> = {
      name: (a, b) => a.company.name.localeCompare(b.company.name),
      city: (a, b) => a.company.city.localeCompare(b.company.city) || a.company.name.localeCompare(b.company.name),
      verified: (a, b) => (VERIFY_RANK[a.company.verification_status] ?? 9) - (VERIFY_RANK[b.company.verification_status] ?? 9),
      contact: (a, b) => Number(!!b.company.email) - Number(!!a.company.email) || Number(!!b.company.phone) - Number(!!a.company.phone),
    };
    return by[sort] ? [...out].sort(by[sort]) : out;
  }, [results, view, query, sort]);
  const pages = Math.max(1, Math.ceil(shown.length / PAGE));
  const rows = shown.slice(page * PAGE, page * PAGE + PAGE);
  const pageNew = rows.filter((r) => !r.duplicate_lead_id);
  const allPicked = pageNew.length > 0 && pageNew.every((r) => picked.has(r.company.id));
  const imp = useMutation({
    mutationFn: (ids: string[]) => api.post<{ imported: number; duplicates: number; lead_ids: string[] }>("/catalog/import", { company_ids: ids, tags: list(tags) }),
    onSuccess: (r) => {
      toast.success(`Imported ${r.imported} leads${r.duplicates ? ` (${r.duplicates} already existed)` : ""} — scoring is running`);
      qc.invalidateQueries({ queryKey: ["leads"] });
      qc.invalidateQueries({ queryKey: ["catalog-facets"] });
      qc.invalidateQueries({ queryKey: ["catalog-search"] });
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const importAndOpen = useMutation({
    mutationFn: (id: string) => api.post<{ imported: number; duplicates: number; lead_ids: string[] }>("/catalog/import", { company_ids: [id], tags: list(tags) }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["leads"] });
      qc.invalidateQueries({ queryKey: ["catalog-search"] });
      if (r.lead_ids?.[0]) router.push(`/leads/${r.lead_ids[0]}`);
      else toast.info("This company is already a lead.");
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const openResult = results.find((r) => r.company.id === openId) ?? null;
  const togglePick = (id: string, on: boolean) => setPicked((s) => { const n = new Set(s); if (on) n.add(id); else n.delete(id); return n; });

  return (
    <>
      {/* Toolbar */}
      <div className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2 border-b px-4 py-2 text-sm">
        {canEdit && (
          <label className="flex items-center gap-2 text-muted-foreground">
            <Checkbox checked={allPicked} disabled={pageNew.length === 0}
              onCheckedChange={(on) => setPicked((s) => { const n = new Set(s); pageNew.forEach((r) => (on ? n.add(r.company.id) : n.delete(r.company.id))); return n; })} />
            Select all
          </label>
        )}
        <div className="inline-flex rounded-md bg-muted p-0.5">
          {([["all", "All", results.length], ["new", "New", importable.length], ["existing", "Already leads", results.length - importable.length]] as const).map(([k, label, n]) => (
            <button key={k} type="button" onClick={() => setView(k)}
              className={`rounded px-2.5 py-0.5 transition ${view === k ? "bg-background font-medium shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>
              {label} <span className="tabular-nums text-muted-foreground">{n}</span>
            </button>
          ))}
        </div>
        <span className="flex items-center gap-1.5 text-muted-foreground">Sort by
          <FilterSelect className="h-7 w-36 border-none px-1 font-medium text-foreground shadow-none" value={sort} onChange={setSort} placeholder="Best match" options={SORTS} />
        </span>
        <span className="flex-1" />
        <span className="flex items-center gap-1">
          <Button variant="ghost" size="icon" className="size-7" aria-label="Refresh" onClick={() => search.refetch()} disabled={search.isFetching || !!offline}>
            <RefreshCw className={search.isFetching ? "animate-spin" : ""} />
          </Button>
          <span className="rounded-md border px-2 py-0.5 tabular-nums text-muted-foreground">
            {shown.length ? `${num(page * PAGE + 1)} – ${num(Math.min(shown.length, (page + 1) * PAGE))} of ${num(shown.length)}` : "0 of 0"}
          </span>
          <Button variant="outline" size="icon" className="size-7" aria-label="Previous page" disabled={page === 0} onClick={() => setPage((p) => p - 1)}><ChevronLeft /></Button>
          <Button variant="outline" size="icon" className="size-7" aria-label="Next page" disabled={page >= pages - 1} onClick={() => setPage((p) => p + 1)}><ChevronRight /></Button>
          <Button variant={showFilters ? "secondary" : "outline"} size="icon" className={`size-7 ${showFilters ? "text-primary ring-1 ring-primary/40" : ""}`}
            aria-label={showFilters ? "Hide filters" : "Show filters"} aria-pressed={showFilters} onClick={onToggleFilters}><Filter /></Button>
        </span>
      </div>

      {/* Selection / import bar */}
      {canEdit && (picked.size > 0 || dirty) && (
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-primary/5 px-4 py-1.5 text-sm">
          {dirty && <span className="text-amber-700 dark:text-amber-400">Filters changed — showing results for the saved filters until you Save &amp; apply.</span>}
          <span className="flex-1" />
          {picked.size > 0 && (
            <>
              <span className="font-medium">{picked.size} selected</span>
              <Input className="h-7 w-36 bg-background" placeholder="Tags (optional)" value={tags} onChange={(e) => setTags(e.target.value)} />
              <Button size="sm" className="h-7" disabled={imp.isPending} onClick={() => imp.mutate([...picked])}><Download /> Import {picked.size} as leads</Button>
              <Button size="sm" variant="ghost" className="h-7" onClick={() => setPicked(new Set())}>Clear</Button>
            </>
          )}
        </div>
      )}

      {/* Table */}
      <div className={`min-h-0 flex-1 overflow-auto transition-opacity [&_[data-slot=table-container]]:overflow-visible ${search.isFetching ? "opacity-60" : ""}`}>
        {offline ? (
          <p className="m-4 rounded-md border border-dashed p-3 text-sm text-muted-foreground">
            {status.data!.configured ? "The company database is unreachable. Is company-scraper running?" : "The company database is not connected. Set SCRAPER_DATABASE_URL on the server."}
          </p>
        ) : search.isError ? (
          <p className="m-4 rounded-md border border-dashed p-3 text-sm text-destructive">{errorMessage(search.error)}</p>
        ) : search.isPending ? (
          <div className="space-y-2 p-4">{Array.from({ length: 8 }, (_, i) => <Skeleton key={i} className="h-10 w-full" />)}</div>
        ) : (
          <Table className="min-w-[720px] table-fixed text-[13px]">
            <colgroup><col className="w-10" /><col /><col className="w-36" /><col className="w-44" /><col className="w-32" /><col className="w-24" /></colgroup>
            <TableHeader className="sticky top-0 z-10 bg-muted shadow-[0_1px_0_var(--border)]">
              <TableRow>
                <TableHead />
                <TableHead>Company</TableHead>
                <TableHead>Location</TableHead>
                <TableHead>Contact</TableHead>
                <TableHead>Website</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map(({ company: c, duplicate_lead_id: dup }) => (
                <TableRow key={c.id} onClick={() => setOpenId(c.id)}
                  className={`group h-12 cursor-pointer ${picked.has(c.id) ? "bg-primary/5" : ""} ${openId === c.id ? "bg-muted" : ""}`}>
                  <TableCell onClick={(e) => e.stopPropagation()}>{!dup && canEdit && <Checkbox checked={picked.has(c.id)} onCheckedChange={(on) => togglePick(c.id, !!on)} />}</TableCell>
                  <TableCell title={c.name}>
                    {dup ? (
                      <Link href={`/leads/${dup}`} onClick={(e) => e.stopPropagation()} className="block truncate font-medium text-primary hover:underline">{c.name}</Link>
                    ) : (
                      <p className="truncate font-medium group-hover:text-primary group-hover:underline">{c.name}</p>
                    )}
                    <p className="truncate text-xs capitalize text-muted-foreground">{c.industry || "—"}</p>
                  </TableCell>
                  <TableCell title={[c.city, c.state].filter(Boolean).join(", ")}>
                    <p className="truncate">{c.country && <span className="mr-1.5">{flag(countryCode(c.country, facets))}</span>}{c.city || "—"}</p>
                    <p className="truncate text-xs text-muted-foreground">{c.state || " "}</p>
                  </TableCell>
                  <TableCell>
                    <p className="flex items-center gap-1.5 truncate tabular-nums"><Phone className="size-3 shrink-0 text-muted-foreground" />{c.phone || <span className="text-muted-foreground">—</span>}</p>
                    <p className="flex items-center gap-1.5 truncate text-xs" title={c.email ?? ""}><Mail className="size-3 shrink-0 text-muted-foreground" />{c.email || <span className="text-muted-foreground">No email</span>}</p>
                  </TableCell>
                  <TableCell className="truncate">
                    {c.website ? (
                      <a href={c.website} target="_blank" rel="noreferrer noopener" onClick={(e) => e.stopPropagation()} className="inline-flex max-w-full items-center gap-1 hover:underline" title={c.website}>
                        {QUALITY_TONE[c.website_quality]
                          ? <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium capitalize ${QUALITY_TONE[c.website_quality]}`}>{c.website_quality}</span>
                          : <span className="truncate">{c.domain || "Website"}</span>}
                        <ExternalLink className="size-3 shrink-0 text-muted-foreground" />
                      </a>
                    ) : <span className="text-muted-foreground">None</span>}
                  </TableCell>
                  <TableCell>
                    {dup ? (
                      <Link href={`/leads/${dup}`} onClick={(e) => e.stopPropagation()}><Badge variant="outline" className="border-sky-300 bg-sky-50 text-sky-800 hover:bg-sky-100 dark:border-sky-500/40 dark:bg-sky-500/10 dark:text-sky-200">Lead</Badge></Link>
                    ) : (
                      <Badge variant={VERIFICATION_TONE[c.verification_status] ?? "outline"} className="capitalize">{c.verification_status}</Badge>
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {rows.length === 0 && (
                <TableRow><TableCell colSpan={6} className="py-16 text-center text-sm text-muted-foreground">
                  {results.length === 0 ? "No companies match these filters. Loosen them on the right, or collect more with company-scraper." : "Nothing matches your search in this view."}
                </TableCell></TableRow>
              )}
            </TableBody>
          </Table>
        )}
      </div>

      {/* Footer */}
      <div className="flex shrink-0 flex-wrap items-center gap-3 border-t px-4 py-2 text-xs text-muted-foreground">
        <span>{num(results.length)} found · {num(importable.length)} new · {num(results.filter((r) => r.company.email).length)} with email · {num(results.filter((r) => r.company.verification_status === "verified").length)} verified</span>
        <FilterSelect className="h-7 w-28 text-xs" value={limit} onChange={(x) => setLimit(x || "100")} placeholder="Up to 100" options={LIMITS} />
        <label className="flex items-center gap-1.5"><Switch checked={verifiedOnly} onCheckedChange={setVerifiedOnly} /> Verified only</label>
        <span className="flex-1" />
        {results.some((r) => r.company.source === "openstreetmap") && (
          <a className="underline" title="Business data © OpenStreetMap contributors, available under the ODbL" href="https://www.openstreetmap.org/copyright" target="_blank" rel="noreferrer noopener">© OpenStreetMap</a>
        )}
        {canEdit && importable.length > 0 && (
          <Button size="sm" variant="outline" className="h-7" disabled={imp.isPending} onClick={() => imp.mutate(importable.map((r) => r.company.id))}><Download /> Import all {importable.length} new</Button>
        )}
      </div>

      <CompanySheet result={openResult} facets={facets} canEdit={canEdit} onClose={() => setOpenId(null)}
        importing={importAndOpen.isPending} onImport={(id) => importAndOpen.mutate(id)} />
    </>
  );
}

function Detail({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[110px_minmax(0,1fr)] gap-3 py-1.5 text-sm">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

const shortUrl = (u: string) => { try { const x = new URL(u); return x.hostname.replace(/^www\./, "") + x.pathname.replace(/\/$/, ""); } catch { return u; } };
const domainOf = (u: string) => { try { return new URL(u).hostname.replace(/^www\./, ""); } catch { return u; } };

/** Side panel with everything the company database knows about one company. */
function CompanySheet({ result, facets, canEdit, onClose, onImport, importing }: {
  result: CatalogResult | null; facets?: CatalogFacets; canEdit: boolean; onClose: () => void; onImport: (id: string) => void; importing: boolean;
}) {
  const c = result?.company;
  const dup = result?.duplicate_lead_id;
  const report = c?.website_report;
  const checks = Object.entries(c?.verification?.checks ?? {});
  return (
    <Sheet open={!!result} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="flex w-full flex-col gap-0 p-0 sm:max-w-lg">
        {c && (
          <>
            <SheetHeader className="border-b p-5">
              <div className="flex flex-wrap items-center gap-2 pr-8">
                <Badge variant={VERIFICATION_TONE[c.verification_status] ?? "outline"} className="capitalize">{c.verification_status}</Badge>
                {dup && <Badge variant="outline" className="border-sky-300 bg-sky-50 text-sky-800 dark:border-sky-500/40 dark:bg-sky-500/10 dark:text-sky-200">Already a lead</Badge>}
                <span className="text-xs capitalize text-muted-foreground">{c.source.replace("_", " ")}</span>
              </div>
              <SheetTitle className="text-lg leading-snug">{c.name}</SheetTitle>
              <SheetDescription className="capitalize">{[c.industry, [c.city, c.state].filter(Boolean).join(", ")].filter(Boolean).join(" · ")}</SheetDescription>
            </SheetHeader>

            <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-5">
              {(c.description || report?.description) && <p className="text-sm text-muted-foreground">{c.description || report?.description}</p>}

              <section>
                <h4 className="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Contact</h4>
                <dl className="divide-y">
                  <Detail label="Phone">{c.phone ? <a className="tabular-nums hover:underline" href={`tel:${c.phone}`}>{c.phone}</a> : "—"}{c.phone_valid === false && <span className="ml-2 text-xs text-destructive">invalid</span>}</Detail>
                  <Detail label="Email">{c.email ? <a className="hover:underline" href={`mailto:${c.email}`}>{c.email}</a> : "—"}{c.email && c.email_status && <span className="ml-2 text-xs capitalize text-muted-foreground">{c.email_status}</span>}</Detail>
                  <Detail label="Address">
                    {c.address || [c.city, c.state].filter(Boolean).join(", ") || "—"}
                    {c.country && <span className="ml-1.5">{flag(countryCode(c.country, facets))}</span>}
                  </Detail>
                  {c.source_url && <Detail label="Listing"><a className="inline-flex items-center gap-1 hover:underline" href={c.source_url} target="_blank" rel="noreferrer noopener"><MapPin className="size-3.5" /> View on {c.source === "openstreetmap" ? "OpenStreetMap" : c.source === "google_places" ? "Google Maps" : "source"}</a></Detail>}
                </dl>
              </section>

              <section>
                <h4 className="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Website</h4>
                <dl className="divide-y">
                  <Detail label="Website">
                    {c.website ? <a className="inline-flex items-center gap-1 hover:underline" href={c.website} target="_blank" rel="noreferrer noopener">{c.domain || domainOf(c.website)} <ExternalLink className="size-3" /></a> : "No website"}
                  </Detail>
                  {c.website && <Detail label="Quality"><span className={`rounded px-1.5 py-0.5 text-xs font-medium capitalize ${QUALITY_TONE[c.website_quality] ?? "bg-muted"}`}>{c.website_quality || "unknown"}</span>
                    {report && <span className="ml-2 text-xs text-muted-foreground">{[report.https ? "HTTPS" : report.https === false ? "No HTTPS" : "", report.mobile_friendly ? "mobile-friendly" : report.mobile_friendly === false ? "not mobile-friendly" : "", report.load_ms ? `${report.load_ms} ms` : ""].filter(Boolean).join(" · ")}</span>}
                  </Detail>}
                  {!!c.tech_stack?.length && <Detail label="Technology"><span className="flex flex-wrap gap-1">{c.tech_stack.map((t) => <span key={t} className="rounded bg-muted px-1.5 py-0.5 text-xs">{t}</span>)}</span></Detail>}
                  {!!report?.issues?.length && <Detail label="Issues"><ul className="list-disc space-y-0.5 pl-4 text-xs">{report.issues.map((i) => <li key={i}>{i}</li>)}</ul></Detail>}
                  {report?.copyright_year ? <Detail label="Last updated">© {report.copyright_year}</Detail> : null}
                  {(c.linkedin_url || !!c.social_urls?.length) && (
                    <Detail label="Social">
                      <span className="flex flex-col gap-0.5">
                        {[...new Set([c.linkedin_url, ...(c.social_urls ?? [])].filter(Boolean) as string[])].map((u) => (
                          <a key={u} className="truncate text-xs hover:underline" href={u} target="_blank" rel="noreferrer noopener">{shortUrl(u)}</a>
                        ))}
                      </span>
                    </Detail>
                  )}
                </dl>
              </section>

              {checks.length > 0 && (
                <section>
                  <h4 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Verification</h4>
                  <ul className="space-y-1.5 text-sm">
                    {checks.map(([k, v]) => (
                      <li key={k} className="flex items-center gap-2">
                        {v.passed ? <CircleCheck className="size-4 text-emerald-600" /> : <CircleX className="size-4 text-destructive" />}
                        <span className="capitalize">{k}</span>
                        <span className="text-xs text-muted-foreground">{v.status ?? v.detail ?? ""}</span>
                      </li>
                    ))}
                  </ul>
                  {c.verified_at && <p className="mt-2 text-xs text-muted-foreground">Checked {new Date(c.verified_at).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })}</p>}
                </section>
              )}
              {c.attribution && <p className="text-xs text-muted-foreground">{c.attribution}</p>}
            </div>

            <SheetFooter className="flex-row justify-end gap-2 border-t p-4">
              <Button variant="ghost" onClick={onClose}>Close</Button>
              {dup ? (
                <Button asChild><Link href={`/leads/${dup}`}><ArrowUpRight /> Open lead</Link></Button>
              ) : canEdit && (
                <Button onClick={() => onImport(c.id)} disabled={importing}>{importing ? <Loader2 className="animate-spin" /> : <Download />} Import &amp; open lead</Button>
              )}
            </SheetFooter>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
