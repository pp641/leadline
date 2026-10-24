"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, Ban, Pause, Play, Rocket, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { AudienceTab } from "@/components/campaign/audience-tab";
import { OverviewTab } from "@/components/campaign/overview-tab";
import { PersonalizationTab } from "@/components/campaign/personalization-tab";
import { SequenceTab } from "@/components/campaign/sequence-tab";
import { SettingsTab } from "@/components/campaign/settings-tab";
import { PageHeader, StatusBadge } from "@/components/shared";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ApiError, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import { initialTab } from "@/lib/url";
import { campaignsService } from "@/services/campaigns";

const TABS = ["overview", "sequence", "audience", "personalization", "settings"] as const;

export default function CampaignPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const [tab, setTab] = useState<string>(() => initialTab(TABS, "overview"));
  const [launchProblems, setLaunchProblems] = useState<Record<string, string>>({});

  const campaign = useQuery({
    queryKey: ["campaign", id],
    queryFn: () => campaignsService.get(id),
    refetchInterval: (q) => (q.state.data?.status === "running" ? 15_000 : false),
  });

  const action = useMutation({
    mutationFn: (a: "launch" | "pause" | "resume" | "cancel") => campaignsService.action(id, a),
    onSuccess: (c, a) => {
      setLaunchProblems({});
      toast.success({ launch: c.status === "scheduled" ? "Campaign scheduled" : "Campaign launched", pause: "Campaign paused", resume: "Campaign resumed", cancel: "Campaign cancelled" }[a]);
      qc.setQueryData(["campaign", id], c);
      qc.invalidateQueries({ queryKey: ["campaigns"] });
      qc.invalidateQueries({ queryKey: ["campaign", id] });
    },
    onError: (e) => {
      if (e instanceof ApiError && Object.keys(e.fields).length) setLaunchProblems(e.fields);
      else toast.error(errorMessage(e));
    },
  });
  const remove = useMutation({
    mutationFn: () => campaignsService.remove(id),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["campaigns"] }); router.push("/campaigns"); },
    onError: (e) => toast.error(errorMessage(e)),
  });

  if (campaign.isLoading) return <Skeleton className="h-96" />;
  const c = campaign.data;
  if (!c) return <p className="text-muted-foreground">Campaign not found.</p>;
  const closed = c.status === "completed" || c.status === "cancelled";

  return (
    <>
      <Button variant="ghost" size="sm" asChild className="mb-2 -ml-2"><Link href="/campaigns"><ArrowLeft /> Campaigns</Link></Button>
      <PageHeader
        title={c.name}
        description={`${c.daily_limit} emails/day · ${c.send_window_start}–${c.send_window_end} ${c.timezone}`}
        actions={canEdit && (
          <>
            <StatusBadge status={c.status} className="self-center" />
            {c.status === "draft" && <Button onClick={() => action.mutate("launch")} disabled={action.isPending}><Rocket /> Launch</Button>}
            {(c.status === "running" || c.status === "scheduled") && <Button variant="outline" onClick={() => action.mutate("pause")}><Pause /> Pause</Button>}
            {c.status === "paused" && <Button onClick={() => action.mutate("resume")}><Play /> Resume</Button>}
            {!closed && c.status !== "draft" && (
              <Button variant="ghost" onClick={() => confirm("Cancel this campaign? Pending emails will not be sent and it cannot be restarted.") && action.mutate("cancel")}><Ban /> Cancel</Button>
            )}
            {c.status === "draft" && <Button variant="ghost" aria-label="Delete draft" onClick={() => confirm("Delete this draft?") && remove.mutate()}><Trash2 /></Button>}
          </>
        )}
      />

      {Object.keys(launchProblems).length > 0 && (
        <Alert variant="destructive" className="mb-4">
          <AlertTriangle />
          <AlertTitle>Not ready to launch</AlertTitle>
          <AlertDescription>
            <ul className="list-disc pl-4">{Object.values(launchProblems).map((p) => <li key={p}>{p}</li>)}</ul>
          </AlertDescription>
        </Alert>
      )}
      {c.status === "paused" && c.paused_reason && (
        <Alert className="mb-4">
          <Pause />
          <AlertTitle>Paused</AlertTitle>
          <AlertDescription>{c.paused_reason}</AlertDescription>
        </Alert>
      )}

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="mb-4">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="sequence">Sequence ({c.steps?.length ?? 0})</TabsTrigger>
          <TabsTrigger value="audience">Audience ({c.summary?.leads ?? 0})</TabsTrigger>
          <TabsTrigger value="personalization">Personalization</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
        </TabsList>
        <TabsContent value="overview"><OverviewTab campaign={c} /></TabsContent>
        <TabsContent value="sequence"><SequenceTab campaign={c} /></TabsContent>
        <TabsContent value="audience"><AudienceTab campaign={c} /></TabsContent>
        <TabsContent value="personalization"><PersonalizationTab campaign={c} /></TabsContent>
        <TabsContent value="settings"><SettingsTab campaign={c} /></TabsContent>
      </Tabs>
    </>
  );
}
