"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { ScoringConfig, ScoringRule, ScoringSuggestion } from "@/types/api";

const FIELDS: Record<string, "text" | "number" | "bool"> = {
  website: "text", website_quality: "text", tech_stack: "text", icp_fit: "number", industry: "text", city: "text", state: "text", country: "text", description: "text",
  source: "text", email_status: "text", tags: "text", employee_count: "number", founded_year: "number",
  social_count: "number", has_email: "bool", has_phone: "bool",
};
const OPS = {
  text: ["is_empty", "not_empty", "eq", "in", "contains"],
  number: ["is_empty", "not_empty", "gte", "lte", "between", "within_years"],
  bool: ["is_true", "is_false"],
};
const OP_LABEL: Record<string, string> = {
  is_empty: "is empty", not_empty: "is not empty", eq: "equals", in: "is one of", contains: "contains",
  gte: "≥", lte: "≤", between: "between", within_years: "within last N years", is_true: "is true", is_false: "is false",
};

export function ScoringTab() {
  const cfg = useQuery({ queryKey: ["scoring"], queryFn: () => api.get<ScoringConfig>("/scoring") });
  return cfg.data ? <ScoringEditor key={cfg.data.version} config={cfg.data} /> : null;
}

function ScoringEditor({ config }: { config: ScoringConfig }) {
  const qc = useQueryClient();
  const canEdit = useCan("admin");
  const cfg = { data: config };
  const [rules, setRules] = useState<ScoringRule[]>(config.rules);

  const save = useMutation({
    mutationFn: () => api.put<ScoringConfig>("/scoring", { rules }),
    onSuccess: (c) => { toast.success(`Saved as version ${c.version}; leads are being rescored`); qc.setQueryData(["scoring"], c); qc.invalidateQueries({ queryKey: ["leads"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const set = (i: number, patch: Partial<ScoringRule>) => setRules((r) => r.map((x, j) => (j === i ? { ...x, ...patch } : x)));

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Lead scoring rules {cfg.data && <span className="font-normal text-muted-foreground">· version {cfg.data.version}</span>}</CardTitle>
        <CardDescription>Each matching rule adds (or subtracts) points and a reason shown on the lead. Scores are clamped to 0–100.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-2">
        {rules.map((r, i) => {
          const kind = FIELDS[r.field] ?? "text";
          return (
            <fieldset key={r.id ?? i} disabled={!canEdit} className="flex flex-wrap items-center gap-2 rounded-lg border p-2">
              <Switch checked={r.enabled} onCheckedChange={(c) => set(i, { enabled: c })} aria-label="Enabled" />
              <FilterSelect className="w-36" value={r.field} placeholder="Field" options={Object.keys(FIELDS).map((f) => ({ value: f, label: f.replace("_", " ") }))}
                onChange={(f) => f && set(i, { field: f, operator: OPS[FIELDS[f]][0], values: [], min: null, max: null })} />
              <FilterSelect className="w-40" value={r.operator} placeholder="Operator" options={OPS[kind].map((o) => ({ value: o, label: OP_LABEL[o] }))}
                onChange={(o) => o && set(i, { operator: o })} />
              {["eq", "in", "contains"].includes(r.operator) && (
                <Input className="w-44" placeholder="values, comma-separated" value={(r.values ?? []).join(", ")}
                  onChange={(e) => set(i, { values: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) })} />
              )}
              {["gte", "between", "within_years"].includes(r.operator) && (
                <Input className="w-20" type="number" placeholder={r.operator === "within_years" ? "years" : "min"} value={r.min ?? ""}
                  onChange={(e) => set(i, { min: e.target.value === "" ? null : Number(e.target.value) })} />
              )}
              {["lte", "between"].includes(r.operator) && (
                <Input className="w-20" type="number" placeholder="max" value={r.max ?? ""} onChange={(e) => set(i, { max: e.target.value === "" ? null : Number(e.target.value) })} />
              )}
              <span className="text-sm text-muted-foreground">→</span>
              <Input className="w-20" type="number" value={r.points} onChange={(e) => set(i, { points: Number(e.target.value) })} aria-label="Points" />
              <Input className="min-w-40 flex-1" placeholder="Reason shown on the lead" value={r.reason} onChange={(e) => set(i, { reason: e.target.value })} />
              <Button size="icon-sm" variant="ghost" aria-label="Delete rule" onClick={() => setRules((x) => x.filter((_, j) => j !== i))}><Trash2 /></Button>
            </fieldset>
          );
        })}
        <Suggestions onAdd={(r) => setRules((x) => [...x, r])} canEdit={canEdit} />
        {canEdit && (
          <div className="flex gap-2 pt-2">
            <Button variant="outline" onClick={() => setRules((r) => [...r, { field: "industry", operator: "in", values: [], points: 10, reason: "", enabled: true }])}><Plus /> Add rule</Button>
            <Button onClick={() => save.mutate()} disabled={save.isPending}>Save & rescore</Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function Suggestions({ onAdd, canEdit }: { onAdd: (r: ScoringRule) => void; canEdit: boolean }) {
  const q = useQuery({ queryKey: ["scoring-suggestions"], queryFn: () => api.get<ScoringSuggestion[]>("/scoring/suggestions") });
  return (
    <div className="mt-4 rounded-lg border border-dashed p-3">
      <p className="text-sm font-medium">Suggested from your results</p>
      <p className="text-xs text-muted-foreground">Segments whose contacted leads became interested much more (or less) often than average. Needs at least 10 contacted leads per segment.</p>
      {q.data?.length === 0 && <p className="mt-2 text-sm text-muted-foreground">Not enough outcome data yet.</p>}
      <ul className="mt-2 space-y-1 text-sm">
        {(q.data ?? []).map((s) => (
          <li key={s.segment} className="flex flex-wrap items-center gap-2">
            <span className={s.rule.points > 0 ? "text-emerald-700 dark:text-emerald-400" : "text-amber-700 dark:text-amber-400"}>{s.rule.points > 0 ? "+" : ""}{s.rule.points}</span>
            <span>{s.rule.reason}</span>
            <span className="text-xs text-muted-foreground">({s.leads} leads, {(s.positive_rate * 100).toFixed(0)}% vs {(s.baseline_rate * 100).toFixed(0)}% overall)</span>
            {canEdit && <Button size="xs" variant="outline" onClick={() => onAdd(s.rule)}>Add rule</Button>}
          </li>
        ))}
      </ul>
    </div>
  );
}
