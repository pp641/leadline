"use client";

import Link from "next/link";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Circle, ListTodo } from "lucide-react";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { dateTime } from "@/components/shared";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { Task } from "@/types/api";

export function TaskList({ assignee, leadId }: { assignee?: string; leadId?: string }) {
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const [status, setStatus] = useState("open");
  const tasks = useQuery({ queryKey: ["tasks", assignee, leadId, status], queryFn: () => api.get<Task[]>("/tasks", { assignee, lead_id: leadId, status }) });
  const toggle = useMutation({
    mutationFn: (t: Task) => api.put(`/tasks/${t.id}`, { lead_id: t.lead_id, assignee_id: t.assignee_id, title: t.title, notes: t.notes, due_at: t.due_at, completed: !t.completed_at }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tasks"] }),
    onError: (e) => toast.error(errorMessage(e)),
  });
  const overdue = (t: Task) => !t.completed_at && t.due_at && new Date(t.due_at) < new Date();
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="flex items-center gap-2 text-base"><ListTodo className="size-4" /> Tasks</CardTitle>
        <FilterSelect value={status} onChange={setStatus} placeholder="All" className="w-28" options={[{ value: "open", label: "Open" }, { value: "done", label: "Done" }]} />
      </CardHeader>
      <CardContent className="divide-y">
        {(tasks.data ?? []).map((t) => (
          <div key={t.id} className="flex items-start gap-2 py-2 text-sm">
            <button disabled={!canEdit} onClick={() => toggle.mutate(t)} aria-label={t.completed_at ? "Reopen" : "Complete"} className="mt-0.5">
              {t.completed_at ? <CheckCircle2 className="size-4 text-emerald-600" /> : <Circle className="size-4 text-muted-foreground" />}
            </button>
            <div className="min-w-0 flex-1">
              <p className={t.completed_at ? "text-muted-foreground line-through" : ""}>{t.title}</p>
              <p className="text-xs text-muted-foreground">
                {t.company_name && !leadId && <><Link className="underline" href={`/leads/${t.lead_id}`}>{t.company_name}</Link> · </>}
                {t.assignee_name ?? "Unassigned"}
                {t.due_at && <span className={overdue(t) ? "text-destructive" : ""}> · due {dateTime(t.due_at)}</span>}
              </p>
            </div>
          </div>
        ))}
        {tasks.data?.length === 0 && <p className="py-4 text-center text-sm text-muted-foreground">No {status} tasks.</p>}
      </CardContent>
    </Card>
  );
}
