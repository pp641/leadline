"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Building2, Plus } from "lucide-react";
import { toast } from "sonner";
import { num } from "@/components/shared";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage } from "@/lib/api";
import { useAuth, useCan } from "@/lib/auth";
import type { AgencyClient } from "@/types/api";

export function AgencyTab() {
  const qc = useQueryClient();
  const { org, reloadOrgs, selectOrg } = useAuth();
  const isOwner = useCan("owner");
  const canAdmin = useCan("admin");
  const [name, setName] = useState("");
  const [address, setAddress] = useState("");
  const clients = useQuery({ queryKey: ["agency-clients"], queryFn: () => api.get<AgencyClient[]>("/agency/clients"), enabled: !!org?.is_agency });
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => api.put("/agency", { enabled }),
    onSuccess: () => reloadOrgs(),
    onError: (e) => toast.error(errorMessage(e)),
  });
  const create = useMutation({
    mutationFn: () => api.post("/agency/clients", { name, physical_address: address }),
    onSuccess: async () => {
      toast.success("Client created");
      setName(""); setAddress("");
      qc.invalidateQueries({ queryKey: ["agency-clients"] });
      await reloadOrgs();
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  if (org?.parent_id) {
    return <p className="text-sm text-muted-foreground">This is a client organization managed by an agency.</p>;
  }
  return (
    <div className="space-y-4">
      <Card className="max-w-2xl">
        <CardHeader>
          <CardTitle className="text-base">Agency mode</CardTitle>
          <CardDescription>
            Manage several clients from one login. Each client gets its own isolated workspace (leads, campaigns, mailboxes, suppression list);
            your team members automatically get access — owners and admins as admins, members as members, viewers as viewers.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={!!org?.is_agency} disabled={!isOwner || toggle.isPending} onCheckedChange={(v) => toggle.mutate(v)} /> Enable agency mode
          </label>
        </CardContent>
      </Card>
      {org?.is_agency && (
        <Card>
          <CardHeader><CardTitle className="text-base">Clients</CardTitle></CardHeader>
          <CardContent className="space-y-3">
            {canAdmin && (
              <form className="flex flex-wrap gap-2" onSubmit={(e) => { e.preventDefault(); if (name.trim()) create.mutate(); }}>
                <Input className="w-56" placeholder="Client name" value={name} onChange={(e) => setName(e.target.value)} />
                <Input className="w-80" placeholder="Client postal address (email footer)" value={address} onChange={(e) => setAddress(e.target.value)} />
                <Button type="submit" disabled={!name.trim()}><Plus /> Add client</Button>
              </form>
            )}
            <Table>
              <TableHeader>
                <TableRow><TableHead>Client</TableHead><TableHead className="text-right">Leads</TableHead><TableHead className="text-right">Active campaigns</TableHead>
                  <TableHead className="text-right">Sent (30d)</TableHead><TableHead className="text-right">Replies (30d)</TableHead><TableHead className="text-right">Bounces (30d)</TableHead><TableHead /></TableRow>
              </TableHeader>
              <TableBody>
                {(clients.data ?? []).map((c) => (
                  <TableRow key={c.id}>
                    <TableCell className="font-medium"><Building2 className="mr-1 inline size-4 text-muted-foreground" />{c.name}</TableCell>
                    <TableCell className="text-right tabular-nums">{num(c.leads)}</TableCell>
                    <TableCell className="text-right tabular-nums">{num(c.active_campaigns)}</TableCell>
                    <TableCell className="text-right tabular-nums">{num(c.sent_30d)}</TableCell>
                    <TableCell className="text-right tabular-nums">{num(c.replies_30d)}</TableCell>
                    <TableCell className="text-right tabular-nums">{num(c.bounced_30d)}</TableCell>
                    <TableCell className="text-right"><Button size="sm" variant="ghost" onClick={() => selectOrg(c.id)}>Open <ArrowRight /></Button></TableCell>
                  </TableRow>
                ))}
                {clients.data?.length === 0 && <TableRow><TableCell colSpan={7} className="text-center text-muted-foreground">No clients yet</TableCell></TableRow>}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
