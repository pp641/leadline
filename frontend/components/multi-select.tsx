"use client";

import { useMemo, useState } from "react";
import { Check, ChevronsUpDown, Plus, X } from "lucide-react";
import { cn } from "cn";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

export interface MultiOption {
  value: string;
  label: string;
  /** Shown before the label, e.g. a flag. */
  icon?: string;
  /** Shown on the right, e.g. a company count. */
  hint?: string;
  /** Secondary line, e.g. the parent state of a city. */
  group?: string;
}

/** Searchable multi-select dropdown. With `creatable`, typed values not in the list can be added. */
export function MultiSelect({
  value, onChange, options, placeholder = "Any", emptyText = "No matches", creatable = false, disabled, className, max = 200,
}: {
  value: string[];
  onChange: (v: string[]) => void;
  options: MultiOption[];
  placeholder?: string;
  emptyText?: string;
  creatable?: boolean;
  disabled?: boolean;
  className?: string;
  /** Limits how many options render at once; search narrows the rest. */
  max?: number;
}) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const selected = useMemo(() => new Set(value.map((v) => v.toLowerCase())), [value]);
  const byValue = useMemo(() => new Map(options.map((o) => [o.value.toLowerCase(), o])), [options]);
  const term = q.trim().toLowerCase();
  const filtered = useMemo(
    () => (term ? options.filter((o) => o.label.toLowerCase().includes(term) || o.value.toLowerCase().includes(term) || o.group?.toLowerCase().includes(term)) : options),
    [options, term],
  );
  const shown = filtered.slice(0, max);
  const canCreate = creatable && term !== "" && !byValue.has(term) && !selected.has(term);

  const toggle = (v: string) => {
    const k = v.toLowerCase();
    onChange(selected.has(k) ? value.filter((x) => x.toLowerCase() !== k) : [...value, v]);
  };
  const add = () => {
    if (canCreate) toggle(q.trim());
    setQ("");
  };

  return (
    <Popover open={open} onOpenChange={(o) => { setOpen(o); if (!o) setQ(""); }}>
      <PopoverTrigger asChild disabled={disabled}>
        <button
          type="button"
          className={cn(
            "flex min-h-9 w-full items-center gap-1.5 rounded-md border border-input bg-transparent px-2.5 py-1.5 text-left text-sm shadow-xs transition-colors",
            "hover:bg-muted/40 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
            className,
          )}
        >
          <span className="flex flex-1 flex-wrap gap-1">
            {value.length === 0 && <span className="text-muted-foreground">{placeholder}</span>}
            {value.map((v) => {
              const o = byValue.get(v.toLowerCase());
              return (
                <span key={v} className="inline-flex max-w-full items-center gap-1 rounded-md bg-secondary px-1.5 py-0.5 text-xs font-medium text-secondary-foreground">
                  {o?.icon && <span aria-hidden>{o.icon}</span>}
                  <span className="truncate">{o?.label ?? v}</span>
                  {!disabled && (
                    <span
                      role="button"
                      tabIndex={-1}
                      aria-label={`Remove ${o?.label ?? v}`}
                      className="rounded-sm text-muted-foreground hover:text-foreground"
                      onPointerDown={(e) => { e.preventDefault(); e.stopPropagation(); toggle(v); }}
                    >
                      <X className="size-3" />
                    </span>
                  )}
                </span>
              );
            })}
          </span>
          <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-(--radix-popover-trigger-width) min-w-64 gap-0 p-0">
        <div className="border-b p-2">
          <input
            autoFocus
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                const exact = shown.find((o) => o.label.toLowerCase() === term || o.value.toLowerCase() === term);
                if (exact) { toggle(exact.value); setQ(""); }
                else if (canCreate) add();
                else if (shown.length > 0 && term) { toggle(shown[0].value); setQ(""); }
              }
              if (e.key === "Backspace" && q === "" && value.length) onChange(value.slice(0, -1));
            }}
            placeholder={creatable ? "Search or type to add…" : "Search…"}
            className="h-8 w-full rounded-md bg-muted/50 px-2 text-sm outline-none placeholder:text-muted-foreground"
          />
        </div>
        <div className="max-h-72 overflow-y-auto p-1" role="listbox" aria-multiselectable>
          {canCreate && (
            <button type="button" onClick={add} className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted">
              <Plus className="size-4 text-muted-foreground" /> Add “{q.trim()}”
            </button>
          )}
          {shown.map((o) => {
            const on = selected.has(o.value.toLowerCase());
            return (
              <button
                key={o.value}
                type="button"
                role="option"
                aria-selected={on}
                onClick={() => toggle(o.value)}
                className={cn("flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-muted", on && "bg-muted/60")}
              >
                <span className={cn("flex size-4 shrink-0 items-center justify-center rounded-sm border", on ? "border-primary bg-primary text-primary-foreground" : "border-input")}>
                  {on && <Check className="size-3" />}
                </span>
                {o.icon && <span className="text-base leading-none" aria-hidden>{o.icon}</span>}
                <span className="min-w-0 flex-1">
                  <span className="block truncate">{o.label}</span>
                  {o.group && <span className="block truncate text-xs text-muted-foreground">{o.group}</span>}
                </span>
                {o.hint && <span className="shrink-0 text-xs tabular-nums text-muted-foreground">{o.hint}</span>}
              </button>
            );
          })}
          {shown.length === 0 && !canCreate && <p className="px-2 py-6 text-center text-sm text-muted-foreground">{emptyText}</p>}
          {filtered.length > shown.length && <p className="px-2 py-1.5 text-xs text-muted-foreground">{filtered.length - shown.length} more — keep typing to narrow</p>}
        </div>
        {value.length > 0 && (
          <div className="flex items-center justify-between border-t px-3 py-1.5 text-xs text-muted-foreground">
            <span>{value.length} selected</span>
            <button type="button" className="hover:text-foreground" onClick={() => onChange([])}>Clear</button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}

/** Flag emoji for an ISO 3166-1 alpha-2 code ("IN" → 🇮🇳). */
export function flag(code: string): string {
  if (!/^[A-Za-z]{2}$/.test(code)) return "🏳️";
  return String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65));
}
