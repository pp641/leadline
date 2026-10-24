/* eslint-disable @typescript-eslint/no-explicit-any */
// In-browser database for demo mode: deterministic synthetic seed data that
// mirrors the Go API's shapes, persisted to localStorage so changes survive a
// reload. Nothing here talks to a server.
import fixtures from "./fixtures.json";

export type Row = Record<string, any>;

export interface MockDB {
  version: number;
  seq: number;
  users: Row[];
  orgs: Row[];
  members: Row[]; // {organization_id, user_id, role, created_at}
  leads: Row[];
  contacts: Row[];
  events: Row[];
  notes: Row[];
  tasks: Row[];
  icps: Row[];
  mailboxes: Row[];
  campaigns: Row[];
  steps: Row[];
  campaignLeads: Row[];
  daily: Row[]; // {organization_id, date, sent, bounced, replied}
  inbox: Row[];
  integrations: Row[];
  dashboards: Row[];
  suppressions: Row[];
  apiKeys: Row[];
  webhooks: Row[];
  deliveries: Row[];
  audit: Row[];
  imports: Row[];
  personalizations: Row[];
  invitations: Row[];
  verifications: Row[]; // {user_id, code, expires_at}
  catalog: Row[]; // company database (company-scraper) companies
  scoring: Row; // by org id
  // Deals & workflows: created lazily (see handlers/deals.ts) so older saved demo data keeps working.
  pipelines?: Row[]; // {id, organization_id, name, is_default, stages: [...]}
  deals?: Row[];
  dealEvents?: Row[];
  workflows?: Row[];
  workflowRuns?: Row[];
}

const KEY = "leadline.mock.db.v1";
const SESSION_KEY = "leadline.mock.session";
export const DEMO_ORG = "00000000-0000-4000-8000-000000000001";
export const DEMO_USER = "00000000-0000-4000-8000-0000000000a1";

// ───────────── deterministic randomness ─────────────

function mulberry32(seed: number) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
let rnd = mulberry32(42);
const pick = <T,>(a: readonly T[]) => a[Math.floor(rnd() * a.length)];
const chance = (p: number) => rnd() < p;
const int = (lo: number, hi: number) => lo + Math.floor(rnd() * (hi - lo + 1));

export function uuid(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    return (c === "x" ? r : (r & 0x3) | 0x8).toString(16);
  });
}
// Seed ids are deterministic so links stay valid across resets.
let seedCounter = 0;
const sid = (kind: number) => `00000000-0000-4000-${(8000 + kind).toString(16).padStart(4, "0")}-${(++seedCounter).toString(16).padStart(12, "0")}`;

export const now = () => new Date();
export const iso = (d: Date) => d.toISOString();
const daysAgo = (n: number, h = 10) => {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() - n);
  d.setUTCHours(h, int(0, 59), 0, 0);
  // Past dates never land later today (in the future).
  if (n >= 0 && d.getTime() > Date.now()) d.setTime(Date.now() - int(5, 240) * 60_000);
  return d;
};

// ───────────── vocab ─────────────

const PLACES = [
  ["Noida", "Uttar Pradesh"], ["Greater Noida", "Uttar Pradesh"], ["Ghaziabad", "Uttar Pradesh"], ["New Delhi", "Delhi"],
  ["Gurugram", "Haryana"], ["Faridabad", "Haryana"], ["Mumbai", "Maharashtra"], ["Pune", "Maharashtra"],
  ["Bengaluru", "Karnataka"], ["Hyderabad", "Telangana"],
] as const;
const ABROAD = [["Dubai", "Dubai", "United Arab Emirates"], ["London", "England", "United Kingdom"], ["Austin", "Texas", "United States"]] as const;
const INDUSTRIES: Record<string, [string[], string[]]> = {
  restaurant: [["Spice", "Saffron", "Tandoor", "Masala", "Curry", "Royal", "Urban", "Coastal"], ["Kitchen", "Dhaba", "Bistro", "House", "Table", "Grill"]],
  cafe: [["Brew", "Bean", "Morning", "Mocha", "Little", "Third"], ["Cafe", "Coffee Co", "Roasters", "Corner"]],
  salon: [["Glow", "Mirror", "Velvet", "Studio", "Luxe"], ["Salon", "Hair Studio", "Beauty Lounge", "Spa"]],
  gym: [["Iron", "Pulse", "Atlas", "Core", "Titan"], ["Fitness", "Gym", "Strength Club", "Athletics"]],
  clinic: [["CarePoint", "Lifeline", "Sunrise", "Medi", "Healwell"], ["Clinic", "Health Centre", "Polyclinic"]],
  dentist: [["Bright", "Pearl", "Smile", "Ivory"], ["Dental", "Dental Care", "Smile Studio"]],
  "real estate": [["Skyline", "Keystone", "Prime", "Urban Nest", "Landmark"], ["Realty", "Properties", "Estates", "Homes"]],
  bakery: [["Golden", "Crumb", "Butter", "Daily"], ["Bakery", "Bakes", "Patisserie"]],
  retail: [["City", "Daily", "Metro", "Smart"], ["Mart", "Store", "Bazaar"]],
  "software company": [["Nimbus", "Quantum", "Vertex", "Pixel", "Cloudnine"], ["Labs", "Technologies", "Software", "Systems"]],
  "marketing agency": [["Growthwise", "Spark", "Brandly", "Northstar"], ["Media", "Digital", "Marketing", "Studio"]],
  hotel: [["Grand", "Palm", "Heritage", "Lotus"], ["Hotel", "Residency", "Inn", "Suites"]],
};
const FIRST = ["Aarav", "Priya", "Rohan", "Ananya", "Vikram", "Neha", "Arjun", "Kavya", "Rahul", "Isha", "Karan", "Meera", "Aditya", "Sneha", "Dev", "Pooja"];
const LAST = ["Sharma", "Verma", "Gupta", "Mehta", "Iyer", "Reddy", "Kapoor", "Singh", "Nair", "Bose", "Malhotra", "Chopra"];
const TITLES = ["Owner", "Founder", "Managing Director", "Marketing Manager", "Operations Head", "CEO", "General Manager"];
const TECH = ["wordpress", "wix", "shopify", "jquery", "bootstrap", "google-analytics", "react", "woocommerce", "squarespace"];
const TAGS = ["company-scraper", "delhi-ncr", "q4-push", "high-intent", "agency-clients", "webinar"];
const SOURCES = ["google_places", "google_places", "google_places", "openstreetmap", "apollo", "csv", "hunter"];
const STATUSES: [string, number][] = [["new", 50], ["contacted", 20], ["replied", 8], ["interested", 7], ["qualified", 5], ["meeting", 4], ["won", 3], ["lost", 3]];

function weighted<T>(list: [T, number][]): T {
  const total = list.reduce((n, [, w]) => n + w, 0);
  let r = rnd() * total;
  for (const [v, w] of list) { r -= w; if (r <= 0) return v; }
  return list[0][0];
}

const slug = (s: string) => s.toLowerCase().replace(/&/g, "and").replace(/[^a-z0-9]+/g, "");

export function scoreLead(l: Row, hasValidEmail: boolean): { score: number; reasons: string[] } {
  const reasons: string[] = [];
  let s = 0;
  if (l.founded_year && l.founded_year >= new Date().getFullYear() - 2) { s += 20; reasons.push("Business opened recently"); }
  if (!l.website) { s += 20; reasons.push("No website"); }
  if (l.website_quality === "poor") { s += 15; reasons.push("Website is outdated"); }
  if ((l.icp_fit ?? 0) >= 70) { s += 15; reasons.push("Strong ICP fit"); }
  if (hasValidEmail) { s += 15; reasons.push("Verified contact email"); }
  if ((l.social_urls ?? []).length) { s += 5; reasons.push("Active on social media"); }
  if (l.phone) { s += 5; reasons.push("Has a phone number"); }
  return { score: Math.min(100, s), reasons };
}

// ───────────── seed ─────────────

export function seed(): MockDB {
  rnd = mulberry32(42);
  seedCounter = 0;
  const t0 = iso(daysAgo(120));
  const db: MockDB = {
    version: 1, seq: 1000, users: [], orgs: [], members: [], leads: [], contacts: [], events: [], notes: [], tasks: [], icps: [],
    mailboxes: [], campaigns: [], steps: [], campaignLeads: [], daily: [], inbox: [], integrations: [], dashboards: [],
    suppressions: [], apiKeys: [], webhooks: [], deliveries: [], audit: [], imports: [], personalizations: [], invitations: [],
    verifications: [], catalog: [], scoring: {},
  };
  const ORG = DEMO_ORG;
  db.users.push(
    { id: DEMO_USER, email: "demo@leadline.test", full_name: "Demo User", password: "demo-password-123", email_verified: true, created_at: t0, last_login_at: null },
    { id: sid(1), email: "aarav@demo-agency.test", full_name: "Aarav Mehta", password: "demo-password-123", email_verified: true, created_at: t0, last_login_at: null },
    { id: sid(1), email: "sara@demo-agency.test", full_name: "Sara Khan", password: "demo-password-123", email_verified: true, created_at: t0, last_login_at: null },
  );
  db.orgs.push({
    id: ORG, name: "Demo Agency", slug: "demo-agency", legal_name: "Demo Agency Pvt Ltd",
    physical_address: "Plot 12, Sector 62, Noida, Uttar Pradesh 201301, India", daily_send_limit: 500, data_retention_days: null,
    created_at: t0, parent_id: null, is_agency: false,
    onboarding: { completed: true, step: "done", profile: { industry: "Marketing / advertising agency", team_size: "2–10", country: "India" } },
  });
  db.members.push(
    { organization_id: ORG, user_id: DEMO_USER, role: "owner", created_at: t0 },
    { organization_id: ORG, user_id: db.users[1].id, role: "admin", created_at: t0 },
    { organization_id: ORG, user_id: db.users[2].id, role: "member", created_at: t0 },
  );
  const owners = [DEMO_USER, db.users[1].id, db.users[2].id];
  db.scoring[ORG] = structuredClone((fixtures as any).scoring.config);

  db.icps.push({
    id: sid(2), organization_id: ORG, name: "Delhi NCR restaurants & cafes", is_default: true, created_at: t0, updated_at: t0,
    criteria: { industries: ["restaurant", "cafe", "bakery"], countries: ["India"], states: [], cities: ["Noida", "Greater Noida", "New Delhi", "Gurugram", "Ghaziabad"],
      employees_min: null, employees_max: null, founded_after_year: null, website: "missing_or_poor", keywords: [], tech_stack: [], buying_signals: [], require_social: false, require_email: false },
  });

  // Leads
  const usedNames = new Set<string>();
  for (let i = 0; i < 260; i++) {
    const industry = pick(Object.keys(INDUSTRIES));
    const [a, b] = INDUSTRIES[industry];
    let name = `${pick(a)} ${pick(b)}`;
    if (usedNames.has(name)) name += ` ${pick(["Express", "Plus", "& Co", "Studio", "Hub", "Prime"])}`;
    if (usedNames.has(name)) name += ` ${i}`;
    usedNames.add(name);
    const abroad = chance(0.06);
    const [city, state, country] = abroad ? pick(ABROAD) : [...pick(PLACES), "India"];
    const hasSite = chance(0.62);
    const domain = hasSite ? `${slug(name)}.${pick(["in", "com", "co.in"])}` : "";
    const quality = !hasSite ? "none" : weighted([["good", 50], ["ok", 15], ["poor", 25], ["", 10]] as [string, number][]);
    const created = daysAgo(int(0, 90), int(6, 20));
    const status = weighted(STATUSES);
    const lead: Row = {
      id: sid(3), organization_id: ORG, company_name: name, website: hasSite ? `https://www.${domain}` : "", domain, industry,
      description: `${name} is a ${industry} in ${city}.`, country, state, city,
      address: `${int(1, 220)}, ${pick(["MG Road", "Sector " + int(1, 140), "Main Market", "Station Road", "Ring Road"])}, ${city}`,
      phone: chance(0.9) ? `+91 ${int(70000, 99999)} ${int(10000, 99999)}` : "", business_email: null,
      linkedin_url: chance(0.3) ? `https://www.linkedin.com/company/${slug(name)}` : "",
      social_urls: chance(0.4) ? [`https://instagram.com/${slug(name)}`] : [],
      employee_count: chance(0.7) ? pick([4, 8, 12, 18, 25, 40, 60, 90, 150, 300]) : null,
      founded_year: chance(0.75) ? int(2008, new Date().getFullYear()) : null,
      source: pick(SOURCES), source_url: "", import_id: null, lead_score: 0, status,
      owner_id: status === "new" ? (chance(0.3) ? pick(owners) : null) : pick(owners),
      website_quality: quality, tech_stack: hasSite ? [pick(TECH), ...(chance(0.5) ? [pick(TECH)] : [])].filter((v, j, arr) => arr.indexOf(v) === j) : [],
      icp_fit: null, enriched_at: iso(created), tags: [], latest_campaign: null,
      created_at: iso(created), updated_at: iso(created),
    };
    if (lead.source === "google_places") lead.source_url = `https://maps.google.com/?cid=${int(1e9, 9e9)}`;
    if (lead.source === "google_places" || lead.source === "openstreetmap") lead.tags.push("company-scraper");
    if (["Noida", "Greater Noida", "Ghaziabad", "New Delhi", "Gurugram", "Faridabad"].includes(city) && chance(0.5)) lead.tags.push("delhi-ncr");
    if (chance(0.15)) lead.tags.push(pick(TAGS.slice(2)));
    lead.tags = [...new Set(lead.tags)];
    db.leads.push(lead);

    // Contacts
    const nContacts = chance(0.55) ? int(1, 2) : 0;
    for (let c = 0; c < nContacts; c++) {
      const first = pick(FIRST), last = pick(LAST);
      const email = domain ? `${first.toLowerCase()}@${domain}` : chance(0.4) ? `${first.toLowerCase()}.${slug(name)}@gmail.com` : null;
      const st = email ? weighted([["valid", 70], ["risky", 12], ["unknown", 10], ["invalid", 8]] as [string, number][]) : "unverified";
      db.contacts.push({
        id: sid(4), organization_id: ORG, lead_id: lead.id, first_name: first, last_name: last, job_title: pick(TITLES), email,
        email_status: st, email_verified_at: email ? lead.created_at : null, verification_details: email ? { provider: "basic" } : {},
        linkedin_url: "", source: lead.source, confidence: 0.9, is_primary: c === 0, suppressed: false, created_at: lead.created_at,
      });
    }
    if (domain && chance(0.35)) lead.business_email = `info@${domain}`;
    db.events.push({ id: ++db.seq, organization_id: ORG, lead_id: lead.id, event_type: lead.source === "csv" ? "imported" : "discovered",
      actor_name: "Demo User", data: { source: lead.source }, created_at: lead.created_at });
  }

  // Mailboxes
  db.mailboxes.push(
    { id: sid(5), organization_id: ORG, name: "Outreach — Demo User", from_email: "hello@demo-agency.test", from_name: "Demo User", reply_to: null,
      daily_limit: 80, status: "active", created_at: t0, smtp_host: "", smtp_port: 587, smtp_username: "", smtp_tls: "starttls", has_smtp_password: false,
      imap_host: "imap.demo-agency.test", imap_port: 993, imap_username: "hello@demo-agency.test", imap_tls: true, has_imap_password: true,
      inbox_sync_enabled: true, last_synced_at: iso(daysAgo(0, 9)), sync_error: null },
    { id: sid(5), organization_id: ORG, name: "Outreach — Aarav", from_email: "aarav@demo-agency.test", from_name: "Aarav Mehta", reply_to: null,
      daily_limit: 60, status: "active", created_at: t0, smtp_host: "", smtp_port: 587, smtp_username: "", smtp_tls: "starttls", has_smtp_password: false,
      imap_host: "", imap_port: 993, imap_username: "", imap_tls: true, has_imap_password: false, inbox_sync_enabled: false, last_synced_at: null, sync_error: null },
  );
  const [mb1, mb2] = db.mailboxes;

  // Campaigns
  const mkCampaign = (name: string, status: string, ago: number, mailboxes: string[]) => {
    const c: Row = {
      id: sid(6), organization_id: ORG, name, status, mailbox_id: mailboxes[0] ?? null, daily_limit: 100, start_at: null, timezone: "Asia/Kolkata",
      send_window_start: "09:00", send_window_end: "17:00", send_days: [1, 2, 3, 4, 5], allowed_email_statuses: ["valid"], max_bounce_rate: 0.05,
      stop_on_reply: true, mailbox_ids: mailboxes, ai_personalization: false, personalization_brief: "", require_personalization_approval: true,
      paused_reason: status === "paused" ? "Paused by Demo User" : null, launched_at: status === "draft" ? null : iso(daysAgo(ago)),
      completed_at: null, created_at: iso(daysAgo(ago + 2)),
    };
    db.campaigns.push(c);
    return c;
  };
  const camp1 = mkCampaign("Delhi NCR restaurants — website refresh", "running", 28, [mb1.id, mb2.id]);
  const camp2 = mkCampaign("Gyms & salons — Q4 offer", "paused", 18, [mb1.id]);
  mkCampaign("Clinics & dentists — intro", "draft", 2, [mb2.id]);
  const STEPS = [
    ["Quick idea for {{company_name}}", "Hi {{first_name}},\n\n{{personalized_line}}\n\nI noticed {{company_name}} in {{city}} could get more bookings from Google with a faster, mobile-friendly website. We've done this for 30+ local businesses in Delhi NCR.\n\nWorth a 15-minute call next week?\n\n{{sender_name}}"],
    ["Re: Quick idea for {{company_name}}", "Hi {{first_name}}, just bringing this to the top of your inbox — happy to share two examples from {{city}}.\n\n{{sender_name}}"],
    ["Should I close the loop?", "Hi {{first_name}}, I'll assume the timing isn't right. If a new website is on your list for later this year, just reply and I'll send ideas.\n\n{{sender_name}}"],
  ];
  for (const c of db.campaigns) {
    STEPS.forEach(([subject, body], i) => db.steps.push({ id: sid(7), campaign_id: c.id, step_number: i + 1, delay_days: [0, 3, 5][i], subject_template: subject, body_template: body }));
  }
  // Campaign leads: engaged leads were contacted by a campaign.
  const engaged = db.leads.filter((l) => l.status !== "new" && db.contacts.some((c) => c.lead_id === l.id && c.email));
  engaged.forEach((l, i) => {
    const camp = i % 3 === 2 ? camp2 : camp1;
    const contact = db.contacts.find((c) => c.lead_id === l.id && c.email)!;
    const replied = ["replied", "interested", "qualified", "meeting", "won", "lost"].includes(l.status);
    const sentSteps = replied ? int(1, 2) : int(1, 3);
    const lastSent = daysAgo(int(1, 20));
    db.campaignLeads.push({
      id: sid(8), organization_id: ORG, campaign_id: camp.id, lead_id: l.id, contact_id: contact.id,
      status: replied ? "replied" : sentSteps >= 3 ? "completed" : camp.status === "paused" ? "paused" : "active",
      next_step_number: replied ? sentSteps : Math.min(3, sentSteps + 1), next_send_at: replied || sentSteps >= 3 ? null : iso(daysAgo(-int(1, 3))),
      last_sent_at: iso(lastSent), stop_reason: replied ? "replied" : null, sent_steps: sentSteps, mailbox_id: pick(camp.mailbox_ids), created_at: iso(daysAgo(25)),
    });
    l.latest_campaign = camp.name;
    db.events.push({ id: ++db.seq, organization_id: ORG, lead_id: l.id, event_type: "email_sent", actor_name: null,
      data: { campaign: camp.name, step: 1 }, created_at: iso(lastSent) });
    if (replied) {
      const cls = l.status === "lost" ? "not_interested" : l.status === "replied" ? pick(["question", "not_now", "out_of_office"]) : pick(["positive", "interested"]);
      db.inbox.push({
        id: sid(9), organization_id: ORG, lead_id: l.id, company_name: l.company_name, campaign_id: camp.id, campaign_name: camp.name,
        from_email: contact.email, from_name: `${contact.first_name} ${contact.last_name}`,
        subject: `Re: Quick idea for ${l.company_name}`,
        body_text: {
          positive: "This sounds interesting — can we talk on Thursday afternoon?",
          interested: "Yes, we've been meaning to redo our website. What would it cost roughly?",
          question: "Do you also handle Google Business Profile and Instagram?",
          not_now: "Not right now, maybe after Diwali. Please check back in December.",
          out_of_office: "I'm travelling until next Monday with limited access to email.",
          not_interested: "Thanks, but we already work with an agency.",
        }[cls as string] ?? "Thanks for reaching out.",
        received_at: iso(daysAgo(int(0, 15), int(9, 19))), kind: cls === "out_of_office" ? "auto_reply" : "reply", classification: cls,
        confidence: 0.86, summary: "", classified_by: "rules", status: chance(0.5) ? "new" : "handled",
      });
      db.events.push({ id: ++db.seq, organization_id: ORG, lead_id: l.id, event_type: "replied", actor_name: null, data: { classification: cls }, created_at: iso(daysAgo(int(0, 15))) });
    }
  });
  // Daily send series for the last 90 days.
  for (let d = 89; d >= 0; d--) {
    const date = iso(daysAgo(d)).slice(0, 10);
    const weekday = new Date(date + "T00:00:00Z").getUTCDay();
    const active = d < 30 && weekday !== 0 && weekday !== 6;
    const sent = active ? int(25, 70) : d < 30 ? int(0, 6) : 0;
    db.daily.push({ organization_id: ORG, date, sent, bounced: sent ? int(0, 2) : 0, replied: sent ? int(0, Math.max(1, Math.round(sent * 0.06))) : 0 });
  }

  // Notes and tasks
  for (const l of db.leads.filter((x) => ["interested", "qualified", "meeting"].includes(x.status)).slice(0, 18)) {
    db.notes.push({ id: sid(10), organization_id: ORG, lead_id: l.id, author_id: l.owner_id ?? DEMO_USER, body: pick([
      "Spoke to the owner — wants a quote for a 5-page site with online booking.", "Prefers WhatsApp. Follow up after the weekend.",
      "Budget around ₹60–80k. Decision with partner.", "Interested in SEO retainer as well."]), created_at: iso(daysAgo(int(0, 10))) });
    db.tasks.push({ id: sid(11), organization_id: ORG, lead_id: l.id, assignee_id: l.owner_id ?? DEMO_USER, created_by: DEMO_USER,
      title: pick(["Send proposal", "Book discovery call", "Share portfolio examples", "Follow up on quote"]), notes: "",
      due_at: iso(daysAgo(int(-5, 3))), completed_at: chance(0.25) ? iso(daysAgo(1)) : null, created_at: iso(daysAgo(5)) });
  }

  // Integrations (connected)
  for (const [kind, config, secrets] of [["google_places", {}, ["api_key"]], ["hunter", {}, ["api_key"]], ["hubspot", { auto_sync: true }, ["token"]]] as const) {
    db.integrations.push({ organization_id: ORG, kind, config, status: "active", last_error: null, has_secret: true, secrets_set: [...secrets], updated_at: iso(daysAgo(int(5, 30))) });
  }

  // Dashboard
  db.dashboards.push({
    id: sid(12), organization_id: ORG, name: "Lead overview", steps: [], created_by: DEMO_USER, created_at: t0, updated_at: t0,
    widgets: [
      { id: "w1", type: "kpi", title: "Leads", metric: "leads", width: 3 }, { id: "w2", type: "kpi", title: "With email", metric: "email_rate", width: 3 },
      { id: "w3", type: "kpi", title: "Avg lead score", metric: "avg_score", width: 3 }, { id: "w4", type: "kpi", title: "Meetings / won", metric: "won", width: 3 },
      { id: "w5", type: "trend", title: "Leads added over time", width: 8 }, { id: "w6", type: "stages", title: "Pipeline stage", width: 4 },
      { id: "w7", type: "bar", title: "Top industries", dimension: "industry", width: 8 }, { id: "w8", type: "donut", title: "Lead source", dimension: "source", width: 4 },
      { id: "w9", type: "compare", title: "Cities compared", dimension: "city", metric: "mix", width: 12 },
    ],
  });

  // Suppressions, API key, webhook, import history, audit
  for (const [email, reason] of [["unsubscribe@example.com", "unsubscribed"], ["bounced@oldmail.in", "hard_bounce"], ["noreply@spamtrap.test", "complaint"], ["former.client@acme.in", "manual"]]) {
    db.suppressions.push({ id: sid(13), organization_id: ORG, email, reason, source: reason === "manual" ? "manual" : "campaign", created_at: iso(daysAgo(int(1, 40))) });
  }
  db.apiKeys.push({ id: sid(14), organization_id: ORG, name: "Zapier", prefix: "ll_demo1", role: "member", last_used_at: iso(daysAgo(2)), revoked_at: null, created_at: iso(daysAgo(40)) });
  const wh = { id: sid(15), organization_id: ORG, url: "https://hooks.example.com/leadline", description: "", events: ["lead.created", "reply.received"], active: true };
  db.webhooks.push(wh);
  for (let i = 0; i < 4; i++) db.deliveries.push({ id: sid(16), endpoint_id: wh.id, event_type: pick(wh.events), status: "succeeded", attempts: 1, response_status: 200, last_error: null, created_at: iso(daysAgo(i)) });
  db.imports.push({ id: sid(17), organization_id: ORG, filename: "expo-visitors.csv", status: "completed", mapping: { "0": "company_name", "1": "city", "2": "email" },
    headers: ["Company", "City", "Email"], source: "csv", consent_basis: "legitimate_interest", total_rows: 42, valid_rows: 40, invalid_rows: 2, duplicate_rows: 3,
    imported_rows: 37, preview: null, error: null, created_at: iso(daysAgo(33)), rows: [] });
  for (const [action, ago] of [["user.login", 0], ["campaign.launched", 28], ["integration.saved", 20], ["import.completed", 33], ["dashboard.created", 10]] as const) {
    db.audit.push({ id: ++db.seq, organization_id: ORG, user_id: DEMO_USER, user_email: "demo@leadline.test", action, entity_type: action.split(".")[0], entity_id: "", metadata: {}, ip: "127.0.0.1", created_at: iso(daysAgo(ago)) });
  }

  // Company database (what company-scraper collected), partly overlapping existing leads.
  for (let i = 0; i < 180; i++) {
    const overlap = i < 30 ? db.leads[i * 7] : null;
    const industry = overlap?.industry ?? pick(Object.keys(INDUSTRIES));
    const [a, b] = INDUSTRIES[industry];
    const name = overlap?.company_name ?? `${pick(a)} ${pick(b)} ${pick(["", "", "Express", "Hub"])}`.trim();
    const [city, state] = overlap ? [overlap.city, overlap.state] : pick(PLACES);
    const hasSite = chance(0.55);
    const domain = hasSite ? `${slug(name)}.in` : "";
    const quality = hasSite ? pick(["good", "good", "poor", "ok"]) : "none";
    const vs = weighted([["verified", 25], ["partial", 55], ["unverified", 12], ["failed", 8]] as [string, number][]);
    db.catalog.push({
      id: sid(18), name, industry, description: "", website: hasSite ? `https://${domain}` : "", domain, email: hasSite && chance(0.3) ? `info@${domain}` : null,
      phone: `+91 ${int(70000, 99999)} ${int(10000, 99999)}`, address: `${int(1, 200)}, Sector ${int(1, 140)}, ${city}`, city, state, country: overlap?.country ?? "India",
      employee_count: chance(0.4) ? pick([5, 12, 30, 80]) : null, founded_year: chance(0.5) ? int(2010, 2025) : null,
      source: pick(["google_places", "openstreetmap"]), source_url: `https://maps.google.com/?cid=${int(1e9, 9e9)}`, attribution: "Google Maps",
      website_status: hasSite ? "reachable" : "none", website_quality: quality, email_status: "unknown", phone_valid: true, verification_status: vs,
      linkedin_url: "", social_urls: [], tech_stack: hasSite ? [pick(TECH)] : [], verified_at: iso(daysAgo(int(1, 20))),
      verification: { checks: { phone: { passed: true, status: "valid" }, website: { passed: hasSite, status: hasSite ? "reachable" : "missing" } }, passed: hasSite ? 2 : 1, failed: hasSite ? 0 : 1 },
      website_report: hasSite ? { quality, https: chance(0.7), mobile_friendly: quality === "good", load_ms: int(300, 2400), issues: quality === "poor" ? ["not mobile-friendly", "copyright 2017"] : [] } : null,
    });
  }

  recomputeAll(db);
  return db;
}

// ───────────── derived fields ─────────────

export function icpFit(c: Row | undefined, l: Row): { fit: number; matched: string[]; missed: string[] } | null {
  if (!c) return null;
  let total = 0, got = 0;
  const matched: string[] = [], missed: string[] = [];
  const check = (w: number, set: boolean, ok: boolean, label: string) => {
    if (!set) return;
    total += w;
    if (ok) { got += w; matched.push(label); } else missed.push(label);
  };
  const low = (a?: string[]) => (a ?? []).map((x) => x.toLowerCase());
  const inds = low(c.industries);
  check(25, inds.length > 0, inds.some((i) => (l.industry ?? "").toLowerCase().includes(i)), "Industry");
  const cities = low(c.cities), states = low(c.states), countries = low(c.countries);
  check(20, cities.length + states.length + countries.length > 0,
    (!cities.length || cities.includes((l.city ?? "").toLowerCase())) && (!states.length || states.includes((l.state ?? "").toLowerCase())) &&
    (!countries.length || countries.includes((l.country ?? "").toLowerCase())), "Location");
  check(15, c.founded_after_year != null, l.founded_year != null && l.founded_year >= c.founded_after_year, "Opened recently");
  const w = c.website;
  const site = !!l.website;
  const okSite = w === "missing" ? !site : w === "missing_or_poor" ? !site || ["poor", "none"].includes(l.website_quality) : w === "poor" ? l.website_quality === "poor"
    : w === "present" ? site : w === "good" ? ["good", "ok"].includes(l.website_quality) : true;
  check(15, !!w && w !== "any", okSite, "Website");
  check(5, !!c.require_email, !!l.business_email, "Contact email");
  return { fit: total ? Math.round((got * 100) / total) : 0, matched, missed };
}

export function recomputeLead(db: MockDB, l: Row) {
  const icp = db.icps.find((i) => i.organization_id === l.organization_id && i.is_default);
  l.icp_fit = icp ? icpFit(icp.criteria, l)!.fit : null;
  const valid = db.contacts.some((c) => c.lead_id === l.id && c.email_status === "valid");
  l.lead_score = scoreLead(l, valid).score;
}

export function recomputeAll(db: MockDB) {
  for (const l of db.leads) recomputeLead(db, l);
}

// ───────────── persistence ─────────────

let cache: MockDB | null = null;

export function getDB(): MockDB {
  if (cache) return cache;
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as MockDB;
      if (parsed.version === 1) return (cache = parsed);
    }
  } catch { /* corrupted or unavailable: reseed */ }
  cache = seed();
  saveDB();
  return cache;
}

let saveTimer: ReturnType<typeof setTimeout> | null = null;
export function saveDB() {
  if (saveTimer) clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    try { localStorage.setItem(KEY, JSON.stringify(cache)); } catch { /* quota or private mode: keep in memory */ }
  }, 50);
}

/** Wipes demo data (and the demo session) back to the seed. */
export function resetDB() {
  try { localStorage.removeItem(KEY); localStorage.removeItem(SESSION_KEY); localStorage.removeItem("leadline.org"); } catch { /* ignore */ }
  cache = null;
}

export const mockSession = {
  get(): string | null { try { return localStorage.getItem(SESSION_KEY); } catch { return null; } },
  set(userId: string | null) { try { if (userId) localStorage.setItem(SESSION_KEY, userId); else localStorage.removeItem(SESSION_KEY); } catch { /* ignore */ } },
};

export { fixtures };
