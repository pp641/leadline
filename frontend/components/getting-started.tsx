"use client";

import Link from "next/link";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, CircleCheck, Rocket, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { OnboardingStatus } from "@/types/api";

/** Dashboard checklist of setup tasks, computed by the server from real data. */
export function GettingStarted() {
  const qc = useQueryClient();
  const canAdmin = useCan("admin");
  const q = useQuery({ queryKey: ["onboarding"], queryFn: () => api.get<OnboardingStatus>("/onboarding") });
  const dismiss = useMutation({
    mutationFn: () => api.put<OnboardingStatus>("/onboarding", { checklist_dismissed: true }),
    onSuccess: (d) => qc.setQueryData(["onboarding"], d),
  });
  const s = q.data;
  if (!s || s.state.checklist_dismissed) return null;
  const total = s.checklist.length;
  const pct = Math.round((s.completed / total) * 100);
  const next = s.checklist.find((c) => !c.done);
  return (
    <Card className="mb-6 overflow-hidden">
      <CardHeader className="flex flex-row items-start gap-4">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Rocket className="size-5" /></span>
        <div className="min-w-0 flex-1 space-y-1">
          <CardTitle className="text-base">{next ? "Finish setting up Leadline" : "Setup complete"}</CardTitle>
          <CardDescription>{s.completed} of {total} done{next ? ` · Next: ${next.title.toLowerCase()}` : " — you're ready to go."}</CardDescription>
          <div className="mt-2 h-1.5 max-w-md overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-[var(--series-3)] transition-all" style={{ width: `${pct}%` }} /></div>
        </div>
        {canAdmin && <Button size="icon-sm" variant="ghost" aria-label="Hide checklist" onClick={() => dismiss.mutate()}><X /></Button>}
      </CardHeader>
      <CardContent>
        <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
          {s.checklist.map((c) => (
            <li key={c.key}>
              <Link href={c.href} className={`group flex h-full items-start gap-2.5 rounded-lg border p-3 transition hover:border-primary/40 hover:bg-muted/40 ${c.done ? "opacity-60" : ""}`}>
                {c.done ? <CircleCheck className="mt-0.5 size-4 shrink-0 text-[var(--series-3)]" /> : <span className="mt-0.5 size-4 shrink-0 rounded-full border-2" />}
                <span className="min-w-0 flex-1">
                  <span className={`block text-sm font-medium ${c.done ? "line-through" : ""}`}>{c.title}</span>
                  <span className="block text-xs text-muted-foreground">{c.hint}</span>
                </span>
                {!c.done && <ArrowRight className="mt-0.5 size-3.5 shrink-0 text-muted-foreground opacity-0 transition group-hover:opacity-100" />}
              </Link>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}
