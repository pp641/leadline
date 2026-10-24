"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, RefreshCw, Sparkles, X } from "lucide-react";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { EmptyState, StatusBadge } from "@/components/shared";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { Campaign, Personalization } from "@/types/api";

export function PersonalizationTab({ campaign }: { campaign: Campaign }) {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const [status, setStatus] = useState("");
  const key = ["campaign", campaign.id, "personalizations", status];
  const list = useQuery({
    queryKey: key,
    queryFn: () => api.page<Personalization[]>(`/campaigns/${campaign.id}/personalizations`, { status }),
    refetchInterval: (q) => ((q.state.data?.meta?.counts as Record<string, number> | undefined)?.pending ? 3000 : false),
  });
  const counts = (list.data?.meta.counts ?? {}) as Record<string, number>;
  const aiEnabled = list.data?.meta.ai_enabled as boolean | undefined;
  const refresh = () => qc.invalidateQueries({ queryKey: ["campaign", campaign.id, "personalizations"] });

  const generate = useMutation({
    mutationFn: (regenerate: boolean) => api.post<{ queued: number }>(`/campaigns/${campaign.id}/personalizations/generate`, { regenerate }),
    onSuccess: (r) => { toast.success(`Generating ${r.queued} openers`); refresh(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const approveAll = useMutation({
    mutationFn: () => api.post<{ approved: number }>(`/campaigns/${campaign.id}/personalizations/approve-all`),
    onSuccess: (r) => { toast.success(`Approved ${r.approved}`); refresh(); },
    onError: (e) => toast.error(errorMessage(e)),
  });

  if (!campaign.ai_personalization) {
    return (
      <EmptyState icon={Sparkles} title="AI personalization is off for this campaign">
        Turn it on in the Settings tab, then use <code>{"{{personalized_line|fallback}}"}</code> in your sequence.
      </EmptyState>
    );
  }
  return (
    <div className="space-y-4">
      {aiEnabled === false && (
        <Alert><AlertDescription>The server has no AI provider configured (set LLM_PROVIDER=anthropic and LLM_API_KEY). Openers cannot be generated.</AlertDescription></Alert>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <FilterSelect value={status} onChange={setStatus} placeholder="All" className="w-40"
          options={["generated", "approved", "rejected", "pending", "failed"].map((s) => ({ value: s, label: `${s} (${counts[s] ?? 0})` }))} />
        {canEdit && (
          <div className="ml-auto flex gap-2">
            <Button variant="outline" disabled={generate.isPending} onClick={() => generate.mutate(false)}><Sparkles /> Generate missing</Button>
            <Button variant="outline" disabled={generate.isPending} onClick={() => confirm("Regenerate every opener that is not approved?") && generate.mutate(true)}><RefreshCw /> Regenerate</Button>
            <Button disabled={!counts.generated || approveAll.isPending} onClick={() => approveAll.mutate()}><Check /> Approve all ({counts.generated ?? 0})</Button>
          </div>
        )}
      </div>
      <p className="text-xs text-muted-foreground">
        Openers are written only from facts stored on each lead. Review them before approving — leads wait until their opener is approved{campaign.require_personalization_approval ? "" : " (review is currently optional for this campaign)"}.
        Rejected or failed openers fall back to the template&apos;s default text.
      </p>
      <div className="divide-y rounded-lg border">
        {(list.data?.data ?? []).map((p) => <Row key={p.id} p={p} canEdit={canEdit} onDone={refresh} />)}
        {list.data?.data.length === 0 && <p className="p-6 text-center text-sm text-muted-foreground">Nothing here yet. Click “Generate missing”.</p>}
      </div>
    </div>
  );
}

function Row({ p, canEdit, onDone }: { p: Personalization; canEdit: boolean; onDone: () => void }) {
  const [text, setText] = useState(p.content);
  const save = useMutation({
    mutationFn: (status: string) => api.put(`/personalizations/${p.id}`, { status, content: text }),
    onSuccess: onDone,
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <div className="flex flex-col gap-2 p-3 sm:flex-row sm:items-start">
      <div className="w-full sm:w-56 sm:shrink-0">
        <p className="font-medium">{p.company_name}</p>
        <p className="text-xs text-muted-foreground">{p.contact_name || "—"}</p>
        <StatusBadge status={p.status === "generated" ? "previewed" : p.status === "approved" ? "valid" : p.status === "rejected" ? "stopped" : p.status} className="mt-1" />
      </div>
      <div className="flex-1">
        {p.status === "pending" ? <p className="text-sm text-muted-foreground">Generating…</p>
          : p.status === "failed" ? <p className="text-sm text-muted-foreground">{p.error}</p>
            : <Textarea rows={2} value={text} disabled={!canEdit} onChange={(e) => setText(e.target.value)} />}
      </div>
      {canEdit && p.status !== "pending" && p.status !== "failed" && (
        <div className="flex gap-1">
          <Button size="sm" variant={p.status === "approved" ? "default" : "outline"} onClick={() => save.mutate("approved")}><Check /> Approve</Button>
          <Button size="sm" variant="ghost" aria-label="Reject" onClick={() => save.mutate("rejected")}><X /></Button>
        </div>
      )}
    </div>
  );
}
