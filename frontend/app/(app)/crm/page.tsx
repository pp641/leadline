"use client";

import Link from "next/link";
import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { DealsBoard } from "@/components/crm/deals-board";
import { TaskList } from "@/components/crm/task-list";
import { WorkflowsPanel } from "@/components/crm/workflows-panel";
import { PageHeader, ScoreBadge } from "@/components/shared";
import { Card, CardContent } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import { leadsService } from "@/services/leads";
import { LEAD_STATUSES, type Lead, type PipelineColumn } from "@/types/api";

export default function CRMPage() {
  return <Suspense><CRM /></Suspense>;
}

const TABS = ["deals", "stages", "tasks", "workflows"] as const;

function CRM() {
  const router = useRouter();
  const params = useSearchParams();
  const [owner, setOwner] = useState("");
  const dealId = params.get("deal");
  const tabParam = params.get("tab");
  const tab = TABS.includes(tabParam as (typeof TABS)[number]) ? tabParam! : "deals";
  const setParam = (key: string, value: string | null) => {
    const next = new URLSearchParams(params.toString());
    if (value) next.set(key, value); else next.delete(key);
    router.replace(`/crm${next.size ? `?${next}` : ""}`, { scroll: false });
  };
  return (
    <>
      <PageHeader title="CRM" description="Run deals through your pipeline, keep follow-ups on track and automate the busywork."
        actions={tab !== "workflows" && <FilterSelect value={owner} onChange={setOwner} placeholder="Everyone" className="w-40" options={[{ value: "me", label: "Mine" }]} />} />
      <Tabs value={tab} onValueChange={(t) => setParam("tab", t === "deals" ? null : t)}>
        <TabsList className="mb-4">
          <TabsTrigger value="deals">Deals</TabsTrigger>
          <TabsTrigger value="stages">Lead stages</TabsTrigger>
          <TabsTrigger value="tasks">Tasks</TabsTrigger>
          <TabsTrigger value="workflows">Workflows</TabsTrigger>
        </TabsList>
        <TabsContent value="deals"><DealsBoard owner={owner} dealId={dealId} onOpenDeal={(id) => setParam("deal", id)} /></TabsContent>
        <TabsContent value="stages"><Pipeline owner={owner} /></TabsContent>
        <TabsContent value="tasks"><TaskList assignee={owner} /></TabsContent>
        <TabsContent value="workflows"><WorkflowsPanel /></TabsContent>
      </Tabs>
    </>
  );
}

function Pipeline({ owner }: { owner: string }) {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const board = useQuery({ queryKey: ["pipeline", owner], queryFn: () => api.get<PipelineColumn[]>("/crm/pipeline", { owner }) });
  const move = useMutation({
    mutationFn: async ({ id, status }: { id: string; status: string }) => {
      const lead = await leadsService.get(id);
      return leadsService.update(id, { ...(lead as Lead), status });
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["pipeline"] }); qc.invalidateQueries({ queryKey: ["leads"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <div className="space-y-3">
    <p className="text-sm text-muted-foreground">Where each engaged lead stands, by lead status. Deals track the money; lead stages track the relationship.</p>
    <div className="flex gap-3 overflow-x-auto pb-2">
      {(board.data ?? []).map((col) => (
        <div key={col.status} className="w-64 shrink-0 rounded-lg bg-muted/50 p-2">
          <div className="mb-2 flex items-center justify-between px-1 text-sm font-medium capitalize">
            {col.status}<span className="text-xs text-muted-foreground tabular-nums">{col.count}</span>
          </div>
          <div className="space-y-2">
            {col.leads.map((l) => (
              <Card key={l.id} className="gap-1 py-2">
                <CardContent className="space-y-1 px-3">
                  <div className="flex items-start justify-between gap-2">
                    <Link href={`/leads/${l.id}`} className="text-sm font-medium hover:underline">{l.company_name}</Link>
                    <ScoreBadge score={l.lead_score} />
                  </div>
                  <p className="text-xs text-muted-foreground">{[l.city, l.owner_name ?? "Unassigned"].filter(Boolean).join(" · ")}{l.open_tasks > 0 && ` · ${l.open_tasks} task${l.open_tasks > 1 ? "s" : ""}`}</p>
                  {canEdit && (
                    <FilterSelect value={col.status} onChange={(s) => s && s !== col.status && move.mutate({ id: l.id, status: s })} placeholder="Move" className="h-7 w-full text-xs"
                      options={LEAD_STATUSES.map((s) => ({ value: s, label: `Move to ${s}` }))} />
                  )}
                </CardContent>
              </Card>
            ))}
            {col.count > col.leads.length && <p className="px-1 text-xs text-muted-foreground">+{col.count - col.leads.length} more</p>}
          </div>
        </div>
      ))}
    </div>
    </div>
  );
}
