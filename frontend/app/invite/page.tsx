"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Building2, Loader2, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { AuthShell } from "@/components/auth-shell";
import { Button } from "@/components/ui/button";
import { api, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import type { InviteInfo } from "@/types/api";

/** Landing page of an emailed invitation link. */
export default function InvitePage() {
  return <Suspense><InviteView /></Suspense>;
}

function InviteView() {
  const { status, user, logout, reloadOrgs, selectOrg, refreshUser } = useAuth();
  const router = useRouter();
  const token = useSearchParams().get("token") ?? "";
  const info = useQuery({ queryKey: ["invite", token], queryFn: () => api.get<InviteInfo>("/invitations/lookup", { token }), enabled: !!token, retry: false });
  const accept = useMutation({
    mutationFn: () => api.post<{ organization_id: string }>("/invitations/accept", { token }),
    onSuccess: async (r) => {
      await refreshUser();
      await reloadOrgs();
      selectOrg(r.organization_id);
      toast.success(`You joined ${info.data?.organization ?? "the organization"}`);
      router.replace("/dashboard");
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const q = encodeURIComponent(token);
  const signedInAs = status === "authenticated" ? user?.email : null;
  const matches = !!signedInAs && !!info.data && signedInAs.toLowerCase() === info.data.email.toLowerCase();

  // Signed in with the invited address: accept straight away.
  useEffect(() => {
    if (matches && accept.isIdle) accept.mutate();
  }, [matches, accept]);

  if (!token || info.isError) {
    return (
      <AuthShell title="Invitation unavailable" footer={<Link href="/" className="underline">Back to home</Link>}>
        <p className="flex gap-2 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">
          <TriangleAlert className="size-4 shrink-0" /> {token ? errorMessage(info.error) : "This link is missing its invitation token."}
        </p>
      </AuthShell>
    );
  }
  if (!info.data || status === "loading") {
    return <AuthShell title="Checking your invitation…"><Loader2 className="size-5 animate-spin text-muted-foreground" /></AuthShell>;
  }
  const i = info.data;
  return (
    <AuthShell title={`Join ${i.organization}`} description={<>{i.invited_by || "A teammate"} invited <b className="text-foreground">{i.email}</b> as <b className="text-foreground capitalize">{i.role}</b>.</>}>
      <div className="flex items-center gap-3 rounded-xl border bg-muted/40 p-4">
        <span className="flex size-10 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Building2 className="size-5" /></span>
        <span><span className="block font-medium">{i.organization}</span>
          <span className="block text-xs text-muted-foreground">Expires {new Date(i.expires_at).toLocaleDateString(undefined, { dateStyle: "medium" })}</span></span>
      </div>
      {matches ? (
        <Button className="h-10 w-full" disabled><Loader2 className="animate-spin" /> Joining…</Button>
      ) : signedInAs ? (
        <div className="space-y-3">
          <p className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-sm">You&apos;re signed in as <b>{signedInAs}</b>, but this invitation is for <b>{i.email}</b>.</p>
          <Button className="h-10 w-full" variant="outline" onClick={() => logout(`/invite?token=${q}`)}>Sign out and continue</Button>
        </div>
      ) : (
        <div className="space-y-2">
          <Button asChild className="h-10 w-full"><Link href={i.account_exists ? `/login?invite=${q}` : `/register?invite=${q}`}>{i.account_exists ? "Sign in to accept" : "Create account & join"}</Link></Button>
          <Button asChild variant="ghost" className="w-full"><Link href={i.account_exists ? `/register?invite=${q}` : `/login?invite=${q}`}>{i.account_exists ? "Use a new account instead" : "I already have an account"}</Link></Button>
        </div>
      )}
    </AuthShell>
  );
}
