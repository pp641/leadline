"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft, ArrowRight, Building2, Check, CircleCheck, Loader2, Mail, MailCheck, Rocket, Send, Target, Users,
} from "lucide-react";
import { toast } from "sonner";
import { MultiSelect, flag } from "@/components/multi-select";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import type { CatalogFacets, OnboardingStatus, Organization } from "@/types/api";

// ───────────── steps ─────────────

const STEPS = [
  { key: "verify", title: "Verify your email", blurb: "Confirm it's you", icon: MailCheck },
  { key: "company", title: "Company profile", blurb: "Who you are", icon: Building2 },
  { key: "goals", title: "Your goals", blurb: "Tailor Leadline", icon: Rocket },
  { key: "icp", title: "Ideal customer", blurb: "Who you sell to", icon: Target },
  { key: "mailbox", title: "Sending mailbox", blurb: "Where emails come from", icon: Mail },
  { key: "team", title: "Invite your team", blurb: "Work together", icon: Users },
  { key: "done", title: "All set", blurb: "Start using Leadline", icon: CircleCheck },
] as const;
type StepKey = (typeof STEPS)[number]["key"];

const INDUSTRIES = ["Marketing / advertising agency", "Software / SaaS", "Web design & development", "Consulting", "Recruiting / staffing",
  "Real estate", "Financial services", "Education", "Healthcare", "Other"];
const TEAM_SIZES = ["Just me", "2–10", "11–50", "51–200", "201–1,000", "1,000+"];
const ROLES = ["Founder / owner", "Sales", "Marketing", "Agency / consultant", "Operations", "Other"];
const USE_CASES = [
  { value: "local", title: "Find local businesses", text: "Restaurants, clinics, shops… by city and category" },
  { value: "b2b", title: "B2B prospecting", text: "Companies and decision makers by industry and size" },
  { value: "agency", title: "Lead generation for clients", text: "Run separate client workspaces as an agency" },
  { value: "manage", title: "Manage existing leads", text: "Organize, score and follow up on leads I have" },
];
const VOLUMES = ["Under 500", "500–2,000", "2,000–10,000", "10,000+"];

// ───────────── pieces ─────────────

function Field({ label, children, hint }: { label: string; children: React.ReactNode; hint?: string }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-sm">{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

function Choice<T extends string>({ value, onChange, options }: { value?: T; onChange: (v: T) => void; options: readonly T[] }) {
  return (
    <div className="flex flex-wrap gap-2">
      {options.map((o) => (
        <button key={o} type="button" onClick={() => onChange(o)}
          className={`rounded-full border px-3 py-1.5 text-sm transition ${value === o ? "border-primary bg-primary text-primary-foreground" : "hover:bg-muted"}`}>{o}</button>
      ))}
    </div>
  );
}

function SelectBox({ value, onChange, options, placeholder }: { value?: string; onChange: (v: string) => void; options: string[]; placeholder: string }) {
  return (
    <Select value={value || undefined} onValueChange={onChange}>
      <SelectTrigger className="w-full"><SelectValue placeholder={placeholder} /></SelectTrigger>
      <SelectContent>{options.map((o) => <SelectItem key={o} value={o}>{o}</SelectItem>)}</SelectContent>
    </Select>
  );
}

/** Six single-digit boxes with paste support. */
function CodeInput({ value, onChange, onComplete, disabled }: { value: string; onChange: (v: string) => void; onComplete: (v: string) => void; disabled?: boolean }) {
  const refs = useRef<(HTMLInputElement | null)[]>([]);
  const set = (next: string) => {
    const v = next.replace(/\D/g, "").slice(0, 6);
    onChange(v);
    refs.current[Math.min(v.length, 5)]?.focus();
    if (v.length === 6) onComplete(v);
  };
  return (
    <div className="flex gap-2" onPaste={(e) => { e.preventDefault(); set(e.clipboardData.getData("text")); }}>
      {Array.from({ length: 6 }, (_, i) => (
        <input key={i} ref={(el) => { refs.current[i] = el; }} inputMode="numeric" autoComplete={i === 0 ? "one-time-code" : "off"} maxLength={1}
          disabled={disabled} aria-label={`Digit ${i + 1}`} value={value[i] ?? ""}
          onChange={(e) => { const d = e.target.value.replace(/\D/g, "").slice(-1); if (!d) return; set(value.slice(0, i) + d + value.slice(i + 1)); }}
          onKeyDown={(e) => {
            if (e.key === "Backspace") { e.preventDefault(); const v = value.slice(0, Math.max(0, value[i] ? i : i - 1)); onChange(v); refs.current[Math.max(0, v.length)]?.focus(); }
            if (e.key === "ArrowLeft") refs.current[i - 1]?.focus();
            if (e.key === "ArrowRight") refs.current[i + 1]?.focus();
          }}
          className="size-12 rounded-lg border bg-background text-center font-mono text-xl font-semibold outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50 disabled:opacity-50" />
      ))}
    </div>
  );
}

// ───────────── page ─────────────

export default function OnboardingPage() {
  const { status, user, orgs, org, reloadOrgs, selectOrg, refreshUser, logout } = useAuth();
  const router = useRouter();
  const qc = useQueryClient();
  const creating = useRef(false);
  const [createError, setCreateError] = useState<string | null>(null);

  // Accounts without an organization get one automatically, named after the user.
  useEffect(() => {
    if (status === "anonymous") { router.replace("/login"); return; }
    if (status !== "authenticated" || !user || orgs.length > 0 || creating.current) return;
    creating.current = true;
    const name = (user.full_name?.trim() || user.email.split("@")[0]).slice(0, 120);
    api.post<Organization>("/organizations", { name, legal_name: "", physical_address: "" })
      .then(async (o) => { await reloadOrgs(); selectOrg(o.id); })
      .catch((e) => { creating.current = false; setCreateError(errorMessage(e)); });
  }, [status, user, orgs.length, reloadOrgs, selectOrg, router]);

  const ob = useQuery({ queryKey: ["onboarding"], queryFn: () => api.get<OnboardingStatus>("/onboarding"), enabled: !!org });
  const facets = useQuery({ queryKey: ["catalog-facets"], queryFn: () => api.get<CatalogFacets>("/catalog/facets"), enabled: !!org, staleTime: 300_000 });

  const [chosen, setStep] = useState<StepKey | null>(null);
  const s = ob.data;
  useEffect(() => {
    // Already set up and verified: nothing to do here.
    if (s && s.state.completed === true && s.email_verified) router.replace("/dashboard");
  }, [s, router]);
  // Resume where the user left off; unverified accounts start by verifying.
  const saved = s?.state.step as StepKey | undefined;
  const step: StepKey | null = chosen ?? (!s ? null : !s.email_verified ? "verify" : saved && saved !== "verify" ? saved : "company");

  const save = useMutation({
    mutationFn: (patch: Record<string, unknown>) => api.put<OnboardingStatus>("/onboarding", patch),
    onSuccess: (d) => qc.setQueryData(["onboarding"], d),
  });
  const go = (next: StepKey, patch: Record<string, unknown> = {}) => { save.mutate({ ...patch, step: next }); setStep(next); window.scrollTo({ top: 0 }); };
  const finish = async (patch: Record<string, unknown> = {}) => {
    await save.mutateAsync({ ...patch, completed: true, step: "done" });
    qc.invalidateQueries({ queryKey: ["onboarding"] });
    router.replace("/dashboard");
  };

  if (createError) {
    return (
      <main className="flex min-h-svh flex-col items-center justify-center gap-3 p-6 text-center">
        <p className="font-medium">We couldn&apos;t finish setting up your account</p>
        <p className="max-w-sm text-sm text-muted-foreground">{createError}</p>
        <Button onClick={() => window.location.reload()}>Try again</Button>
      </main>
    );
  }
  if (!org || !s || !step) {
    return <main className="flex min-h-svh items-center justify-center gap-2 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" /> Setting up your account…</main>;
  }
  const index = STEPS.findIndex((x) => x.key === step);
  const pct = Math.round((index / (STEPS.length - 1)) * 100);
  const isAdmin = org.role === "owner" || org.role === "admin";

  return (
    <main className="min-h-svh bg-muted/30">
      <header className="sticky top-0 z-10 border-b bg-background/90 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-4 px-4">
          <Link href="/" className="flex items-center gap-2 font-semibold"><span className="flex size-7 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Send className="size-3.5" /></span>Leadline</Link>
          <span className="hidden text-sm text-muted-foreground sm:inline">· Account setup</span>
          <span className="flex-1" />
          <span className="hidden text-xs text-muted-foreground sm:inline">{user?.email}</span>
          {step !== "verify" && step !== "done" && isAdmin && <Button size="sm" variant="ghost" onClick={() => finish()}>Skip setup</Button>}
          <Button size="sm" variant="ghost" onClick={() => logout()}>Sign out</Button>
        </div>
        <div className="h-0.5 bg-muted"><div className="h-full bg-primary transition-all" style={{ width: `${pct}%` }} /></div>
      </header>

      <div className="mx-auto grid max-w-6xl gap-8 px-4 py-8 md:grid-cols-[240px_1fr] md:py-12">
        <nav aria-label="Setup steps" className="hidden md:block">
          <ol className="space-y-1">
            {STEPS.map((st, i) => {
              const done = i < index || (st.key === "verify" && s.email_verified);
              const current = st.key === step;
              const reachable = s.email_verified && st.key !== "verify";
              return (
                <li key={st.key}>
                  <button type="button" disabled={!reachable || current} onClick={() => setStep(st.key)}
                    className={`flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left transition ${current ? "bg-background shadow-xs ring-1 ring-border" : reachable ? "hover:bg-background/60" : ""}`}>
                    <span className={`flex size-7 shrink-0 items-center justify-center rounded-full border text-xs font-semibold ${done ? "border-primary bg-primary text-primary-foreground" : current ? "border-primary text-primary" : "text-muted-foreground"}`}>
                      {done ? <Check className="size-3.5" /> : i + 1}
                    </span>
                    <span className="min-w-0"><span className={`block text-sm ${current ? "font-semibold" : ""}`}>{st.title}</span><span className="block text-xs text-muted-foreground">{st.blurb}</span></span>
                  </button>
                </li>
              );
            })}
          </ol>
        </nav>

        <section className="rounded-2xl border bg-card p-6 shadow-xs sm:p-8">
          <p className="mb-1 text-xs font-medium tracking-wide text-muted-foreground uppercase">Step {index + 1} of {STEPS.length}</p>
          {step === "verify" && <VerifyStep email={user?.email ?? ""} onVerified={async () => {
            await refreshUser();
            await qc.invalidateQueries({ queryKey: ["onboarding"] });
            if (s.state.completed === true) router.replace("/dashboard"); else go("company");
          }} />}
          {step === "company" && <CompanyStep s={s} facets={facets.data} onNext={(patch) => go("goals", patch)} />}
          {step === "goals" && <GoalsStep s={s} onBack={() => setStep("company")} onNext={(patch) => go("icp", patch)} />}
          {step === "icp" && <ICPStep s={s} facets={facets.data} onBack={() => setStep("goals")} onNext={() => go("mailbox")} />}
          {step === "mailbox" && <MailboxStep onBack={() => setStep("icp")} onNext={() => go("team")} />}
          {step === "team" && <TeamStep onBack={() => setStep("mailbox")} onNext={() => go("done")} />}
          {step === "done" && <DoneStep s={s} onFinish={() => finish()} />}
        </section>
      </div>
    </main>
  );
}

function StepHead({ title, text }: { title: string; text: string }) {
  return <div className="mb-6 space-y-1"><h1 className="text-2xl font-semibold tracking-tight">{title}</h1><p className="text-muted-foreground">{text}</p></div>;
}

function Nav({ onBack, onSkip, next, nextLabel = "Continue", busy, disabled }: {
  onBack?: () => void; onSkip?: () => void; next: () => void; nextLabel?: string; busy?: boolean; disabled?: boolean;
}) {
  return (
    <div className="mt-8 flex flex-wrap items-center gap-2 border-t pt-5">
      {onBack && <Button variant="ghost" onClick={onBack}><ArrowLeft /> Back</Button>}
      <span className="flex-1" />
      {onSkip && <Button variant="ghost" onClick={onSkip}>Skip for now</Button>}
      <Button onClick={next} disabled={busy || disabled}>{busy && <Loader2 className="animate-spin" />} {nextLabel} <ArrowRight /></Button>
    </div>
  );
}

// ───────────── step: verify ─────────────

function VerifyStep({ email, onVerified }: { email: string; onVerified: () => void }) {
  const [code, setCode] = useState("");
  const [cooldown, setCooldown] = useState(60);
  useEffect(() => {
    if (cooldown <= 0) return;
    const t = setTimeout(() => setCooldown((c) => c - 1), 1000);
    return () => clearTimeout(t);
  }, [cooldown]);
  const verify = useMutation({
    mutationFn: (c: string) => api.post("/auth/verify-email", { code: c }),
    onSuccess: () => { toast.success("Email verified"); onVerified(); },
    onError: (e) => { toast.error(errorMessage(e)); setCode(""); },
  });
  const resend = useMutation({
    mutationFn: () => api.post("/auth/verify-email/send", {}),
    onSuccess: () => { toast.success("A new code is on its way"); setCooldown(60); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <>
      <StepHead title="Check your inbox" text={`We sent a 6-digit code to ${email}. Enter it to verify your email address.`} />
      <div className="space-y-5">
        <CodeInput value={code} onChange={setCode} onComplete={(c) => verify.mutate(c)} disabled={verify.isPending} />
        <div className="flex flex-wrap items-center gap-3 text-sm">
          <Button onClick={() => verify.mutate(code)} disabled={code.length !== 6 || verify.isPending}>{verify.isPending && <Loader2 className="animate-spin" />} Verify email</Button>
          <span className="text-muted-foreground">Didn&apos;t get it?</span>
          <Button variant="link" className="px-0" disabled={cooldown > 0 || resend.isPending} onClick={() => resend.mutate()}>
            {cooldown > 0 ? `Resend code in ${cooldown}s` : "Resend code"}
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">The code expires after 15 minutes. Check your spam folder if it hasn&apos;t arrived.</p>
      </div>
    </>
  );
}

// ───────────── step: company ─────────────

function CompanyStep({ s, facets, onNext }: { s: OnboardingStatus; facets?: CatalogFacets; onNext: (patch: Record<string, unknown>) => void }) {
  const qc = useQueryClient();
  const p = s.state.profile ?? {};
  const [v, setV] = useState({
    name: s.organization.name, legal_name: s.organization.legal_name, physical_address: s.organization.physical_address,
    website: p.website ?? "", industry: p.industry ?? "", team_size: p.team_size ?? "", country: p.country ?? "",
  });
  const set = (x: Partial<typeof v>) => setV((o) => ({ ...o, ...x }));
  const countries = useMemo(() => (facets?.countries ?? []).map((c) => ({ value: c.name, label: c.name, icon: flag(c.code) })), [facets]);
  const save = useMutation({
    mutationFn: () => api.patch("/organizations/current", { name: v.name.trim(), legal_name: v.legal_name.trim(), physical_address: v.physical_address.trim() }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["onboarding"] }); onNext({ profile: { website: v.website, industry: v.industry, team_size: v.team_size, country: v.country } }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <>
      <StepHead title="Tell us about your company" text="This appears on your outreach and helps us tailor Leadline for you." />
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Organization name"><Input value={v.name} onChange={(e) => set({ name: e.target.value })} /></Field>
        <Field label="Website"><Input value={v.website} placeholder="https://company.com" onChange={(e) => set({ website: e.target.value })} /></Field>
        <Field label="Industry"><SelectBox value={v.industry} onChange={(x) => set({ industry: x })} options={INDUSTRIES} placeholder="Choose…" /></Field>
        <Field label="Team size"><SelectBox value={v.team_size} onChange={(x) => set({ team_size: x })} options={TEAM_SIZES} placeholder="Choose…" /></Field>
        <Field label="Country">
          <MultiSelect value={v.country ? [v.country] : []} onChange={(x) => set({ country: x[x.length - 1] ?? "" })} options={countries} placeholder="Choose a country" />
        </Field>
        <Field label="Legal entity name (optional)"><Input value={v.legal_name} placeholder="Acme Growth Pvt Ltd" onChange={(e) => set({ legal_name: e.target.value })} /></Field>
        <div className="sm:col-span-2">
          <Field label="Business postal address" hint="Required in the footer of every outreach email by anti-spam laws (CAN-SPAM, GDPR/PECR). You can add it later, but campaigns can't launch without it.">
            <Textarea rows={3} value={v.physical_address} placeholder="Street, city, state, postal code, country" onChange={(e) => set({ physical_address: e.target.value })} />
          </Field>
        </div>
      </div>
      <Nav next={() => save.mutate()} busy={save.isPending} disabled={!v.name.trim()} />
    </>
  );
}

// ───────────── step: goals ─────────────

function GoalsStep({ s, onBack, onNext }: { s: OnboardingStatus; onBack: () => void; onNext: (patch: Record<string, unknown>) => void }) {
  const g = s.state.goals ?? {};
  const [v, setV] = useState({ role: (g.role as string) ?? "", use_case: (g.use_case as string) ?? "", volume: (g.volume as string) ?? "" });
  return (
    <>
      <StepHead title="What do you want to achieve?" text="We'll set up sensible defaults and point you to the right features." />
      <div className="space-y-6">
        <Field label="Your role"><Choice value={v.role} onChange={(role) => setV((o) => ({ ...o, role }))} options={ROLES} /></Field>
        <Field label="Main use case">
          <div className="grid gap-2 sm:grid-cols-2">
            {USE_CASES.map((u) => (
              <button key={u.value} type="button" onClick={() => setV((o) => ({ ...o, use_case: u.value }))}
                className={`rounded-xl border p-4 text-left transition hover:border-primary/50 ${v.use_case === u.value ? "border-primary bg-primary/5 ring-1 ring-primary/30" : ""}`}>
                <span className="block font-medium">{u.title}</span><span className="block text-sm text-muted-foreground">{u.text}</span>
              </button>
            ))}
          </div>
        </Field>
        <Field label="New leads per month"><Choice value={v.volume} onChange={(volume) => setV((o) => ({ ...o, volume }))} options={VOLUMES} /></Field>
      </div>
      <Nav onBack={onBack} onSkip={() => onNext({})} next={() => onNext({ goals: v })} />
    </>
  );
}

// ───────────── step: ICP ─────────────

function ICPStep({ s, facets, onBack, onNext }: { s: OnboardingStatus; facets?: CatalogFacets; onBack: () => void; onNext: () => void }) {
  const qc = useQueryClient();
  const country = s.state.profile?.country;
  const [v, setV] = useState({ countries: country ? [country] : [] as string[], cities: [] as string[], industries: [] as string[] });
  const countries = useMemo(() => (facets?.countries ?? []).map((c) => ({ value: c.name, label: c.name, icon: flag(c.code), hint: c.count ? c.count.toLocaleString() : undefined })), [facets]);
  const cities = useMemo(() => {
    const seen = new Set<string>();
    return (facets?.cities ?? []).filter((c) => !seen.has(c.name.toLowerCase()) && !!seen.add(c.name.toLowerCase()))
      .map((c) => ({ value: c.name, label: c.name, group: c.state || undefined }));
  }, [facets]);
  const industries = useMemo(() => (facets?.industries ?? []).map((i) => ({ value: i.value, label: i.value })), [facets]);
  const create = useMutation({
    mutationFn: () => api.post("/icps", { name: "My ideal customer", is_default: true, criteria: { countries: v.countries, cities: v.cities, industries: v.industries, website: "any" } }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["icps"] }); toast.success("Ideal customer profile created"); onNext(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const empty = !v.countries.length && !v.cities.length && !v.industries.length;
  return (
    <>
      <StepHead title="Who is your ideal customer?" text="Leadline scores every lead against this profile and uses it to find new companies. You can refine it any time in ICP." />
      <div className="space-y-4">
        <Field label="Countries"><MultiSelect value={v.countries} onChange={(countries) => setV((o) => ({ ...o, countries }))} options={countries} placeholder="Any country" /></Field>
        <Field label="Cities" hint="Pick from your data or type any city and press Enter."><MultiSelect value={v.cities} onChange={(c) => setV((o) => ({ ...o, cities: c }))} options={cities} creatable placeholder="Any city" /></Field>
        <Field label="Industries" hint="e.g. restaurant, clinic, real estate, software"><MultiSelect value={v.industries} onChange={(i) => setV((o) => ({ ...o, industries: i }))} options={industries} creatable placeholder="Any industry" /></Field>
      </div>
      <Nav onBack={onBack} onSkip={onNext} next={() => create.mutate()} nextLabel="Create profile" busy={create.isPending} disabled={empty} />
    </>
  );
}

// ───────────── step: mailbox ─────────────

function MailboxStep({ onBack, onNext }: { onBack: () => void; onNext: () => void }) {
  const { user } = useAuth();
  const [v, setV] = useState({ from_name: user?.full_name ?? "", from_email: user?.email ?? "", daily_limit: "50" });
  const create = useMutation({
    mutationFn: () => api.post("/mailboxes", { name: v.from_email, from_name: v.from_name, from_email: v.from_email, daily_limit: Number(v.daily_limit) || 50 }),
    onSuccess: () => { toast.success("Mailbox added"); onNext(); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <>
      <StepHead title="Connect a sending mailbox" text="Campaign emails are sent from this address. You can add your SMTP/IMAP details or a sending service (SES, SendGrid, Gmail…) later." />
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Sender name"><Input value={v.from_name} onChange={(e) => setV((o) => ({ ...o, from_name: e.target.value }))} /></Field>
        <Field label="Sender email"><Input type="email" value={v.from_email} onChange={(e) => setV((o) => ({ ...o, from_email: e.target.value }))} /></Field>
        <Field label="Daily sending limit" hint="Start low (30–50/day) for a new domain and increase gradually.">
          <Input type="number" min={1} max={2000} value={v.daily_limit} onChange={(e) => setV((o) => ({ ...o, daily_limit: e.target.value }))} />
        </Field>
      </div>
      <div className="mt-5 rounded-xl border bg-muted/40 p-4 text-sm text-muted-foreground">
        <b className="text-foreground">Deliverability checklist:</b> set up SPF, DKIM and DMARC for your sending domain, warm up new mailboxes gradually,
        and keep daily limits conservative. Leadline paces sending and pauses campaigns automatically on provider errors.
      </div>
      <Nav onBack={onBack} onSkip={onNext} next={() => create.mutate()} nextLabel="Add mailbox" busy={create.isPending} disabled={!v.from_email.includes("@") || !v.from_name.trim()} />
    </>
  );
}

// ───────────── step: team ─────────────

function TeamStep({ onBack, onNext }: { onBack: () => void; onNext: () => void }) {
  const [emails, setEmails] = useState("");
  const [role, setRole] = useState("member");
  const list = emails.split(/[\s,;]+/).map((e) => e.trim()).filter(Boolean);
  const invite = useMutation({
    mutationFn: () => api.post<{ invited: string[]; already_members: string[]; invalid: string[] }>("/invitations", { emails: list, role }),
    onSuccess: (r) => {
      toast.success(`Invited ${r.invited.length} ${r.invited.length === 1 ? "person" : "people"}`);
      if (r.invalid.length) toast.error(`Skipped invalid: ${r.invalid.join(", ")}`);
      onNext();
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  return (
    <>
      <StepHead title="Invite your team" text="Teammates get an email invitation and join with the role you choose. Invitations expire after 7 days." />
      <div className="space-y-4">
        <Field label="Email addresses" hint="Separate several addresses with commas or new lines (up to 20).">
          <Textarea rows={4} value={emails} placeholder={"alex@company.com\nsam@company.com"} onChange={(e) => setEmails(e.target.value)} />
        </Field>
        <Field label="Role">
          <div className="grid gap-2 sm:grid-cols-3">
            {[["admin", "Admin", "Manage settings, members and integrations"], ["member", "Member", "Work with leads and run campaigns"], ["viewer", "Viewer", "Read-only access to everything"]].map(([k, t, d]) => (
              <button key={k} type="button" onClick={() => setRole(k)}
                className={`rounded-xl border p-3 text-left transition hover:border-primary/50 ${role === k ? "border-primary bg-primary/5 ring-1 ring-primary/30" : ""}`}>
                <span className="block text-sm font-medium">{t}</span><span className="block text-xs text-muted-foreground">{d}</span>
              </button>
            ))}
          </div>
        </Field>
      </div>
      <Nav onBack={onBack} onSkip={onNext} next={() => invite.mutate()} nextLabel={list.length ? `Send ${list.length} invitation${list.length === 1 ? "" : "s"}` : "Send invitations"} busy={invite.isPending} disabled={!list.length} />
    </>
  );
}

// ───────────── step: done ─────────────

function DoneStep({ s, onFinish }: { s: OnboardingStatus; onFinish: () => void }) {
  return (
    <>
      <div className="mb-6 flex items-center gap-3">
        <span className="flex size-12 items-center justify-center rounded-full bg-[var(--series-3)]/15 text-[var(--series-3)]"><CircleCheck className="size-6" /></span>
        <div><h1 className="text-2xl font-semibold tracking-tight">You&apos;re all set</h1><p className="text-muted-foreground">{s.completed} of {s.checklist.length} setup tasks done. Finish the rest from your dashboard any time.</p></div>
      </div>
      <ul className="divide-y rounded-xl border">
        {s.checklist.map((c) => (
          <li key={c.key} className="flex items-center gap-3 px-4 py-3">
            {c.done ? <CircleCheck className="size-5 text-[var(--series-3)]" /> : <span className="size-5 rounded-full border-2" />}
            <span className="min-w-0 flex-1"><span className={`block text-sm font-medium ${c.done ? "text-muted-foreground line-through" : ""}`}>{c.title}</span><span className="block text-xs text-muted-foreground">{c.hint}</span></span>
          </li>
        ))}
      </ul>
      <Nav next={onFinish} nextLabel="Go to dashboard" />
    </>
  );
}
