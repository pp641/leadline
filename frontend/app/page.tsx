import type { Metadata } from "next";
import Link from "next/link";
import {
  ArrowRight, BarChart3, Bot, Building2, Check, ChevronRight, CircleCheck, Database, FileSpreadsheet, Filter, Globe,
  Inbox, KeyRound, Layers, LayoutDashboard, Lock, Mail, MapPin, PieChart, PlugZap, Radar, Send, ShieldCheck, Sparkles,
  Target, Users, Webhook, Workflow, Zap,
} from "lucide-react";
import { HeroActions, NavActions } from "@/components/landing/auth-actions";

export const metadata: Metadata = {
  title: "Leadline — find, engage and convert leads in one platform",
  description:
    "Leadline replaces your lead database, enrichment tools, email sequencer, inbox, CRM and reporting with one platform: discover companies, score them against your ICP, run compliant outreach and manage clients through to the deal.",
};

// ───────────── small building blocks ─────────────

function Logo() {
  return (
    <span className="flex items-center gap-2 text-base font-semibold tracking-tight">
      <span className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Send className="size-4" /></span>
      Leadline
    </span>
  );
}

function Eyebrow({ children }: { children: React.ReactNode }) {
  return <p className="text-sm font-semibold tracking-wide text-[var(--series-1)] uppercase">{children}</p>;
}

function SectionHead({ eyebrow, title, children }: { eyebrow: string; title: string; children?: React.ReactNode }) {
  return (
    <div className="mx-auto max-w-3xl space-y-3 text-center">
      <Eyebrow>{eyebrow}</Eyebrow>
      <h2 className="text-3xl font-semibold tracking-tight text-balance sm:text-4xl">{title}</h2>
      {children && <p className="text-lg text-pretty text-muted-foreground">{children}</p>}
    </div>
  );
}

function Tick({ children }: { children: React.ReactNode }) {
  return (
    <li className="flex gap-2.5">
      <CircleCheck className="mt-0.5 size-5 shrink-0 text-[var(--series-3)]" />
      <span>{children}</span>
    </li>
  );
}

const BrowserFrame = ({ children, url = "app.leadline.io" }: { children: React.ReactNode; url?: string }) => (
  <div className="overflow-hidden rounded-xl border bg-card shadow-2xl shadow-black/10 ring-1 ring-black/5 dark:shadow-black/40">
    <div className="flex items-center gap-2 border-b bg-muted/60 px-4 py-2.5">
      <span className="flex gap-1.5"><span className="size-2.5 rounded-full bg-[#ff5f57]" /><span className="size-2.5 rounded-full bg-[#febc2e]" /><span className="size-2.5 rounded-full bg-[#28c840]" /></span>
      <span className="mx-auto rounded-md bg-background px-3 py-0.5 text-xs text-muted-foreground">{url}</span>
    </div>
    {children}
  </div>
);

// ───────────── product previews (illustrative, built in HTML) ─────────────

function HeroPreview() {
  const kpis = [["Leads", "12,480"], ["With email", "71%"], ["Reply rate", "8.4%"], ["Meetings", "126"]];
  const bars = [38, 52, 44, 61, 58, 73, 69, 84, 77, 92, 88, 97];
  const stages = [["New", 46, "var(--series-1)"], ["In outreach", 27, "var(--series-2)"], ["Engaged", 15, "var(--series-3)"], ["Won", 7, "var(--series-4)"], ["Closed", 5, "var(--series-other)"]] as const;
  return (
    <BrowserFrame>
      <div className="grid grid-cols-[150px_1fr] text-left">
        <aside className="hidden space-y-1 border-r bg-muted/30 p-3 text-xs sm:block">
          {[["Dashboard", LayoutDashboard], ["Leads", Users], ["ICP", Target], ["Campaigns", Send], ["Inbox", Inbox], ["CRM", Layers], ["Analytics", BarChart3]].map(([n, Icon], i) => {
            const I = Icon as typeof Users;
            return <div key={n as string} className={`flex items-center gap-2 rounded-md px-2 py-1.5 ${i === 6 ? "bg-background font-medium shadow-xs" : "text-muted-foreground"}`}><I className="size-3.5" />{n as string}</div>;
          })}
        </aside>
        <div className="col-span-2 space-y-3 p-4 sm:col-span-1">
          <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
            {kpis.map(([k, v]) => (
              <div key={k} className="rounded-lg border bg-background p-2.5">
                <p className="text-[10px] text-muted-foreground">{k}</p><p className="text-lg font-semibold tracking-tight">{v}</p>
              </div>
            ))}
          </div>
          <div className="grid gap-2 md:grid-cols-[1.6fr_1fr]">
            <div className="rounded-lg border bg-background p-3">
              <p className="mb-2 text-xs font-medium">Leads added per week</p>
              <div className="flex h-28 items-end gap-1.5">
                {bars.map((h, i) => <div key={i} className="flex-1 rounded-t-[4px] bg-[var(--series-1)]" style={{ height: `${h}%` }} />)}
              </div>
            </div>
            <div className="rounded-lg border bg-background p-3">
              <p className="mb-2 text-xs font-medium">Pipeline</p>
              <div className="flex h-3 overflow-hidden rounded-full">
                {stages.map(([n, w, c]) => <div key={n} style={{ width: `${w}%`, background: c }} className="border-r-2 border-background last:border-r-0" />)}
              </div>
              <ul className="mt-3 space-y-1 text-[11px]">
                {stages.map(([n, w, c]) => (
                  <li key={n} className="flex items-center gap-1.5"><span className="size-2 rounded-sm" style={{ background: c }} />{n}<span className="ml-auto tabular-nums text-muted-foreground">{w}%</span></li>
                ))}
              </ul>
            </div>
          </div>
          <div className="rounded-lg border bg-background">
            {[["Spice Route Kitchen", "Restaurant · Noida", 92, "Interested"], ["Brightline Dental", "Clinic · Gurugram", 87, "Meeting"], ["Atlas Fitness", "Gym · Delhi", 81, "Replied"]].map(([n, m, s, st]) => (
              <div key={n as string} className="flex items-center gap-3 border-b px-3 py-2 text-xs last:border-b-0">
                <span className="flex size-6 items-center justify-center rounded-md bg-muted text-[10px] font-semibold">{(n as string)[0]}</span>
                <span className="min-w-0 flex-1"><span className="block truncate font-medium">{n as string}</span><span className="text-muted-foreground">{m as string}</span></span>
                <span className="rounded-md bg-[var(--series-1)]/10 px-1.5 py-0.5 font-semibold text-[var(--series-1)] tabular-nums">{s as number}</span>
                <span className="hidden rounded-full border px-2 py-0.5 sm:inline">{st as string}</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </BrowserFrame>
  );
}

function LeadsPreview() {
  return (
    <BrowserFrame url="app.leadline.io/icp">
      <div className="grid gap-0 text-left text-xs md:grid-cols-[1fr_200px]">
        <div className="p-4">
          <div className="mb-3 flex items-center gap-2"><span className="rounded-md border px-2 py-1">All 340</span><span className="text-muted-foreground">New 212</span><span className="text-muted-foreground">Already leads 128</span></div>
          {[["The Silk Bite Cafe", "Noida", "+91 98xx", "Verified"], ["Garden Grill", "Ghaziabad", "+91 93xx", "Partial"], ["Cold Rock Cafe", "Noida", "+91 96xx", "Verified"], ["Umaah Cafe", "Noida", "+91 92xx", "Partial"], ["The Pool Walk Bistro", "Ghaziabad", "+91 96xx", "Verified"]].map(([n, c, p, v], i) => (
            <div key={n} className="flex items-center gap-3 border-b py-2 last:border-b-0">
              <span className={`size-3.5 rounded border ${i < 3 ? "border-primary bg-primary" : ""}`} />
              <span className="min-w-0 flex-1 truncate font-medium">{n}</span>
              <span className="hidden w-20 text-muted-foreground sm:block">🇮🇳 {c}</span>
              <span className="hidden w-16 text-muted-foreground tabular-nums sm:block">{p}</span>
              <span className={`rounded-full px-2 py-0.5 ${v === "Verified" ? "bg-[var(--series-1)] text-white" : "border"}`}>{v}</span>
            </div>
          ))}
        </div>
        <div className="space-y-2.5 border-t bg-muted/30 p-4 md:border-t-0 md:border-l">
          <p className="font-semibold">Filter</p>
          {[["Countries", "🇮🇳 India"], ["Cities", "Noida · Ghaziabad"], ["Industries", "Cafe · Restaurant"], ["Website", "No website or outdated"]].map(([k, v]) => (
            <div key={k}><p className="text-[10px] text-muted-foreground">{k}</p><p className="mt-0.5 rounded-md border bg-background px-2 py-1">{v}</p></div>
          ))}
          <p className="rounded-md bg-primary px-2 py-1.5 text-center font-medium text-primary-foreground">Save &amp; apply</p>
        </div>
      </div>
    </BrowserFrame>
  );
}

function AnalyticsPreview() {
  const ind = [["Restaurant", 92], ["Cafe", 64], ["Real estate", 58], ["Clinic", 44], ["Salon", 37]] as const;
  const donut = "conic-gradient(var(--series-1) 0 52%, var(--card) 52% 52.6%, var(--series-2) 52.6% 74%, var(--card) 74% 74.6%, var(--series-3) 74.6% 89%, var(--card) 89% 89.6%, var(--series-4) 89.6% 100%)";
  return (
    <BrowserFrame url="app.leadline.io/analytics">
      <div className="space-y-3 p-4 text-left text-xs">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="rounded-lg bg-muted px-2.5 py-1">All leads <b>12,480</b></span><ChevronRight className="size-3 text-muted-foreground" />
          <span className="rounded-lg border px-2.5 py-1"><b className="mr-1 rounded-full bg-primary px-1.5 text-[10px] text-primary-foreground">1</b>Industry: Restaurant <b>3,120</b></span><ChevronRight className="size-3 text-muted-foreground" />
          <span className="rounded-lg border px-2.5 py-1"><b className="mr-1 rounded-full bg-primary px-1.5 text-[10px] text-primary-foreground">2</b>Has email <b>1,904</b></span>
        </div>
        <div className="grid gap-3 sm:grid-cols-[1.4fr_1fr]">
          <div className="rounded-lg border bg-background p-3">
            <p className="mb-2 font-medium">By industry</p>
            <div className="space-y-1.5">
              {ind.map(([n, w]) => (
                <div key={n} className="grid grid-cols-[70px_1fr_28px] items-center gap-2">
                  <span className="truncate text-muted-foreground">{n}</span>
                  <span className="h-3 rounded-r-[4px] bg-[var(--series-1)]" style={{ width: `${w}%` }} />
                  <span className="text-right tabular-nums text-muted-foreground">{w * 12}</span>
                </div>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-3 rounded-lg border bg-background p-3">
            <div className="relative size-20 shrink-0 rounded-full" style={{ background: donut }}>
              <div className="absolute inset-[22%] flex items-center justify-center rounded-full bg-background text-[11px] font-semibold">1,904</div>
            </div>
            <ul className="space-y-1">
              {[["Google Maps", "var(--series-1)"], ["Apollo", "var(--series-2)"], ["CSV", "var(--series-3)"], ["Hunter", "var(--series-4)"]].map(([n, c]) => (
                <li key={n} className="flex items-center gap-1.5"><span className="size-2 rounded-sm" style={{ background: c }} />{n}</li>
              ))}
            </ul>
          </div>
        </div>
        <div className="grid grid-cols-12 gap-2">
          {[3, 3, 6].map((span, i) => (
            <div key={i} className="rounded-lg border border-dashed bg-background p-2.5" style={{ gridColumn: `span ${span}` }}>
              <p className="text-[10px] text-muted-foreground">{["Avg score", "Avg ICP fit", "Founded year"][i]}</p>
              {i < 2 ? <p className="text-base font-semibold">{["47.1", "74%"][i]}</p> : (
                <div className="mt-1 flex h-8 items-end gap-1">{[30, 45, 50, 62, 48, 70, 88].map((h, j) => <span key={j} className="flex-1 rounded-t-[3px] bg-[var(--series-1)]" style={{ height: `${h}%` }} />)}</div>
              )}
            </div>
          ))}
        </div>
      </div>
    </BrowserFrame>
  );
}

// ───────────── content ─────────────

const REPLACES = [
  { icon: Database, name: "Lead databases" }, { icon: Sparkles, name: "Enrichment tools" }, { icon: Mail, name: "Email finders" },
  { icon: Send, name: "Sequencers" }, { icon: Inbox, name: "Shared inboxes" }, { icon: Layers, name: "CRMs" }, { icon: BarChart3, name: "BI dashboards" },
];

const PILLARS = [
  { icon: Radar, title: "Lead discovery", text: "Collect local businesses and B2B companies from your own company database, Google Maps, Apollo, People Data Labs, Crunchbase and more — or import a CSV of up to 50,000 rows." },
  { icon: Target, title: "ICP & scoring", text: "Describe your ideal customer by country, state, city, industry, size, website quality and buying signals. Every lead gets an explainable 0–100 fit and a configurable score." },
  { icon: ShieldCheck, title: "Verification", text: "Emails are checked for syntax, disposable and role addresses and MX records; websites and phones are verified before anything reaches a campaign." },
  { icon: Send, title: "Campaigns & sequences", text: "Multi-step sequences with threaded follow-ups, sending windows, daily limits, pacing and rotation across mailboxes — with AI-written openers you review first." },
  { icon: Bot, title: "Inbox & AI replies", text: "Replies and bounces are synced from every mailbox, matched to the right lead and classified — interested, not now, wrong person, unsubscribe — so you act on the right ones." },
  { icon: Layers, title: "CRM & client management", text: "Owners, notes, tasks and a pipeline board take interested leads to meetings and won deals, with optional sync to HubSpot, Salesforce, Pipedrive or Zoho." },
  { icon: PieChart, title: "Analytics & dashboards", text: "Lead insights by industry, location, founded year, tags and more; outreach performance by campaign, step and mailbox; and drag-and-drop custom dashboards." },
  { icon: PlugZap, title: "Integrations & API", text: "Connect 19 data, email, CRM and sending services in a click. A REST API, API keys and signed webhooks plug Leadline into the rest of your stack." },
];

const STEPS = [
  { icon: Target, title: "Define your ideal customer", text: "Pick countries, states and cities, industries, company size and the signals that matter." },
  { icon: Radar, title: "Find & enrich", text: "Pull matching companies from every connected source; duplicates are merged, emails verified, leads scored." },
  { icon: Send, title: "Engage at scale", text: "Launch personalized, compliant sequences across your mailboxes at a pace that protects your domain." },
  { icon: Users, title: "Convert & manage", text: "Interested replies land in your pipeline with notes, tasks and owners — and sync to your CRM." },
];

const INTEGRATIONS: { name: string; color: string; group: string }[] = [
  { name: "Apollo", color: "#5A5CF0", group: "Data" }, { name: "People Data Labs", color: "#7C3AED", group: "Data" },
  { name: "Google Maps", color: "#1A73E8", group: "Data" }, { name: "Crunchbase", color: "#0288D1", group: "Data" },
  { name: "Lusha", color: "#6D28D9", group: "Data" }, { name: "Hunter", color: "#FF5722", group: "Email finding" },
  { name: "Snov.io", color: "#2563EB", group: "Email finding" }, { name: "Clay", color: "#111827", group: "Workflows" },
  { name: "HubSpot", color: "#FF7A59", group: "CRM" }, { name: "Salesforce", color: "#00A1E0", group: "CRM" },
  { name: "Pipedrive", color: "#1A1A1A", group: "CRM" }, { name: "Zoho CRM", color: "#E42527", group: "CRM" },
  { name: "Amazon SES", color: "#FF9900", group: "Sending" }, { name: "SendGrid", color: "#1A82E2", group: "Sending" },
  { name: "Mailgun", color: "#F06B66", group: "Sending" }, { name: "Postmark", color: "#D4B400", group: "Sending" },
  { name: "Microsoft 365", color: "#0078D4", group: "Sending" }, { name: "Gmail", color: "#EA4335", group: "Sending" },
];

const initials = (n: string) => n.replace(/[^A-Za-z0-9 ]/g, "").split(" ").filter(Boolean).slice(0, 2).map((w) => w[0]).join("").toUpperCase();

const SECURITY = [
  { icon: Lock, title: "Encrypted credentials", text: "Integration keys, mailbox passwords and webhook secrets are sealed with AES-256-GCM and never shown again." },
  { icon: KeyRound, title: "Roles & tenancy", text: "Owner, admin, member and viewer roles; every query is scoped to the organization; rotating sessions with reuse detection." },
  { icon: ShieldCheck, title: "Compliance built in", text: "Unsubscribe link and one-click header (RFC 8058), suppression list, consent basis on every lead, audit log, export and erasure." },
  { icon: Building2, title: "Agency mode", text: "Run client workspaces under one agency with inherited access and per-client rollups." },
  { icon: Webhook, title: "API & webhooks", text: "Org-scoped API keys and HMAC-signed webhooks with retries and a delivery log." },
  { icon: Globe, title: "SSRF-safe networking", text: "Every user-supplied host passes a dial-time guard that blocks private and metadata addresses." },
];

const FAQ = [
  ["Where do the leads come from?", "From your own company database (a scraper that collects businesses from OpenStreetMap and Google Places, then checks their websites and contact details), from connected providers like Apollo, People Data Labs, Crunchbase, Lusha, Hunter and Snov.io, from Clay, or from CSV files. Every lead keeps its source and consent basis."],
  ["Do I need separate tools for email finding, sending and CRM?", "No. Leadline verifies emails, runs multi-mailbox sequences, syncs replies and manages the pipeline itself. If you already use a CRM or a sending service, connect it and Leadline works alongside it."],
  ["How does Leadline protect my sending reputation?", "Emails are verified before sending, sending is paced against campaign, mailbox and organization daily limits, bounces and complaints suppress the address automatically, and a provider fault pauses the campaign instead of burning through your list."],
  ["Can my whole team and my clients use it?", "Yes. Invite teammates with owner, admin, member or viewer roles, and use agency mode to run separate client workspaces with per-client reporting."],
  ["Is AI required?", "No. AI personalization and reply classification are optional. Without them, openers are off and replies are classified by rules — opt-outs are always rule-based."],
];

// ───────────── page ─────────────

export default function LandingPage() {
  return (
    <div className="min-h-svh bg-background text-foreground">
      {/* Navigation */}
      <header className="sticky top-0 z-40 border-b bg-background/80 backdrop-blur-md">
        <nav className="mx-auto flex h-16 max-w-7xl items-center gap-6 px-4 md:px-8" aria-label="Main">
          <Link href="/" aria-label="Leadline home"><Logo /></Link>
          <div className="hidden items-center gap-6 text-sm text-muted-foreground lg:flex">
            {[["Platform", "#platform"], ["How it works", "#how"], ["Integrations", "#integrations"], ["Analytics", "#analytics"], ["Security", "#security"], ["FAQ", "#faq"]].map(([n, h]) => (
              <a key={h} href={h} className="transition hover:text-foreground">{n}</a>
            ))}
          </div>
          <span className="flex-1" />
          <NavActions />
        </nav>
      </header>

      <main>
        {/* Hero */}
        <section className="relative overflow-hidden">
          <div aria-hidden className="pointer-events-none absolute inset-0 -z-10 bg-[radial-gradient(60rem_30rem_at_50%_-10%,color-mix(in_oklab,var(--series-1)_18%,transparent),transparent)]" />
          <div aria-hidden className="pointer-events-none absolute inset-0 -z-10 bg-[linear-gradient(to_right,var(--border)_1px,transparent_1px),linear-gradient(to_bottom,var(--border)_1px,transparent_1px)] bg-[size:56px_56px] [mask-image:radial-gradient(ellipse_at_top,black_30%,transparent_70%)] opacity-60" />
          <div className="mx-auto max-w-7xl px-4 pt-16 pb-20 md:px-8 md:pt-24">
            <div className="mx-auto max-w-4xl space-y-6 text-center">
              <a href="#platform" className="inline-flex items-center gap-2 rounded-full border bg-background/70 px-3 py-1 text-sm text-muted-foreground shadow-xs backdrop-blur transition hover:text-foreground">
                <span className="rounded-full bg-[var(--series-1)] px-2 py-0.5 text-xs font-semibold text-white">One platform</span>
                Lead discovery → outreach → CRM, connected <ChevronRight className="size-3.5" />
              </a>
              <h1 className="text-4xl font-semibold tracking-tight text-balance sm:text-5xl lg:text-6xl">
                The only software you need to <span className="bg-gradient-to-r from-[var(--series-1)] to-[var(--series-3)] bg-clip-text text-transparent">find, win and manage</span> clients
              </h1>
              <p className="mx-auto max-w-2xl text-lg text-pretty text-muted-foreground sm:text-xl">
                Leadline discovers the right companies, verifies and scores them against your ideal customer, runs compliant multi-mailbox outreach,
                and moves every interested reply through your pipeline — with analytics and integrations built in.
              </p>
              <div className="flex justify-center"><HeroActions /></div>
              <ul className="flex flex-wrap justify-center gap-x-6 gap-y-2 text-sm text-muted-foreground">
                {["Runs on your own infrastructure", "Bring your own data providers", "Works with your CRM"].map((t) => (
                  <li key={t} className="flex items-center gap-1.5"><Check className="size-4 text-[var(--series-3)]" /> {t}</li>
                ))}
              </ul>
            </div>
            <div className="relative mx-auto mt-14 max-w-5xl">
              <div aria-hidden className="absolute -inset-x-8 -top-8 -bottom-8 -z-10 rounded-[2rem] bg-gradient-to-b from-[var(--series-1)]/15 to-transparent blur-2xl" />
              <HeroPreview />
              <p className="mt-3 text-center text-xs text-muted-foreground">Illustrative data</p>
            </div>
          </div>
        </section>

        {/* Replaces */}
        <section className="border-y bg-muted/30">
          <div className="mx-auto flex max-w-7xl flex-col items-center gap-6 px-4 py-10 md:flex-row md:px-8">
            <p className="shrink-0 text-sm font-medium text-muted-foreground md:w-56">Replace a stack of disconnected tools with one platform</p>
            <div className="flex flex-1 flex-wrap items-center justify-center gap-2 md:justify-start">
              {REPLACES.map((r) => (
                <span key={r.name} className="inline-flex items-center gap-1.5 rounded-full border bg-background px-3 py-1.5 text-sm text-muted-foreground line-through decoration-muted-foreground/40">
                  <r.icon className="size-3.5" /> {r.name}
                </span>
              ))}
              <ArrowRight className="mx-1 size-4 text-muted-foreground" />
              <span className="inline-flex items-center gap-1.5 rounded-full bg-primary px-3.5 py-1.5 text-sm font-medium text-primary-foreground"><Send className="size-3.5" /> Leadline</span>
            </div>
          </div>
        </section>

        {/* Platform pillars */}
        <section id="platform" className="scroll-mt-20">
          <div className="mx-auto max-w-7xl space-y-12 px-4 py-24 md:px-8">
            <SectionHead eyebrow="The platform" title="Everything from the first search to the signed client">
              Eight capabilities that usually live in eight different products — sharing one data model, so nothing gets lost between them.
            </SectionHead>
            <div className="grid gap-px overflow-hidden rounded-2xl border bg-border sm:grid-cols-2 lg:grid-cols-4">
              {PILLARS.map((p) => (
                <div key={p.title} className="group space-y-3 bg-card p-6 transition hover:bg-muted/40">
                  <span className="flex size-10 items-center justify-center rounded-lg bg-[var(--series-1)]/10 text-[var(--series-1)] transition group-hover:bg-[var(--series-1)] group-hover:text-white"><p.icon className="size-5" /></span>
                  <h3 className="font-semibold">{p.title}</h3>
                  <p className="text-sm leading-relaxed text-muted-foreground">{p.text}</p>
                </div>
              ))}
            </div>
          </div>
        </section>

        {/* How it works */}
        <section id="how" className="scroll-mt-20 bg-muted/30">
          <div className="mx-auto max-w-7xl space-y-12 px-4 py-24 md:px-8">
            <SectionHead eyebrow="How it works" title="From ideal customer to closed deal in four steps" />
            <ol className="grid gap-6 md:grid-cols-4">
              {STEPS.map((s, i) => (
                <li key={s.title} className="relative space-y-3 rounded-2xl border bg-card p-6 shadow-xs">
                  <div className="flex items-center justify-between">
                    <span className="flex size-10 items-center justify-center rounded-full bg-primary text-sm font-semibold text-primary-foreground">{i + 1}</span>
                    <s.icon className="size-5 text-muted-foreground" />
                  </div>
                  <h3 className="font-semibold">{s.title}</h3>
                  <p className="text-sm leading-relaxed text-muted-foreground">{s.text}</p>
                  {i < STEPS.length - 1 && <ChevronRight aria-hidden className="absolute top-1/2 -right-5 hidden size-5 -translate-y-1/2 text-muted-foreground md:block" />}
                </li>
              ))}
            </ol>
          </div>
        </section>

        {/* Lead management deep-dive */}
        <section className="mx-auto grid max-w-7xl items-center gap-12 px-4 py-24 md:px-8 lg:grid-cols-2">
          <div className="space-y-5">
            <Eyebrow>Lead management</Eyebrow>
            <h2 className="text-3xl font-semibold tracking-tight text-balance sm:text-4xl">Target exactly who you want — down to the city</h2>
            <p className="text-lg text-muted-foreground">
              Build ideal customer profiles with country (with flags), state and city filters that narrow step by step, then search every source at once.
              Leadline flags companies you already have, verifies contact details and scores each lead before it reaches a campaign.
            </p>
            <ul className="space-y-3 text-sm">
              <Tick>Country → state → city filters plus industry, size, founding year, website quality and technology</Tick>
              <Tick>Duplicates detected by domain and by name + city across every import</Tick>
              <Tick>CSV import with column mapping, validation and preview — 10,000 rows in seconds</Tick>
              <Tick>Bulk tags, owners, exports and campaign assignment</Tick>
            </ul>
          </div>
          <LeadsPreview />
        </section>

        {/* Integrations */}
        <section id="integrations" className="scroll-mt-20 bg-muted/30">
          <div className="mx-auto max-w-7xl space-y-12 px-4 py-24 md:px-8">
            <SectionHead eyebrow="Integrations" title="Plug in the services you already pay for">
              Connect data providers, email finders, CRMs and sending services in a click. Results from every provider are mapped to the same lead format.
            </SectionHead>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
              {INTEGRATIONS.map((p) => (
                <div key={p.name} className="flex items-center gap-3 rounded-xl border bg-card p-3 shadow-xs">
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-lg text-xs font-bold text-white" style={{ background: p.color }} aria-hidden>{initials(p.name)}</span>
                  <span className="min-w-0"><span className="block truncate text-sm font-medium">{p.name}</span><span className="block text-xs text-muted-foreground">{p.group}</span></span>
                </div>
              ))}
            </div>
            <div className="grid gap-4 md:grid-cols-3">
              {[
                { icon: FileSpreadsheet, title: "Import from anywhere", text: "CSV, your company database, Apollo, PDL, Google Maps, Crunchbase, Lusha, Hunter, Snov.io or Clay — from one Import menu." },
                { icon: Workflow, title: "Sync to your CRM", text: "Push interested leads to HubSpot, Salesforce, Pipedrive or Zoho as companies and contacts, manually or automatically." },
                { icon: Zap, title: "Send through your provider", text: "Deliver campaigns through SES, SendGrid, Mailgun, Postmark, Microsoft 365, Gmail or any SMTP relay." },
              ].map((c) => (
                <div key={c.title} className="flex gap-4 rounded-2xl border bg-card p-5">
                  <c.icon className="mt-0.5 size-5 shrink-0 text-[var(--series-1)]" />
                  <div className="space-y-1"><h3 className="font-semibold">{c.title}</h3><p className="text-sm text-muted-foreground">{c.text}</p></div>
                </div>
              ))}
            </div>
          </div>
        </section>

        {/* Analytics deep-dive */}
        <section id="analytics" className="mx-auto grid max-w-7xl scroll-mt-20 items-center gap-12 px-4 py-24 md:px-8 lg:grid-cols-2">
          <div className="order-last lg:order-first"><AnalyticsPreview /></div>
          <div className="space-y-5">
            <Eyebrow>Analytics</Eyebrow>
            <h2 className="text-3xl font-semibold tracking-tight text-balance sm:text-4xl">See your whole funnel — and build the views your team needs</h2>
            <p className="text-lg text-muted-foreground">
              Bar, pie and distribution charts by industry, location, founded year, size, tags, source and score. Compare segments side by side,
              drill down with a click, and save drag-and-drop dashboards with step-by-step filters.
            </p>
            <ul className="space-y-3 text-sm">
              <Tick>Lead insights with filters for every field and saved views</Tick>
              <Tick>Outreach analytics: delivery, bounces, replies, positive replies and meetings by campaign, step and mailbox</Tick>
              <Tick>Custom dashboards shared across your organization</Tick>
              <Tick>Every chart has a table view and works in light and dark mode</Tick>
            </ul>
          </div>
        </section>

        {/* Built for */}
        <section className="bg-muted/30">
          <div className="mx-auto max-w-7xl space-y-12 px-4 py-24 md:px-8">
            <SectionHead eyebrow="Built for" title="One platform for every team that grows revenue" />
            <div className="grid gap-6 md:grid-cols-3">
              {[
                { icon: Building2, title: "Agencies", points: ["Separate client workspaces", "Per-client rollups and reporting", "One login across clients"] },
                { icon: Users, title: "Sales teams", points: ["Shared pipeline with owners and tasks", "Mailbox rotation and daily limits", "Reply classification and CRM sync"] },
                { icon: MapPin, title: "Local-business outreach", points: ["Discover businesses by city and category", "Website-quality signals for web & marketing offers", "Phone and website verification"] },
              ].map((c) => (
                <div key={c.title} className="space-y-4 rounded-2xl border bg-card p-6 shadow-xs">
                  <span className="flex size-10 items-center justify-center rounded-lg bg-primary text-primary-foreground"><c.icon className="size-5" /></span>
                  <h3 className="text-lg font-semibold">{c.title}</h3>
                  <ul className="space-y-2 text-sm text-muted-foreground">{c.points.map((p) => <li key={p} className="flex gap-2"><Check className="mt-0.5 size-4 shrink-0 text-[var(--series-3)]" />{p}</li>)}</ul>
                </div>
              ))}
            </div>
          </div>
        </section>

        {/* Security */}
        <section id="security" className="scroll-mt-20">
          <div className="mx-auto max-w-7xl space-y-12 px-4 py-24 md:px-8">
            <SectionHead eyebrow="Enterprise-ready" title="Secure, compliant and built for scale">
              Outreach only works long-term when it respects recipients and protects your data. Leadline makes that the default.
            </SectionHead>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {SECURITY.map((s) => (
                <div key={s.title} className="space-y-2 rounded-2xl border p-6">
                  <s.icon className="size-5 text-[var(--series-1)]" />
                  <h3 className="font-semibold">{s.title}</h3>
                  <p className="text-sm leading-relaxed text-muted-foreground">{s.text}</p>
                </div>
              ))}
            </div>
            <p className="text-center text-xs text-muted-foreground">Review your local email-marketing and data-protection law (CAN-SPAM, GDPR/PECR, India&apos;s DPDP Act and others) before sending.</p>
          </div>
        </section>

        {/* FAQ */}
        <section id="faq" className="scroll-mt-20 bg-muted/30">
          <div className="mx-auto max-w-3xl space-y-10 px-4 py-24 md:px-8">
            <SectionHead eyebrow="FAQ" title="Questions, answered" />
            <div className="divide-y rounded-2xl border bg-card">
              {FAQ.map(([q, a]) => (
                <details key={q} className="group px-6 py-4 [&_summary::-webkit-details-marker]:hidden">
                  <summary className="flex cursor-pointer list-none items-center justify-between gap-4 font-medium">
                    {q}<ChevronRight className="size-4 shrink-0 text-muted-foreground transition group-open:rotate-90" />
                  </summary>
                  <p className="mt-3 text-sm leading-relaxed text-muted-foreground">{a}</p>
                </details>
              ))}
            </div>
          </div>
        </section>

        {/* Closing CTA */}
        <section className="px-4 py-24 md:px-8">
          <div className="relative mx-auto max-w-5xl overflow-hidden rounded-3xl bg-primary px-6 py-16 text-center text-primary-foreground sm:px-16">
            <div aria-hidden className="pointer-events-none absolute inset-0 bg-[radial-gradient(40rem_20rem_at_50%_0%,color-mix(in_oklab,var(--series-1)_45%,transparent),transparent)]" />
            <div className="relative space-y-6">
              <Filter className="mx-auto size-8 opacity-80" />
              <h2 className="text-3xl font-semibold tracking-tight text-balance sm:text-4xl">Stop stitching tools together. Start converting.</h2>
              <p className="mx-auto max-w-2xl text-lg opacity-80">Sign up, connect your sources and launch your first compliant campaign today.</p>
              <div className="flex flex-wrap justify-center gap-3">
                <Link href="/register" className="inline-flex h-11 items-center gap-2 rounded-md bg-primary-foreground px-6 text-base font-medium text-primary transition hover:opacity-90">Sign up <ArrowRight className="size-4" /></Link>
                <Link href="/login" className="inline-flex h-11 items-center rounded-md border border-primary-foreground/30 px-6 text-base font-medium transition hover:bg-primary-foreground/10">Sign in</Link>
              </div>
            </div>
          </div>
        </section>
      </main>

      {/* Footer */}
      <footer className="border-t">
        <div className="mx-auto grid max-w-7xl gap-8 px-4 py-12 text-sm md:grid-cols-[1.5fr_1fr_1fr_1fr] md:px-8">
          <div className="space-y-3">
            <Logo />
            <p className="max-w-xs text-muted-foreground">Lead intelligence and compliant outbound — from first search to signed client, in one platform.</p>
          </div>
          {[
            { h: "Platform", l: [["Lead discovery", "#platform"], ["ICP & scoring", "#platform"], ["Campaigns", "#platform"], ["CRM", "#platform"]] },
            { h: "Product", l: [["Integrations", "#integrations"], ["Analytics", "#analytics"], ["Security", "#security"], ["FAQ", "#faq"]] },
            { h: "Account", l: [["Sign up", "/register"], ["Sign in", "/login"]] },
          ].map((c) => (
            <div key={c.h} className="space-y-3">
              <p className="font-semibold">{c.h}</p>
              <ul className="space-y-2 text-muted-foreground">{c.l.map(([n, h]) => <li key={n}><a href={h} className="hover:text-foreground">{n}</a></li>)}</ul>
            </div>
          ))}
        </div>
        <div className="border-t">
          <p className="mx-auto max-w-7xl px-4 py-6 text-xs text-muted-foreground md:px-8">© {new Date().getFullYear()} Leadline. All rights reserved.</p>
        </div>
      </footer>
    </div>
  );
}
