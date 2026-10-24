"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { toast } from "sonner";
import { Eye, EyeOff, Loader2, MailCheck, TriangleAlert } from "lucide-react";
import { AuthShell, PasswordMeter, isPersonalEmail } from "@/components/auth-shell";
import { FieldError } from "@/components/auth-card";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError, api, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import type { InviteInfo } from "@/types/api";

const schema = z.object({
  full_name: z.string().trim().min(1, "Enter your name").max(120),
  email: z.email("Enter a valid email"),
  password: z.string().min(10, "Use at least 10 characters").max(72, "Use at most 72 characters"),
  accept_terms: z.boolean().refine((v) => v, "Accept the terms to continue"),
});
type Values = z.infer<typeof schema>;

export default function RegisterPage() {
  return <Suspense><RegisterForm /></Suspense>;
}

function RegisterForm() {
  const { register: signUp, status, reloadOrgs, selectOrg, refreshUser } = useAuth();
  const router = useRouter();
  const token = useSearchParams().get("invite");
  const [invite, setInvite] = useState<InviteInfo | null>(null);
  const [inviteError, setInviteError] = useState<string | null>(null);
  const [show, setShow] = useState(false);
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { full_name: "", email: "", password: "", accept_terms: false } });
  const email = form.watch("email");
  const password = form.watch("password");

  useEffect(() => {
    if (!token) return;
    api.get<InviteInfo>("/invitations/lookup", { token })
      .then((i) => { setInvite(i); form.setValue("email", i.email); })
      .catch((e) => setInviteError(errorMessage(e)));
  }, [token, form]);

  useEffect(() => {
    if (status === "authenticated" && !form.formState.isSubmitting && !form.formState.isSubmitSuccessful) {
      router.replace(token ? `/invite?token=${encodeURIComponent(token)}` : "/dashboard");
    }
  }, [status, router, token, form.formState.isSubmitting, form.formState.isSubmitSuccessful]);

  const onSubmit = form.handleSubmit(async (v) => {
    try {
      await signUp(v.full_name, v.email, v.password, v.accept_terms);
      if (token && invite) {
        const r = await api.post<{ organization_id: string }>("/invitations/accept", { token });
        await refreshUser(); // accepting an emailed invitation verifies the address
        await reloadOrgs();
        selectOrg(r.organization_id);
        toast.success(`Welcome to ${invite.organization}`);
        router.replace("/dashboard");
      } else {
        router.replace("/onboarding");
      }
    } catch (e) {
      if (e instanceof ApiError) {
        for (const [field, msg] of Object.entries(e.fields)) form.setError(field as keyof Values, { message: msg });
      }
      toast.error(errorMessage(e));
    }
  });

  const err = form.formState.errors;
  return (
    <AuthShell
      title={invite ? `Join ${invite.organization}` : "Create your account"}
      description={invite ? <>{invite.invited_by || "A teammate"} invited you as <b className="text-foreground capitalize">{invite.role}</b>.</> : "Start with your account — we'll guide you through setup next."}
      footer={<>Already have an account? <Link href={token ? `/login?invite=${encodeURIComponent(token)}` : "/login"} className="font-medium text-foreground underline-offset-4 hover:underline">Sign in</Link></>}>
      {inviteError && (
        <p className="flex gap-2 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"><TriangleAlert className="size-4 shrink-0" /> {inviteError}</p>
      )}
      <form onSubmit={onSubmit} className="space-y-4" noValidate>
        <div className="space-y-1.5">
          <Label htmlFor="full_name">Full name</Label>
          <Input id="full_name" autoComplete="name" placeholder="Priya Sharma" {...form.register("full_name")} />
          <FieldError message={err.full_name?.message} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="email">Work email</Label>
          <Input id="email" type="email" autoComplete="email" placeholder="you@company.com" readOnly={!!invite} className={invite ? "bg-muted" : ""} {...form.register("email")} />
          <FieldError message={err.email?.message} />
          {!invite && !err.email && isPersonalEmail(email) && (
            <p className="text-xs text-amber-700 dark:text-amber-400">Tip: a work email makes it easier for teammates to find and join your organization.</p>
          )}
          {invite && <p className="flex items-center gap-1 text-xs text-muted-foreground"><MailCheck className="size-3.5" /> The invitation was sent to this address.</p>}
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="password">Password</Label>
          <div className="relative">
            <Input id="password" type={show ? "text" : "password"} autoComplete="new-password" className="pr-9" {...form.register("password")} />
            <button type="button" aria-label={show ? "Hide password" : "Show password"} onClick={() => setShow((x) => !x)}
              className="absolute top-1/2 right-2 -translate-y-1/2 text-muted-foreground hover:text-foreground">{show ? <EyeOff className="size-4" /> : <Eye className="size-4" />}</button>
          </div>
          <PasswordMeter password={password} />
          <FieldError message={err.password?.message} />
        </div>
        <div className="space-y-1">
          <label className="flex items-start gap-2.5 text-sm">
            <Checkbox className="mt-0.5" checked={form.watch("accept_terms")} onCheckedChange={(v) => form.setValue("accept_terms", v === true, { shouldValidate: form.formState.isSubmitted })} />
            <span className="text-muted-foreground">I agree to the <Link href="/terms" target="_blank" className="text-foreground underline underline-offset-2">Terms of Service</Link> and <Link href="/privacy" target="_blank" className="text-foreground underline underline-offset-2">Privacy Policy</Link>.</span>
          </label>
          <FieldError message={err.accept_terms?.message} />
        </div>
        <Button type="submit" className="h-10 w-full" disabled={form.formState.isSubmitting || (!!token && !invite && !inviteError)}>
          {form.formState.isSubmitting && <Loader2 className="animate-spin" />} {invite ? "Create account & join" : "Create account"}
        </Button>
      </form>
    </AuthShell>
  );
}
