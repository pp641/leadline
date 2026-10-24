"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, MoreHorizontal, Plus, Search, Send, Tag, Trash2, Users, X } from "lucide-react";
import { toast } from "sonner";
import { ImportMenu } from "@/components/lead/import-sources";
import { Pagination } from "@/components/pagination";
import { FilterSelect } from "@/components/filter-select";
import { LeadFormDialog } from "@/components/lead-form-dialog";
import { EmptyState, PageHeader, ScoreBadge, StatusBadge, num } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent,
  DropdownMenuSubTrigger, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage } from "@/lib/api";
import { useAuth, useCan } from "@/lib/auth";
import { campaignsService } from "@/services/campaigns";
import { emptyFilters, leadsService, toFilterBody, type LeadFilterState } from "@/services/leads";
import { LEAD_STATUSES } from "@/types/api";

const EMAIL_STATUSES = ["valid", "role_based", "risky", "unknown", "unverified", "invalid", "disposable"];

export default function LeadsPage() {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const { user } = useAuth();
  const [filters, setFilters] = useState<LeadFilterState>(emptyFilters);
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [perPage, setPerPageState] = useState(() => {
    try { const n = Number(localStorage.getItem("leads.perPage")); return [25, 50, 100, 200].includes(n) ? n : 50; } catch { return 50; }
  });
  const setPerPage = (n: number) => {
    setPerPageState(n);
    setPage(1);
    try { localStorage.setItem("leads.perPage", String(n)); } catch { /* storage unavailable */ }
  };
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [allMatching, setAllMatching] = useState(false);
  const [dialog, setDialog] = useState<null | "create" | "campaign" | "tag">(null);

  // Any filter change resets paging and selection.
  const applyFilters = (next: (f: LeadFilterState) => LeadFilterState) => {
    setFilters(next);
    setPage(1);
    setSelected(new Set());
    setAllMatching(false);
  };
  // Debounce the search box.
  useEffect(() => {
    const t = setTimeout(() => {
      if (filters.q !== search) applyFilters((f) => ({ ...f, q: search }));
    }, 300);
    return () => clearTimeout(t);
  }, [search, filters.q]);

  const list = useQuery({
    queryKey: ["leads", filters, page, perPage],
    queryFn: () => leadsService.list(filters, page, perPage),
    placeholderData: keepPreviousData,
  });
  const industries = useQuery({ queryKey: ["facets", "industry"], queryFn: () => leadsService.facets("industry") });
  const cities = useQuery({ queryKey: ["facets", "city"], queryFn: () => leadsService.facets("city") });

  const leads = list.data?.leads ?? [];
  const total = list.data?.meta.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / perPage));
  const pageAllSelected = leads.length > 0 && leads.every((l) => selected.has(l.id));
  const selectionCount = allMatching ? total : selected.size;
  const target = () => (allMatching ? { filter: toFilterBody(filters) } : { ids: [...selected] });
  const set = (patch: Partial<LeadFilterState>) => applyFilters((f) => ({ ...f, ...patch }));
  const filtered = JSON.stringify({ ...filters, q: "" }) !== JSON.stringify({ ...emptyFilters, q: "" }) || !!filters.q;

  const bulk = useMutation({
    mutationFn: (body: { action: string; tags?: string[]; status?: string }) => leadsService.bulk({ ...body, ...target() }),
    onSuccess: (r) => {
      toast.success(`Updated ${num(r.affected)} leads`);
      setSelected(new Set());
      setAllMatching(false);
      qc.invalidateQueries({ queryKey: ["leads"] });
      qc.invalidateQueries({ queryKey: ["facets"] });
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  // Actions served by other endpoints (CRM, CRM sync).
  const extra = useMutation({
    mutationFn: async (kind: "assign" | "unassign" | "hubspot") => {
      const t = target();
      let ids = t.ids;
      if (!ids) {
        // "All matching": page through every matching lead.
        ids = [];
        for (let page = 1; ; page++) {
          const res = await leadsService.list(filters, page, 200);
          ids.push(...res.leads.map((l) => l.id));
          if (res.leads.length < 200 || ids.length >= res.meta.total) break;
        }
      }
      if (kind === "hubspot") return api.post<{ queued: number }>("/crm/sync", { lead_ids: ids });
      return api.post<{ affected: number }>("/crm/assign", { lead_ids: ids, owner_id: kind === "assign" ? user?.id : null });
    },
    onSuccess: () => {
      toast.success("Done");
      qc.invalidateQueries({ queryKey: ["leads"] });
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  const toggle = (id: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      setAllMatching(false);
      return n;
    });

  return (
    <>
      <PageHeader
        title="Leads"
        description={`${num(total)} ${filtered ? "matching" : "total"} leads`}
        actions={
          canEdit && (
            <>
              <Button variant="outline" onClick={() => leadsService.export(filters, allMatching ? [] : [...selected]).catch((e) => toast.error(errorMessage(e)))}>
                <Download /> Export CSV
              </Button>
              <ImportMenu />
              <Button onClick={() => setDialog("create")}><Plus /> Add lead</Button>
            </>
          )
        }
      />

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="relative w-full sm:w-64">
          <Search className="absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-8" placeholder="Search company, email, name…" value={search} onChange={(e) => setSearch(e.target.value)} />
        </div>
        <FilterSelect value={filters.industry} onChange={(v) => set({ industry: v })} placeholder="All industries"
          options={(industries.data ?? []).map((f) => ({ value: f.value, label: `${f.value} (${f.count})` }))} />
        <FilterSelect value={filters.city} onChange={(v) => set({ city: v })} placeholder="All cities"
          options={(cities.data ?? []).map((f) => ({ value: f.value, label: `${f.value} (${f.count})` }))} />
        <FilterSelect value={filters.status} onChange={(v) => set({ status: v })} placeholder="Any status" className="w-36"
          options={LEAD_STATUSES.map((s) => ({ value: s, label: s }))} />
        <FilterSelect value={filters.emailStatus} onChange={(v) => set({ emailStatus: v })} placeholder="Any email" className="w-36"
          options={EMAIL_STATUSES.map((s) => ({ value: s, label: s.replace("_", " ") }))} />
        <FilterSelect value={filters.website} onChange={(v) => set({ website: v as LeadFilterState["website"] })} placeholder="Website: any" className="w-36"
          options={[{ value: "yes", label: "Has website" }, { value: "no", label: "No website" }]} />
        <Input className="w-28" type="number" min={0} max={100} placeholder="Min score" value={filters.minScore} onChange={(e) => set({ minScore: e.target.value })} />
        <FilterSelect value={filters.websiteQuality} onChange={(v) => set({ websiteQuality: v })} placeholder="Site quality: any" className="w-40"
          options={[{ value: "none", label: "No / dead site" }, { value: "poor", label: "Outdated site" }, { value: "ok", label: "OK site" }, { value: "good", label: "Modern site" }]} />
        <FilterSelect value={filters.owner} onChange={(v) => set({ owner: v })} placeholder="Any owner" className="w-36"
          options={[{ value: user?.id ?? "me", label: "My leads" }, { value: "none", label: "Unassigned" }]} />
        <FilterSelect value={filters.sort} onChange={(v) => set({ sort: v })} placeholder="Newest first" className="w-40"
          options={[{ value: "-score", label: "Highest score" }, { value: "-icp_fit", label: "Best ICP fit" }, { value: "company_name", label: "Company A–Z" }, { value: "created_at", label: "Oldest first" }]} />
        {filtered && (
          <Button variant="ghost" size="sm" onClick={() => { setSearch(""); applyFilters(() => emptyFilters); }}>
            <X /> Clear
          </Button>
        )}
      </div>

      {selectionCount > 0 && canEdit && (
        <div className="mb-3 flex flex-wrap items-center gap-2 rounded-lg border bg-muted/50 px-3 py-2 text-sm">
          <span className="font-medium">{num(selectionCount)} selected</span>
          {pageAllSelected && !allMatching && total > leads.length && (
            <Button variant="link" size="sm" onClick={() => setAllMatching(true)}>Select all {num(total)} matching</Button>
          )}
          <div className="ml-auto flex gap-2">
            <Button size="sm" onClick={() => setDialog("campaign")}><Send /> Add to campaign</Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button size="sm" variant="outline"><MoreHorizontal /> Actions</Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => setDialog("tag")}><Tag className="size-4" /> Add tag</DropdownMenuItem>
                <DropdownMenuSub>
                  <DropdownMenuSubTrigger>Set status</DropdownMenuSubTrigger>
                  <DropdownMenuSubContent>
                    {LEAD_STATUSES.map((s) => (
                      <DropdownMenuItem key={s} className="capitalize" onSelect={() => bulk.mutate({ action: "set_status", status: s })}>{s}</DropdownMenuItem>
                    ))}
                  </DropdownMenuSubContent>
                </DropdownMenuSub>
                <DropdownMenuItem onSelect={() => bulk.mutate({ action: "rescore" })}>Recalculate scores</DropdownMenuItem>
                <DropdownMenuItem onSelect={() => extra.mutate("assign")}>Assign to me</DropdownMenuItem>
                <DropdownMenuItem onSelect={() => extra.mutate("unassign")}>Unassign</DropdownMenuItem>
                <DropdownMenuItem onSelect={() => extra.mutate("hubspot")}>Sync to HubSpot</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onSelect={() => {
                  if (confirm(`Permanently delete ${selectionCount} leads and their history?`)) bulk.mutate({ action: "delete" });
                }}>
                  <Trash2 className="size-4" /> Delete
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <Button size="sm" variant="ghost" onClick={() => { setSelected(new Set()); setAllMatching(false); }}>Clear</Button>
          </div>
        </div>
      )}

      {list.isLoading ? (
        <Skeleton className="h-96" />
      ) : leads.length === 0 ? (
        <EmptyState icon={Users} title={filtered ? "No leads match these filters" : "No leads yet"}>
          {filtered ? "Try removing a filter." : <>Import a CSV or add a lead manually. <Link className="underline" href="/leads/import">Import leads</Link></>}
        </EmptyState>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">
                  <Checkbox aria-label="Select page" checked={pageAllSelected}
                    onCheckedChange={(c) => {
                      setAllMatching(false);
                      setSelected((s) => {
                        const n = new Set(s);
                        leads.forEach((l) => (c ? n.add(l.id) : n.delete(l.id)));
                        return n;
                      });
                    }} />
                </TableHead>
                <TableHead className="w-14">Score</TableHead>
                <TableHead>Company</TableHead>
                <TableHead>Industry</TableHead>
                <TableHead>Location</TableHead>
                <TableHead>Website</TableHead>
                <TableHead>Contact</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Owner</TableHead>
                <TableHead>Campaign</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {leads.map((l) => (
                <TableRow key={l.id} data-state={selected.has(l.id) || allMatching ? "selected" : undefined}>
                  <TableCell>
                    <Checkbox aria-label={`Select ${l.company_name}`} checked={allMatching || selected.has(l.id)} onCheckedChange={() => toggle(l.id)} />
                  </TableCell>
                  <TableCell>
                    <ScoreBadge score={l.lead_score} />
                    {l.icp_fit !== null && <span className="block text-[10px] text-muted-foreground tabular-nums">fit {l.icp_fit}%</span>}
                  </TableCell>
                  <TableCell className="max-w-56">
                    <Link href={`/leads/${l.id}`} className="block truncate font-medium hover:underline">{l.company_name}</Link>
                    {l.tags.length > 0 && <span className="block truncate text-xs text-muted-foreground">{l.tags.join(", ")}</span>}
                  </TableCell>
                  <TableCell className="capitalize">{l.industry || "—"}</TableCell>
                  <TableCell>{[l.city, l.state].filter(Boolean).join(", ") || "—"}</TableCell>
                  <TableCell className="max-w-40 truncate">
                    {l.website ? <a href={l.website} target="_blank" rel="noreferrer noopener" className="text-muted-foreground hover:underline">{l.domain}</a> : <span className="text-muted-foreground">None</span>}
                    {l.website_quality && l.website_quality !== "good" && l.website_quality !== "ok" && l.website && (
                      <span className="block text-xs text-amber-700 dark:text-amber-400">{l.website_quality === "poor" ? "outdated" : "unreachable"}</span>
                    )}
                  </TableCell>
                  <TableCell className="max-w-56">
                    {l.primary_contact ? (
                      <div className="flex flex-col">
                        <span className="truncate">{[l.primary_contact.first_name, l.primary_contact.last_name].filter(Boolean).join(" ") || l.primary_contact.email}</span>
                        {l.primary_contact.email && (
                          <span className="flex items-center gap-1.5 truncate text-xs text-muted-foreground">
                            {l.primary_contact.email} <StatusBadge status={l.primary_contact.email_status} className="px-1 py-0 text-[10px]" />
                          </span>
                        )}
                      </div>
                    ) : "—"}
                  </TableCell>
                  <TableCell><StatusBadge status={l.status} /></TableCell>
                  <TableCell className="max-w-32 truncate text-muted-foreground">{l.owner_name ?? "—"}</TableCell>
                  <TableCell className="max-w-40 truncate text-muted-foreground">{l.latest_campaign ?? "—"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {total > 0 && (
        <Pagination className="mt-3" page={page} pages={pages} total={total} perPage={perPage} onPerPage={setPerPage} noun="leads"
          onPage={(p) => { setPage(p); window.scrollTo({ top: 0, behavior: "smooth" }); }} />
      )}

      <LeadFormDialog open={dialog === "create"} onOpenChange={(o) => setDialog(o ? "create" : null)} />
      <AddToCampaignDialog open={dialog === "campaign"} onClose={() => setDialog(null)} count={selectionCount} target={target} />
      <TagDialog open={dialog === "tag"} onClose={() => setDialog(null)} onSubmit={(tag) => bulk.mutate({ action: "add_tags", tags: [tag] })} />
    </>
  );
}

function TagDialog({ open, onClose, onSubmit }: { open: boolean; onClose: () => void; onSubmit: (tag: string) => void }) {
  const [tag, setTag] = useState("");
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader><DialogTitle>Add tag</DialogTitle></DialogHeader>
        <form onSubmit={(e) => { e.preventDefault(); if (tag.trim()) { onSubmit(tag.trim()); setTag(""); onClose(); } }} className="space-y-4">
          <Input autoFocus placeholder="e.g. delhi-ncr" value={tag} onChange={(e) => setTag(e.target.value)} />
          <DialogFooter><Button type="submit" disabled={!tag.trim()}>Add tag</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function AddToCampaignDialog({ open, onClose, count, target }: {
  open: boolean; onClose: () => void; count: number; target: () => { ids?: string[]; filter?: object };
}) {
  const qc = useQueryClient();
  const campaigns = useQuery({ queryKey: ["campaigns"], queryFn: campaignsService.list, enabled: open });
  const eligible = useMemo(() => (campaigns.data ?? []).filter((c) => !["completed", "cancelled"].includes(c.status)), [campaigns.data]);
  const [campaignId, setCampaignId] = useState("");
  const add = useMutation({
    mutationFn: () => {
      const t = target();
      return campaignsService.addLeads(campaignId, t.ids ? { lead_ids: t.ids } : { filter: t.filter });
    },
    onSuccess: (r) => {
      toast.success(`Added ${num(r.added)} leads${r.skipped ? ` · ${num(r.skipped)} skipped (no email, suppressed or already added)` : ""}`);
      qc.invalidateQueries({ queryKey: ["leads"] });
      qc.invalidateQueries({ queryKey: ["campaign", campaignId] });
      onClose();
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add {num(count)} leads to a campaign</DialogTitle>
          <DialogDescription>Each lead&apos;s best contact email is used. Suppressed addresses are skipped.</DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <Label>Campaign</Label>
          <FilterSelect value={campaignId} onChange={setCampaignId} placeholder="Choose a campaign" className="w-full"
            options={eligible.map((c) => ({ value: c.id, label: `${c.name} (${c.status})` }))} />
          {campaigns.data && eligible.length === 0 && (
            <p className="text-sm text-muted-foreground">No open campaigns. <Link className="underline" href="/campaigns">Create one</Link>.</p>
          )}
        </div>
        <DialogFooter>
          <Button disabled={!campaignId || add.isPending} onClick={() => add.mutate()}>Add leads</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
