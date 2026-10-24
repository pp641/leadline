"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { MailWarning } from "lucide-react";
import { toast } from "sonner";
import { AppSidebar } from "@/components/app-sidebar";
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { api, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import type { OnboardingStatus } from "@/types/api";

export default function AppLayout({ children }: { children: React.ReactNode }) {
  const { status, org, orgs, user } = useAuth();
  const router = useRouter();
  const isAdmin = org?.role === "owner" || org?.role === "admin";
  const onboarding = useQuery({
    queryKey: ["onboarding"], queryFn: () => api.get<OnboardingStatus>("/onboarding"),
    enabled: status === "authenticated" && !!org && isAdmin, staleTime: 60_000,
  });

  useEffect(() => {
    if (status === "anonymous") router.replace("/login");
    else if (status === "authenticated" && orgs.length === 0) router.replace("/onboarding");
    // Owners/admins finish (or skip) the setup wizard first; invited members never see it.
    else if (onboarding.data && onboarding.data.state.completed !== true) router.replace("/onboarding");
  }, [status, orgs.length, onboarding.data, router]);

  if (status !== "authenticated" || !org) {
    return (
      <div className="flex min-h-svh items-center justify-center">
        <Skeleton className="h-8 w-48" />
      </div>
    );
  }

  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset className="min-w-0">
        <header className="sticky top-0 z-10 flex h-12 items-center gap-2 border-b bg-background/95 px-4 backdrop-blur md:hidden">
          <SidebarTrigger />
          <span className="text-sm font-medium">{org.name}</span>
        </header>
        {user && !user.email_verified && <VerifyBanner email={user.email} />}
        {/* key: remount pages when switching tenant so no state leaks across orgs */}
        <div key={org.id} className="mx-auto w-full min-w-0 max-w-7xl px-4 py-6 md:px-8">
          {children}
        </div>
      </SidebarInset>
    </SidebarProvider>
  );
}

function VerifyBanner({ email }: { email: string }) {
  const resend = useMutation({
    mutationFn: () => api.post("/auth/verify-email/send", {}),
    onSuccess: () => toast.success(`Verification code sent to ${email}`),
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-amber-500/30 bg-amber-500/10 px-4 py-2 text-sm md:px-8">
      <MailWarning className="size-4 text-amber-600" />
      <span>Verify <b>{email}</b> to invite teammates and secure your account.</span>
      <span className="flex-1" />
      <Button size="sm" variant="ghost" disabled={resend.isPending} onClick={() => resend.mutate()}>Resend code</Button>
      <Button size="sm" asChild><Link href="/onboarding">Enter code</Link></Button>
    </div>
  );
}
