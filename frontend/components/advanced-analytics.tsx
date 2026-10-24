"use client";

import { useQuery } from "@tanstack/react-query";
import { StatTile } from "@/components/charts";
import { num, pct } from "@/components/shared";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import type { AdvancedAnalytics } from "@/types/api";

/** Breakdown tables: reply classifications, mailboxes, segments, steps. */
export function AdvancedAnalyticsPanel({ days }: { days: string }) {
  const q = useQuery({ queryKey: ["analytics", "advanced", days], queryFn: () => api.get<AdvancedAnalytics>("/analytics/advanced", { days }) });
  const a = q.data;
  if (!a) return null;
  const totalReplies = a.classifications.reduce((n, c) => n + c.count, 0);
  return (
    <div className="mt-8 space-y-4">
      <h2 className="text-base font-semibold">Deeper insights</h2>
      <div className="grid gap-4 lg:grid-cols-3">
        <StatTile label="Median time to reply" value={a.median_hours_to_reply === null ? "—" : a.median_hours_to_reply < 48 ? `${a.median_hours_to_reply.toFixed(1)} h` : `${(a.median_hours_to_reply / 24).toFixed(1)} days`} />
        <Card className="lg:col-span-2">
          <CardHeader><CardTitle className="text-base">Replies by classification</CardTitle></CardHeader>
          <CardContent className="space-y-1.5">
            {a.classifications.length === 0 && <p className="text-sm text-muted-foreground">No replies in this period.</p>}
            {a.classifications.map((c) => (
              <div key={c.status} className="grid grid-cols-[8rem_1fr_3rem] items-center gap-2 text-sm">
                <span className="capitalize">{c.status.replaceAll("_", " ")}</span>
                <span className="h-2 rounded-sm bg-muted"><span className="block h-2 rounded-sm" style={{ width: `${(100 * c.count) / Math.max(1, totalReplies)}%`, background: "var(--series-1)" }} /></span>
                <span className="text-right tabular-nums">{num(c.count)}</span>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle className="text-base">Mailbox performance</CardTitle></CardHeader>
          <CardContent className="overflow-x-auto">
            <Table>
              <TableHeader><TableRow><TableHead>Mailbox</TableHead><TableHead className="text-right">Sent</TableHead><TableHead className="text-right">Bounce</TableHead><TableHead className="text-right">Reply</TableHead></TableRow></TableHeader>
              <TableBody>
                {a.mailboxes.map((m) => (
                  <TableRow key={m.id}>
                    <TableCell>{m.from_email}</TableCell>
                    <TableCell className="text-right tabular-nums">{num(m.sent)}</TableCell>
                    <TableCell className="text-right tabular-nums">{pct(m.bounce_rate)}</TableCell>
                    <TableCell className="text-right tabular-nums">{pct(m.reply_rate)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle className="text-base">By sequence step</CardTitle></CardHeader>
          <CardContent>
            <Table>
              <TableHeader><TableRow><TableHead>Step</TableHead><TableHead className="text-right">Sent</TableHead><TableHead className="text-right">Bounced</TableHead><TableHead className="text-right">Replies</TableHead></TableRow></TableHeader>
              <TableBody>
                {a.replies_by_step.map((s) => (
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
      </div>
      <Card>
        <CardHeader><CardTitle className="text-base">Which leads respond</CardTitle></CardHeader>
        <CardContent>
          <Tabs defaultValue="industry">
            <TabsList>
              {Object.keys(a.segments).sort().map((k) => <TabsTrigger key={k} value={k} className="capitalize">{k.replace("_", " ")}</TabsTrigger>)}
            </TabsList>
            {Object.entries(a.segments).map(([k, rows]) => (
              <TabsContent key={k} value={k}>
                <Table>
                  <TableHeader><TableRow><TableHead className="capitalize">{k.replace("_", " ")}</TableHead><TableHead className="text-right">Contacted</TableHead><TableHead className="text-right">Reply rate</TableHead><TableHead className="text-right">Positive rate</TableHead></TableRow></TableHeader>
                  <TableBody>
                    {rows.map((r) => (
                      <TableRow key={r.value}>
                        <TableCell className="capitalize">{r.value}</TableCell>
                        <TableCell className="text-right tabular-nums">{num(r.contacted)}</TableCell>
                        <TableCell className="text-right tabular-nums">{pct(r.reply_rate)}</TableCell>
                        <TableCell className="text-right tabular-nums">{pct(r.positive_rate)}</TableCell>
                      </TableRow>
                    ))}
                    {rows.length === 0 && <TableRow><TableCell colSpan={4} className="text-center text-muted-foreground">Needs at least 3 contacted leads per segment.</TableCell></TableRow>}
                  </TableBody>
                </Table>
              </TabsContent>
            ))}
          </Tabs>
        </CardContent>
      </Card>
    </div>
  );
}
