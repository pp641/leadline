// Small presentational pieces shared by many pages.
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

export function PageHeader({ title, description, actions }: { title: string; description?: string; actions?: React.ReactNode }) {
  return (
    <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}

export function EmptyState({ icon: Icon, title, children }: { icon: React.ElementType; title: string; children?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center rounded-xl border border-dashed px-6 py-14 text-center">
      <Icon className="mb-3 size-8 text-muted-foreground" />
      <h3 className="font-medium">{title}</h3>
      {children && <div className="mt-2 max-w-md text-sm text-muted-foreground">{children}</div>}
    </div>
  );
}

// Status badges always pair color with a text label (never color alone).
const tone: Record<string, string> = {
  // campaign
  draft: "bg-muted text-muted-foreground",
  scheduled: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  running: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  paused: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  completed: "bg-muted text-foreground",
  cancelled: "bg-muted text-muted-foreground line-through",
  // email verification
  valid: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  role_based: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  risky: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  unknown: "bg-muted text-muted-foreground",
  unverified: "bg-muted text-muted-foreground",
  invalid: "bg-red-500/15 text-red-700 dark:text-red-300",
  disposable: "bg-red-500/15 text-red-700 dark:text-red-300",
  // lead / campaign-lead
  new: "bg-muted text-foreground",
  contacted: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  replied: "bg-violet-500/15 text-violet-700 dark:text-violet-300",
  interested: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  qualified: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  meeting: "bg-emerald-500/20 text-emerald-800 dark:text-emerald-200",
  won: "bg-emerald-600 text-white",
  lost: "bg-muted text-muted-foreground",
  unsubscribed: "bg-red-500/15 text-red-700 dark:text-red-300",
  pending: "bg-muted text-muted-foreground",
  active: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  sending: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  bounced: "bg-red-500/15 text-red-700 dark:text-red-300",
  suppressed: "bg-red-500/15 text-red-700 dark:text-red-300",
  failed: "bg-red-500/15 text-red-700 dark:text-red-300",
  stopped: "bg-muted text-muted-foreground",
  hard_bounce: "bg-red-500/15 text-red-700 dark:text-red-300",
  complaint: "bg-red-500/15 text-red-700 dark:text-red-300",
  manual: "bg-muted text-foreground",
  erased: "bg-muted text-muted-foreground",
  processing: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  queued: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  previewed: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
};

export function StatusBadge({ status, className }: { status: string; className?: string }) {
  return (
    <Badge variant="secondary" className={cn("font-normal capitalize", tone[status], className)}>
      {status.replaceAll("_", " ")}
    </Badge>
  );
}

export function ScoreBadge({ score }: { score: number }) {
  const cls = score >= 60 ? "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300" : score >= 30 ? "bg-amber-500/15 text-amber-700 dark:text-amber-300" : "bg-muted text-muted-foreground";
  return <span className={cn("inline-flex min-w-9 justify-center rounded-md px-1.5 py-0.5 text-xs font-semibold tabular-nums", cls)}>{score}</span>;
}

export const pct = (v: number) => `${(v * 100).toFixed(v > 0 && v < 0.1 ? 1 : 0)}%`;
export const num = (v: number) => v.toLocaleString();
export const dateTime = (s: string | null) => (s ? new Date(s).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" }) : "—");
