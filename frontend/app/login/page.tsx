"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { toast } from "sonner";
import { Eye, EyeOff, Loader2 } from "lucide-react";
import { AuthShell } from "@/components/auth-shell";
import { FieldError } from "@/components/auth-card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { DEMO_CREDENTIALS, MOCK_MODE } from "@/lib/mock/config";

const schema = z.object({
  email: z.email("Enter a valid email"),
  password: z.string().min(1, "Enter your password"),
});

export default function LoginPage() {
  return <Suspense><LoginForm /></Suspense>;
}

function LoginForm() {
  const { login, status } = useAuth();
  const router = useRouter();
  const invite = useSearchParams().get("invite");
  const [show, setShow] = useState(false);
  const form = useForm<z.infer<typeof schema>>({ resolver: zodResolver(schema), defaultValues: { email: "", password: "" } });

  useEffect(() => {
    // An invitation is accepted on the invite page once signed in.
    if (status === "authenticated") router.replace(invite ? `/invite?token=${encodeURIComponent(invite)}` : "/dashboard");
  }, [status, router, invite]);

  const onSubmit = form.handleSubmit(async (v) => {
    try {
      await login(v.email, v.password);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  });

  return (
    <AuthShell title="Welcome back" description={invite ? "Sign in to accept your invitation." : "Sign in to your Leadline account."}
      footer={<>New to Leadline? <Link href={invite ? `/register?invite=${encodeURIComponent(invite)}` : "/register"} className="font-medium text-foreground underline-offset-4 hover:underline">Create an account</Link></>}>
      {MOCK_MODE && (
        <div className="space-y-2 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm">
          <p><b>Demo mode.</b> Sign in with <code>{DEMO_CREDENTIALS.email}</code> / <code>{DEMO_CREDENTIALS.password}</code>, or create a new account.</p>
          <Button type="button" size="sm" variant="outline" className="bg-background"
            onClick={() => { form.setValue("email", DEMO_CREDENTIALS.email); form.setValue("password", DEMO_CREDENTIALS.password); void onSubmit(); }}>
            Use demo account
          </Button>
        </div>
      )}
      <form onSubmit={onSubmit} className="space-y-4" noValidate>
        <div className="space-y-1.5">
          <Label htmlFor="email">Work email</Label>
          <Input id="email" type="email" autoComplete="email" placeholder="you@company.com" {...form.register("email")} />
          <FieldError message={form.formState.errors.email?.message} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="password">Password</Label>
          <div className="relative">
            <Input id="password" type={show ? "text" : "password"} autoComplete="current-password" className="pr-9" {...form.register("password")} />
            <button type="button" aria-label={show ? "Hide password" : "Show password"} onClick={() => setShow((x) => !x)}
              className="absolute top-1/2 right-2 -translate-y-1/2 text-muted-foreground hover:text-foreground">{show ? <EyeOff className="size-4" /> : <Eye className="size-4" />}</button>
          </div>
          <FieldError message={form.formState.errors.password?.message} />
        </div>
        <Button type="submit" className="h-10 w-full" disabled={form.formState.isSubmitting}>
          {form.formState.isSubmitting && <Loader2 className="animate-spin" />} Sign in
        </Button>
      </form>
    </AuthShell>
  );
}
