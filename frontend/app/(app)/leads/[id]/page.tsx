"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Ban, Globe, Mail, MapPin, Pencil, Phone, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { LeadDeals } from "@/components/crm/deals-board";
import { EnrichmentCard, FitCard, LeadTasks, NotesCard, OwnerSelect } from "@/components/lead/crm-panels";
import { LeadFormDialog } from "@/components/lead-form-dialog";
import { PageHeader, ScoreBadge, StatusBadge, dateTime } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import { leadsService } from "@/services/leads";
import { LEAD_STATUSES, type Contact, type LeadEvent } from "@/types/api";

export default function LeadDetailPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const qc = useQueryClient();
  const canEdit = useCan("member");
  const [editing, setEditing] = useState(false);
  const [contactDialog, setContactDialog] = useState<Contact | "new" | null>(null);

  const lead = useQuery({ queryKey: ["lead", id], queryFn: () => leadsService.get(id) });
  const events = useQuery({ queryKey: ["lead", id, "events"], queryFn: () => leadsService.events(id) });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["lead", id] });
    qc.invalidateQueries({ queryKey: ["leads"] });
  };

  const setStatus = useMutation({
    mutationFn: (status: string) => leadsService.update(id, { ...lead.data, status, contact: undefined }),
    onSuccess: () => { toast.success("Status updated"); refresh(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const remove = useMutation({
    mutationFn: () => leadsService.remove(id),
    onSuccess: () => { toast.success("Lead deleted"); qc.invalidateQueries({ queryKey: ["leads"] }); router.push("/leads"); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const contactAction = useMutation({
    mutationFn: async ({ kind, c }: { kind: "delete" | "verify"; c: Contact }) =>
      kind === "delete" ? api.del(`/contacts/${c.id}`) : api.post("/contacts/verify", { contact_ids: [c.id] }),
    onSuccess: (_, v) => { toast.success(v.kind === "delete" ? "Contact removed" : "Verification queued"); refresh(); },
    onError: (e) => toast.error(errorMessage(e)),
  });

  if (lead.isLoading) return <Skeleton className="h-96" />;
  if (!lead.data) return <p className="text-muted-foreground">Lead not found.</p>;
  const l = lead.data;

  return (
    <>
      <Button variant="ghost" size="sm" asChild className="mb-2 -ml-2">
        <Link href="/leads"><ArrowLeft /> Leads</Link>
      </Button>
      <PageHeader
        title={l.company_name}
        description={[l.industry, [l.city, l.state, l.country].filter(Boolean).join(", ")].filter(Boolean).join(" · ")}
        actions={canEdit && (
          <>
            <OwnerSelect lead={l} />
            <FilterSelect value={l.status} onChange={(s) => s && setStatus.mutate(s)} placeholder="Status" className="w-36"
              options={LEAD_STATUSES.map((s) => ({ value: s, label: s }))} />
            <Button variant="outline" onClick={() => setEditing(true)}><Pencil /> Edit</Button>
            <Button variant="destructive" onClick={() => confirm("Delete this lead and all its history?") && remove.mutate()}><Trash2 /> Delete</Button>
          </>
        )}
      />

      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <Card>
            <CardHeader><CardTitle className="text-base">Company</CardTitle></CardHeader>
            <CardContent className="grid gap-3 text-sm sm:grid-cols-2">
              <Info icon={Globe} label="Website">{l.website ? <a className="hover:underline" href={l.website} target="_blank" rel="noreferrer noopener">{l.domain}</a> : "No website"}</Info>
              <Info icon={Phone} label="Phone">{l.phone || "—"}</Info>
              <Info icon={Mail} label="Business email">{l.business_email || "—"}</Info>
              <Info icon={MapPin} label="Address">{l.address || "—"}</Info>
              <Info label="Employees">{l.employee_count ?? "—"}</Info>
              <Info label="Founded / opened">{l.founded_year ?? "—"}</Info>
              <Info label="Source">{l.source}{l.source_url && <> · <a className="hover:underline" href={l.source_url} target="_blank" rel="noreferrer noopener">link</a></>}</Info>
              <Info label="Tags">{l.tags.length ? l.tags.join(", ") : "—"}</Info>
              {l.description && <p className="text-muted-foreground sm:col-span-2">{l.description}</p>}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="flex flex-row items-center justify-between">
              <CardTitle className="text-base">Contacts</CardTitle>
              {canEdit && <Button size="sm" variant="outline" onClick={() => setContactDialog("new")}><Plus /> Add contact</Button>}
            </CardHeader>
            <CardContent className="divide-y">
              {l.contacts.length === 0 && <p className="text-sm text-muted-foreground">No contacts yet.</p>}
              {l.contacts.map((c) => (
                <div key={c.id} className="flex flex-wrap items-center gap-3 py-3 first:pt-0 last:pb-0">
                  <div className="min-w-0 flex-1">
                    <p className="font-medium">
                      {[c.first_name, c.last_name].filter(Boolean).join(" ") || "Unnamed"}
                      {c.is_primary && <span className="ml-2 text-xs font-normal text-muted-foreground">Primary</span>}
                    </p>
                    <p className="text-sm text-muted-foreground">{[c.job_title, c.email].filter(Boolean).join(" · ")}</p>
                  </div>
                  {c.email && (
                    <Tooltip>
                      <TooltipTrigger asChild><span><StatusBadge status={c.email_status} /></span></TooltipTrigger>
                      <TooltipContent>{c.verification_details?.reason ?? "Not verified yet"}</TooltipContent>
                    </Tooltip>
                  )}
                  {c.suppressed && <span className="flex items-center gap-1 text-xs text-destructive"><Ban className="size-3" /> Suppressed</span>}
                  {canEdit && (
                    <div className="flex gap-1">
                      {c.email && <Button size="icon-sm" variant="ghost" aria-label="Re-verify email" onClick={() => contactAction.mutate({ kind: "verify", c })}><RefreshCw /></Button>}
                      <Button size="icon-sm" variant="ghost" aria-label="Edit contact" onClick={() => setContactDialog(c)}><Pencil /></Button>
                      <Button size="icon-sm" variant="ghost" aria-label="Delete contact" onClick={() => confirm("Remove this contact?") && contactAction.mutate({ kind: "delete", c })}><Trash2 /></Button>
                    </div>
                  )}
                </div>
              ))}
            </CardContent>
          </Card>
          <LeadTasks leadId={id} />
          <NotesCard leadId={id} />
        </div>

        <div className="space-y-4">
          <LeadDeals lead={l} />
          <FitCard lead={l} />
          <EnrichmentCard lead={l} />
          <Card>
            <CardHeader className="flex flex-row items-center justify-between">
              <CardTitle className="text-base">Lead score</CardTitle>
              <ScoreBadge score={l.score.score} />
            </CardHeader>
            <CardContent>
              {l.score.reasons.length === 0 ? (
                <p className="text-sm text-muted-foreground">No scoring rules matched.</p>
              ) : (
                <ul className="space-y-1 text-sm">{l.score.reasons.map((r) => <li key={r}>• {r}</li>)}</ul>
              )}
              <p className="mt-3 text-xs text-muted-foreground">Rules version {l.score.score_version || "—"} · <Link className="underline" href="/settings?tab=scoring">Edit rules</Link></p>
            </CardContent>
          </Card>
          <Card>
            <CardHeader><CardTitle className="text-base">Activity</CardTitle></CardHeader>
            <CardContent>
              <ol className="space-y-3 text-sm">
                {(events.data ?? []).map((e) => (
                  <li key={e.id} className="border-l-2 pl-3">
                    <p className="font-medium">{describe(e)}</p>
                    <p className="text-xs text-muted-foreground">{dateTime(e.created_at)}{e.actor_name && ` · ${e.actor_name}`}</p>
                  </li>
                ))}
                {events.data?.length === 0 && <li className="text-muted-foreground">No activity yet.</li>}
              </ol>
            </CardContent>
          </Card>
        </div>
      </div>

      <LeadFormDialog open={editing} onOpenChange={setEditing} lead={l} />
      <ContactDialog leadId={id} contact={contactDialog} onClose={() => { setContactDialog(null); refresh(); }} />
    </>
  );
}

function Info({ icon: Icon, label, children }: { icon?: React.ElementType; label: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">{Icon && <Icon className="size-3" />}{label}</p>
      <p className="mt-0.5 break-words">{children}</p>
    </div>
  );
}

function describe(e: LeadEvent): string {
  const d = e.data as Record<string, string>;
  switch (e.event_type) {
    case "created": return "Lead created";
    case "imported": return `Imported from ${d.filename ?? "CSV"}`;
    case "status_changed": return `Status changed${d.from ? ` from ${d.from}` : ""} to ${d.to}`;
    case "email_sent": return `Email sent (step ${d.step}): ${d.subject ?? ""}`;
    case "email_bounced": return "Email bounced — address suppressed";
    case "email_soft_bounced": return "Temporary delivery failure";
    case "replied": return `Replied${d.classification ? ` (${d.classification.replace("_", " ")})` : ""}`;
    case "unsubscribed": return "Unsubscribed";
    case "spam_complaint": return "Marked an email as spam — address suppressed";
    case "contact_added": return `Contact added: ${d.name || "unnamed"}`;
    case "discovered": return d.source === "company_db" ? `Imported from the company database (${d.verification_status})` : `Discovered via ${String(d.provider).replace("_", " ")}`;
    case "enriched": return `Enriched — website ${d.website_quality || "not analysed"}${Number(d.contacts_added) ? `, ${d.contacts_added} contacts found` : ""}`;
    case "auto_reply": return "Auto-reply received (sequence continues)";
    case "owner_changed": return "Owner changed";
    case "note_added": return "Note added";
    case "task_created": return `Task created: ${d.title}`;
    case "task_completed": return `Task completed: ${d.title}`;
    case "crm_synced": return "Synced to HubSpot";
    default: return e.event_type.replaceAll("_", " ");
  }
}

function ContactDialog({ leadId, contact, onClose }: { leadId: string; contact: Contact | "new" | null; onClose: () => void }) {
  const editing = contact && contact !== "new" ? contact : null;
  const [v, setV] = useState({ first_name: "", last_name: "", job_title: "", email: "", is_primary: false });
  const [loadedFor, setLoadedFor] = useState<Contact | "new" | null>(null);
  if (contact !== loadedFor) {
    setLoadedFor(contact);
    setV(editing
      ? { first_name: editing.first_name, last_name: editing.last_name, job_title: editing.job_title, email: editing.email ?? "", is_primary: editing.is_primary }
      : { first_name: "", last_name: "", job_title: "", email: "", is_primary: false });
  }
  const save = useMutation({
    mutationFn: () => (editing ? api.put(`/contacts/${editing.id}`, v) : api.post(`/leads/${leadId}/contacts`, v)),
    onSuccess: () => { toast.success("Contact saved — email verification queued"); onClose(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <Dialog open={!!contact} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader><DialogTitle>{editing ? "Edit contact" : "Add contact"}</DialogTitle></DialogHeader>
        <form className="grid gap-3 sm:grid-cols-2" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
          {(["first_name", "last_name", "job_title", "email"] as const).map((k) => (
            <div key={k} className="space-y-1.5">
              <Label htmlFor={k} className="capitalize">{k.replace("_", " ")}</Label>
              <Input id={k} type={k === "email" ? "email" : "text"} value={v[k]} onChange={(e) => setV({ ...v, [k]: e.target.value })} />
            </div>
          ))}
          <label className="flex items-center gap-2 text-sm sm:col-span-2">
            <input type="checkbox" checked={v.is_primary} onChange={(e) => setV({ ...v, is_primary: e.target.checked })} /> Primary contact
          </label>
          <DialogFooter className="sm:col-span-2"><Button type="submit" disabled={save.isPending}>Save contact</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
