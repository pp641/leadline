"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Send, Upload } from "lucide-react";
import { RepliesChart, SendsChart, StatTile } from "@/components/charts";
import { GettingStarted } from "@/components/getting-started";
import { EmptyState, PageHeader, StatusBadge, num, pct } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { CampaignAnalyticsRow, Overview } from "@/types/api";

export default function DashboardPage() {
  const overview = useQuery({ queryKey: ["analytics", "overview", 30], queryFn: () => api.get<Overview>("/analytics/overview", { days: 30 }) });
  const campaigns = useQuery({ queryKey: ["analytics", "campaigns"], queryFn: () => api.get<CampaignAnalyticsRow[]>("/analytics/campaigns") });
  const o = overview.data;

  return (
    <>
      <PageHeader
        title="Dashboard"
        description="Last 30 days of outreach across your organization."
        actions={
          <>
            <Button variant="outline" asChild>
              <Link href="/leads/import"><Upload /> Import leads</Link>
            </Button>
            <Button asChild>
              <Link href="/campaigns"><Send /> Campaigns</Link>
            </Button>
          </>
        }
      />
      <GettingStarted />

      {!o ? (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4 lg:grid-cols-5">
          {Array.from({ length: 10 }).map((_, i) => <Skeleton key={i} className="h-24" />)}
        </div>
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4 lg:grid-cols-5">
            <StatTile label="Total leads" value={num(o.total_leads)} />
            <StatTile label="Verified leads" value={num(o.verified_leads)} hint="with a valid contact email" />
            <StatTile label="Active campaigns" value={num(o.active_campaigns)} />
            <StatTile label="Emails sent" value={num(o.period.sent)} />
            <StatTile label="Delivery rate" value={pct(o.period.delivery_rate)} hint="sent minus hard bounces" />
            <StatTile label="Bounce rate" value={pct(o.period.bounce_rate)} />
            <StatTile label="Replies" value={num(o.period.replied_leads)} hint={`${pct(o.period.reply_rate)} of contacted leads`} />
            <StatTile label="Positive replies" value={num(o.period.positive_replies)} hint={pct(o.period.positive_reply_rate)} />
            <StatTile label="Interested leads" value={num(o.interested_leads)} />
            <StatTile label="Meetings" value={num(o.meetings)} hint={`${pct(o.conversion_rate)} conversion`} />
          </div>
          <div className="mt-4 grid gap-4 lg:grid-cols-2">
            <SendsChart data={o.daily} />
            <RepliesChart data={o.daily} />
          </div>
        </>
      )}

      <Card className="mt-4">
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">Campaign performance</CardTitle>
          <Button variant="ghost" size="sm" asChild>
            <Link href="/analytics">Analytics <ArrowRight /></Link>
          </Button>
        </CardHeader>
        <CardContent>
          {campaigns.data && campaigns.data.length === 0 ? (
            <EmptyState icon={Send} title="No launched campaigns yet">
              Import leads, create a campaign and launch it to see performance here.
            </EmptyState>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Campaign</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead className="text-right">Leads</TableHead>
                    <TableHead className="text-right">Sent</TableHead>
                    <TableHead className="text-right">Bounce</TableHead>
                    <TableHead className="text-right">Reply</TableHead>
                    <TableHead className="text-right">Positive</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {campaigns.data?.slice(0, 8).map((c) => (
                    <TableRow key={c.id}>
                      <TableCell className="font-medium">
                        <Link href={`/campaigns/${c.id}`} className="hover:underline">{c.name}</Link>
                      </TableCell>
                      <TableCell><StatusBadge status={c.status} /></TableCell>
                      <TableCell className="text-right tabular-nums">{num(c.leads)}</TableCell>
                      <TableCell className="text-right tabular-nums">{num(c.sent)}</TableCell>
                      <TableCell className="text-right tabular-nums">{pct(c.bounce_rate)}</TableCell>
                      <TableCell className="text-right tabular-nums">{pct(c.reply_rate)}</TableCell>
                      <TableCell className="text-right tabular-nums">{num(c.positive_replies)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </>
  );
}
