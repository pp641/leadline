"use client";

// Charts follow one rule set: one y-axis per chart, thin marks, recessive grid,
// series colors from CSS tokens (--series-N, light and dark steps), text in
// text tokens, and a hover tooltip on every chart.
import { Bar, BarChart, CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { TooltipContentProps } from "recharts";
import type { NameType, ValueType } from "recharts/types/component/DefaultTooltipContent";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { DailyPoint } from "@/types/api";

export function StatTile({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <Card className="gap-1 py-4">
      <CardContent className="px-4">
        <p className="text-xs font-medium text-muted-foreground">{label}</p>
        <p className="mt-1 text-2xl font-semibold tabular-nums tracking-tight">{value}</p>
        {hint && <p className="mt-0.5 text-xs text-muted-foreground">{hint}</p>}
      </CardContent>
    </Card>
  );
}

const axis = { stroke: "var(--muted-foreground)", fontSize: 11, tickLine: false, axisLine: false } as const;
const shortDate = (d: string) => new Date(d + "T00:00:00").toLocaleDateString(undefined, { month: "short", day: "numeric" });

function ChartTooltip({ active, payload, label }: TooltipContentProps<ValueType, NameType>) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-lg border bg-popover px-3 py-2 text-xs shadow-md">
      <p className="mb-1 font-medium text-popover-foreground">{shortDate(String(label))}</p>
      {payload.map((p) => (
        <p key={String(p.dataKey)} className="flex items-center gap-2 text-muted-foreground">
          <span className="inline-block size-2 rounded-full" style={{ background: p.color }} />
          <span>{p.name}</span>
          <span className="ml-auto font-medium tabular-nums text-popover-foreground">{p.value}</span>
        </p>
      ))}
    </div>
  );
}

export function SendsChart({ data }: { data: DailyPoint[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Emails sent per day</CardTitle>
        <CardDescription>Accepted by the email provider, UTC days</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="h-56">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={data} margin={{ top: 4, right: 4, left: -16, bottom: 0 }} barCategoryGap={2}>
              <CartesianGrid vertical={false} stroke="var(--border)" />
              <XAxis dataKey="date" tickFormatter={shortDate} minTickGap={24} {...axis} />
              <YAxis allowDecimals={false} width={44} {...axis} />
              <Tooltip content={ChartTooltip} cursor={{ fill: "var(--muted)", opacity: 0.6 }} />
              <Bar dataKey="sent" name="Sent" fill="var(--series-1)" radius={[4, 4, 0, 0]} maxBarSize={18} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </CardContent>
    </Card>
  );
}

export function RepliesChart({ data }: { data: DailyPoint[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Replies and bounces per day</CardTitle>
        <CardDescription>Events received, UTC days</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="h-56">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={data} margin={{ top: 4, right: 8, left: -16, bottom: 0 }}>
              <CartesianGrid vertical={false} stroke="var(--border)" />
              <XAxis dataKey="date" tickFormatter={shortDate} minTickGap={24} {...axis} />
              <YAxis allowDecimals={false} width={44} {...axis} />
              <Tooltip content={ChartTooltip} cursor={{ stroke: "var(--muted-foreground)", strokeDasharray: "3 3" }} />
              <Legend iconType="plainline" wrapperStyle={{ fontSize: 12, color: "var(--muted-foreground)" }} />
              <Line type="monotone" dataKey="replied" name="Replies" stroke="var(--series-1)" strokeWidth={2} dot={false}
                activeDot={{ r: 4, stroke: "var(--card)", strokeWidth: 2 }} />
              <Line type="monotone" dataKey="bounced" name="Bounces" stroke="var(--series-2)" strokeWidth={2} dot={false}
                strokeDasharray="5 3" activeDot={{ r: 4, stroke: "var(--card)", strokeWidth: 2 }} />
            </LineChart>
          </ResponsiveContainer>
        </div>
      </CardContent>
    </Card>
  );
}
