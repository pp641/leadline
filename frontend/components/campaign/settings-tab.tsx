"use client";

import Link from "next/link";
import { useEffect } from "react";
import { Controller, useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { FieldError } from "@/components/auth-card";
import { FilterSelect } from "@/components/filter-select";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import { WEEKDAYS, campaignsService } from "@/services/campaigns";
import type { Campaign } from "@/types/api";

const STATUS_OPTIONS = [
  { value: "valid", label: "Valid", hint: "verified deliverable domain, personal address" },
  { value: "role_based", label: "Role-based", hint: "info@, sales@ …" },
  { value: "risky", label: "Risky", hint: "accept-all or uncertain" },
  { value: "unknown", label: "Unknown", hint: "could not be verified" },
  { value: "unverified", label: "Unverified", hint: "not checked yet" },
];

const schema = z.object({
  name: z.string().trim().min(1).max(200),
  mailbox_ids: z.array(z.string()),
  ai_personalization: z.boolean(),
  personalization_brief: z.string().max(2000),
  require_personalization_approval: z.boolean(),
  daily_limit: z.coerce.number<number>().int().min(1).max(5000),
  start_at: z.string(),
  timezone: z.string().min(1),
  send_window_start: z.string().regex(/^\d\d:\d\d$/),
  send_window_end: z.string().regex(/^\d\d:\d\d$/),
  send_days: z.array(z.number()).min(1, "Pick at least one day"),
  allowed_email_statuses: z.array(z.string()).min(1, "Pick at least one status"),
  max_bounce_rate_pct: z.coerce.number<number>().min(0.5).max(20),
  stop_on_reply: z.boolean(),
}).refine((v) => v.send_window_end > v.send_window_start, { path: ["send_window_end"], message: "End must be after start" });
type Values = z.infer<typeof schema>;

const timezones: string[] = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : ["UTC"];

function toLocalInput(iso: string | null) {
  if (!iso) return "";
  const d = new Date(iso);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}

export function SettingsTab({ campaign }: { campaign: Campaign }) {
  const qc = useQueryClient();
  const canEdit = useCan("member") && !["completed", "cancelled"].includes(campaign.status);
  const mailboxes = useQuery({ queryKey: ["mailboxes"], queryFn: campaignsService.mailboxes });
  const form = useForm<Values>({ resolver: zodResolver(schema) });

  useEffect(() => {
    form.reset({
      name: campaign.name, daily_limit: campaign.daily_limit,
      mailbox_ids: campaign.mailbox_ids?.length ? campaign.mailbox_ids : campaign.mailbox_id ? [campaign.mailbox_id] : [],
      ai_personalization: campaign.ai_personalization, personalization_brief: campaign.personalization_brief,
      require_personalization_approval: campaign.require_personalization_approval,
      start_at: toLocalInput(campaign.start_at), timezone: campaign.timezone,
      send_window_start: campaign.send_window_start, send_window_end: campaign.send_window_end,
      send_days: campaign.send_days, allowed_email_statuses: campaign.allowed_email_statuses,
      max_bounce_rate_pct: +(campaign.max_bounce_rate * 100).toFixed(2), stop_on_reply: campaign.stop_on_reply,
    });
  }, [campaign, form]);

  const save = useMutation({
    mutationFn: (v: Values) => campaignsService.update(campaign.id, {
      name: v.name, mailbox_id: v.mailbox_ids[0] ?? null, mailbox_ids: v.mailbox_ids, daily_limit: v.daily_limit,
      ai_personalization: v.ai_personalization, personalization_brief: v.personalization_brief,
      require_personalization_approval: v.require_personalization_approval,
      start_at: v.start_at ? new Date(v.start_at).toISOString() : null, timezone: v.timezone,
      send_window_start: v.send_window_start, send_window_end: v.send_window_end, send_days: [...v.send_days].sort(),
      allowed_email_statuses: v.allowed_email_statuses, max_bounce_rate: v.max_bounce_rate_pct / 100, stop_on_reply: v.stop_on_reply,
    }),
    onSuccess: (c) => { toast.success("Settings saved"); qc.setQueryData(["campaign", campaign.id], c); qc.invalidateQueries({ queryKey: ["campaigns"] }); },
    onError: (e) => toast.error(e instanceof ApiError && Object.keys(e.fields).length ? Object.values(e.fields).join("; ") : errorMessage(e)),
  });

  const err = form.formState.errors;
  const selected = form.watch("mailbox_ids") ?? [];
  const capacity = (mailboxes.data ?? []).filter((m) => selected.includes(m.id) && m.status === "active").reduce((n, m) => n + m.daily_limit, 0);

  return (
    <form onSubmit={form.handleSubmit((v) => save.mutate(v))} className="grid gap-4 lg:grid-cols-2" noValidate>
      <fieldset disabled={!canEdit} className="contents">
        <Card>
          <CardHeader><CardTitle className="text-base">Sending</CardTitle><CardDescription>Who sends, and how fast.</CardDescription></CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-1.5"><Label>Name</Label><Input {...form.register("name")} /></div>
            <Controller control={form.control} name="mailbox_ids" render={({ field }) => (
              <div className="space-y-1.5">
                <Label>Sending mailboxes</Label>
                {(mailboxes.data ?? []).map((m) => (
                  <label key={m.id} className="flex items-center gap-2 text-sm">
                    <input type="checkbox" checked={field.value?.includes(m.id) ?? false}
                      onChange={(e) => field.onChange(e.target.checked ? [...(field.value ?? []), m.id] : field.value.filter((x) => x !== m.id))} />
                    {m.from_name} &lt;{m.from_email}&gt;
                    <span className="text-xs text-muted-foreground">{m.status === "disabled" ? "disabled" : `${m.sent_last_24h}/${m.daily_limit} today`}</span>
                  </label>
                ))}
                {mailboxes.data?.length === 0 && <p className="text-xs text-muted-foreground"><Link className="underline" href="/settings?tab=mailboxes">Add a mailbox</Link> first.</p>}
                <p className="text-xs text-muted-foreground">
                  Selecting several mailboxes rotates sending between them ({capacity}/day combined). Each lead stays on one mailbox so follow-ups thread.
                </p>
              </div>
            )} />
            <div className="space-y-1.5">
              <Label>Campaign daily limit</Label>
              <Input type="number" min={1} max={5000} {...form.register("daily_limit")} />
              <p className="text-xs text-muted-foreground">Sends are spread evenly across the sending window. The lowest of the campaign, mailbox and organization limits applies.</p>
              <FieldError message={err.daily_limit?.message} />
            </div>
            <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
              <div>
                <Label>Stop on reply</Label>
                <p className="text-xs text-muted-foreground">Unsubscribes, &quot;not interested&quot; and spam replies always stop the sequence.</p>
              </div>
              <Controller control={form.control} name="stop_on_reply" render={({ field }) => <Switch checked={field.value} onCheckedChange={field.onChange} />} />
            </div>
            <div className="space-y-1.5">
              <Label>Auto-pause above bounce rate (%)</Label>
              <Input type="number" step="0.5" min={0.5} max={20} {...form.register("max_bounce_rate_pct")} />
              <FieldError message={err.max_bounce_rate_pct?.message} />
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader><CardTitle className="text-base">Schedule & audience rules</CardTitle></CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-1.5">
              <Label>Start at (optional)</Label>
              <Input type="datetime-local" {...form.register("start_at")} />
              <p className="text-xs text-muted-foreground">Leave empty to start as soon as you launch.</p>
            </div>
            <div className="space-y-1.5">
              <Label>Timezone</Label>
              <Controller control={form.control} name="timezone" render={({ field }) => (
                <FilterSelect className="w-full" value={field.value} onChange={(v) => field.onChange(v || "UTC")} placeholder="UTC"
                  options={timezones.map((t) => ({ value: t, label: t }))} />
              )} />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5"><Label>Window start</Label><Input type="time" {...form.register("send_window_start")} /></div>
              <div className="space-y-1.5"><Label>Window end</Label><Input type="time" {...form.register("send_window_end")} /><FieldError message={err.send_window_end?.message} /></div>
            </div>
            <Controller control={form.control} name="send_days" render={({ field }) => (
              <div className="space-y-1.5">
                <Label>Sending days</Label>
                <div className="flex flex-wrap gap-1.5">
                  {WEEKDAYS.map((d, i) => {
                    const on = field.value?.includes(i + 1);
                    return (
                      <Button key={d} type="button" size="sm" variant={on ? "default" : "outline"} aria-pressed={on}
                        onClick={() => field.onChange(on ? field.value.filter((x) => x !== i + 1) : [...(field.value ?? []), i + 1])}>{d}</Button>
                    );
                  })}
                </div>
                <FieldError message={err.send_days?.message} />
              </div>
            )} />
            <Controller control={form.control} name="allowed_email_statuses" render={({ field }) => (
              <div className="space-y-1.5">
                <Label>Send to email statuses</Label>
                {STATUS_OPTIONS.map((o) => (
                  <label key={o.value} className="flex items-center gap-2 text-sm">
                    <input type="checkbox" checked={field.value?.includes(o.value) ?? false}
                      onChange={(e) => field.onChange(e.target.checked ? [...field.value, o.value] : field.value.filter((x) => x !== o.value))} />
                    {o.label} <span className="text-xs text-muted-foreground">— {o.hint}</span>
                  </label>
                ))}
                <p className="text-xs text-muted-foreground">Invalid and disposable addresses are never sent to.</p>
                <FieldError message={err.allowed_email_statuses?.message} />
              </div>
            )} />
          </CardContent>
        </Card>
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle className="text-base">AI personalization</CardTitle>
            <CardDescription>
              Writes one opening sentence per lead from its stored facts (industry, location, website signals). Use it in templates as <code>{"{{personalized_line}}"}</code>.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
              <Label>Generate AI openers for this campaign</Label>
              <Controller control={form.control} name="ai_personalization" render={({ field }) => <Switch checked={field.value} onCheckedChange={field.onChange} />} />
            </div>
            {form.watch("ai_personalization") && (
              <>
                <div className="space-y-1.5">
                  <Label>What you offer (context for the AI, never quoted)</Label>
                  <Textarea rows={3} placeholder="We build fast restaurant websites with online menus and table booking." {...form.register("personalization_brief")} />
                </div>
                <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
                  <div>
                    <Label>Require human review</Label>
                    <p className="text-xs text-muted-foreground">Leads wait until their opener is approved in the Personalization tab. Recommended.</p>
                  </div>
                  <Controller control={form.control} name="require_personalization_approval" render={({ field }) => <Switch checked={field.value} onCheckedChange={field.onChange} />} />
                </div>
              </>
            )}
          </CardContent>
        </Card>
        {canEdit && (
          <div className="lg:col-span-2"><Button type="submit" disabled={save.isPending}>Save settings</Button></div>
        )}
      </fieldset>
    </form>
  );
}
