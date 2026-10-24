"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Plus, Send } from "lucide-react";
import { toast } from "sonner";
import { EmptyState, PageHeader, StatusBadge, dateTime, num } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import { campaignsService } from "@/services/campaigns";

export default function CampaignsPage() {
  const canEdit = useCan("member");
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const campaigns = useQuery({ queryKey: ["campaigns"], queryFn: campaignsService.list });
  const mailboxes = useQuery({ queryKey: ["mailboxes"], queryFn: campaignsService.mailboxes });

  const create = useMutation({
    mutationFn: () => {
      const mb = mailboxes.data?.find((m) => m.status === "active");
      return campaignsService.create({
        name, mailbox_id: mb?.id ?? null, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
      });
    },
    onSuccess: (c) => router.push(`/campaigns/${c.id}?tab=sequence`),
    onError: (e) => toast.error(errorMessage(e)),
  });

  return (
    <>
      <PageHeader title="Campaigns" description="Email sequences sent gradually from your mailboxes."
        actions={canEdit && <Button onClick={() => setOpen(true)}><Plus /> New campaign</Button>} />

      {campaigns.isLoading ? <Skeleton className="h-64" /> : campaigns.data?.length === 0 ? (
        <EmptyState icon={Send} title="No campaigns yet">Create a campaign, write a sequence, add leads and launch.</EmptyState>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead><TableHead>Status</TableHead>
                <TableHead className="text-right">Leads</TableHead><TableHead className="text-right">In sequence</TableHead>
                <TableHead className="text-right">Sent</TableHead><TableHead className="text-right">Replies</TableHead>
                <TableHead className="text-right">Bounces</TableHead><TableHead>Daily limit</TableHead><TableHead>Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {campaigns.data?.map((c) => (
                <TableRow key={c.id}>
                  <TableCell className="font-medium"><Link href={`/campaigns/${c.id}`} className="hover:underline">{c.name}</Link></TableCell>
                  <TableCell><StatusBadge status={c.status} /></TableCell>
                  <TableCell className="text-right tabular-nums">{num(c.summary?.leads ?? 0)}</TableCell>
                  <TableCell className="text-right tabular-nums">{num(c.summary?.active ?? 0)}</TableCell>
                  <TableCell className="text-right tabular-nums">{num(c.summary?.sent ?? 0)}</TableCell>
                  <TableCell className="text-right tabular-nums">{num(c.summary?.replied ?? 0)}</TableCell>
                  <TableCell className="text-right tabular-nums">{num(c.summary?.bounced ?? 0)}</TableCell>
                  <TableCell className="tabular-nums">{c.daily_limit}/day</TableCell>
                  <TableCell>{dateTime(c.created_at)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New campaign</DialogTitle>
            <DialogDescription>You can configure the sequence, audience and schedule next.</DialogDescription>
          </DialogHeader>
          <form onSubmit={(e) => { e.preventDefault(); if (name.trim()) create.mutate(); }} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="cname">Campaign name</Label>
              <Input id="cname" autoFocus placeholder="Delhi restaurants — Q4" value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            {mailboxes.data?.length === 0 && (
              <p className="text-sm text-muted-foreground">You have no sending mailbox yet — add one in <Link className="underline" href="/settings?tab=mailboxes">Settings</Link> before launching.</p>
            )}
            <DialogFooter><Button type="submit" disabled={!name.trim() || create.isPending}>Create</Button></DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}
