"use client";

import Link from "next/link";
import { useState } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, Check, Inbox as InboxIcon, Sparkles } from "lucide-react";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { EmptyState, PageHeader, StatusBadge, dateTime } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import { REPLY_CLASSIFICATIONS } from "@/services/campaigns";
import type { InboundMessage } from "@/types/api";

const tone: Record<string, string> = {
  positive: "interested", interested: "interested", question: "contacted", not_now: "paused", not_interested: "lost",
  unsubscribe: "unsubscribed", wrong_person: "stopped", out_of_office: "pending", spam: "bounced", unknown: "unknown",
};

export default function InboxPage() {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const [classification, setClassification] = useState("");
  const [status, setStatus] = useState("new");
  const [kind, setKind] = useState("reply");
  const [openId, setOpenId] = useState<string | null>(null);
  const list = useQuery({
    queryKey: ["inbox", classification, status, kind],
    queryFn: () => api.get<InboundMessage[]>("/inbox", { classification, status, kind, per_page: 100 }),
    placeholderData: keepPreviousData,
    refetchInterval: 30_000,
  });
  const msgs = list.data ?? [];
  const open = msgs.find((m) => m.id === openId) ?? msgs[0] ?? null;

  const update = useMutation({
    mutationFn: ({ id, body }: { id: string; body: { status?: string; classification?: string } }) => api.patch(`/inbox/${id}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["inbox"] }),
    onError: (e) => toast.error(errorMessage(e)),
  });

  return (
    <>
      <PageHeader title="Inbox" description="Replies to your campaigns, synced from connected mailboxes and classified automatically." />
      <div className="mb-3 flex flex-wrap gap-2">
        <FilterSelect value={status} onChange={setStatus} placeholder="Any status" className="w-36"
          options={[{ value: "new", label: "New" }, { value: "handled", label: "Handled" }, { value: "archived", label: "Archived" }]} />
        <FilterSelect value={kind} onChange={setKind} placeholder="All messages" className="w-40"
          options={[{ value: "reply", label: "Replies" }, { value: "auto_reply", label: "Auto-replies" }, { value: "bounce", label: "Bounces" }]} />
        <FilterSelect value={classification} onChange={setClassification} placeholder="Any classification" className="w-48"
          options={REPLY_CLASSIFICATIONS.map((c) => ({ value: c, label: c.replace("_", " ") }))} />
      </div>
      {msgs.length === 0 ? (
        <EmptyState icon={InboxIcon} title="Nothing here">
          Turn on inbox sync for a mailbox in <Link className="underline" href="/settings?tab=mailboxes">Settings → Mailboxes</Link>, or forward replies to the inbound webhook.
        </EmptyState>
      ) : (
        <div className="grid gap-4 lg:grid-cols-5">
          <div className="divide-y rounded-lg border lg:col-span-2">
            {msgs.map((m) => (
              <button key={m.id} onClick={() => setOpenId(m.id)} className={`block w-full p-3 text-left hover:bg-muted ${open?.id === m.id ? "bg-muted" : ""}`}>
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate font-medium">{m.company_name ?? m.from_email}</span>
                  <span className="shrink-0 text-xs text-muted-foreground">{dateTime(m.received_at)}</span>
                </div>
                <div className="mt-1 flex items-center gap-2">
                  {m.classification && <StatusBadge status={tone[m.classification] ?? "unknown"} className="text-[10px]" />}
                  <span className="text-xs capitalize text-muted-foreground">{(m.classification ?? m.kind).replaceAll("_", " ")}</span>
                </div>
                <p className="mt-1 line-clamp-2 text-sm text-muted-foreground">{m.summary || m.body_text}</p>
              </button>
            ))}
          </div>
          {open && (
            <Card className="lg:col-span-3">
              <CardHeader>
                <CardTitle className="text-base">{open.subject || "(no subject)"}</CardTitle>
                <p className="text-sm text-muted-foreground">
                  {open.from_name ? `${open.from_name} <${open.from_email}>` : open.from_email}
                  {open.lead_id && <> · <Link className="underline" href={`/leads/${open.lead_id}`}>{open.company_name}</Link></>}
                  {open.campaign_id && <> · <Link className="underline" href={`/campaigns/${open.campaign_id}`}>{open.campaign_name}</Link></>}
                </p>
              </CardHeader>
              <CardContent className="space-y-4">
                {open.summary && (
                  <p className="flex gap-2 rounded-md bg-muted p-2 text-sm">
                    <Sparkles className="mt-0.5 size-4 shrink-0" />
                    <span>{open.summary} <span className="text-xs text-muted-foreground">({open.classified_by}{open.confidence !== null && `, ${Math.round(open.confidence * 100)}%`})</span></span>
                  </p>
                )}
                <pre className="max-h-96 overflow-auto font-sans text-sm whitespace-pre-wrap wrap-anywhere">{open.body_text || "(empty)"}</pre>
                {canEdit && (
                  <div className="flex flex-wrap items-center gap-2 border-t pt-3">
                    <FilterSelect value={open.classification ?? ""} placeholder="Classification" className="w-48"
                      options={REPLY_CLASSIFICATIONS.map((c) => ({ value: c, label: c.replace("_", " ") }))}
                      onChange={(c) => c && update.mutate({ id: open.id, body: { classification: c } }, { onSuccess: () => toast.success("Reclassified") })} />
                    <Button size="sm" variant="outline" onClick={() => update.mutate({ id: open.id, body: { status: "handled" } })}><Check /> Mark handled</Button>
                    <Button size="sm" variant="ghost" onClick={() => update.mutate({ id: open.id, body: { status: "archived" } })}><Archive /> Archive</Button>
                    <p className="w-full text-xs text-muted-foreground">Changing the label to “unsubscribe” or “spam” adds the sender to your suppression list.</p>
                  </div>
                )}
              </CardContent>
            </Card>
          )}
        </div>
      )}
    </>
  );
}
