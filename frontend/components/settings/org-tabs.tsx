"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { FilterSelect } from "@/components/filter-select";
import { dateTime } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage } from "@/lib/api";
import { useAuth, useCan } from "@/lib/auth";
import type { Invitation, Member, Organization } from "@/types/api";

export function OrganizationTab() {
  const { org } = useAuth();
  // Keyed so the form re-initializes whenever the saved organization changes.
  return org ? <OrganizationForm key={JSON.stringify(org)} org={org} /> : null;
}

function OrganizationForm({ org }: { org: Organization }) {
  const { reloadOrgs } = useAuth();
  const canEdit = useCan("admin");
  const [v, setV] = useState({ name: org.name, legal_name: org.legal_name, physical_address: org.physical_address, daily_send_limit: org.daily_send_limit });
  const save = useMutation({
    mutationFn: () => api.patch<Organization>("/organizations/current", v),
    onSuccess: async () => { toast.success("Organization saved"); await reloadOrgs(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <Card className="max-w-2xl">
      <CardHeader>
        <CardTitle className="text-base">Organization</CardTitle>
        <CardDescription>Your legal name and postal address appear in every email footer.</CardDescription>
      </CardHeader>
      <CardContent>
        <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
          <fieldset disabled={!canEdit} className="space-y-4">
            <div className="space-y-1.5"><Label>Name</Label><Input value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} /></div>
            <div className="space-y-1.5"><Label>Legal entity name</Label><Input value={v.legal_name} onChange={(e) => setV({ ...v, legal_name: e.target.value })} /></div>
            <div className="space-y-1.5"><Label>Postal address</Label><Textarea rows={3} value={v.physical_address} onChange={(e) => setV({ ...v, physical_address: e.target.value })} /></div>
            <div className="space-y-1.5">
              <Label>Organization daily send limit</Label>
              <Input type="number" min={0} value={v.daily_send_limit} onChange={(e) => setV({ ...v, daily_send_limit: Number(e.target.value) })} className="max-w-40" />
              <p className="text-xs text-muted-foreground">Upper bound across all campaigns and mailboxes, per rolling 24 hours.</p>
            </div>
            {canEdit && <Button type="submit" disabled={save.isPending}>Save</Button>}
          </fieldset>
        </form>
      </CardContent>
    </Card>
  );
}

export function MembersTab() {
  const qc = useQueryClient();
  const { user } = useAuth();
  const canEdit = useCan("admin");
  const members = useQuery({ queryKey: ["members"], queryFn: () => api.get<Member[]>("/organizations/current/members") });
  const mutate = useMutation({
    mutationFn: (a: { kind: "role"; id: string; role: string } | { kind: "remove"; id: string }) =>
      a.kind === "role" ? api.patch(`/organizations/current/members/${a.id}`, { role: a.role })
        : api.del(`/organizations/current/members/${a.id}`),
    onSuccess: () => { toast.success("Members updated"); qc.invalidateQueries({ queryKey: ["members"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const roles = ["owner", "admin", "member", "viewer"].map((r) => ({ value: r, label: r }));
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Members</CardTitle>
        <CardDescription>Owners and admins manage settings; members run campaigns; viewers have read-only access.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {canEdit && <InviteForm />}
        <div className="overflow-x-auto">
          <Table>
            <TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Email</TableHead><TableHead>Role</TableHead><TableHead /></TableRow></TableHeader>
            <TableBody>
              {(members.data ?? []).map((m) => (
                <TableRow key={m.user_id}>
                  <TableCell>{m.full_name}{m.user_id === user?.id && <span className="text-muted-foreground"> (you)</span>}</TableCell>
                  <TableCell>{m.email}</TableCell>
                  <TableCell>
                    {canEdit ? <FilterSelect value={m.role} onChange={(r) => r && mutate.mutate({ kind: "role", id: m.user_id, role: r })} placeholder="Role" options={roles} className="w-32" />
                      : <span className="capitalize">{m.role}</span>}
                  </TableCell>
                  <TableCell className="text-right">
                    {canEdit && m.user_id !== user?.id && (
                      <Button size="sm" variant="ghost" onClick={() => confirm(`Remove ${m.email}?`) && mutate.mutate({ kind: "remove", id: m.user_id })}>Remove</Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        {canEdit && <Invitations />}
      </CardContent>
    </Card>
  );
}

interface AuditRecord { id: number; user_email: string | null; action: string; entity_type: string; ip: string; created_at: string }

export function ComplianceTab() {
  const canAdmin = useCan("admin");
  const [email, setEmail] = useState("");
  const audit = useQuery({ queryKey: ["audit"], queryFn: () => api.get<AuditRecord[]>("/organizations/current/audit-logs", { per_page: 100 }), enabled: canAdmin });
  const erase = useMutation({
    mutationFn: () => api.post("/compliance/erase", { email }),
    onSuccess: () => { toast.success("Personal data erased and address suppressed"); setEmail(""); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  if (!canAdmin) return <p className="text-sm text-muted-foreground">Admins only.</p>;
  return (
    <div className="space-y-4">
      <Card className="max-w-2xl">
        <CardHeader>
          <CardTitle className="text-base">Erasure request</CardTitle>
          <CardDescription>
            Deletes every contact record with this address, redacts it from sent-email history, and keeps only a suppression entry so it is never emailed again.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="flex flex-wrap gap-2" onSubmit={(e) => {
            e.preventDefault();
            if (confirm(`Permanently erase all data for ${email}? This cannot be undone.`)) erase.mutate();
          }}>
            <Input type="email" placeholder="person@company.com" value={email} onChange={(e) => setEmail(e.target.value)} className="w-full sm:w-72" />
            <Button type="submit" variant="destructive" disabled={!email || erase.isPending}>Erase data</Button>
          </form>
          <p className="mt-3 text-xs text-muted-foreground">To export data, use Leads → Export CSV.</p>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle className="text-base">Audit log</CardTitle></CardHeader>
        <CardContent className="overflow-x-auto">
          <Table>
            <TableHeader><TableRow><TableHead>When</TableHead><TableHead>Who</TableHead><TableHead>Action</TableHead><TableHead>IP</TableHead></TableRow></TableHeader>
            <TableBody>
              {(audit.data ?? []).map((a) => (
                <TableRow key={a.id}>
                  <TableCell className="whitespace-nowrap">{dateTime(a.created_at)}</TableCell>
                  <TableCell>{a.user_email ?? "system"}</TableCell>
                  <TableCell><code className="text-xs">{a.action}</code></TableCell>
                  <TableCell className="text-muted-foreground">{a.ip}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}

function InviteForm() {
  const qc = useQueryClient();
  const [emails, setEmails] = useState("");
  const [role, setRole] = useState("member");
  const list = emails.split(/[\s,;]+/).map((e) => e.trim()).filter(Boolean);
  const invite = useMutation({
    mutationFn: () => api.post<{ invited: string[]; already_members: string[]; invalid: string[] }>("/invitations", { emails: list, role }),
    onSuccess: (r) => {
      if (r.invited.length) toast.success(`Invitation sent to ${r.invited.join(", ")}`);
      if (r.already_members.length) toast.info(`Already members: ${r.already_members.join(", ")}`);
      if (r.invalid.length) toast.error(`Not valid emails: ${r.invalid.join(", ")}`);
      setEmails("");
      qc.invalidateQueries({ queryKey: ["invitations"] });
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <form className="space-y-2 rounded-lg border bg-muted/30 p-3" onSubmit={(e) => { e.preventDefault(); if (list.length) invite.mutate(); }}>
      <p className="text-sm font-medium">Invite people</p>
      <div className="flex flex-wrap gap-2">
        <Input placeholder="alex@company.com, sam@company.com" value={emails} onChange={(e) => setEmails(e.target.value)} className="min-w-64 flex-1 bg-background" />
        <FilterSelect value={role} onChange={(r) => r && setRole(r)} placeholder="Role" className="w-32 bg-background"
          options={[{ value: "admin", label: "Admin" }, { value: "member", label: "Member" }, { value: "viewer", label: "Viewer" }]} />
        <Button type="submit" disabled={!list.length || invite.isPending}>Send invitation{list.length > 1 ? "s" : ""}</Button>
      </div>
      <p className="text-xs text-muted-foreground">They&apos;ll get an email with a link to join. Links expire after 7 days; you must have verified your own email.</p>
    </form>
  );
}

function Invitations() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["invitations"], queryFn: () => api.get<Invitation[]>("/invitations") });
  const act = useMutation({
    mutationFn: (a: { kind: "resend"; inv: Invitation } | { kind: "revoke"; id: string }) =>
      a.kind === "resend" ? api.post("/invitations", { emails: [a.inv.email], role: a.inv.role }) : api.del(`/invitations/${a.id}`),
    onSuccess: (_, a) => { toast.success(a.kind === "resend" ? "Invitation re-sent" : "Invitation withdrawn"); qc.invalidateQueries({ queryKey: ["invitations"] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const rows = q.data ?? [];
  if (!rows.length) return null;
  return (
    <div className="space-y-2">
      <p className="text-sm font-medium">Invitations</p>
      <div className="overflow-x-auto rounded-lg border">
        <Table>
          <TableHeader><TableRow><TableHead>Email</TableHead><TableHead>Role</TableHead><TableHead>Status</TableHead><TableHead>Invited</TableHead><TableHead /></TableRow></TableHeader>
          <TableBody>
            {rows.map((i) => (
              <TableRow key={i.id}>
                <TableCell>{i.email}</TableCell>
                <TableCell className="capitalize">{i.role}</TableCell>
                <TableCell>
                  <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${i.status === "accepted" ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400" : i.status === "expired" ? "bg-muted text-muted-foreground" : "bg-amber-500/10 text-amber-700 dark:text-amber-400"}`}>
                    {i.status === "pending" ? "Pending" : i.status === "accepted" ? "Joined" : "Expired"}
                  </span>
                </TableCell>
                <TableCell className="text-muted-foreground">{new Date(i.created_at).toLocaleDateString(undefined, { dateStyle: "medium" })}{i.invited_by ? ` · ${i.invited_by}` : ""}</TableCell>
                <TableCell className="text-right">
                  {i.status !== "accepted" && (
                    <>
                      <Button size="sm" variant="ghost" onClick={() => act.mutate({ kind: "resend", inv: i })}>Resend</Button>
                      {i.status === "pending" && <Button size="sm" variant="ghost" onClick={() => act.mutate({ kind: "revoke", id: i.id })}>Withdraw</Button>}
                    </>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
