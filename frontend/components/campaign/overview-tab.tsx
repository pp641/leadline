"use client";

import { useQuery } from "@tanstack/react-query";
import { RepliesChart, SendsChart, StatTile } from "@/components/charts";
import { StatusBadge, num, pct } from "@/components/shared";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { campaignsService } from "@/services/campaigns";
import type { Campaign } from "@/types/api";

export function OverviewTab({ campaign }: { campaign: Campaign }) {
  const a = useQuery({
    queryKey: ["campaign", campaign.id, "analytics"],
    queryFn: () => campaignsService.analytics(campaign.id),
    refetchInterval: campaign.status === "running" ? 15_000 : false,
  });
  if (!a.data) return <Skeleton className="h-64" />;
  const d = a.data;
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4 lg:grid-cols-6">
        <StatTile label="Leads" value={num(d.leads)} />
        <StatTile label="Sent" value={num(d.sent)} />
        <StatTile label="Delivery rate" value={pct(d.delivery_rate)} />
        <StatTile label="Bounce rate" value={pct(d.bounce_rate)} hint={`limit ${pct(campaign.max_bounce_rate)}`} />
        <StatTile label="Reply rate" value={pct(d.reply_rate)} hint={`${num(d.replied_leads)} leads`} />
        <StatTile label="Unsubscribes" value={num(d.unsubscribed)} hint={pct(d.unsubscribe_rate)} />
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle className="text-base">By step</CardTitle></CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow><TableHead>Step</TableHead><TableHead className="text-right">Sent</TableHead><TableHead className="text-right">Bounced</TableHead><TableHead className="text-right">Replied</TableHead></TableRow>
              </TableHeader>
              <TableBody>
                {d.steps.map((s) => (
                  <TableRow key={s.step_number}>
                    <TableCell>Email {s.step_number}</TableCell>
                    <TableCell className="text-right tabular-nums">{num(s.sent)}</TableCell>
                    <TableCell className="text-right tabular-nums">{num(s.bounced)}</TableCell>
                    <TableCell className="text-right tabular-nums">{num(s.replied)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle className="text-base">Where leads are</CardTitle></CardHeader>
          <CardContent>
            <Table>
              <TableBody>
                {d.funnel.map((f) => (
                  <TableRow key={f.status}>
                    <TableCell><StatusBadge status={f.status} /></TableCell>
                    <TableCell className="text-right tabular-nums">{num(f.count)}</TableCell>
                  </TableRow>
                ))}
                {d.funnel.length === 0 && <TableRow><TableCell className="text-muted-foreground">No leads in this campaign yet.</TableCell></TableRow>}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      </div>
      {campaign.status !== "draft" && (
        <div className="grid gap-4 lg:grid-cols-2">
          <SendsChart data={d.daily} />
          <RepliesChart data={d.daily} />
        </div>
      )}
    </div>
  );
}
