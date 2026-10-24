"use client";

import { useState } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { StatusBadge, dateTime, num } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { PageMeta, Suppression } from "@/types/api";

export function SuppressionTab() {
  const qc = useQueryClient();
  const canAdd = useCan("member");
  const canRemove = useCan("admin");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const [bulk, setBulk] = useState("");
  const list = useQuery({
    queryKey: ["suppressions", q, page],
    queryFn: () => api.page<Suppression[]>("/suppressions", { q, page, per_page: 50 }),
    placeholderData: keepPreviousData,
  });
  const meta = list.data?.meta as unknown as PageMeta | undefined;

  const add = useMutation({
    mutationFn: () => api.post<{ added: number }>("/suppressions", { emails: bulk.split(/[\s,;]+/).filter(Boolean) }),
    onSuccess: (r) => { toast.success(`Suppressed ${num(r.added)} addresses`); setBulk(""); qc.invalidateQueries({ queryKey: ["suppressions"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.del(`/suppressions/${id}`),
    onSuccess: () => { toast.success("Removed"); qc.invalidateQueries({ queryKey: ["suppressions"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });

  return (
    <div className="grid gap-4 lg:grid-cols-3">
      <Card className="lg:col-span-2">
        <CardHeader>
          <CardTitle className="text-base">Suppression list</CardTitle>
          <CardDescription>Addresses that will never be emailed. Unsubscribes, hard bounces and spam complaints are added automatically.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <Input placeholder="Search email" value={q} onChange={(e) => { setQ(e.target.value); setPage(1); }} className="max-w-xs" />
          <div className="overflow-x-auto">
            <Table>
              <TableHeader><TableRow><TableHead>Email</TableHead><TableHead>Reason</TableHead><TableHead>Added</TableHead><TableHead /></TableRow></TableHeader>
              <TableBody>
                {(list.data?.data ?? []).map((s) => (
                  <TableRow key={s.id}>
                    <TableCell>{s.email}</TableCell>
                    <TableCell><StatusBadge status={s.reason} /></TableCell>
                    <TableCell>{dateTime(s.created_at)}</TableCell>
                    <TableCell className="text-right">
                      {canRemove && (s.reason === "manual" || s.reason === "hard_bounce") && (
                        <Button size="sm" variant="ghost" onClick={() => confirm(`Allow emailing ${s.email} again?`) && remove.mutate(s.id)}>Remove</Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
                {list.data?.data.length === 0 && <TableRow><TableCell colSpan={4} className="py-6 text-center text-muted-foreground">Empty</TableCell></TableRow>}
              </TableBody>
            </Table>
          </div>
          {meta && meta.total > 50 && (
            <div className="flex items-center justify-end gap-2 text-sm">
              <span className="text-muted-foreground">{num(meta.total)} addresses</span>
              <Button size="sm" variant="outline" disabled={page <= 1} onClick={() => setPage(page - 1)}>Previous</Button>
              <Button size="sm" variant="outline" disabled={page * 50 >= meta.total} onClick={() => setPage(page + 1)}>Next</Button>
            </div>
          )}
          <p className="text-xs text-muted-foreground">Unsubscribe, complaint and erasure entries reflect the recipient&apos;s choice and cannot be removed.</p>
        </CardContent>
      </Card>
      {canAdd && (
        <Card>
          <CardHeader><CardTitle className="text-base">Add addresses</CardTitle><CardDescription>One per line, or comma-separated.</CardDescription></CardHeader>
          <CardContent className="space-y-3">
            <Textarea rows={8} value={bulk} onChange={(e) => setBulk(e.target.value)} placeholder={"someone@example.com\nother@example.com"} />
            <Button disabled={!bulk.trim() || add.isPending} onClick={() => add.mutate()}>Suppress</Button>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
