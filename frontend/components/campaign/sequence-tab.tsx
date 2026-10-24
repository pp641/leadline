"use client";

import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Eye, Plus, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import { TEMPLATE_VARIABLES, campaignsService } from "@/services/campaigns";
import type { Campaign, Step } from "@/types/api";

const starter: Step[] = [
  { delay_days: 0, subject_template: "Quick idea for {{company_name}}", body_template: "Hi {{first_name|there}},\n\nI noticed {{company_name}} in {{city}} and had an idea that might help.\n\n…\n\nBest,\n{{sender_name}}" },
  { delay_days: 3, subject_template: "", body_template: "Hi {{first_name|there}},\n\nJust following up on my note below — would it be worth a quick chat?\n\n{{sender_name}}" },
];

export function SequenceTab({ campaign }: { campaign: Campaign }) {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const locked = !canEdit || ["running", "completed", "cancelled"].includes(campaign.status);
  const [steps, setSteps] = useState<Step[]>(() => (campaign.steps?.length ? campaign.steps : starter));
  const [dirty, setDirty] = useState(!campaign.steps?.length);
  const [active, setActive] = useState(0);
  const [cursor, setCursor] = useState<{ field: "subject_template" | "body_template"; pos: number }>({ field: "body_template", pos: 0 });

  const update = (i: number, patch: Partial<Step>) => {
    setSteps((s) => s.map((st, j) => (j === i ? { ...st, ...patch } : st)));
    setDirty(true);
  };
  const move = (i: number, by: number) => {
    setSteps((s) => {
      const n = [...s];
      [n[i], n[i + by]] = [n[i + by], n[i]];
      return n;
    });
    setActive(i + by);
    setDirty(true);
  };
  const insertVar = (v: string) => {
    const st = steps[active];
    const text = st[cursor.field];
    const pos = Math.min(cursor.pos, text.length);
    update(active, { [cursor.field]: `${text.slice(0, pos)}{{${v}}}${text.slice(pos)}` });
  };

  const save = useMutation({
    mutationFn: () => campaignsService.saveSteps(campaign.id, steps.map(({ delay_days, subject_template, body_template }) => ({ delay_days, subject_template, body_template }))),
    onSuccess: () => {
      toast.success("Sequence saved");
      setDirty(false);
      qc.invalidateQueries({ queryKey: ["campaign", campaign.id] });
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  // "Day N" labels: cumulative delays from the first email.
  const days = steps.reduce<number[]>((acc, s, i) => [...acc, i === 0 ? 0 : acc[i - 1] + s.delay_days], []);
  return (
    <div className="grid gap-4 lg:grid-cols-5">
      <div className="space-y-3 lg:col-span-3">
        {campaign.status === "running" && (
          <Alert><AlertDescription>Pause the campaign to edit its sequence. Emails already sent keep the content they were sent with.</AlertDescription></Alert>
        )}
        {steps.map((s, i) => {
          const day = days[i];
          return (
            <Card key={i} className={i === active ? "ring-2 ring-ring/40" : ""} onClick={() => setActive(i)}>
              <CardHeader className="flex flex-row items-center gap-2">
                <CardTitle className="text-sm">Email {i + 1}</CardTitle>
                <Badge variant="secondary" className="font-normal">Day {day}</Badge>
                {!locked && (
                  <div className="ml-auto flex gap-1">
                    <Button size="icon-xs" variant="ghost" aria-label="Move up" disabled={i === 0} onClick={() => move(i, -1)}><ArrowUp /></Button>
                    <Button size="icon-xs" variant="ghost" aria-label="Move down" disabled={i === steps.length - 1} onClick={() => move(i, 1)}><ArrowDown /></Button>
                    <Button size="icon-xs" variant="ghost" aria-label="Remove step" disabled={steps.length === 1}
                      onClick={() => { setSteps((st) => st.filter((_, j) => j !== i)); setActive(0); setDirty(true); }}><Trash2 /></Button>
                  </div>
                )}
              </CardHeader>
              <CardContent className="space-y-3">
                {i > 0 && (
                  <div className="flex items-center gap-2 text-sm">
                    <span>Wait</span>
                    <Input type="number" min={0} max={90} className="w-20" disabled={locked} value={s.delay_days}
                      onChange={(e) => update(i, { delay_days: Math.max(0, Number(e.target.value)) })} />
                    <span>days after the previous email, if no reply</span>
                  </div>
                )}
                <div className="space-y-1">
                  <Label>Subject</Label>
                  <Input disabled={locked} value={s.subject_template} placeholder={i > 0 ? "Leave empty to reply in the same thread" : ""}
                    onChange={(e) => update(i, { subject_template: e.target.value })}
                    onSelect={(e) => setCursor({ field: "subject_template", pos: e.currentTarget.selectionStart ?? 0 })} />
                </div>
                <div className="space-y-1">
                  <Label>Body</Label>
                  <Textarea disabled={locked} rows={8} value={s.body_template} className="font-mono text-sm"
                    onChange={(e) => update(i, { body_template: e.target.value })}
                    onSelect={(e) => setCursor({ field: "body_template", pos: e.currentTarget.selectionStart ?? 0 })} />
                  <p className="text-xs text-muted-foreground">A footer with your company address and an unsubscribe link is added automatically.</p>
                </div>
              </CardContent>
            </Card>
          );
        })}
        {!locked && (
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" disabled={steps.length >= 10} onClick={() => {
              setSteps((s) => [...s, { delay_days: 3, subject_template: "", body_template: "" }]);
              setActive(steps.length);
              setDirty(true);
            }}><Plus /> Add follow-up</Button>
            <Button onClick={() => save.mutate()} disabled={!dirty || save.isPending}><Save /> Save sequence</Button>
          </div>
        )}
      </div>

      <div className="space-y-3 lg:col-span-2">
        {!locked && (
          <Card>
            <CardHeader><CardTitle className="text-sm">Insert variable</CardTitle></CardHeader>
            <CardContent className="flex flex-wrap gap-1.5">
              {TEMPLATE_VARIABLES.map((v) => (
                <Button key={v} size="xs" variant="outline" onClick={() => insertVar(v)}>{v}</Button>
              ))}
              <p className="mt-1 w-full text-xs text-muted-foreground">Fallbacks: <code>{"{{first_name|there}}"}</code> renders “there” when empty.</p>
            </CardContent>
          </Card>
        )}
        <PreviewCard campaignId={campaign.id} step={steps[active]} stepNumber={active + 1} />
      </div>
    </div>
  );
}

function PreviewCard({ campaignId, step, stepNumber }: { campaignId: string; step?: Step; stepNumber: number }) {
  const [debounced, setDebounced] = useState(step);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setDebounced(step), 400);
    return () => clearTimeout(timer.current);
  }, [step]);

  const preview = useQuery({
    queryKey: ["preview", campaignId, stepNumber, debounced?.subject_template, debounced?.body_template],
    queryFn: () => campaignsService.preview(campaignId, { step_number: stepNumber, subject_template: debounced?.subject_template, body_template: debounced?.body_template }),
    enabled: !!debounced,
  });
  const p = preview.data;
  return (
    <Card className="lg:sticky lg:top-4">
      <CardHeader><CardTitle className="flex items-center gap-2 text-sm"><Eye className="size-4" /> Preview — Email {stepNumber}</CardTitle></CardHeader>
      <CardContent className="space-y-2 text-sm">
        {preview.error && <p className="text-destructive">{errorMessage(preview.error)}</p>}
        {p && (
          <>
            <p className="text-xs text-muted-foreground">To {p.to} ({p.company_name}) · From {p.from}</p>
            <p className="font-medium">{p.subject || <span className="text-muted-foreground">(no subject)</span>}</p>
            <pre className="max-h-[28rem] overflow-auto rounded-md bg-muted p-3 font-sans text-sm whitespace-pre-wrap wrap-anywhere">{p.text}</pre>
            {p.empty_variables.length > 0 && (
              <p className="text-xs text-amber-700 dark:text-amber-400">Empty for this lead: {p.empty_variables.join(", ")} — add a fallback like <code>{`{{${p.empty_variables[0]}|…}}`}</code></p>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}
