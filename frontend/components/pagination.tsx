"use client";

import { useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { cn } from "cn";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

/** Page numbers to show: a window around the current page plus the first and last, with gaps as "…". */
export function pageItems(page: number, pages: number, window = 5): (number | "gap")[] {
  if (pages <= window + 2) return Array.from({ length: pages }, (_, i) => i + 1);
  let start = Math.max(2, page - Math.floor(window / 2));
  const end = Math.min(pages - 1, start + window - 1);
  start = Math.max(2, end - window + 1);
  const out: (number | "gap")[] = [1];
  if (start > 2) out.push("gap");
  for (let p = start; p <= end; p++) out.push(p);
  if (end < pages - 1) out.push("gap");
  out.push(pages);
  return out;
}

/** 1-based pagination: "Showing 101–150 of 1,558", Previous, 1 … 3 4 [5] 6 7 … 32, Next, "Page 5 of 32" and go-to-page. */
export function Pagination({ page, pages, onPage, total, perPage, onPerPage, perPageOptions = [25, 50, 100, 200], noun = "results", className }: {
  page: number; pages: number; onPage: (p: number) => void; total?: number; perPage?: number;
  /** When set, shows a "Rows per page" selector. */
  onPerPage?: (n: number) => void; perPageOptions?: number[]; noun?: string; className?: string;
}) {
  const [jump, setJump] = useState("");
  const go = (p: number) => onPage(Math.min(pages, Math.max(1, p)));
  const fmt = (n: number) => n.toLocaleString();
  return (
    <nav aria-label="Pagination" className={cn("flex flex-wrap items-center justify-between gap-3 text-sm", className)}>
      <div className="flex flex-wrap items-center gap-3 text-muted-foreground">
        {onPerPage && perPage && (
          <label className="flex items-center gap-2">
            Rows per page
            <Select value={String(perPage)} onValueChange={(v) => onPerPage(Number(v))}>
              <SelectTrigger size="sm" className="h-8 w-20 tabular-nums" aria-label="Rows per page"><SelectValue /></SelectTrigger>
              <SelectContent>{perPageOptions.map((n) => <SelectItem key={n} value={String(n)}>{n}</SelectItem>)}</SelectContent>
            </Select>
          </label>
        )}
        {total != null && perPage
          ? <span>Showing <span className="font-medium text-foreground">{fmt(total === 0 ? 0 : (page - 1) * perPage + 1)}–{fmt(Math.min(total, page * perPage))}</span> of {fmt(total)} {noun}</span>
          : null}
      </div>
      <div className={cn("flex flex-wrap items-center gap-1", pages <= 1 && "invisible")}>
        <Button size="sm" variant="outline" className="h-8" disabled={page <= 1} onClick={() => go(page - 1)}><ChevronLeft /> Previous</Button>
        {pageItems(page, pages).map((p, i) => p === "gap" ? (
          <span key={`gap-${i}`} className="px-1.5 text-muted-foreground" aria-hidden>…</span>
        ) : (
          <Button key={p} size="sm" variant={p === page ? "default" : "ghost"} className="h-8 min-w-8 px-2 tabular-nums"
            aria-label={`Page ${p}`} aria-current={p === page ? "page" : undefined} onClick={() => go(p)}>{p}</Button>
        ))}
        <Button size="sm" variant="outline" className="h-8" disabled={page >= pages} onClick={() => go(page + 1)}>Next <ChevronRight /></Button>
      </div>
      <div className="flex items-center gap-2 text-muted-foreground">
        <span>Page <span className="font-medium text-foreground tabular-nums">{fmt(page)}</span> of <span className="tabular-nums">{fmt(pages)}</span></span>
        {pages > 7 && (
          <form className="flex items-center gap-1" onSubmit={(e) => { e.preventDefault(); const n = parseInt(jump, 10); if (n) go(n); setJump(""); }}>
            <span className="text-muted-foreground">· Go to</span>
            <input value={jump} onChange={(e) => setJump(e.target.value.replace(/\D/g, ""))} inputMode="numeric" aria-label="Go to page"
              placeholder={String(page)} className="h-8 w-14 rounded-md border bg-transparent px-2 text-center tabular-nums outline-none focus-visible:ring-2 focus-visible:ring-ring/50" />
          </form>
        )}
      </div>
    </nav>
  );
}
