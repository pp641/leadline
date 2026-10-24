"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Mail, Plus } from "lucide-react";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { EmptyState, StatusBadge, dateTime } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage } from "@/lib/api";
import { useCan } from "@/lib/auth";
import type { Mailbox } from "@/types/api";

type Form = {
  name: string; from_email: string; from_name: string; reply_to: string; daily_limit: number; status: "active" | "disabled";
  smtp_host: string; smtp_port: number; smtp_username: string; smtp_password: string; smtp_tls: "none" | "starttls" | "tls";
  imap_host: string; imap_port: number; imap_username: string; imap_password: string; imap_tls: boolean; inbox_sync_enabled: boolean;
};
const blank: Form = {
  name: "", from_email: "", from_name: "", reply_to: "", daily_limit: 50, status: "active",
  smtp_host: "", smtp_port: 587, smtp_username: "", smtp_password: "", smtp_tls: "starttls",
  imap_host: "", imap_port: 993, imap_username: "", imap_password: "", imap_tls: true, inbox_sync_enabled: false,
};

export function MailboxesTab() {
  const qc = useQueryClient();
  const canEdit = useCan("admin");
  const [editing, setEditing] = useState<Mailbox | "new" | null>(null);
  const [v, setV] = useState<Form>(blank);
  const list = useQuery({ queryKey: ["mailboxes"], queryFn: () => api.get<Mailbox[]>("/mailboxes") });

  const open = (m: Mailbox | "new") => {
    setEditing(m);
    setV(m === "new" ? blank : {
      name: m.name, from_email: m.from_email, from_name: m.from_name, reply_to: m.reply_to ?? "", daily_limit: m.daily_limit, status: m.status,
      smtp_host: m.smtp_host, smtp_port: m.smtp_port, smtp_username: m.smtp_username, smtp_password: "", smtp_tls: m.smtp_tls,
      imap_host: m.imap_host, imap_port: m.imap_port, imap_username: m.imap_username, imap_password: "", imap_tls: m.imap_tls,
      inbox_sync_enabled: m.inbox_sync_enabled,
    });
  };
  const save = useMutation({
    mutationFn: () => (editing === "new" ? api.post("/mailboxes", v) : api.put(`/mailboxes/${(editing as Mailbox).id}`, v)),
    onSuccess: () => { toast.success("Mailbox saved"); setEditing(null); qc.invalidateQueries({ queryKey: ["mailboxes"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const check = useMutation({
    mutationFn: (id: string) => api.post<{ smtp: string; imap: string }>(`/mailboxes/${id}/test`),
    onSuccess: (r) => toast.message(`SMTP: ${r.smtp} · IMAP: ${r.imap}`),
    onError: (e) => toast.error(errorMessage(e)),
  });
  const syncNow = useMutation({
    mutationFn: (id: string) => api.post(`/mailboxes/${id}/sync`),
    onSuccess: () => { toast.success("Inbox sync queued"); setTimeout(() => qc.invalidateQueries({ queryKey: ["mailboxes"] }), 4000); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.del(`/mailboxes/${id}`),
    onSuccess: () => { toast.success("Mailbox deleted"); qc.invalidateQueries({ queryKey: ["mailboxes"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div>
          <CardTitle className="text-base">Sending mailboxes</CardTitle>
          <CardDescription>
            The sender identity on outgoing email. Use a domain you control with SPF, DKIM and DMARC set up at your email provider; the address must be real and able to receive replies.
          </CardDescription>
        </div>
        {canEdit && <Button onClick={() => open("new")}><Plus /> Add mailbox</Button>}
      </CardHeader>
      <CardContent>
        {list.data?.length === 0 ? (
          <EmptyState icon={Mail} title="No mailboxes yet">Add the address your campaigns will send from.</EmptyState>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader><TableRow><TableHead>Name</TableHead><TableHead>From</TableHead><TableHead>Status</TableHead><TableHead className="text-right">Sent (24h) / limit</TableHead><TableHead /></TableRow></TableHeader>
              <TableBody>
                {(list.data ?? []).map((m) => (
                  <TableRow key={m.id}>
                    <TableCell className="font-medium">{m.name}</TableCell>
                    <TableCell>
                      {m.from_name} &lt;{m.from_email}&gt;
                      <span className="block text-xs text-muted-foreground">
                        {m.smtp_host ? `Own SMTP: ${m.smtp_host}` : "Server email provider"}
                        {m.inbox_sync_enabled && ` · Inbox sync ${m.sync_error ? "error" : m.last_synced_at ? `ok ${dateTime(m.last_synced_at)}` : "pending"}`}
                      </span>
                      {m.sync_error && <span className="block max-w-80 truncate text-xs text-destructive" title={m.sync_error}>{m.sync_error}</span>}
                    </TableCell>
                    <TableCell><StatusBadge status={m.status === "active" ? "active" : "stopped"} /></TableCell>
                    <TableCell className="text-right tabular-nums">{m.sent_last_24h} / {m.daily_limit}</TableCell>
                    <TableCell className="text-right">
                      {canEdit && <>
                        <Button size="sm" variant="ghost" onClick={() => check.mutate(m.id)} disabled={check.isPending}>Test</Button>
                        {m.inbox_sync_enabled && <Button size="sm" variant="ghost" onClick={() => syncNow.mutate(m.id)}>Sync</Button>}
                        <Button size="sm" variant="ghost" onClick={() => open(m)}>Edit</Button>
                        <Button size="sm" variant="ghost" onClick={() => confirm(`Delete ${m.from_email}?`) && remove.mutate(m.id)}>Delete</Button>
                      </>}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>

      <Dialog open={!!editing} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editing === "new" ? "Add mailbox" : "Edit mailbox"}</DialogTitle>
            <DialogDescription>Email is delivered through the provider configured on the server (EMAIL_PROVIDER).</DialogDescription>
          </DialogHeader>
          <form className="grid gap-3 sm:grid-cols-2" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
            <div className="space-y-1.5 sm:col-span-2"><Label>Label</Label><Input value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} placeholder="Sales outreach" /></div>
            <div className="space-y-1.5"><Label>From name</Label><Input value={v.from_name} onChange={(e) => setV({ ...v, from_name: e.target.value })} placeholder="Priya Sharma" /></div>
            <div className="space-y-1.5"><Label>From email</Label><Input type="email" value={v.from_email} onChange={(e) => setV({ ...v, from_email: e.target.value })} /></div>
            <div className="space-y-1.5"><Label>Reply-to (optional)</Label><Input type="email" value={v.reply_to} onChange={(e) => setV({ ...v, reply_to: e.target.value })} /></div>
            <div className="space-y-1.5"><Label>Daily limit</Label><Input type="number" min={0} max={10000} value={v.daily_limit} onChange={(e) => setV({ ...v, daily_limit: Number(e.target.value) })} /></div>
            <label className="flex items-center gap-2 text-sm sm:col-span-2">
              <Switch checked={v.status === "active"} onCheckedChange={(c) => setV({ ...v, status: c ? "active" : "disabled" })} /> Active
            </label>
            <p className="text-xs text-muted-foreground sm:col-span-2">New mailboxes should start low (20–50/day) and increase gradually as the domain builds a sending reputation.</p>
            <details className="rounded-lg border p-3 sm:col-span-2" open={!!v.smtp_host}>
              <summary className="cursor-pointer text-sm font-medium">Own SMTP server (optional)</summary>
              <p className="mt-1 text-xs text-muted-foreground">Send through this mailbox&apos;s own provider (e.g. Google Workspace, Microsoft 365, SES). Leave empty to use the server default.</p>
              <div className="mt-2 grid gap-2 sm:grid-cols-2">
                <Input placeholder="smtp.gmail.com" value={v.smtp_host} onChange={(e) => setV({ ...v, smtp_host: e.target.value })} />
                <Input type="number" placeholder="587" value={v.smtp_port} onChange={(e) => setV({ ...v, smtp_port: Number(e.target.value) })} />
                <Input placeholder="Username" autoComplete="off" value={v.smtp_username} onChange={(e) => setV({ ...v, smtp_username: e.target.value })} />
                <Input type="password" autoComplete="new-password" placeholder={editing && editing !== "new" && editing.has_smtp_password ? "•••••• (unchanged)" : "Password / app password"}
                  value={v.smtp_password} onChange={(e) => setV({ ...v, smtp_password: e.target.value })} />
                <FilterSelect value={v.smtp_tls} onChange={(x) => x && setV({ ...v, smtp_tls: x as Form["smtp_tls"] })} placeholder="TLS" className="w-full"
                  options={[{ value: "starttls", label: "STARTTLS (587)" }, { value: "tls", label: "TLS (465)" }, { value: "none", label: "None" }]} />
              </div>
            </details>
            <details className="rounded-lg border p-3 sm:col-span-2" open={v.inbox_sync_enabled}>
              <summary className="cursor-pointer text-sm font-medium">Inbox sync — detect replies & bounces (IMAP)</summary>
              <p className="mt-1 text-xs text-muted-foreground">Read-only: Leadline only stores messages that answer your campaign emails.</p>
              <div className="mt-2 grid gap-2 sm:grid-cols-2">
                <Input placeholder="imap.gmail.com" value={v.imap_host} onChange={(e) => setV({ ...v, imap_host: e.target.value })} />
                <Input type="number" placeholder="993" value={v.imap_port} onChange={(e) => setV({ ...v, imap_port: Number(e.target.value) })} />
                <Input placeholder="Username" autoComplete="off" value={v.imap_username} onChange={(e) => setV({ ...v, imap_username: e.target.value })} />
                <Input type="password" autoComplete="new-password" placeholder={editing && editing !== "new" && editing.has_imap_password ? "•••••• (unchanged)" : "Password / app password"}
                  value={v.imap_password} onChange={(e) => setV({ ...v, imap_password: e.target.value })} />
                <label className="flex items-center gap-2 text-sm"><Switch checked={v.imap_tls} onCheckedChange={(c) => setV({ ...v, imap_tls: c })} /> TLS</label>
                <label className="flex items-center gap-2 text-sm"><Switch checked={v.inbox_sync_enabled} onCheckedChange={(c) => setV({ ...v, inbox_sync_enabled: c })} /> Sync inbox every 2 minutes</label>
              </div>
            </details>
            <DialogFooter className="sm:col-span-2"><Button type="submit" disabled={save.isPending}>Save mailbox</Button></DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
