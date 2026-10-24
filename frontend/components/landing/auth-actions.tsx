"use client";

import Link from "next/link";
import { ArrowRight, LayoutDashboard } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/lib/auth";

/** Header buttons: sign in / sign up, or "Open app" for a signed-in visitor. */
export function NavActions() {
  const { status } = useAuth();
  if (status === "authenticated") {
    return <Button asChild size="sm"><Link href="/dashboard"><LayoutDashboard /> Open app</Link></Button>;
  }
  return (
    <div className="flex items-center gap-2">
      <Button asChild size="sm" variant="ghost"><Link href="/login">Sign in</Link></Button>
      <Button asChild size="sm"><Link href="/register">Sign up <ArrowRight /></Link></Button>
    </div>
  );
}

/** Primary calls to action: sign up and sign in (or open the app when signed in). */
export function HeroActions() {
  const { status } = useAuth();
  if (status === "authenticated") {
    return (
      <div className="flex flex-wrap items-center justify-center gap-3">
        <Button asChild size="lg" className="h-11 px-6 text-base"><Link href="/dashboard"><LayoutDashboard /> Open app</Link></Button>
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center justify-center gap-3">
      <Button asChild size="lg" className="h-11 px-6 text-base"><Link href="/register">Sign up <ArrowRight /></Link></Button>
      <Button asChild size="lg" variant="outline" className="h-11 px-6 text-base"><Link href="/login">Sign in</Link></Button>
    </div>
  );
}
