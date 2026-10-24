"use client";

import Link from "next/link";
import { useState } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, MessageSquareReply, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { AddAudienceDialog } from "@/components/campaign/add-audience-dialog";
import { FilterSelect } from "@/components/filter-select";
import { StatusBadge, dateTime, num } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import { REPLY_CLASSIFICATIONS, campaignsService } from "@/services/campaigns";
import type { Campaign, CampaignLead } from "@/types/api";

const CL_STATUSES = ["pending", "active", "sending", "completed", "replied", "bounced", "unsubscribed", "suppressed", "failed", "stopped"];

export function AudienceTab({ campaign }: { campaign: Campaign }) {
  const qc = useQueryClient();
  const canEdit = useCan("member") && !["completed", "cancelled"].includes(campaign.status);
  const [status, setStatus] = useState("");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [adding, setAdding] = useState(false);
  const [replyFor, setReplyFor] = useState<CampaignLead | null>(null);

  const list = useQuery({
    queryKey: ["campaign", campaign.id, "leads", status, q, page],
    queryFn: () => campaignsService.leads(campaign.id, page, status, q),
    placeholderData: keepPreviousData,
  });
  const rows = list.data?.leads ?? [];
  const total = list.data?.meta.total ?? 0;

  const remove = useMutation({
    mutationFn: () => campaignsService.removeLeads(campaign.id, [...selected]),
    onSuccess: (r) => {
      toast.success(`Removed ${r.removed} leads`);
      setSelected(new Set());
      qc.invalidateQueries({ queryKey: ["campaign", campaign.id] });
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <Input placeholder="Search company or email" className="w-full sm:w-64" value={q} onChange={(e) => { setQ(e.target.value); setPage(1); }} />
        <FilterSelect value={status} onChange={(v) => { setStatus(v); setPage(1); }} placeholder="All statuses" options={CL_STATUSES.map((s) => ({ value: s, label: s }))} />
        <div className="ml-auto flex gap-2">
          {canEdit && selected.size > 0 && (
            <Button variant="outline" onClick={() => remove.mutate()}><Trash2 /> Remove {selected.size}</Button>
          )}
          {canEdit && <Button onClick={() => setAdding(true)}><Plus /> Add leads</Button>}
        </div>
      </div>

      <div className="overflow-x-auto rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              {canEdit && <TableHead className="w-10" />}
              <TableHead>Company</TableHead><TableHead>Recipient</TableHead><TableHead>Email status</TableHead>
              <TableHead>Status</TableHead><TableHead>Next</TableHead><TableHead>Last sent</TableHead><TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((r) => (
              <TableRow key={r.id}>
                {canEdit && (
                  <TableCell>
                    <Checkbox aria-label={`Select ${r.company_name}`} checked={selected.has(r.lead_id)} onCheckedChange={(c) => setSelected((s) => {
                      const n = new Set(s);
                      if (c) n.add(r.lead_id);
                      else n.delete(r.lead_id);
                      return n;
                    })} />
                  </TableCell>
                )}
                <TableCell className="font-medium"><Link href={`/leads/${r.lead_id}`} className="hover:underline">{r.company_name}</Link></TableCell>
                <TableCell>
                  <span className="block">{r.contact_name || "—"}</span>
                  <span className="text-xs text-muted-foreground">{r.email}</span>
                </TableCell>
                <TableCell><StatusBadge status={r.email_status} /></TableCell>
                <TableCell>
                  <StatusBadge status={r.status} />
                  {r.stop_reason && <span className="block max-w-48 truncate text-xs text-muted-foreground" title={r.stop_reason}>{r.stop_reason}</span>}
                </TableCell>
                <TableCell className="text-sm">{["active", "pending"].includes(r.status) ? <>Email {r.next_step_number}<span className="block text-xs text-muted-foreground">{dateTime(r.next_send_at)}</span></> : "—"}</TableCell>
                <TableCell className="text-sm">{dateTime(r.last_sent_at)}</TableCell>
                <TableCell>
                  {canLogReply(r) && (
                    <Button size="sm" variant="ghost" onClick={() => setReplyFor(r)}><MessageSquareReply /> Log reply</Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
            {rows.length === 0 && (
              <TableRow><TableCell colSpan={8} className="py-10 text-center text-muted-foreground">No leads{status || q ? " match" : " yet — add some from your lead list"}.</TableCell></TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      {total > 50 && (
        <div className="flex items-center justify-end gap-2 text-sm">
          <span className="text-muted-foreground">{num(total)} leads · page {page}</span>
          <Button size="icon-sm" variant="outline" aria-label="Previous page" disabled={page <= 1} onClick={() => setPage(page - 1)}><ChevronLeft /></Button>
          <Button size="icon-sm" variant="outline" aria-label="Next page" disabled={page * 50 >= total} onClick={() => setPage(page + 1)}><ChevronRight /></Button>
        </div>
      )}

      <AddAudienceDialog campaign={campaign} open={adding} onClose={() => setAdding(false)} />
      <ReplyDialog campaignId={campaign.id} lead={replyFor} onClose={() => setReplyFor(null)} />
    </div>
  );
}

// A reply can be logged once at least one email went out and the lead hasn't already replied/opted out.
function canLogReply(r: CampaignLead) {
  return !!r.last_sent_at && !["replied", "unsubscribed", "bounced"].includes(r.status);
}

function ReplyDialog({ campaignId, lead, onClose }: { campaignId: string; lead: CampaignLead | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [classification, setClassification] = useState("interested");
  const [note, setNote] = useState("");
  const save = useMutation({
    mutationFn: () => campaignsService.recordReply(lead!.id, classification, note),
    onSuccess: () => {
      toast.success("Reply recorded — the sequence stops for this lead");
      qc.invalidateQueries({ queryKey: ["campaign", campaignId] });
      setNote("");
      onClose();
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <Dialog open={!!lead} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Log a reply from {lead?.company_name}</DialogTitle>
          <DialogDescription>Automatic reply detection arrives with inbox sync; until then, log replies you received here.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1"><Label>Classification</Label>
            <FilterSelect className="w-full" value={classification} onChange={(v) => v && setClassification(v)} placeholder="Choose"
              options={REPLY_CLASSIFICATIONS.map((c) => ({ value: c, label: c.replace("_", " ") }))} /></div>
          <div className="space-y-1"><Label>Note (optional)</Label><Textarea rows={3} value={note} onChange={(e) => setNote(e.target.value)} /></div>
          {["unsubscribe", "spam"].includes(classification) && (
            <p className="text-sm text-muted-foreground">This address will also be added to your suppression list.</p>
          )}
        </div>
        <DialogFooter><Button disabled={save.isPending} onClick={() => save.mutate()}>Save reply</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
