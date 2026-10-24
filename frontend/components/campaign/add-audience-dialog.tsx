"use client";

import { useEffect, useMemo, useState } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AtSign, ChevronLeft, ChevronRight, CircleCheck, CircleX, Filter, Info, Loader2, Search, Target, UserCheck, Users } from "lucide-react";
import { toast } from "sonner";
import { MultiSelect } from "@/components/multi-select";
import { num } from "@/components/shared";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage } from "@/lib/api";
import { campaignsService, type AudienceBody, type AudienceResult } from "@/services/campaigns";
import { leadsService } from "@/services/leads";
import type { Campaign, ICP, Lead, LeadFilter, PageMeta } from "@/types/api";

type Mode = "filter" | "icp" | "pick" | "custom";

const STATUSES = ["new", "contacted", "replied", "interested", "qualified", "meeting", "won", "lost"];
const EMAIL_STATUSES = [{ value: "valid", label: "Verified (valid)" }, { value: "role_based", label: "Role-based (info@…)" }, { value: "risky", label: "Risky" },
  { value: "unknown", label: "Unknown" }, { value: "unverified", label: "Not verified yet" }];

function useDebounced<T>(value: T, ms = 350): T {
  const [v, setV] = useState(value);
  useEffect(() => { const t = setTimeout(() => setV(value), ms); return () => clearTimeout(t); }, [value, ms]);
  return v;
}

// ───────────── custom email parsing ─────────────

const PERSONAL_DOMAINS = new Set(["gmail.com", "googlemail.com", "yahoo.com", "yahoo.in", "outlook.com", "hotmail.com", "live.com", "icloud.com", "proton.me", "protonmail.com", "rediffmail.com"]);
const isPersonal = (email: string) => PERSONAL_DOMAINS.has(email.split("@")[1]?.toLowerCase() ?? "");

export interface ParsedContact { email: string; first_name: string; last_name: string; company: string; valid: boolean; raw: string }
const EMAIL_RE = /[^\s<>,;"']+@[^\s<>,;"']+\.[^\s<>,;"']+/;

/** Accepts one contact per line: "email", "Name <email>", "email, Name, Company" or "Name, email, Company". */
export function parseContacts(text: string): ParsedContact[] {
  const out: ParsedContact[] = [];
  const seen = new Set<string>();
  for (const raw of text.split(/\r?\n/).map((l) => l.trim()).filter(Boolean)) {
    // A line that is just a list of addresses yields several contacts.
    const bare = raw.split(/[,;\s]+/).filter(Boolean);
    if (bare.length > 1 && bare.every((x) => EMAIL_RE.test(x) && !x.includes("<"))) {
      for (const e of bare) push(e, "", "", e);
      continue;
    }
    const m = raw.match(EMAIL_RE);
    if (!m) { out.push({ email: raw, first_name: "", last_name: "", company: "", valid: false, raw }); continue; }
    const email = m[0].replace(/[<>]/g, "");
    const rest = raw.replace(m[0], "").replace(/[<>"]/g, "").split(/[,;\t]/).map((x) => x.trim()).filter(Boolean);
    const [name = "", company = ""] = rest;
    const [first = "", ...last] = name.split(/\s+/);
    push(email, first, last.join(" "), raw, company);
  }
  return out;
  function push(email: string, first: string, last: string, raw: string, company = "") {
    const key = email.toLowerCase();
    if (seen.has(key)) return;
    seen.add(key);
    out.push({ email, first_name: first, last_name: last, company, valid: EMAIL_RE.test(email), raw });
  }
}

// ───────────── dialog ─────────────

export function AddAudienceDialog({ campaign, open, onClose }: { campaign: Campaign; open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const [mode, setMode] = useState<Mode>("filter");
  // Filter
  const [f, setF] = useState<LeadFilter>({ email_statuses: ["valid"] });
  const setFilter = (p: Partial<LeadFilter>) => setF((x) => ({ ...x, ...p }));
  // ICP
  const [chosenIcp, setIcpId] = useState("");
  const [minFit, setMinFit] = useState(70);
  const [icpFilter, setIcpFilter] = useState(true);
  // Pick
  const [picked, setPicked] = useState<Map<string, Lead>>(new Map());
  // Custom
  const [text, setText] = useState("");
  const parsed = useMemo(() => parseContacts(text), [text]);

  const facet = (field: string) => ({ queryKey: ["facets", field], queryFn: () => leadsService.facets(field), enabled: open, staleTime: 60_000 });
  const industries = useQuery(facet("industry")), cities = useQuery(facet("city")), tags = useQuery(facet("tag")), sources = useQuery(facet("source"));
  const icps = useQuery({ queryKey: ["icps"], queryFn: () => api.get<ICP[]>("/icps"), enabled: open });
  // Preselect the default profile until the user picks one.
  const icpId = chosenIcp || (icps.data?.find((i) => i.is_default) ?? icps.data?.[0])?.id || "";

  const body: AudienceBody | null = useMemo(() => {
    switch (mode) {
      case "filter": return { filter: { ...f, not_in_campaign_id: campaign.id } };
      case "icp": return icpId ? { icp_id: icpId, min_fit: minFit, ...(icpFilter ? { filter: { has_email: true } } : {}) } : null;
      case "pick": return picked.size ? { lead_ids: [...picked.keys()] } : null;
      case "custom": {
        const valid = parsed.filter((p) => p.valid);
        return valid.length || parsed.length ? { contacts: parsed.map(({ email, first_name, last_name, company }) => ({ email, first_name, last_name, company })) } : null;
      }
    }
  }, [mode, f, campaign.id, icpId, minFit, icpFilter, picked, parsed]);
  const debouncedBody = useDebounced(body);
  const preview = useQuery({
    queryKey: ["audience-preview", campaign.id, debouncedBody],
    queryFn: () => campaignsService.addLeads(campaign.id, { ...debouncedBody!, dry_run: true }),
    enabled: open && !!debouncedBody, placeholderData: keepPreviousData,
  });
  const p = debouncedBody ? preview.data : undefined;

  const add = useMutation({
    mutationFn: () => campaignsService.addLeads(campaign.id, body!),
    onSuccess: (r) => {
      toast.success(`Added ${num(r.added)} ${r.added === 1 ? "person" : "people"} to the campaign${r.created ? ` · ${num(r.created)} new leads created` : ""}`,
        { description: r.skipped ? `${num(r.skipped)} skipped (already in campaign, no usable email or suppressed)` : undefined });
      qc.invalidateQueries({ queryKey: ["campaign", campaign.id] });
      qc.invalidateQueries({ queryKey: ["leads"] });
      setPicked(new Map()); setText("");
      onClose();
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex max-h-[90svh] flex-col gap-0 p-0 sm:max-w-3xl">
        <DialogHeader className="border-b p-5">
          <DialogTitle className="flex items-center gap-2"><Users className="size-5" /> Add people to “{campaign.name}”</DialogTitle>
          <DialogDescription>Choose leads by filter, by ideal customer profile, one by one, or paste email addresses. Each person is emailed from their best verified address.</DialogDescription>
        </DialogHeader>

        <Tabs value={mode} onValueChange={(v) => setMode(v as Mode)} className="flex min-h-0 flex-1 flex-col gap-0">
          <div className="border-b px-5 pt-3">
            <TabsList className="mb-3 grid h-auto w-full grid-cols-2 gap-1 sm:grid-cols-4">
              <TabsTrigger value="filter" className="py-1.5"><Filter /> By filter</TabsTrigger>
              <TabsTrigger value="icp" className="py-1.5"><Target /> From ICP</TabsTrigger>
              <TabsTrigger value="pick" className="py-1.5"><UserCheck /> Pick leads{picked.size > 0 && <Badge className="ml-1 h-4 px-1.5 text-[10px]">{picked.size}</Badge>}</TabsTrigger>
              <TabsTrigger value="custom" className="py-1.5"><AtSign /> Custom emails</TabsTrigger>
            </TabsList>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto p-5">
            <TabsContent value="filter" className="mt-0 grid gap-4 sm:grid-cols-2">
              <Field label="Industries"><MultiSelect value={f.industries ?? []} onChange={(v) => setFilter({ industries: v })} placeholder="Any industry"
                options={(industries.data ?? []).map((x) => ({ value: x.value, label: x.value, hint: num(x.count) }))} /></Field>
              <Field label="Cities"><MultiSelect value={f.cities ?? []} onChange={(v) => setFilter({ cities: v })} placeholder="Any city"
                options={(cities.data ?? []).map((x) => ({ value: x.value, label: x.value, hint: num(x.count) }))} /></Field>
              <Field label="Tags"><MultiSelect value={f.tags ?? []} onChange={(v) => setFilter({ tags: v })} placeholder="Any tag"
                options={(tags.data ?? []).map((x) => ({ value: x.value, label: x.value, hint: num(x.count) }))} /></Field>
              <Field label="Source"><MultiSelect value={f.sources ?? []} onChange={(v) => setFilter({ sources: v })} placeholder="Any source"
                options={(sources.data ?? []).map((x) => ({ value: x.value, label: x.value.replace("_", " "), hint: num(x.count) }))} /></Field>
              <Field label="Pipeline status"><MultiSelect value={f.statuses ?? []} onChange={(v) => setFilter({ statuses: v })} placeholder="Any status"
                options={STATUSES.map((s) => ({ value: s, label: s[0].toUpperCase() + s.slice(1) }))} /></Field>
              <Field label="Email status" hint="Verified addresses protect your sending reputation.">
                <MultiSelect value={f.email_statuses ?? []} onChange={(v) => setFilter({ email_statuses: v })} placeholder="Any email status" options={EMAIL_STATUSES} /></Field>
              <Field label="Minimum lead score"><Input type="number" min={0} max={100} placeholder="0–100" value={f.min_score ?? ""}
                onChange={(e) => setFilter({ min_score: e.target.value === "" ? undefined : Number(e.target.value) })} /></Field>
              <Field label="Website">
                <Select value={f.has_website === undefined ? "any" : f.has_website ? "yes" : "no"} onValueChange={(v) => setFilter({ has_website: v === "any" ? undefined : v === "yes" })}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="any">Any</SelectItem><SelectItem value="yes">Has a website</SelectItem><SelectItem value="no">No website</SelectItem></SelectContent>
                </Select>
              </Field>
            </TabsContent>

            <TabsContent value="icp" className="mt-0 space-y-4">
              {icps.data?.length === 0 && <p className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">No ideal customer profiles yet — create one in ICP first.</p>}
              <div className="grid gap-2 sm:grid-cols-2">
                {(icps.data ?? []).map((i) => {
                  const c = i.criteria;
                  const chips = [...(c.countries ?? []), ...(c.states ?? []), ...(c.cities ?? []), ...(c.industries ?? [])].slice(0, 6);
                  return (
                    <button key={i.id} type="button" onClick={() => setIcpId(i.id)}
                      className={`rounded-xl border p-3 text-left transition hover:border-primary/50 ${icpId === i.id ? "border-primary bg-primary/5 ring-1 ring-primary/30" : ""}`}>
                      <span className="flex items-center gap-2 font-medium"><Target className="size-4 text-primary" /> {i.name}{i.is_default && <Badge variant="secondary">Default</Badge>}</span>
                      <span className="mt-2 flex flex-wrap gap-1">{chips.length ? chips.map((x) => <span key={x} className="rounded bg-muted px-1.5 py-0.5 text-xs capitalize text-muted-foreground">{x}</span>)
                        : <span className="text-xs text-muted-foreground">No criteria — every lead fits</span>}</span>
                    </button>
                  );
                })}
              </div>
              <div className="space-y-2 rounded-xl border p-4">
                <div className="flex items-center justify-between text-sm"><Label>Minimum fit</Label><span className="font-semibold tabular-nums">{minFit}%</span></div>
                <input type="range" min={0} max={100} step={5} value={minFit} onChange={(e) => setMinFit(Number(e.target.value))} className="w-full accent-primary" aria-label="Minimum fit" />
                <p className="text-xs text-muted-foreground">Fit is the share of the profile&apos;s criteria a lead meets. 100% = matches everything; lower it to include near-matches.</p>
                <label className="flex items-center gap-2 pt-1 text-sm"><Checkbox checked={icpFilter} onCheckedChange={(v) => setIcpFilter(v === true)} /> Only leads that have an email address</label>
              </div>
            </TabsContent>

            <TabsContent value="pick" className="mt-0"><PickLeads campaignId={campaign.id} picked={picked} setPicked={setPicked} active={open && mode === "pick"} /></TabsContent>

            <TabsContent value="custom" className="mt-0 space-y-3">
              <Field label="Email addresses" hint="One per line. Also accepts “Name <email>”, “email, Name, Company”, or a pasted list separated by commas.">
                <Textarea rows={7} value={text} onChange={(e) => setText(e.target.value)} className="font-mono text-sm"
                  placeholder={"priya@brightdental.in\nRohan Mehta <rohan@spiceroute.in>\nanita@urbanfit.in, Anita Rao, Urban Fit"} />
              </Field>
              {parsed.length > 0 && (
                <div className="overflow-hidden rounded-lg border">
                  <div className="max-h-52 overflow-y-auto">
                    <table className="w-full text-sm">
                      <thead className="sticky top-0 bg-muted text-left text-xs text-muted-foreground"><tr><th className="px-3 py-1.5" /><th className="px-3 py-1.5">Email</th><th className="px-3 py-1.5">Name</th><th className="px-3 py-1.5">Company</th></tr></thead>
                      <tbody>
                        {parsed.map((c) => (
                          <tr key={c.raw + c.email} className="border-t">
                            <td className="px-3 py-1.5">{c.valid ? <CircleCheck className="size-4 text-emerald-600" /> : <CircleX className="size-4 text-destructive" />}</td>
                            <td className={`px-3 py-1.5 ${c.valid ? "" : "text-destructive line-through"}`}>{c.email}</td>
                            <td className="px-3 py-1.5 text-muted-foreground">{[c.first_name, c.last_name].filter(Boolean).join(" ") || "—"}</td>
                            <td className="px-3 py-1.5 text-muted-foreground">{!c.valid ? "—" : c.company || (isPersonal(c.email) ? "personal address (named after the person)" : "from email domain")}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              )}
              <div className="flex gap-2 rounded-lg bg-muted/60 p-3 text-xs text-muted-foreground">
                <Info className="size-4 shrink-0" />
                <p>Addresses that already belong to a lead are matched to it. New addresses become leads (company from the email domain), tagged{" "}
                  <b className="text-foreground">added-to-campaign</b>, and are verified before sending. Suppressed and unsubscribed addresses are always skipped.</p>
              </div>
            </TabsContent>
          </div>
        </Tabs>

        <div className="flex flex-wrap items-center gap-3 border-t bg-muted/30 px-5 py-3">
          <div className="min-w-0 flex-1 text-sm">
            {!body ? <span className="text-muted-foreground">{mode === "pick" ? "Select leads to add." : mode === "custom" ? "Paste at least one email address." : "Choose a profile."}</span>
              : !p ? <span className="flex items-center gap-2 text-muted-foreground"><Loader2 className="size-4 animate-spin" /> Checking…</span>
                : <AudiencePreview r={p} loading={preview.isFetching} />}
          </div>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button disabled={!body || !p || p.added === 0 || add.isPending} onClick={() => add.mutate()}>
            {add.isPending && <Loader2 className="animate-spin" />} Add {p ? num(p.added) : ""} {p?.added === 1 ? "person" : "people"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function AudiencePreview({ r, loading }: { r: AudienceResult; loading: boolean }) {
  const bits = [
    r.already_in_campaign ? `${num(r.already_in_campaign)} already in campaign` : "",
    r.no_email ? `${num(r.no_email)} without email` : "",
    r.suppressed ? `${num(r.suppressed)} suppressed` : "",
    r.created ? `${num(r.created)} new leads` : "",
    r.invalid_emails?.length ? `${num(r.invalid_emails.length)} invalid` : "",
  ].filter(Boolean);
  return (
    <span className={`flex flex-wrap items-center gap-x-2 gap-y-0.5 transition-opacity ${loading ? "opacity-60" : ""}`}>
      <b className="text-foreground">{num(r.added)} will be added</b>
      {bits.length > 0 && <span className="text-xs text-muted-foreground">· {bits.join(" · ")}</span>}
    </span>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return <div className="space-y-1.5"><Label className="text-xs">{label}</Label>{children}{hint && <p className="text-xs text-muted-foreground">{hint}</p>}</div>;
}

const PAGE = 25;
function PickLeads({ campaignId, picked, setPicked, active }: { campaignId: string; picked: Map<string, Lead>; setPicked: (m: Map<string, Lead>) => void; active: boolean }) {
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const dq = useDebounced(q);
  const res = useQuery({
    queryKey: ["leads", "pick", campaignId, dq, page],
    queryFn: () => api.page<Lead[]>("/leads", { q: dq, has_email: true, not_in_campaign_id: campaignId, page, per_page: PAGE, sort: "-score" }),
    enabled: active, placeholderData: keepPreviousData,
  });
  const rows = res.data?.data ?? [];
  const total = (res.data?.meta as unknown as PageMeta | undefined)?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / PAGE));
  const toggle = (l: Lead, on: boolean) => { const m = new Map(picked); if (on) m.set(l.id, l); else m.delete(l.id); setPicked(m); };
  const allOnPage = rows.length > 0 && rows.every((l) => picked.has(l.id));
  const email = (l: Lead) => l.primary_contact?.email ?? l.business_email ?? "";
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-56 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-8" placeholder="Search company, contact or email" value={q} onChange={(e) => { setQ(e.target.value); setPage(1); }} />
        </div>
        {picked.size > 0 && <Button size="sm" variant="ghost" onClick={() => setPicked(new Map())}>Clear {picked.size} selected</Button>}
      </div>
      <div className="overflow-hidden rounded-lg border">
        <div className="flex items-center gap-3 border-b bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          <Checkbox checked={allOnPage} disabled={!rows.length} aria-label="Select all on this page"
            onCheckedChange={(on) => { const m = new Map(picked); rows.forEach((l) => (on ? m.set(l.id, l) : m.delete(l.id))); setPicked(m); }} />
          <span className="flex-1">{num(total)} leads with an email that aren&apos;t in this campaign</span>
          {res.isFetching && <Loader2 className="size-3.5 animate-spin" />}
        </div>
        <ul className="max-h-80 divide-y overflow-y-auto">
          {rows.map((l) => (
            <li key={l.id}>
              <label className={`flex cursor-pointer items-center gap-3 px-3 py-2 text-sm hover:bg-muted/40 ${picked.has(l.id) ? "bg-primary/5" : ""}`}>
                <Checkbox checked={picked.has(l.id)} onCheckedChange={(on) => toggle(l, on === true)} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{l.company_name}</span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {[l.primary_contact ? `${l.primary_contact.first_name} ${l.primary_contact.last_name}`.trim() : "", email(l)].filter(Boolean).join(" · ")}
                  </span>
                </span>
                <span className="hidden text-xs text-muted-foreground sm:block">{l.city}</span>
                {l.primary_contact?.email_status && <Badge variant="outline" className="capitalize">{l.primary_contact.email_status.replace("_", " ")}</Badge>}
                <span className="w-8 text-right text-xs font-semibold tabular-nums">{l.lead_score}</span>
              </label>
            </li>
          ))}
          {!res.isLoading && rows.length === 0 && <li className="px-3 py-8 text-center text-sm text-muted-foreground">No leads match.</li>}
        </ul>
      </div>
      {pages > 1 && (
        <div className="flex items-center justify-end gap-2 text-sm text-muted-foreground">
          <Button size="icon-sm" variant="outline" aria-label="Previous page" disabled={page <= 1} onClick={() => setPage((x) => x - 1)}><ChevronLeft /></Button>
          <span className="tabular-nums">{page} / {pages}</span>
          <Button size="icon-sm" variant="outline" aria-label="Next page" disabled={page >= pages} onClick={() => setPage((x) => x + 1)}><ChevronRight /></Button>
        </div>
      )}
    </div>
  );
}
