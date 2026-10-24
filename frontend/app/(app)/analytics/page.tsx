"use client";

import Link from "next/link";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { LayoutDashboard, PieChart, Send } from "lucide-react";
import { AdvancedAnalyticsPanel } from "@/components/advanced-analytics";
import { DashboardsPanel } from "@/components/dashboards/dashboards-panel";
import { LeadInsightsPanel } from "@/components/lead-insights";
import { RepliesChart, SendsChart, StatTile } from "@/components/charts";
import { FilterSelect } from "@/components/filter-select";
import { PageHeader, StatusBadge, num, pct } from "@/components/shared";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import { initialTab } from "@/lib/url";
import type { CampaignAnalyticsRow, Overview } from "@/types/api";

export default function AnalyticsPage() {
  const [tab, setTab] = useState<string>(() => initialTab(["leads", "dashboards", "outreach"] as const, "leads"));
  return (
    <>
      <PageHeader title="Analytics" description="Understand your lead base and how your outreach performs." />
      <Tabs value={tab} onValueChange={setTab} className="gap-4">
        <TabsList>
          <TabsTrigger value="leads" className="px-3"><PieChart /> Lead insights</TabsTrigger>
          <TabsTrigger value="dashboards" className="px-3"><LayoutDashboard /> Dashboards</TabsTrigger>
          <TabsTrigger value="outreach" className="px-3"><Send /> Outreach</TabsTrigger>
        </TabsList>
        <TabsContent value="leads"><LeadInsightsPanel /></TabsContent>
        <TabsContent value="dashboards"><DashboardsPanel /></TabsContent>
        <TabsContent value="outreach"><Outreach /></TabsContent>
      </Tabs>
    </>
  );
}

function Outreach() {
  const [days, setDays] = useState("30");
  const o = useQuery({ queryKey: ["analytics", "overview", days], queryFn: () => api.get<Overview>("/analytics/overview", { days }) });
  const rows = useQuery({ queryKey: ["analytics", "campaigns"], queryFn: () => api.get<CampaignAnalyticsRow[]>("/analytics/campaigns") });
  const p = o.data?.period;

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">Organization-wide outreach performance.</p>
        <FilterSelect value={days} onChange={(v) => setDays(v || "30")} placeholder="Last 30 days" className="w-40"
          options={[{ value: "7", label: "Last 7 days" }, { value: "90", label: "Last 90 days" }, { value: "365", label: "Last 12 months" }]} />
      </div>

      {!o.data || !p ? <Skeleton className="h-64" /> : (
        <>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
            <StatTile label="Total prospects" value={num(o.data.total_leads)} />
            <StatTile label="Contacted" value={num(p.contacted_leads)} />
            <StatTile label="Emails sent" value={num(p.sent)} hint={`${num(p.failed)} failed`} />
            <StatTile label="Delivery rate" value={pct(p.delivery_rate)} hint={p.delivered ? `${num(p.delivered)} delivery receipts` : undefined} />
            <StatTile label="Bounce rate" value={pct(p.bounce_rate)} hint={`${num(p.bounced)} hard bounces`} />
            <StatTile label="Reply rate" value={pct(p.reply_rate)} hint={`${num(p.replied_leads)} leads`} />
            <StatTile label="Positive reply rate" value={pct(p.positive_reply_rate)} hint={`${num(p.positive_replies)} leads`} />
            <StatTile label="Unsubscribe rate" value={pct(p.unsubscribe_rate)} hint={`${num(p.unsubscribed)} · ${num(p.complaints)} complaints`} />
            <StatTile label="Interested leads" value={num(o.data.interested_leads)} />
            <StatTile label="Meetings" value={num(o.data.meetings)} hint={`${pct(o.data.conversion_rate)} of contacted`} />
          </div>
          <div className="mt-4 grid gap-4 lg:grid-cols-2">
            <SendsChart data={o.data.daily} />
            <RepliesChart data={o.data.daily} />
          </div>
        </>
      )}

      <h2 className="mt-8 mb-3 text-base font-semibold">By campaign (all time)</h2>
      <div className="overflow-x-auto rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Campaign</TableHead><TableHead>Status</TableHead><TableHead className="text-right">Leads</TableHead>
              <TableHead className="text-right">Sent</TableHead><TableHead className="text-right">Delivery</TableHead>
              <TableHead className="text-right">Bounce</TableHead><TableHead className="text-right">Reply</TableHead>
              <TableHead className="text-right">Positive</TableHead><TableHead className="text-right">Unsub</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(rows.data ?? []).map((c) => (
              <TableRow key={c.id}>
                <TableCell className="font-medium"><Link href={`/campaigns/${c.id}`} className="hover:underline">{c.name}</Link></TableCell>
                <TableCell><StatusBadge status={c.status} /></TableCell>
                <TableCell className="text-right tabular-nums">{num(c.leads)}</TableCell>
                <TableCell className="text-right tabular-nums">{num(c.sent)}</TableCell>
                <TableCell className="text-right tabular-nums">{pct(c.delivery_rate)}</TableCell>
                <TableCell className="text-right tabular-nums">{pct(c.bounce_rate)}</TableCell>
                <TableCell className="text-right tabular-nums">{pct(c.reply_rate)}</TableCell>
                <TableCell className="text-right tabular-nums">{pct(c.positive_reply_rate)}</TableCell>
                <TableCell className="text-right tabular-nums">{pct(c.unsubscribe_rate)}</TableCell>
              </TableRow>
            ))}
            {rows.data?.length === 0 && <TableRow><TableCell colSpan={9} className="py-8 text-center text-muted-foreground">No launched campaigns yet.</TableCell></TableRow>}
          </TableBody>
        </Table>
      </div>
      <p className="mt-3 text-xs text-muted-foreground">
        Delivery rate = (sent − hard bounces) ÷ sent. Reply, positive-reply and unsubscribe rates are per contacted lead.
      </p>
      <AdvancedAnalyticsPanel days={days} />
    </>
  );
}
