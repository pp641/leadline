import Link from "next/link";
import { BarChart3, Lock, Radar, Send, ShieldCheck, Users } from "lucide-react";

const POINTS = [
  { icon: Radar, title: "Find the right companies", text: "Every source in one place, deduplicated, verified and scored against your ICP." },
  { icon: Send, title: "Run compliant outreach", text: "Paced multi-mailbox sequences with unsubscribe and suppression built in." },
  { icon: Users, title: "Convert and manage clients", text: "Replies flow into your pipeline with owners, notes, tasks and CRM sync." },
  { icon: BarChart3, title: "Measure everything", text: "Lead insights, outreach analytics and custom dashboards." },
];

/** Split-screen layout for sign-in, sign-up and invitations. */
export function AuthShell({ title, description, children, footer }: {
  title: string; description?: React.ReactNode; children: React.ReactNode; footer?: React.ReactNode;
}) {
  return (
    <main className="grid min-h-svh bg-background lg:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]">
      <aside className="relative hidden overflow-hidden bg-zinc-950 p-10 text-zinc-100 lg:flex lg:flex-col">
        <div aria-hidden className="pointer-events-none absolute inset-0 bg-[radial-gradient(40rem_28rem_at_0%_0%,color-mix(in_oklab,var(--series-1)_35%,transparent),transparent),radial-gradient(30rem_24rem_at_100%_100%,color-mix(in_oklab,var(--series-3)_25%,transparent),transparent)]" />
        <Link href="/" className="relative flex items-center gap-2 text-base font-semibold">
          <span className="flex size-8 items-center justify-center rounded-lg bg-white text-zinc-950"><Send className="size-4" /></span>Leadline
        </Link>
        <div className="relative my-auto max-w-md space-y-8 py-10">
          <h2 className="text-3xl font-semibold tracking-tight text-balance">One platform to find, win and manage your clients.</h2>
          <ul className="space-y-5">
            {POINTS.map((p) => (
              <li key={p.title} className="flex gap-3">
                <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-white/10"><p.icon className="size-4" /></span>
                <span><span className="block font-medium">{p.title}</span><span className="block text-sm text-zinc-400">{p.text}</span></span>
              </li>
            ))}
          </ul>
        </div>
        <div className="relative flex flex-wrap gap-x-6 gap-y-2 text-xs text-zinc-400">
          <span className="flex items-center gap-1.5"><Lock className="size-3.5" /> Credentials encrypted (AES-256-GCM)</span>
          <span className="flex items-center gap-1.5"><ShieldCheck className="size-3.5" /> Role-based access &amp; audit log</span>
        </div>
      </aside>
      <section className="flex flex-col px-4 py-8 sm:px-10">
        <Link href="/" className="mb-8 flex items-center gap-2 font-semibold lg:hidden">
          <span className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Send className="size-4" /></span>Leadline
        </Link>
        <div className="mx-auto my-auto w-full max-w-sm space-y-6">
          <div className="space-y-1.5">
            <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
            {description && <p className="text-sm text-muted-foreground">{description}</p>}
          </div>
          {children}
          {footer && <div className="text-center text-sm text-muted-foreground">{footer}</div>}
        </div>
        <p className="mx-auto mt-8 text-xs text-muted-foreground">
          <Link href="/terms" className="hover:text-foreground">Terms</Link> · <Link href="/privacy" className="hover:text-foreground">Privacy</Link> · © {new Date().getFullYear()} Leadline
        </p>
      </section>
    </main>
  );
}

const FREE_DOMAINS = new Set(["gmail.com", "googlemail.com", "yahoo.com", "yahoo.in", "outlook.com", "hotmail.com", "live.com", "icloud.com",
  "aol.com", "proton.me", "protonmail.com", "rediffmail.com", "zoho.com", "gmx.com", "mail.com", "yandex.com"]);

/** True for consumer mailbox domains (used to nudge towards a work email). */
export function isPersonalEmail(email: string): boolean {
  const d = email.split("@")[1]?.toLowerCase().trim();
  return !!d && FREE_DOMAINS.has(d);
}

/** 0–4 password strength from length and character variety. */
export function passwordScore(pw: string): number {
  if (!pw) return 0;
  let s = 0;
  if (pw.length >= 10) s++;
  if (pw.length >= 14) s++;
  if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) s++;
  if (/\d/.test(pw) && /[^A-Za-z0-9]/.test(pw)) s++;
  if (pw.length < 10) s = Math.min(s, 1);
  return Math.min(s, 4);
}

export function PasswordMeter({ password }: { password: string }) {
  const score = passwordScore(password);
  const labels = ["Too short", "Weak", "Fair", "Good", "Strong"];
  const tone = ["bg-muted", "bg-destructive", "bg-amber-500", "bg-[var(--series-1)]", "bg-[var(--series-3)]"];
  if (!password) return <p className="text-xs text-muted-foreground">At least 10 characters. A longer passphrase is stronger.</p>;
  return (
    <div className="space-y-1">
      <div className="flex gap-1">{[1, 2, 3, 4].map((i) => <span key={i} className={`h-1 flex-1 rounded-full ${i <= score ? tone[score] : "bg-muted"}`} />)}</div>
      <p className="text-xs text-muted-foreground">Password strength: <span className="font-medium text-foreground">{labels[score]}</span></p>
    </div>
  );
}
