"use client";

import Link from "next/link";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, Database, ExternalLink, FileSpreadsheet, Loader2, PlugZap, Search, Settings2, Upload, Users } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage } from "@/lib/api";
import type { LeadSource, ProviderField, SourceCandidate } from "@/types/api";

function Logo({ s, size = "size-6 text-[10px]" }: { s: Pick<LeadSource, "name" | "color">; size?: string }) {
  const initials = s.name.replace(/[^A-Za-z0-9 ]/g, "").split(" ").filter(Boolean).slice(0, 2).map((w) => w[0]).join("").toUpperCase();
  return <span className={`flex ${size} shrink-0 items-center justify-center rounded-md font-bold text-white`} style={{ background: s.color }} aria-hidden>{initials}</span>;
}

/** The Leads page "Import" menu: CSV, the company database, and every data provider. */
export function ImportMenu() {
  const sources = useQuery({ queryKey: ["lead-sources"], queryFn: () => api.get<LeadSource[]>("/sources") });
  const [open, setOpen] = useState<LeadSource | null>(null);
  const list = sources.data ?? [];
  const connected = list.filter((s) => s.connected);
  const others = list.filter((s) => !s.connected);
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline"><Upload /> Import <ChevronDown className="opacity-60" /></Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-72">
          <DropdownMenuLabel className="text-xs text-muted-foreground">Upload</DropdownMenuLabel>
          <DropdownMenuItem asChild><Link href="/leads/import"><FileSpreadsheet /> CSV file <span className="ml-auto text-xs text-muted-foreground">up to 50k rows</span></Link></DropdownMenuItem>
          <DropdownMenuItem asChild><Link href="/icp"><Database /> Company database <span className="ml-auto text-xs text-muted-foreground">your scraper</span></Link></DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuLabel className="text-xs text-muted-foreground">Data providers</DropdownMenuLabel>
          {sources.isLoading && <DropdownMenuItem disabled><Loader2 className="animate-spin" /> Loading…</DropdownMenuItem>}
          {[...connected, ...others].map((s) => (
            <DropdownMenuItem key={s.kind} onSelect={() => setOpen(s)}>
              <Logo s={s} /> <span className="flex-1">{s.name}</span>
              {s.connected ? <Badge variant="secondary" className="text-[10px]">Connected</Badge> : <span className="text-xs text-muted-foreground">Connect</span>}
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
          <DropdownMenuItem asChild><Link href="/integrations?connect=clay"><PlugZap /> From Clay (HTTP API)</Link></DropdownMenuItem>
          <DropdownMenuItem asChild><Link href="/integrations"><Settings2 /> Manage integrations</Link></DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {open && <SourceImportDialog key={open.kind} source={open} onClose={() => setOpen(null)} />}
    </>
  );
}

function SearchField({ f, value, onChange }: { f: ProviderField; value: string; onChange: (v: string) => void }) {
  if (f.type === "toggle") {
    return (
      <label className="flex cursor-pointer items-center justify-between gap-3 text-sm">
        {f.label} <Switch checked={value === "true"} onCheckedChange={(x) => onChange(String(x))} />
      </label>
    );
  }
  return (
    <div className="space-y-1.5">
      <Label className="text-xs">{f.label}{f.required && <span className="text-destructive"> *</span>}</Label>
      {f.type === "select" ? (
        <Select value={value || "_"} onValueChange={(v) => onChange(v === "_" ? "" : v)}>
          <SelectTrigger className="w-full bg-background"><SelectValue /></SelectTrigger>
          <SelectContent>{(f.options ?? []).map((o) => <SelectItem key={o.value || "_"} value={o.value || "_"}>{o.label}</SelectItem>)}</SelectContent>
        </Select>
      ) : f.type === "textarea" ? (
        <Textarea rows={4} className="bg-background" value={value} placeholder={f.placeholder} onChange={(e) => onChange(e.target.value)} />
      ) : (
        <Input className="bg-background" value={value} placeholder={f.placeholder} onChange={(e) => onChange(e.target.value)} />
      )}
      {f.type === "list" && <p className="text-[11px] text-muted-foreground">Separate several with commas.</p>}
    </div>
  );
}

const statusTone: Record<string, string> = {
  valid: "text-emerald-600 dark:text-emerald-400", risky: "text-amber-600 dark:text-amber-400", invalid: "text-destructive",
};

/** Search a connected provider and import the companies you pick as leads. */
export function SourceImportDialog({ source: s, onClose }: { source: LeadSource; onClose: () => void }) {
  const qc = useQueryClient();
  const fields = s.search_fields ?? [];
  const [params, setParams] = useState<Record<string, string>>(() => Object.fromEntries(fields.map((f) => [f.key, f.default ?? ""])));
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const [tags, setTags] = useState("");
  // Mode-dependent fields: people-only filters, and Hunter's two search modes.
  const visibleFields = fields.filter((f) => {
    const mode = params.mode;
    if (s.kind === "hunter") return f.key === "mode" || (mode === "discover" ? f.key === "query" : f.key === "domains" || f.key === "limit");
    if (/\(people\)/i.test(f.label) || f.key === "reveal_emails") return mode === "people";
    return true;
  });
  const missing = visibleFields.some((f) => f.required && !params[f.key]?.trim());

  const search = useMutation({
    mutationFn: () => api.post<SourceCandidate[]>(`/sources/${s.kind}/search`, {
      params: Object.fromEntries(visibleFields.map((f) => [f.key, f.type === "toggle" ? params[f.key] === "true" : params[f.key] ?? ""])),
    }),
    onSuccess: (r) => setPicked(new Set(r.map((c, i) => (c.duplicate_lead_id ? -1 : i)).filter((i) => i >= 0))),
    onError: (e) => toast.error(errorMessage(e)),
  });
  const results = search.data ?? [];
  const importable = results.map((c, i) => [c, i] as const).filter(([c]) => !c.duplicate_lead_id);

  const imp = useMutation({
    mutationFn: async () => {
      const chosen = [...picked].sort((a, b) => a - b).map((i) => results[i]);
      const tagList = tags.split(",").map((t) => t.trim()).filter(Boolean);
      let imported = 0, duplicates = 0;
      for (let i = 0; i < chosen.length; i += 100) {
        const r = await api.post<{ imported: number; duplicates: number }>(`/sources/${s.kind}/import`, { candidates: chosen.slice(i, i + 100), tags: tagList });
        imported += r.imported;
        duplicates += r.duplicates;
      }
      return { imported, duplicates };
    },
    onSuccess: (r) => {
      toast.success(`Imported ${r.imported} leads from ${s.name}${r.duplicates ? ` · ${r.duplicates} already existed` : ""} — verifying emails and scoring`);
      qc.invalidateQueries({ queryKey: ["leads"] });
      onClose();
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex h-[88svh] flex-col gap-0 p-0 sm:max-w-6xl">
        <DialogHeader className="flex-row items-center gap-3 space-y-0 border-b px-5 py-4">
          <Logo s={s} size="size-10 text-sm" />
          <div className="min-w-0 flex-1">
            <DialogTitle>Import from {s.name}</DialogTitle>
            <DialogDescription>{s.tagline}. Results are mapped to your lead fields; companies you already have are marked.</DialogDescription>
          </div>
        </DialogHeader>

        {!s.connected ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 p-10 text-center">
            <Logo s={s} size="size-14 text-lg" />
            <p className="font-medium">Connect {s.name} to import from it</p>
            <p className="max-w-md text-sm text-muted-foreground">{s.description}</p>
            <Button asChild><Link href={`/integrations?connect=${s.kind}`}><PlugZap /> Connect {s.name}</Link></Button>
          </div>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col md:flex-row">
            <form className="flex shrink-0 flex-col border-b bg-muted/20 md:w-80 md:border-r md:border-b-0"
              onSubmit={(e) => { e.preventDefault(); if (!missing) search.mutate(); }}>
              <div className="min-h-0 flex-1 space-y-3.5 overflow-y-auto p-4">
                {visibleFields.map((f) => (
                  <SearchField key={f.key} f={f} value={params[f.key] ?? ""} onChange={(v) => setParams((x) => ({ ...x, [f.key]: v }))} />
                ))}
                {(s.notes ?? []).map((n) => <p key={n} className="rounded-md bg-muted p-2.5 text-xs text-muted-foreground">{n}</p>)}
              </div>
              <div className="border-t p-4">
                <Button type="submit" className="w-full" disabled={missing || search.isPending}>
                  {search.isPending ? <Loader2 className="animate-spin" /> : <Search />} Search {s.name}
                </Button>
              </div>
            </form>

            <div className="flex min-h-0 min-w-0 flex-1 flex-col">
              {!search.data && !search.isPending && (
                <div className="flex flex-1 flex-col items-center justify-center gap-2 p-10 text-center text-sm text-muted-foreground">
                  <Search className="size-6" /> Set your filters and search. Nothing is imported until you choose.
                </div>
              )}
              {search.isPending && <div className="flex flex-1 items-center justify-center gap-2 text-sm text-muted-foreground"><Loader2 className="animate-spin" /> Searching {s.name}…</div>}
              {search.isSuccess && (
                <>
                  <div className="flex shrink-0 flex-wrap items-center gap-3 border-b px-4 py-2.5 text-sm">
                    <label className="flex items-center gap-2">
                      <Checkbox checked={importable.length > 0 && importable.every(([, i]) => picked.has(i))} disabled={importable.length === 0}
                        onCheckedChange={(on) => setPicked(on ? new Set(importable.map(([, i]) => i)) : new Set())} />
                      Select all new
                    </label>
                    <span className="text-muted-foreground">{results.length} found · {importable.length} new · {results.reduce((n, c) => n + (c.contacts?.length ?? 0), 0)} people</span>
                  </div>
                  <div className="min-h-0 flex-1 overflow-auto [&_[data-slot=table-container]]:overflow-visible">
                    <Table className="min-w-[760px] table-fixed text-[13px]">
                      <colgroup><col className="w-10" /><col /><col className="w-40" /><col className="w-56" /><col className="w-28" /></colgroup>
                      <TableHeader className="sticky top-0 z-10 bg-muted shadow-[0_1px_0_var(--border)]">
                        <TableRow><TableHead /><TableHead>Company</TableHead><TableHead>Location</TableHead><TableHead>People & contact</TableHead><TableHead>Status</TableHead></TableRow>
                      </TableHeader>
                      <TableBody>
                        {results.map((c, i) => {
                          const people = c.contacts ?? [];
                          return (
                            <TableRow key={`${c.external_id}-${i}`} className={`align-top ${picked.has(i) ? "bg-primary/5" : ""}`}>
                              <TableCell className="pt-3">{!c.duplicate_lead_id && (
                                <Checkbox checked={picked.has(i)} onCheckedChange={(on) => setPicked((x) => { const n = new Set(x); if (on) n.add(i); else n.delete(i); return n; })} />
                              )}</TableCell>
                              <TableCell className="whitespace-normal">
                                <p className="font-medium leading-snug">{c.company_name}</p>
                                <p className="mt-0.5 flex flex-wrap gap-x-2 text-xs text-muted-foreground">
                                  {c.industry && <span className="capitalize">{c.industry}</span>}
                                  {(c.domain || c.website) && (
                                    <a className="inline-flex items-center gap-0.5 hover:underline" href={c.website || `https://${c.domain}`} target="_blank" rel="noreferrer noopener">
                                      {c.domain || c.website.replace(/^https?:\/\/(www\.)?/, "").replace(/\/$/, "")} <ExternalLink className="size-3" />
                                    </a>
                                  )}
                                  {c.employee_count ? <span>{c.employee_count.toLocaleString()} employees</span> : null}
                                  {c.extra?.last_funding_type ? <span className="capitalize">{String(c.extra.last_funding_type)}</span> : null}
                                  {c.extra?.rating ? <span>★ {String(c.extra.rating)}</span> : null}
                                </p>
                              </TableCell>
                              <TableCell className="whitespace-normal text-xs">
                                <p>{[c.city, c.state].filter(Boolean).join(", ") || "—"}</p>
                                <p className="text-muted-foreground">{c.country}</p>
                              </TableCell>
                              <TableCell className="whitespace-normal text-xs">
                                {people.slice(0, 2).map((p, j) => (
                                  <p key={j} className="truncate">
                                    {[p.first_name, p.last_name].filter(Boolean).join(" ") || "Generic"}
                                    {p.title && <span className="text-muted-foreground"> · {p.title}</span>}
                                    {p.email && <span className={`block truncate ${statusTone[p.email_status] ?? "text-muted-foreground"}`}>{p.email}</span>}
                                  </p>
                                ))}
                                {people.length > 2 && <p className="flex items-center gap-1 text-muted-foreground"><Users className="size-3" /> +{people.length - 2} more</p>}
                                {people.length === 0 && <p className="text-muted-foreground">{c.email || c.phone || "No contacts"}</p>}
                              </TableCell>
                              <TableCell>
                                {c.duplicate_lead_id
                                  ? <Link href={`/leads/${c.duplicate_lead_id}`} className="text-xs text-muted-foreground underline">Already a lead</Link>
                                  : <Badge variant="outline" className="border-emerald-300 text-emerald-700 dark:text-emerald-400">New</Badge>}
                              </TableCell>
                            </TableRow>
                          );
                        })}
                        {results.length === 0 && (
                          <TableRow><TableCell colSpan={5} className="py-16 text-center text-sm text-muted-foreground">No results. Try broader filters.</TableCell></TableRow>
                        )}
                      </TableBody>
                    </Table>
                  </div>
                  <div className="flex shrink-0 flex-wrap items-center gap-2 border-t px-4 py-3">
                    <Input className="h-9 w-56" placeholder="Tags (optional, comma-separated)" value={tags} onChange={(e) => setTags(e.target.value)} />
                    <span className="flex-1 text-xs text-muted-foreground">Leads are tagged “{s.kind}”, emails are verified and every lead is scored.</span>
                    <Button variant="ghost" onClick={onClose}>Cancel</Button>
                    <Button disabled={picked.size === 0 || imp.isPending} onClick={() => imp.mutate()}>
                      {imp.isPending ? <Loader2 className="animate-spin" /> : <Upload />} Import {picked.size} {picked.size === 1 ? "company" : "companies"}
                    </Button>
                  </div>
                </>
              )}
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
